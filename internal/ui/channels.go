package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/tanner-caffrey/letta-tether/internal/channels"
)

func (a *App) lettaCmd() channels.Letter {
	if a.letta != nil {
		return a.letta
	}
	in := a.Channels
	if in.Node == "" {
		return func(context.Context, ...string) ([]byte, error) {
			return nil, errNotSetUp
		}
	}
	return in.Run
}

type uiError string

func (e uiError) Error() string { return string(e) }

const errNotSetUp = uiError("Letta isn't set up yet. Finish setup first.")

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
}

// channelsList: what Letta has, plus whether each switched-on account actually started.
func (a *App) channelsList(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	list, err := channels.List(ctx, a.lettaCmd())
	if err != nil {
		writeErr(w, err)
		return
	}
	channels.MarkLocal(channels.ConfigDir(), list)
	if a.ChannelWatch != nil && a.Config().Channels {
		a.ChannelWatch().Apply(list)
	}
	writeJSON(w, map[string]any{"ok": true, "on": a.Config().Channels, "channels": list, "config_dir": channels.ConfigDir()})
}

func readBody(r *http.Request, v any) error {
	if r.Method != "POST" {
		return uiError("POST only")
	}
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<16)).Decode(v)
}

// channelSwitch turns channels on/off overall, or one account on/off, then restarts Letta
// so it takes effect.
func (a *App) channelSwitch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		All     *bool  `json:"all"`
		Channel string `json:"channel"`
		Account string `json:"account"`
		On      bool   `json:"on"`
	}
	if err := readBody(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	c := a.Config()
	if req.All != nil {
		c.Channels = *req.All
	} else {
		if err := channels.SetEnabled(channels.ConfigDir(), req.Channel, req.Account, req.On); err != nil {
			writeErr(w, err)
			return
		}
		if req.On {
			c.Channels = true // switching one on means "run channels"
		}
	}
	if err := a.Apply(c); err != nil {
		writeErr(w, err)
		return
	}
	a.SetConfig(c)
	writeJSON(w, map[string]any{"ok": true})
}

func (a *App) channelPair(w http.ResponseWriter, r *http.Request) {
	var req struct{ Channel, Account, Code, Agent string }
	if err := readBody(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := channels.Pair(ctx, a.lettaCmd(), req.Channel, req.Account, req.Code, req.Agent); err != nil {
		writeErr(w, err)
		return
	}
	// Letta only reads new pairings when it starts.
	if err := a.Apply(a.Config()); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (a *App) channelInstall(w http.ResponseWriter, r *http.Request) {
	var req struct{ Channel string }
	if err := readBody(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	if err := channels.Install(ctx, a.lettaCmd(), req.Channel); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// agentsList: the person's agents, to pick which one answers a paired chat.
func (a *App) agentsList(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	out, err := a.lettaCmd()(ctx, "agents", "list", "--limit", "100")
	if err != nil {
		writeErr(w, err)
		return
	}
	type agent struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	var list []agent
	if i := strings.IndexByte(string(out), '{'); i >= 0 {
		var env struct{ Body []agent }
		if json.Unmarshal(out[i:], &env) == nil && env.Body != nil {
			list = env.Body
		}
	}
	if list == nil {
		if i := strings.IndexByte(string(out), '['); i >= 0 {
			_ = json.Unmarshal(out[i:], &list)
		}
	}
	// sleeptime agents are background helpers of another agent, never the one that answers
	kept := list[:0]
	for _, g := range list {
		if !strings.HasSuffix(g.Name, "-sleeptime") {
			kept = append(kept, g)
		}
	}
	sort.SliceStable(kept, func(i, j int) bool { return strings.ToLower(kept[i].Name) < strings.ToLower(kept[j].Name) })
	writeJSON(w, map[string]any{"ok": true, "agents": kept})
}

func (a *App) setupStarter() (channels.Starter, error) {
	if a.setupStart != nil {
		return a.setupStart, nil
	}
	in := a.Channels
	if in.Node == "" {
		return nil, errNotSetUp
	}
	return func(ctx context.Context, channel string) *exec.Cmd {
		return in.Command(ctx, "channels", "configure", channel)
	}, nil
}

// channelSetup relays Letta's own channel setup. GET: what it says now. POST with
// action start / answer / cancel.
func (a *App) channelSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		writeJSON(w, map[string]any{"ok": true, "setup": a.setup.State()})
		return
	}
	var req struct {
		Action  string `json:"action"`
		Channel string `json:"channel"`
		Answer  string `json:"answer"`
	}
	if err := readBody(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	var err error
	switch req.Action {
	case "start":
		var start channels.Starter
		if start, err = a.setupStarter(); err == nil {
			err = a.setup.BeginPrepared(a.prepareChannel, start, req.Channel)
		}
	case "answer":
		err = a.setup.Answer(req.Answer)
	case "cancel":
		a.setup.Cancel()
	default:
		err = uiError("unknown action")
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "setup": a.setup.State()})
}

// prepareChannel installs a channel's extra pieces (if it needs them and doesn't have them
// yet) with no input attached, before Letta's questions start. See channels.Preparer.
func (a *App) prepareChannel(ctx context.Context, channel string) error {
	list, err := channels.List(ctx, a.lettaCmd())
	if err != nil {
		return err
	}
	for _, c := range list {
		if c.ID == channel && c.RuntimeInstalled {
			return nil
		}
	}
	return channels.Install(ctx, a.lettaCmd(), channel)
}
