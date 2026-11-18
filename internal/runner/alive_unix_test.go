//go:build !windows

package runner

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

// processAlive: exists and isn't a zombie. Reads /proc when there is one (Linux);
// it must not reap anything, since the runner is the one waiting on its child.
func processAlive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	if b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil {
		s := string(b)
		if i := strings.LastIndexByte(s, ')'); i >= 0 && len(s) > i+2 {
			return s[i+2] != 'Z'
		}
	}
	return true
}
