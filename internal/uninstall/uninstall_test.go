package uninstall

import (
	"os"
	"path/filepath"
	"testing"

	"pgregory.net/rapid"
)

// Never touch the real start-at-login entry from a test.
func TestMain(m *testing.M) {
	setAutostart = func(bool) error { return nil }
	hasAutostart = func() bool { return false }
	os.Exit(m.Run())
}

// The guard is the one thing standing between a bug and someone's files.
func TestGuardOnlyAllowsOurFolder(t *testing.T) {
	home, _ := os.UserHomeDir()
	for _, bad := range []string{"", ".", "/", home, filepath.Join(home, ".letta"), filepath.Join(home, "Documents"),
		"letta-tether", filepath.Join(home, "letta-tether", ".."), filepath.Join(home, ".letta", "..")} {
		if guard(bad) == nil {
			t.Errorf("guard allowed %q", bad)
		}
	}
	if err := guard(filepath.Join(home, "AppData", "Roaming", "letta-tether")); err != nil {
		t.Errorf("guard refused the real folder: %v", err)
	}
}

func TestPropGuardNeverAllowsAnythingElse(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		parts := rapid.SliceOfN(rapid.SampledFrom([]string{"a", "letta-tether", "..", ".", "", ".letta", "Users", "home"}), 0, 5).Draw(t, "parts")
		p := string(filepath.Separator) + filepath.Join(parts...)
		if guard(p) == nil && filepath.Base(filepath.Clean(p)) != "letta-tether" {
			t.Fatalf("guard allowed %q", p)
		}
	})
}

// Run removes the app folder and leaves the neighbours (like ~/.letta) alone.
func TestRunRemovesOnlyOurs(t *testing.T) {
	root := t.TempDir()
	ours := filepath.Join(root, "letta-tether")
	theirs := filepath.Join(root, ".letta")
	os.MkdirAll(filepath.Join(ours, "letta", "bin"), 0o755)
	os.WriteFile(filepath.Join(ours, "config.json"), []byte("{}"), 0o644)
	os.MkdirAll(theirs, 0o755)
	os.WriteFile(filepath.Join(theirs, "settings.json"), []byte("{}"), 0o644)
	if err := Run(Plan{AppDir: ours}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ours); !os.IsNotExist(err) {
		t.Fatal("app folder still there")
	}
	if _, err := os.Stat(filepath.Join(theirs, "settings.json")); err != nil {
		t.Fatal("touched ~/.letta")
	}
}
