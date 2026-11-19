// Package runner finds a Letta install and keeps `letta server` running in the background.
//
// It reads the listener's own status lines (--lifecycle-output jsonl), so the tray
// never has to guess whether things are working.
package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// State is what the listener says it's doing.
type State string

const (
	Stopped      State = "stopped"      // we haven't started it, or it exited
	Starting     State = "starting"     // launched, no status line yet
	Connected    State = "connected"    // registered with Letta, waiting for work
	Working      State = "working"      // an agent is doing something on this computer
	Reconnecting State = "reconnecting" // lost the connection, trying again
	Error        State = "error"        // it gave up (often: not signed in yet)
)

// Install is one way to run Letta: a node binary plus letta.js, or a launcher command.
type Install struct {
	Kind    string // "bundled" (ours, bundled Node) or "existing" (theirs, from npm)
	Version string
	// Node + Script are set for both kinds when we can find them; we always prefer
	// running node directly so the process we watch IS the listener.
	Node   string
	Script string
	// Extra environment (the bundled copy needs its bin dir on PATH).
	Env []string
}

func (i Install) String() string {
	return fmt.Sprintf("%s Letta %s", i.Kind, i.Version)
}

// Command is `letta <args...>` with this install, hidden (no window), for things that
// need to be talked to, like a channel's setup questions.
func (i Install) Command(ctx context.Context, args ...string) *exec.Cmd {
	return i.command(ctx, args...)
}

// Run runs a short letta command (like `channels status`) and returns what it printed.
//
// Output goes through a temp file, not a pipe: Letta exits after the first 64 KB when
// its output is a pipe (found with `agents list`, ~1.7 MB, silently cut to 65536 bytes).
func (i Install) Run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := i.command(ctx, args...)
	var errb strings.Builder
	cmd.Stderr = &errb
	f, ferr := os.CreateTemp("", "letta-tether-out-*")
	if ferr != nil {
		return nil, ferr
	}
	defer os.Remove(f.Name())
	defer f.Close()
	cmd.Stdout = f
	err := cmd.Run()
	out, rerr := os.ReadFile(f.Name())
	if err == nil {
		err = rerr
	}
	if err != nil {
		msg := strings.TrimSpace(errb.String())
		if j := strings.LastIndexByte(msg, '\n'); j >= 0 {
			msg = msg[j+1:]
		}
		if msg != "" {
			return out, fmt.Errorf("%w: %s", err, msg)
		}
	}
	return out, err
}

// command builds the exec.Cmd for `letta <args...>`.
func (i Install) command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, i.Node, append([]string{i.Script}, args...)...)
	cmd.Env = cleanEnv(append(os.Environ(), i.Env...))
	// Start in the person's home folder, like opening a terminal would. Without this Letta
	// inherits wherever we were launched from: at Windows login that's C:\Windows\system32,
	// and Letta tries (and fails) to save project settings there, and treats it as the
	// agent's working folder.
	if home, err := os.UserHomeDir(); err == nil {
		cmd.Dir = home
	}
	setupProcess(cmd)
	return cmd
}

// cleanEnv drops the debug switches: with them set, letta refuses lifecycle output.
func cleanEnv(env []string) []string {
	out := env[:0:0]
	for _, kv := range env {
		k := strings.ToUpper(strings.SplitN(kv, "=", 2)[0])
		if k == "LETTA_DEBUG" || k == "DEBUG" {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// Version asks the install what version it is ("0.34.12").
func (i Install) probeVersion() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := i.command(ctx, "--version").Output()
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "Letta Code") {
			return strings.TrimSpace(strings.SplitN(line, " ", 2)[0]), nil
		}
	}
	return "", errors.New("letta didn't say its version")
}

// AppDir is where our own copy of Letta lives. Nothing outside it is touched.
func AppDir() string {
	base, err := os.UserConfigDir() // %AppData% on Windows, ~/Library/Application Support on Mac
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "letta-tether")
}

