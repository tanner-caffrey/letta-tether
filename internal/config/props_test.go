package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"unicode/utf8"

	"pgregory.net/rapid"
)

// Names people actually type: accents, emoji, CJK, mixed with ASCII.
func genName() *rapid.Generator[string] {
	piece := rapid.SampledFrom([]string{"a", "Z", "9", " ", "-", "é", "ñ", "ü", "日本", "🦋", "👩‍💻", "🇺🇸", "e\u0301", "(", ")", "'"})
	return rapid.Custom(func(t *rapid.T) string {
		n := rapid.IntRange(0, 120).Draw(t, "n")
		s := ""
		for i := 0; i < n; i++ {
			s += piece.Draw(t, "p")
		}
		return s
	})
}

func TestPropCleanNameIsValidAndBounded(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		in := genName().Draw(t, "name")
		out := CleanName(in)
		if !utf8.ValidString(out) {
			t.Fatalf("CleanName(%q) = %q is not valid text", in, out)
		}
		if n := utf8.RuneCountInString(out); n > MaxNameRunes {
			t.Fatalf("CleanName(%q) has %d characters, max %d", in, n, MaxNameRunes)
		}
		if out == "" {
			t.Fatalf("CleanName(%q) is empty", in)
		}
		if CleanName(out) != out {
			t.Fatalf("not stable: %q -> %q -> %q", in, out, CleanName(out))
		}
	})
}

// Whatever is saved comes back the same, and a damaged file never stops the app.
func TestPropConfigRoundTripAndDamage(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		dir, _ := os.MkdirTemp("", "tether-cfg")
		defer os.RemoveAll(dir)
		c := Config{
			SetupDone:    rapid.Bool().Draw(t, "done"),
			Install:      rapid.SampledFrom([]string{"existing", "bundled"}).Draw(t, "inst"),
			ComputerName: CleanName(genName().Draw(t, "name")),
			StartAtLogin: rapid.Bool().Draw(t, "auto"),
			Paused:       rapid.Bool().Draw(t, "paused"),
		}
		switch rapid.IntRange(0, 2).Draw(t, "auto") {
		case 1:
			on := true
			c.AutoUpdate = &on
		case 2:
			off := false
			c.AutoUpdate = &off
		}
		if err := c.saveTo(dir); err != nil {
			t.Fatal(err)
		}
		if got := loadFrom(dir, func() bool { return false }); !reflect.DeepEqual(got, c) || got.AutoUpdateOn() != c.AutoUpdateOn() {
			t.Fatalf("round trip: saved %+v, loaded %+v", c, got)
		}
		// now damage it: truncate, garbage, wrong types
		b, _ := json.Marshal(c)
		damaged := rapid.SampledFrom([][]byte{
			b[:rapid.IntRange(0, len(b)).Draw(t, "cut")],
			[]byte("not json"),
			[]byte(`{"computer_name": 5, "install": ["x"]}`),
			[]byte(`{"install":"nonsense","computer_name":""}`),
			{},
		}).Draw(t, "damage")
		os.WriteFile(filepath.Join(dir, "config.json"), damaged, 0o644)
		got := loadFrom(dir, func() bool { return false })
		if got.ComputerName == "" {
			t.Fatalf("damaged file %q gave an empty name", damaged)
		}
		if got.Install != "existing" && got.Install != "bundled" {
			t.Fatalf("damaged file %q gave install %q", damaged, got.Install)
		}
	})
}

// Settings files from before auto-update existed mean "on"; an explicit off stays off.
func TestAutoUpdateDefaultsOnAndOffSticks(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"setup_done":true,"install":"existing","computer_name":"x"}`), 0o644)
	if !loadFrom(dir, func() bool { return false }).AutoUpdateOn() {
		t.Fatal("old settings file should mean auto-update on")
	}
	os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"computer_name":"x","install":"existing","auto_update":false}`), 0o644)
	if loadFrom(dir, func() bool { return false }).AutoUpdateOn() {
		t.Fatal("explicit off was turned back on")
	}
}
