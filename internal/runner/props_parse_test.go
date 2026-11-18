package runner

// Property tests for the small parsers: lifecycle lines and version comparison.
// Oracles are written independently of the production code (no shared helpers).

import (
	"fmt"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// ---------- versions ----------

// A version as three small numbers, kept small so equal parts are common.
func genVersion() *rapid.Generator[[3]int] {
	return rapid.Custom(func(t *rapid.T) [3]int {
		return [3]int{rapid.IntRange(0, 3).Draw(t, "maj"), rapid.IntRange(0, 40).Draw(t, "min"), rapid.IntRange(0, 70).Draw(t, "patch")}
	})
}

func vstr(v [3]int) string { return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2]) }

// independent oracle: lexicographic compare of the triple
func oracleOlder(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

func TestPropOlderMatchesTripleOrder(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a, b := genVersion().Draw(t, "a"), genVersion().Draw(t, "b")
		if got, want := Older(vstr(a), vstr(b)), oracleOlder(a, b); got != want {
			t.Fatalf("Older(%s, %s) = %v, want %v", vstr(a), vstr(b), got, want)
		}
	})
}

func TestPropOlderIsAStrictOrder(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a, b, c := vstr(genVersion().Draw(t, "a")), vstr(genVersion().Draw(t, "b")), vstr(genVersion().Draw(t, "c"))
		if Older(a, a) {
			t.Fatalf("%s older than itself", a)
		}
		if Older(a, b) && Older(b, a) {
			t.Fatalf("%s and %s each older than the other", a, b)
		}
		if Older(a, b) && Older(b, c) && !Older(a, c) {
			t.Fatalf("not transitive: %s < %s < %s", a, b, c)
		}
	})
}

// Prereleases follow semver: 0.35.0-beta.1 comes before 0.35.0, and after 0.34.x.
// So someone running a beta of the newest release does get told a final one is out.
func TestPropPrereleaseOrdersBeforeItsRelease(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		v := genVersion().Draw(t, "v")
		tag := rapid.SampledFrom([]string{"beta", "beta.1", "rc.2", "alpha", "next.0"}).Draw(t, "tag")
		pre := vstr(v) + "-" + tag
		if !Older(pre, vstr(v)) {
			t.Fatalf("%s should be older than %s", pre, vstr(v))
		}
		if Older(vstr(v), pre) {
			t.Fatalf("%s should not be older than %s", vstr(v), pre)
		}
		if Older(pre, pre) {
			t.Fatalf("%s older than itself", pre)
		}
		// against a different release, the prerelease tag doesn't matter
		w := genVersion().Draw(t, "w")
		if w != v && Older(pre, vstr(w)) != oracleOlder(v, w) {
			t.Fatalf("Older(%s, %s) should follow the release numbers", pre, vstr(w))
		}
	})
}

// Junk must never make us nag someone to update.
func TestPropOlderNeverNagsOnJunk(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		good := vstr(genVersion().Draw(t, "good"))
		junk := rapid.OneOf(
			rapid.String(),
			rapid.SampledFrom([]string{"", "latest", "0.34", "0.34.x", "1..2", ".1.2", "1.2.3.4", "-1.2.3", "１.２.３"}),
		).Draw(t, "junk")
		if parts(junk) != nil {
			t.Skip("generated a valid version")
		}
		if Older(junk, good) || Older(good, junk) {
			t.Fatalf("junk %q compared as a real version against %s", junk, good)
		}
	})
}

// ---------- lifecycle lines ----------

var lifecycleWant = map[string]State{
	"connected": Connected, "idle": Connected, "working": Working,
	"reconnecting": Reconnecting, "error": Error,
}

// The exact line letta prints, plus the decorations real output picks up on its way to us.
func TestPropLifecycleSurvivesRealWorldDecoration(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		state := rapid.SampledFrom([]string{"connected", "idle", "working", "reconnecting", "error"}).Draw(t, "state")
		body := fmt.Sprintf(`{"lettaLifecycleProtocol":1,"state":%q}`, state)
		switch rapid.IntRange(0, 4).Draw(t, "shape") {
		case 1: // Windows line ending (seen in real logs)
			body += "\r"
		case 2: // future protocol adds a field
			body = fmt.Sprintf(`{"lettaLifecycleProtocol":1,"state":%q,"since":"2026-11-18T00:00:00Z"}`, state)
		case 3: // keys in a different order, spaces
			body = fmt.Sprintf(`{ "state" : %q , "lettaLifecycleProtocol" : 1 }`, state)
		case 4: // leading/trailing whitespace
			body = "  " + body + " \t"
		}
		got, ok := ParseLifecycle(body)
		if !ok || got != lifecycleWant[state] {
			t.Fatalf("ParseLifecycle(%q) = %q, %v; want %q", body, got, ok, lifecycleWant[state])
		}
	})
}

// Anything that isn't a lifecycle line must be ignored, never turned into a state.
func TestPropLifecycleIgnoresEverythingElse(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		line := rapid.OneOf(
			rapid.String(),
			rapid.SampledFrom([]string{
				`{"state":"connected"}`,                            // no protocol marker
				`{"lettaLifecycleProtocol":0,"state":"connected"}`, // protocol 0
				`{"lettaLifecycleProtocol":1,"state":"Connected"}`, // wrong case
				`{"lettaLifecycleProtocol":1,"state":"rebooting"}`, // unknown state
				`{"lettaLifecycleProtocol":1}`,                     // no state
				`(node:29292) ExperimentalWarning: SQLite is an experimental feature`,
				`Error saving local project settings: Error: EPERM`,
				`{"type":"stream_delta","state":"connected"}`,
			}),
		).Draw(t, "line")
		if strings.Contains(line, `"lettaLifecycleProtocol":1`) && strings.Count(line, "{") == 1 {
			for s := range lifecycleWant {
				if strings.Contains(line, `"state":"`+s+`"`) {
					t.Skip("generated a real lifecycle line")
				}
			}
		}
		if got, ok := ParseLifecycle(line); ok {
			t.Fatalf("ParseLifecycle(%q) = %q, want ignored", line, got)
		}
	})
}