// FindBundled returns our bundled copy, if it's been installed.
func FindBundled() (Install, bool) {
	payload := filepath.Join(AppDir(), "letta", "letta_code", "_payload")
	node := filepath.Join(payload, "bin", exe("node"))
	script := filepath.Join(payload, "app", "letta.js")
	if !isFile(node) || !isFile(script) {
		return Install{}, false
	}
	in := Install{Kind: "bundled", Node: node, Script: script, Env: []string{
		"PATH=" + filepath.Dir(node) + string(os.PathListSeparator) + os.Getenv("PATH"),
		"LETTA_CODE_DISTRIBUTION=pypi",
	}}
	v, err := in.probeVersion()
	if err != nil {
		return Install{}, false
	}
	in.Version = v
	return in, true
}

// FindExisting looks for a Letta the person installed themselves with npm.
func FindExisting() (Install, bool) {
	node, err := exec.LookPath(exe("node"))
	if err != nil {
		return Install{}, false
	}
	var candidates []string
	if appdata := os.Getenv("APPDATA"); appdata != "" { // Windows npm global
		candidates = append(candidates, filepath.Join(appdata, "npm", "node_modules", "@letta-ai", "letta-code", "letta.js"))
	}
	// Unix: <prefix>/lib/node_modules, where prefix is two levels above the node binary.
	if real, err := filepath.EvalSymlinks(node); err == nil {
		prefix := filepath.Dir(filepath.Dir(real))
		candidates = append(candidates,
			filepath.Join(prefix, "lib", "node_modules", "@letta-ai", "letta-code", "letta.js"),
			filepath.Join(filepath.Dir(real), "node_modules", "@letta-ai", "letta-code", "letta.js"))
	}
	for _, script := range candidates {
		if !isFile(script) {
			continue
		}
		in := Install{Kind: "existing", Node: node, Script: script}
		if v, err := in.probeVersion(); err == nil {
			in.Version = v
			return in, true
		}
	}
	return Install{}, false
}

// Runner keeps one listener running and remembers its latest state.
type Runner struct {
	Install      Install
	ComputerName string
	// OnChange is called (from a background goroutine) whenever the state changes.
	OnChange func(State, string)
	// LogPath is where everything the listener prints goes.
	LogPath string
	// RetryWait is the first wait before restarting a crashed listener (default 5s);
	// it doubles each time, up to 5 minutes.
	RetryWait time.Duration
	// Channels: also run the messaging channels switched on in Letta (Telegram etc.).
	// Uses Letta's "start whatever is enabled" mode, where one broken channel can't take
	// the connection down (naming them with --channels would).
	Channels bool
	// OnStart is called just before each launch (the channels watcher snapshots logs).
	OnStart func()

	mu      sync.Mutex
	state   State
	detail  string
	cancel  context.CancelFunc
	done    chan struct{}
	wanted  bool
	backoff time.Duration
}

func (r *Runner) State() (State, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state == "" {
		return Stopped, ""
	}
	return r.state, r.detail
}

func (r *Runner) set(s State, detail string) {
	r.mu.Lock()
	changed := s != r.state || detail != r.detail
	r.state, r.detail = s, detail
	cb := r.OnChange
	r.mu.Unlock()
	if changed && cb != nil {
		cb(s, detail)
	}
}

// Start launches the listener and restarts it if it dies (with growing waits),
// until Stop is called.
func (r *Runner) Start() {
	r.mu.Lock()
	if r.wanted {
		r.mu.Unlock()
		return
	}
	r.wanted = true
	r.backoff = r.firstWait()
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.done = make(chan struct{})
	r.mu.Unlock()
	go r.loop(ctx)
}

// Stop ends the listener and waits for it to exit.
func (r *Runner) Stop() {
	r.mu.Lock()
	if !r.wanted {
		r.mu.Unlock()
		return
	}
	r.wanted = false
	cancel, done := r.cancel, r.done
	r.mu.Unlock()
	cancel()
	<-done
	r.set(Stopped, "")
}

