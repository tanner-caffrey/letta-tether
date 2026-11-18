package autostart

import (
	"fmt"
	"os"
	"path/filepath"
)

// Mac: a LaunchAgent plist in ~/Library/LaunchAgents. Untested: no Mac to try it on.
func plistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", "com.letta-tether.plist")
}

func Set(on bool) error {
	if !on {
		err := os.Remove(plistPath())
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>com.letta-tether</string>
  <key>ProgramArguments</key><array><string>%s</string><string>--background</string></array>
  <key>RunAtLoad</key><true/>
</dict></plist>
`, exe)
	_ = os.MkdirAll(filepath.Dir(plistPath()), 0o755)
	return os.WriteFile(plistPath(), []byte(plist), 0o644)
}

func IsSet() bool { _, err := os.Stat(plistPath()); return err == nil }
