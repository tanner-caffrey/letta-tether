package uninstall

import (
	"os"
	"os/exec"
	"syscall"
)

// Windows can't delete a running program, so hand it to a tiny hidden cmd that waits
// a few seconds (for us to exit) and then deletes the file.
//
// The command line is set by hand: Go's normal argument quoting escapes the inner
// quotes as \" which cmd.exe doesn't understand, so paths with spaces silently failed.
// cmd's rule: with /s, strip the outer quotes and run the rest as typed.
func removeSelf(exe string) error {
	if exe == "" {
		return nil
	}
	if _, err := os.Stat(exe); err != nil {
		return nil
	}
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000 | 0x00000008, // CREATE_NO_WINDOW | DETACHED_PROCESS: outlives us
		CmdLine:       `cmd.exe /s /c "ping -n 4 127.0.0.1 >nul & del /f /q "` + exe + `""`,
	}
	return cmd.Start()
}
