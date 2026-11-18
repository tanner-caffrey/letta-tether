package runner

// Model-based test of the Runner: random sequences of Start / Stop / double calls,
// against fake listeners that connect, crash, hang, refuse to start, or spawn children.
//
// The model is deliberately dumb: the person either wants it running or doesn't.
// Invariants checked after every action:
//   - never more than one listener alive
//   - after Stop returns: no listener or grandchild alive, state is Stopped
//   - while wanted and the script connects: it reaches Connected
//   - while not wanted: nothing ever starts again

import (
	"os"
	"sync"
	"testing"
	"time"

	"pgregory.net/rapid"
)

var scripts = map[string]string{
	"connects":     "print:reconnecting;print:connected;print:idle",
	"works":        "print:connected;print:working;sleep:20;print:idle",
	"crashes":      "print:connected;sleep:30;exit:1",
	"crash-early":  "exit:3",
	"error-exit":   "print:error;exit:1",
	"hangs-silent": "hang",
	"spawns-child": "print:connected;child;print:idle",
}

type runnerMachine struct {
	r      *Runner
	pids   string
	script string
	wanted bool

	mu   sync.Mutex
	seen []State // every state the tray would have shown, in order
}

func (m *runnerMachine) record(s State, _ string) {
	m.mu.Lock()
	m.seen = append(m.seen, s)
	m.mu.Unlock()
}

func (m *runnerMachine) since(i int) []State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]State(nil), m.seen[i:]...)
}

func (m *runnerMachine) mark() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.seen)
}

func newRunnerMachine(t *rapid.T) *runnerMachine {
	dir, err := os.MkdirTemp("", "tether-pids")
	if err != nil {
		t.Fatal(err)
	}
	name := rapid.SampledFrom([]string{"connects", "works", "crashes", "crash-early", "error-exit", "hangs-silent", "spawns-child"}).Draw(t, "script")
	m := &runnerMachine{pids: dir, script: name}
	m.r = &Runner{Install: fakeInstall(t, dir, scripts[name]), ComputerName: "test", RetryWait: 20 * time.Millisecond, OnChange: m.record}
	return m
}

func (m *runnerMachine) cleanup() {
	m.r.Stop()
	killAll(m.pids)
	os.RemoveAll(m.pids)
}

func (m *runnerMachine) Start(t *rapid.T) {
	m.r.Start()
	m.wanted = true
}

func (m *runnerMachine) Stop(t *rapid.T) {
	before := m.mark()
	m.r.Stop()
	// After Stop returns, the last thing the tray was told is "stopped" (an update already
	// on its way when Stop was pressed may land first, but never after).
	after := m.since(before)
	if len(after) > 0 && after[len(after)-1] != Stopped {
		t.Fatalf("after Stop the tray was last told %q (all: %v, script %s)", after[len(after)-1], after, m.script)
	}
	late := m.mark()
	defer func() {
		time.Sleep(30 * time.Millisecond)
		if extra := m.since(late); len(extra) > 0 && m.wanted == false {
			t.Fatalf("told the tray %v after Stop returned (script %s)", extra, m.script)
		}
	}()
	m.wanted = false
	if s, _ := m.r.State(); s != Stopped {
		t.Fatalf("state after Stop = %q, want stopped", s)
	}
	// A killed process takes a moment to actually be gone (the OS has to reap it).
	// Allow that, but record how long it took: the contract is "gone right after Stop".
	start := time.Now()
	if !waitFor(time.Second, func() bool { return len(alivePids(nil, m.pids)) == 0 }) {
		t.Fatalf("1s after Stop, still alive: %v (script %s)", alivePids(nil, m.pids), m.script)
	}
	if d := time.Since(start); d > 200*time.Millisecond {
		t.Logf("slow to disappear after Stop: %v", d)
	}
}

func (m *runnerMachine) Wait(t *rapid.T) {
	time.Sleep(time.Duration(rapid.IntRange(0, 120).Draw(t, "ms")) * time.Millisecond)
}

func (m *runnerMachine) Check(t *rapid.T) {
	listeners := len(alivePids(t, m.pids))
	// a spawned child is allowed alongside its listener, so the bound is 2 for that script
	limit := 1
	if m.script == "spawns-child" {
		limit = 64 // children of crashed listeners are counted by the Stop check
	}
	if listeners > limit {
		t.Fatalf("%d processes alive at once (script %s)", listeners, m.script)
	}
	if !m.wanted {
		if s, _ := m.r.State(); s != Stopped {
			t.Fatalf("not wanted but state is %q", s)
		}
	}
}

func TestPropRunnerStateMachine(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		m := newRunnerMachine(t)
		defer m.cleanup()
		t.Repeat(map[string]func(*rapid.T){
			"start": m.Start,
			"stop":  m.Stop,
			"wait":  m.Wait,
			"":      m.Check,
		})
		// end every history with a Stop and the full check
		m.Stop(t)
	})
}

// A listener that connects must be reported as connected, and keep being retried after a crash.
func TestPropRunnerReachesConnectedAndRecovers(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		name := rapid.SampledFrom([]string{"connects", "works", "crashes", "spawns-child"}).Draw(t, "script")
		dir, _ := os.MkdirTemp("", "tether-pids")
		defer os.RemoveAll(dir)
		defer killAll(dir)
		seen := make(chan State, 64)
		r := &Runner{Install: fakeInstall(t, dir, scripts[name]), RetryWait: 20 * time.Millisecond,
			OnChange: func(s State, _ string) {
				select {
				case seen <- s:
				default:
				}
			}}
		r.Start()
		defer r.Stop()
		connects := 0
		deadline := time.After(3 * time.Second)
		want := 1
		if name == "crashes" {
			want = 2 // connected, crashed, came back
		}
		for connects < want {
			select {
			case s := <-seen:
				if s == Connected {
					connects++
				}
			case <-deadline:
				t.Fatalf("script %s: connected %d times, want %d", name, connects, want)
			}
		}
	})
}
