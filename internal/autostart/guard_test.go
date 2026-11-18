package autostart_test

// Guard: only these files may switch start-at-login for real. Anything else (above all
// a test) must go through the swappable setAutostart/hasAutostart seams. Added after a
// test run removed a real start-at-login entry on a real machine.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNothingElseCallsTheRealSwitch(t *testing.T) {
	allowed := map[string]bool{
		filepath.FromSlash("internal/ui/server.go"):           true,
		filepath.FromSlash("internal/uninstall/uninstall.go"): true,
	}
	root := filepath.Join("..", "..")
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") || strings.Contains(p, "internal"+string(filepath.Separator)+"autostart") {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		b, _ := os.ReadFile(p)
		if (strings.Contains(string(b), "autostart.Set") || strings.Contains(string(b), "autostart.IsSet")) && !allowed[rel] {
			t.Errorf("%s calls the real start-at-login switch directly", rel)
		}
		return nil
	})
}
