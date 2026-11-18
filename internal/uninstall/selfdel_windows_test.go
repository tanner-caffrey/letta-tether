package uninstall

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Deleting the running program on Windows (it can't delete itself while running):
// a copy of this test binary runs removeSelf on its own path, exits, and must be gone.
func TestRemoveSelfDeletesRunningExe(t *testing.T) {
	if os.Getenv("TETHER_SELFDEL") == "1" {
		exe, _ := os.Executable()
		if err := removeSelf(exe); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	me, _ := os.Executable()
	dir := t.TempDir()
	copyPath := filepath.Join(dir, "a copy with spaces.exe") // the path quoting has to hold
	b, _ := os.ReadFile(me)
	if err := os.WriteFile(copyPath, b, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(copyPath, "-test.run=^TestRemoveSelfDeletesRunningExe$")
	cmd.Env = append(os.Environ(), "TETHER_SELFDEL=1")
	if err := cmd.Run(); err != nil {
		t.Fatalf("copy failed: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(copyPath); os.IsNotExist(err) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("program still there 10s after it removed itself")
}
