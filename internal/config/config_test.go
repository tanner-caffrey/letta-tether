package config

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Regression: names were cut at 80 bytes, which split emoji and accented letters in
// half (invalid text sent to Letta) and kept only 26 characters of a Japanese name.
func TestCleanNameCountsCharacters(t *testing.T) {
	long := strings.Repeat("a", 79) + "🦋"
	if got := CleanName(long); !utf8.ValidString(got) || got != long {
		t.Fatalf("80-character name with an emoji came back as %q", got)
	}
	jp := strings.Repeat("日本", 30) // 60 characters, 180 bytes
	if got := CleanName(jp); got != jp {
		t.Fatalf("60-character Japanese name came back with %d characters", utf8.RuneCountInString(got))
	}
	if got := CleanName(strings.Repeat("é", 200)); utf8.RuneCountInString(got) != MaxNameRunes || !utf8.ValidString(got) {
		t.Fatalf("long name not cut cleanly: %d characters", utf8.RuneCountInString(got))
	}
	if CleanName("  \t\n ") == "" {
		t.Fatal("blank name should fall back to the default")
	}
}
