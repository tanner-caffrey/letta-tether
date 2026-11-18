package main

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/tanner-caffrey/letta-tether/internal/config"
	"github.com/tanner-caffrey/letta-tether/internal/runner"
)

// runHeadless is for servers and Linux boxes with no tray: the same runner (restart
// with backoff, kill the whole process tree on stop), printing status lines instead
// of changing an icon. systemd (or anything) can supervise it.
//
//	letta-tether --headless [--name "my server"] [--use existing|bundled]
func runHeadless(args []string) int {
	c := config.Load()
	for i := 0; i < len(args); i++ {
		switch args[i] {
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
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	fmt.Println("letta-tether: stopping")
	run.Stop()
	return 0
}
