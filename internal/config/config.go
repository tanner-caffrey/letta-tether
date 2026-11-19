// Package config is Letta Tether's own small settings file. It never touches Letta's.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/tanner-caffrey/letta-tether/internal/runner"
)

type Config struct {
	SetupDone    bool   `json:"setup_done"`
	Install      string `json:"install"` // "existing" (theirs) or "bundled" (we download one)
	ComputerName string `json:"computer_name"`
	StartAtLogin bool   `json:"start_at_login"`
	// Paused: the person pressed Stop. Stay stopped across restarts until they press Start.
	Paused bool `json:"paused"`
	// AutoUpdate: install new Letta Tether versions by itself. A pointer so "never set"
	// (older settings files) can mean on, while an explicit false stays off.
	AutoUpdate *bool `json:"auto_update,omitempty"`
	// Channels: also run the messaging channels switched on in Letta (Telegram, Discord...).
	// Off by default: most people don't use them, and it changes nothing until they do.
	Channels bool `json:"channels,omitempty"`
}

// AutoUpdateOn says whether automatic updates are on (default: on).
func (c Config) AutoUpdateOn() bool { return c.AutoUpdate == nil || *c.AutoUpdate }

func path() string { return filepath.Join(runner.AppDir(), "config.json") }

// MaxNameRunes is the longest computer name we keep, in characters (not bytes).
const MaxNameRunes = 80

// CleanName trims a computer name to something safe to show and send: valid text,
// no control characters, at most MaxNameRunes characters, never empty.
func CleanName(s string) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > MaxNameRunes {
		s = strings.TrimSpace(string([]rune(s)[:MaxNameRunes]))
	}
	if s == "" {
		return DefaultName()
	}
	return s
}

// LogPath is where the listener's output goes.
func LogPath() string { return filepath.Join(runner.AppDir(), "letta-tether.log") }

// Load reads the settings. If they don't say which Letta to use yet, it picks the one
// the person already has, or downloading one if they don't.
func Load() Config {
	return loadFrom(runner.AppDir(), func() bool { _, ok := runner.FindExisting(); return ok })
}

func loadFrom(dir string, hasExisting func() bool) Config {
	c := Config{ComputerName: DefaultName(), StartAtLogin: true}
	if b, err := os.ReadFile(filepath.Join(dir, "config.json")); err == nil {
		var read Config
		if json.Unmarshal(b, &read) == nil {
			c = read
		}
	}
	c.ComputerName = CleanName(c.ComputerName)
	if c.Install != "existing" && c.Install != "bundled" {
		c.Install = ""
	}
	if c.Install == "" {
		// Use the Letta they already have, if any; otherwise download one.
		c.Install = "bundled"
		if hasExisting() {
			c.Install = "existing"
		}
	}
	return c
}

func (c Config) Save() error { return c.saveTo(runner.AppDir()) }

func (c Config) saveTo(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	p := filepath.Join(dir, "config.json")
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// DefaultName is "<this computer's name> (tether)", so it never reuses a name
// someone already runs `letta server` under by hand.
func DefaultName() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		h = "my computer"
	}
	h = strings.TrimSuffix(strings.ToLower(h), ".local")
	return h + " (tether)"
}
