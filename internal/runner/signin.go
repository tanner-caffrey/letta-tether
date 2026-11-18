package runner

import (
	"bufio"
	"context"
	"strings"
	"sync"
	"time"
)

// SignIn is Letta's own browser sign-in, run in the background so we can show the
// link and code in our window instead of a terminal.
//
// It runs plain `letta server` (the quiet status mode won't sign anyone in), watches for
// the link, waits for "Authenticated successfully.", then stops it. The runner starts
// the real listener afterwards.
type SignIn struct {
	mu    sync.Mutex
	Phase string // "", "starting", "waiting" (show URL+code), "done", "already", "failed"
	URL   string
	Code  string
	Error string
}

func (s *SignIn) Snapshot() SignIn {
	s.mu.Lock()
	defer s.mu.Unlock()
	return SignIn{Phase: s.Phase, URL: s.URL, Code: s.Code, Error: s.Error}
}

func (s *SignIn) set(f func(*SignIn)) {
	s.mu.Lock()
	f(s)
	s.mu.Unlock()
}

// Run blocks until sign-in finishes, fails, or turns out not to be needed.
func (s *SignIn) Run(in Install, computerName string) {
	s.set(func(x *SignIn) { x.Phase, x.URL, x.Code, x.Error = "starting", "", "", "" })
	ctx, cancel := context.WithTimeout(context.Background(), 16*time.Minute) // device codes last ~15 min
	defer cancel()
	cmd := in.command(ctx, "server", "--computer-name", computerName)
	out, err := cmd.StdoutPipe()
	if err != nil {
		s.set(func(x *SignIn) { x.Phase, x.Error = "failed", err.Error() })
		return
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		s.set(func(x *SignIn) { x.Phase, x.Error = "failed", "couldn't start Letta: "+err.Error() })
		return
	}
	cleanup := afterStart(cmd)
	defer cleanup()
	// The reader stops when Letta's output closes, which happens when we end Letta below.
	// It also gives up handing over lines once we've finished, so it never leaks.
	lines := make(chan string)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			select {
			case lines <- sc.Text():
			case <-ctx.Done():
				return
			}
		}
	}()
	// No guessing from silence: Letta always says one of "visit: <link>",
	// "Authenticated successfully" or "Awaiting instructions" (or exits).
	// A slow first start just means waiting longer.
	finish := func(phase, msg string) {
		s.set(func(x *SignIn) { x.Phase, x.Error = phase, msg })
		cancel()
		_ = cmd.Wait()
	}
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				if s.Snapshot().Phase == "waiting" {
					finish("failed", "Letta stopped before sign-in finished.")
				} else {
					finish("failed", "Letta stopped before it could sign in.")
				}
				return
			}
			switch {
			case strings.Contains(line, "visit:"):
				url := strings.TrimSpace(line[strings.Index(line, "visit:")+len("visit:"):])
				s.set(func(x *SignIn) { x.Phase, x.URL = "waiting", url })
			case strings.HasPrefix(strings.TrimSpace(line), "Your code:"):
				code := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "Your code:"))
				s.set(func(x *SignIn) { x.Code = code })
			case strings.Contains(line, "Authenticated successfully"):
				finish("done", "")
				return
			case strings.Contains(line, "Awaiting instructions"):
				// it connected without asking: they were already signed in.
				if s.Snapshot().Phase == "waiting" {
					finish("done", "")
				} else {
					finish("already", "")
				}
				return
			case strings.Contains(line, "already running on this machine"):
				finish("failed", strings.TrimSpace(line))
				return
			}
		case <-ctx.Done():
			finish("failed", "Sign-in timed out. Try again.")
			return
		}
	}
}
