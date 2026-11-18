//go:build !windows

package runner

import (
	"os/exec"
	"syscall"
)

func exe(name string) string { return name }

// setupProcess puts the listener in its own process group, and makes cancelling
// it kill the whole group: Letta plus anything it started (shells, servers).
func setupProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

// afterStart has nothing to do on Unix: the group was set at spawn.
func afterStart(cmd *exec.Cmd) (cleanup func()) {
	return func() {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	}
}
