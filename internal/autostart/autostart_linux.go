package autostart

import (
	"fmt"
	"os"
	"path/filepath"
)

// Linux: an XDG autostart entry (desktop sessions). Headless boxes should use the systemd installer.
func desktopPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "autostart", "letta-tether.desktop")
}

func Set(on bool) error {
	if !on {
		err := os.Remove(desktopPath())
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	_ = os.MkdirAll(filepath.Dir(desktopPath()), 0o755)
	return os.WriteFile(desktopPath(), []byte(fmt.Sprintf("[Desktop Entry]\nType=Application\nName=Letta Tether\nExec=\"%s\" --background\nX-GNOME-Autostart-enabled=true\n", exe)), 0o644)
}

func IsSet() bool { _, err := os.Stat(desktopPath()); return err == nil }
