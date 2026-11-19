//go:build !windows

package update

import "os/exec"

func hide(cmd *exec.Cmd) {}
