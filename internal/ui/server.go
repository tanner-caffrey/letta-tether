// Package ui is the setup + settings page. It's a small web page served only to this
// computer (127.0.0.1) and opened in the person's own browser. A random token in the
// link means other programs on the computer can't drive it.
package ui

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/tanner-caffrey/letta-tether/internal/autostart"
	"github.com/tanner-caffrey/letta-tether/internal/channels"
	"github.com/tanner-caffrey/letta-tether/internal/config"
	"github.com/tanner-caffrey/letta-tether/internal/runner"
	"github.com/tanner-caffrey/letta-tether/internal/uninstall"
	"github.com/tanner-caffrey/letta-tether/internal/update"
)

//go:embed page.html
var page []byte

// fetchLatest is how we learn the newest Letta version (swapped out in tests).
var fetchLatest = runner.LatestNPM

// The real start-at-login switch. Tests replace these so they never touch the real one.
var (
	setAutostart = autostart.Set
	hasAutostart = autostart.IsSet
)

// App is everything the page can see and change.
type App struct {
	Runner *runner.Runner
	// Restart applies new settings: pick the install, rename, start.
	Apply func(config.Config) error
	Quit  func()
	// Updates is Letta Tether's own updater (nil in tests and dev builds).
	Updates *update.Manager
	// Channels is the Letta install channel commands run with (set when Letta is picked).
	Channels runner.Install
	// ChannelWatch reads which channels started (set by the runner's OnStart).
	ChannelWatch func() channels.Result
	// letta runs a letta subcommand; tests replace it.
	letta channels.Letter

	mu      sync.Mutex
	cfg     config.Config
	signin  runner.SignIn
	install struct {
		Phase, Error string
		Done, Total  int64
		Version      string
	}
	token    string
	baseURL  string
	bundled  *runner.Install
	existing *runner.Install
	latest   string // newest Letta on npm, "" until we know
}

func (a *App) Config() config.Config { a.mu.Lock(); defer a.mu.Unlock(); return a.cfg }

func (a *App) SetConfig(c config.Config) { a.mu.Lock(); a.cfg = c; a.mu.Unlock() }

// Start serves the page on a random local port and returns its URL.
func (a *App) Start(c config.Config) (string, error) {
	a.cfg = c
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	a.token = hex.EncodeToString(b)
	a.refreshInstalls()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if v, err := fetchLatest(ctx); err == nil {
			a.mu.Lock()
			a.latest = v
			a.mu.Unlock()
		}
	}()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", a.guard(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(page)
	}))
	mux.HandleFunc("/api/status", a.guard(a.status))
	mux.HandleFunc("/api/install", a.guard(a.doInstall))
	mux.HandleFunc("/api/signin", a.guard(a.doSignIn))
	mux.HandleFunc("/api/save", a.guard(a.save))
	mux.HandleFunc("/api/start", a.guard(func(w http.ResponseWriter, r *http.Request) {
		c := a.Config()
		c.Paused = false
		_ = c.Save()
		a.SetConfig(c)
		a.Runner.Start()
		ok(w)
	}))
	mux.HandleFunc("/api/tether-check", a.guard(func(w http.ResponseWriter, r *http.Request) {
		if a.Updates == nil {
			http.Error(w, "updates aren't available in this build", http.StatusNotFound)
			return
		}
		a.Updates.Check(r.Context())
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(a.Updates.Status())
	}))
	mux.HandleFunc("/api/tether-update", a.guard(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		if a.Updates == nil {
			http.Error(w, "updates aren't available in this build", http.StatusNotFound)
			return
		}
		go a.Updates.ApplyFromPage(context.Background()) // the page follows along through status
		w.Write([]byte(`{"ok":true}`))
	}))
	mux.HandleFunc("/api/channels", a.guard(a.channelsList))
	mux.HandleFunc("/api/channel-switch", a.guard(a.channelSwitch))
	mux.HandleFunc("/api/channel-pair", a.guard(a.channelPair))
	mux.HandleFunc("/api/channel-install", a.guard(a.channelInstall))
	mux.HandleFunc("/api/agents", a.guard(a.agentsList))
	mux.HandleFunc("/api/uninstall-plan", a.guard(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(uninstall.MakePlan(runner.AppDir()))
	}))
	mux.HandleFunc("/api/uninstall", a.guard(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		a.Runner.Stop() // Windows won't delete files Letta has open
		err := uninstall.Run(uninstall.MakePlan(runner.AppDir()))
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
			return
		}
		w.Write([]byte(`{"ok":true}`))
		if a.Quit != nil {
			go func() { time.Sleep(500 * time.Millisecond); a.Quit() }()
		}
	}))
	mux.HandleFunc("/api/stop", a.guard(func(w http.ResponseWriter, r *http.Request) {
		c := a.Config()
		c.Paused = true
		_ = c.Save()
		a.SetConfig(c)
		go a.Runner.Stop()
		ok(w)
	}))
	go http.Serve(ln, mux)
	a.baseURL = fmt.Sprintf("http://%s/?t=%s", ln.Addr().String(), a.token)
	return a.baseURL, nil
}

