package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/tanner-caffrey/letta-tether/internal/config"
	"github.com/tanner-caffrey/letta-tether/internal/runner"
	"github.com/tanner-caffrey/letta-tether/internal/single"
	"github.com/tanner-caffrey/letta-tether/internal/update"
)

// runHeadless is for servers and Linux boxes with no tray: the same runner (restart
// with backoff, kill the whole process tree on stop), printing status lines instead
// of changing an icon. systemd (or anything) can supervise it.
//
//	letta-tether --headless [--name "my server"] [--use existing|bundled]
func runHeadless(args []string) int {
	update.CleanupAfterUpdate() // remove the previous version's backup, if any
	c := config.Load()
	autoUpdate := true
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--no-auto-update":
			autoUpdate = false
		case "--name":
			if i+1 < len(args) {
				c.ComputerName = config.CleanName(args[i+1])
				i++
			}
		case "--use":
			if i+1 < len(args) {
				c.Install = args[i+1]
				i++
			}
		}
	}
	if err := pick(c); err != nil {
		if c.Install == "bundled" || errors.Is(err, errNoLetta) {
			fmt.Fprintln(os.Stderr, "letta-tether: no Letta found. Install it with `npm install -g @letta-ai/letta-code`,")
			fmt.Fprintln(os.Stderr, "  or run `letta-tether --download` once to fetch Letta's official all-in-one build.")
		} else {
			fmt.Fprintln(os.Stderr, "letta-tether:", err)
		}
		return 1
	}
	fmt.Printf("letta-tether: running %s as %q (log: %s)\n", run.Install, c.ComputerName, config.LogPath())
	run.OnChange = func(s runner.State, detail string) {
		if detail != "" {
			fmt.Printf("letta-tether: %s (%s)\n", s, detail)
		} else {
			fmt.Printf("letta-tether: %s\n", s)
		}
	}
	run.Start()

	// Updates: same as the tray app, minus the tray. Off with --no-auto-update or the
	// setting. After installing, the new version takes over this same process (same PID),
	// so systemd keeps supervising it without noticing.
	restart := make(chan struct{}, 1)
	updates.Version = version
	updates.Auto = func() bool { return autoUpdate && config.Load().AutoUpdateOn() }
	updates.Restart = func(bool) error { restart <- struct{}{}; return nil }
	updates.OnChange = func(st update.Status) {
		switch st.Phase {
		case "downloading":
			if st.Done == 0 {
				fmt.Printf("letta-tether: updating to %s\n", st.Available)
			}
		case "failed":
			fmt.Printf("letta-tether: update failed (still running %s): %s\n", version, st.Error)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go updates.Run(ctx)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	select {
	case <-sig:
		fmt.Println("letta-tether: stopping")
		run.Stop()
		return 0
	case <-restart:
		fmt.Println("letta-tether: restarting into the new version")
		run.Stop()
		lock.Release()
		t, err := update.CurrentTarget()
		if err == nil {
			err = execSelf(t.Program, append([]string{"--headless"}, args...))
		}
		// only reached if the exec failed: keep going on the old version
		fmt.Println("letta-tether: couldn't start the new version, carrying on:", err)
		lock, _ = single.Acquire(filepath.Join(runner.AppDir(), "running.lock"))
		run.Start()
		<-sig
		run.Stop()
		return 0
	}
}
