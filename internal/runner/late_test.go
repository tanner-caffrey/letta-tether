package runner

import (
	"sync"
	"testing"
	"time"
)

// Contract check (no bug found): after Stop returns, "stopped" is the last thing the
// tray hears, even when Stop lands mid crash-loop. An earlier stricter oracle flagged
// pre-Stop news as a bug; it was the test, not the runner.
func TestStopIsTheLastWord(t *testing.T) {
	for i := 0; i < 60; i++ {
		dir := t.TempDir()
		var mu sync.Mutex
		var seen []State
		r := &Runner{Install: fakeInstall(t, dir, "exit:3"), RetryWait: time.Microsecond,
			OnChange: func(s State, _ string) {
				time.Sleep(time.Millisecond) // a tray redraw takes a moment
				mu.Lock()
				seen = append(seen, s)
				mu.Unlock()
			}}
		r.Start()
		time.Sleep(time.Duration(i%40) * time.Millisecond) // land Stop at different points of the crash loop
		r.Stop()
		time.Sleep(10 * time.Millisecond)
		killAll(dir)
		mu.Lock()
		got := append([]State(nil), seen...)
		mu.Unlock()
		if len(got) == 0 || got[len(got)-1] != Stopped {
			t.Fatalf("try %d: tray was told %v, last should be stopped", i, got)
		}
	}
}
