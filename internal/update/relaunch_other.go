//go:build !windows

package update

import (
	"os/exec"
	"syscall"
)

// Relaunch starts program in its own session (not our child), so it keeps running
// after we exit.
func Relaunch(program string, args ...string) error {
	cmd := exec.Command(program, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}

// ReplaceSelf turns this process into program (same PID), for the headless service:
// systemd keeps supervising the same process, now running the new version.
func ReplaceSelf(program string, args []string, env []string) error {
	return syscall.Exec(program, append([]string{program}, args...), env)
}