// failuresBeforeError is how many quick failures in a row (never connected, or
// couldn't even start) before we stop saying "reconnecting" and say something's wrong.
// It keeps retrying either way, in case the problem fixes itself.
const failuresBeforeError = 3

func (r *Runner) loop(ctx context.Context) {
	defer close(r.done)
	quickFails := 0
	for {
		started := time.Now()
		exit, connected := r.runOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		// It exited on its own. If it ran a good while, start fresh; otherwise wait longer each time.
		if time.Since(started) > 2*time.Minute {
			r.backoff = r.firstWait()
		}
		if connected {
			quickFails = 0
		} else {
			quickFails++
		}
		state := r.stateAfterExit()
		// Couldn't even launch it (Letta or Node missing): no point calling that a hiccup.
		if quickFails >= failuresBeforeError || strings.HasPrefix(exit, "couldn't start Letta") {
			state = Error
		}
		r.set(state, exit)
		select {
		case <-ctx.Done():
			return
		case <-time.After(r.backoff):
		}
		if r.backoff < 5*time.Minute {
			r.backoff *= 2
		}
	}
}

func (r *Runner) firstWait() time.Duration {
	if r.RetryWait > 0 {
		return r.RetryWait
	}
	return 5 * time.Second
}

func (r *Runner) stateAfterExit() State {
	s, _ := r.State()
	if s == Error {
		return Error
	}
	return Reconnecting
}

// runOnce runs the listener until it exits. It returns why it stopped, and whether it
// connected to Letta at any point (a crash after a good run is just a hiccup).
func (r *Runner) runOnce(ctx context.Context) (why string, connected bool) {
	r.set(Starting, "")
	args := []string{"server", "--lifecycle-output", "jsonl"}
	if r.ComputerName != "" {
		args = append(args, "--computer-name", r.ComputerName)
	}
	cmd := r.Install.command(ctx, args...)
	if r.Channels {
		cmd.Env = append(cmd.Env, "LETTA_RESTORE_ENABLED_CHANNELS=1")
	}
	if r.OnStart != nil {
		r.OnStart()
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err.Error(), false
	}
	var logw io.Writer = io.Discard
	if r.LogPath != "" {
		_ = os.MkdirAll(filepath.Dir(r.LogPath), 0o755)
		if f, err := os.OpenFile(r.LogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			defer f.Close()
			fmt.Fprintf(f, "\n--- %s starting %s as %q\n", time.Now().Format(time.RFC3339), r.Install, r.ComputerName)
			logw = f
		}
	}
	cmd.Stderr = logw
	if err := cmd.Start(); err != nil {
		return "couldn't start Letta: " + err.Error(), false
	}
	cleanup := afterStart(cmd)
	defer cleanup() // whatever happens, nothing Letta started outlives it
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		line := sc.Text()
		fmt.Fprintln(logw, line)
		if s, ok := ParseLifecycle(line); ok {
			if s == Connected || s == Working {
				connected = true
			}
			r.set(s, "")
		}
	}
	err = cmd.Wait()
	if err != nil {
		return "Letta stopped: " + err.Error(), connected
	}
	return "Letta stopped", connected
}

// ParseLifecycle reads one --lifecycle-output jsonl line.
func ParseLifecycle(line string) (State, bool) {
	var ev struct {
		Protocol int    `json:"lettaLifecycleProtocol"`
		State    string `json:"state"`
	}
	if json.Unmarshal([]byte(line), &ev) != nil || ev.Protocol == 0 {
		return "", false
	}
	switch ev.State {
	case "connected", "idle":
		return Connected, true
	case "working":
		return Working, true
	case "reconnecting":
		return Reconnecting, true
	case "error":
		return Error, true
	}
	return "", false
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