func (a *App) URL() string { return a.baseURL }

func (a *App) guard(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t := r.URL.Query().Get("t")
		if t == "" {
			t = r.Header.Get("X-Tether-Token")
		}
		if t != a.token {
			http.Error(w, "This page only opens from the Letta Tether icon.", http.StatusForbidden)
			return
		}
		if r.Method == "POST" && r.Header.Get("X-Tether-Token") != a.token {
			http.Error(w, "forbidden", http.StatusForbidden) // blocks cross-site form posts
			return
		}
		h(w, r)
	}
}

func ok(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (a *App) refreshInstalls() {
	if in, found := runner.FindBundled(); found {
		a.bundled = &in
	} else {
		a.bundled = nil
	}
	if in, found := runner.FindExisting(); found {
		a.existing = &in
	} else {
		a.existing = nil
	}
}

func (a *App) status(w http.ResponseWriter, r *http.Request) {
	st, detail := a.Runner.State()
	a.mu.Lock()
	ver := func(i *runner.Install) string {
		if i == nil {
			return ""
		}
		return i.Version
	}
	out := map[string]any{
		"state": st, "detail": detail, "config": a.cfg,
		"bundled": ver(a.bundled), "existing": ver(a.existing),
		"install": a.install, "signin": a.signin.Snapshot(),
		"autostart": hasAutostart(), "log": config.LogPath(),
		"latest":       a.latest,
		"existing_old": a.existing != nil && runner.Older(a.existing.Version, a.latest),
		"bundled_old":  a.bundled != nil && runner.Older(a.bundled.Version, a.latest),
	}
	a.mu.Unlock()
	if a.Updates != nil {
		out["tether"] = a.Updates.Status()
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// doInstall downloads the bundled copy in the background; the page polls status.
func (a *App) doInstall(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	if a.install.Phase == "downloading" {
		a.mu.Unlock()
		ok(w)
		return
	}
	a.install.Phase, a.install.Error, a.install.Done, a.install.Total = "downloading", "", 0, 0
	a.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		rel, err := runner.LatestBundled(ctx)
		if err == nil {
			a.mu.Lock()
			a.install.Version, a.install.Total = rel.Version, rel.Size
			a.mu.Unlock()
			wasRunning := a.Runner.Install.Kind == "bundled"
			if wasRunning {
				a.Runner.Stop() // Windows can't replace files that are in use
			}
			err = runner.InstallBundled(ctx, rel, func(d, t int64) {
				a.mu.Lock()
				a.install.Done, a.install.Total = d, t
				a.mu.Unlock()
			})
			a.mu.Lock()
			a.refreshInstalls()
			a.mu.Unlock()
			if wasRunning && !a.Config().Paused {
				if in, found := runner.FindBundled(); found {
					a.Runner.Install = in
				}
				a.Runner.Start()
			}
		}
		a.mu.Lock()
		if err != nil {
			a.install.Phase, a.install.Error = "failed", err.Error()
		} else {
			a.install.Phase = "done"
		}
		a.mu.Unlock()
	}()
	ok(w)
}

func (a *App) chosen() (runner.Install, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfg.Install == "existing" && a.existing != nil {
		return *a.existing, true
	}
	if a.bundled != nil {
		return *a.bundled, true
	}
	return runner.Install{}, false
}

func (a *App) doSignIn(w http.ResponseWriter, r *http.Request) {
	in, found := a.chosen()
	if !found {
		http.Error(w, "Letta isn't set up yet.", http.StatusConflict)
		return
	}
	if a.signin.Snapshot().Phase == "waiting" || a.signin.Snapshot().Phase == "starting" {
		ok(w)
		return
	}
	a.Runner.Stop() // the sign-in run and the listener would fight over the same name
	go a.signin.Run(in, a.Config().ComputerName)
	ok(w)
}

func (a *App) save(w http.ResponseWriter, r *http.Request) {
	var c config.Config
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&c); err != nil {
		http.Error(w, "bad settings", http.StatusBadRequest)
		return
	}
	c.ComputerName = config.CleanName(c.ComputerName)
	if c.Install != "existing" {
		c.Install = "bundled"
	}
	if err := setAutostart(c.StartAtLogin); err != nil {
		fmt.Fprintln(os.Stderr, "autostart:", err)
	}
	if err := a.Apply(c); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	a.SetConfig(c)
	ok(w)
}
