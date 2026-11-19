package update

import (
	"os/exec"
	"syscall"
)

// Relaunch starts program as its own process (not our child), with no console window,
// so it keeps running after we exit.
func Relaunch(program string, args ...string) error {
	cmd := exec.Command(program, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x00000008 | 0x00000200} // DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP
	return cmd.Start()
}
