package main

import (
	"os"

	"github.com/tanner-caffrey/letta-tether/internal/update"
)

// Windows has no exec-in-place: start the new one and exit. (Headless on Windows is
// for people supervising it themselves; the tray app is the normal way there.)
func execSelf(program string, args []string) error {
	if err := update.Relaunch(program, append([]string{"--updated-headless"}, args[1:]...)...); err != nil {
		return err
	}
	os.Exit(0)
	return nil
}
