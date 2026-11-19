package channels

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// Real `letta channels status` output (trimmed from Letta 0.34.65), with a warning first.
const statusSample = `2026-11-19T13:50:46.515Z [secrets] WARN: System secrets not available
{
  "telegram": { "configured": false, "runtimeInstalled": false },
  "fakechat": { "configured": true, "enabled": true, "runtimeInstalled": true,
    "accounts": [ { "accountId": "good", "displayName": "Fake (good)", "enabled": true },
                  { "accountId": "off", "displayName": "", "enabled": false } ] },
  "discord": { "configured": true, "enabled": true, "runtimeInstalled": true,
    "accounts": [ { "accountId": "acct-1", "displayName": "MyBot#0001", "enabled": true } ] }
}`

func TestParseStatus(t *testing.T) {
	list, err := parseStatus([]byte(statusSample))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, c := range list {
		ids = append(ids, c.ID)
	}
	if want := []string{"telegram", "discord", "fakechat"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("order %v, want built-ins first then custom: %v", ids, want)
	}
	if a := list[2].Accounts[1]; a.Name != "off" || a.Enabled {
		t.Fatalf("account without a display name: %+v", a)
	}
}

func writeAccounts(t interface{ Fatal(...any) }, dir, ch, body string) string {
	p := filepath.Join(dir, "channels", ch, "accounts.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// Flipping one account changes that account's `enabled` and nothing else: not the other
// accounts, not tokens, not fields we don't know about, not the file's permissions.
func TestPropSetEnabledTouchesOnlyThatSwitch(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		dir, _ := os.MkdirTemp("", "chan")
		defer os.RemoveAll(dir)
		n := rapid.IntRange(1, 4).Draw(t, "n")
		var accts []map[string]any
		for i := 0; i < n; i++ {
			a := map[string]any{
				"channel": "telegram", "accountId": rapid.SampledFrom([]string{"main", "a1", "bot-2", "x"}).Draw(t, "id") + string(rune('a'+i)),
				"enabled": rapid.Bool().Draw(t, "en"), "token": rapid.StringMatching(`[0-9]{6}:[A-Za-z0-9_-]{10,30}`).Draw(t, "tok"),
				"dmPolicy": "pairing", "allowedUsers": []any{"123", "*"}, "someFutureLettaField": map[string]any{"nested": []any{1.5, "two", nil}},
			}
			accts = append(accts, a)
		}
		body, _ := json.MarshalIndent(map[string]any{"accounts": accts, "topLevelThing": "keep me"}, "", "  ")
		p := writeAccounts(t, dir, "telegram", string(body))
		before, _ := os.Stat(p)
		pick := rapid.IntRange(0, n-1).Draw(t, "pick")
		on := rapid.Bool().Draw(t, "on")
		if err := SetEnabled(dir, "telegram", accts[pick]["accountId"].(string), on); err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		b, _ := os.ReadFile(p)
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatalf("file no longer JSON: %v", err)
		}
		if got["topLevelThing"] != "keep me" {
			t.Fatal("lost a top-level field")
		}
		ga := got["accounts"].([]any)
		for i := range accts {
			g := ga[i].(map[string]any)
			var want map[string]any
			wb, _ := json.Marshal(accts[i])
			json.Unmarshal(wb, &want)
			if i == pick {
				if g["enabled"] != on {
					t.Fatalf("switch not flipped")
				}
				delete(g, "updatedAt")
				want["enabled"] = on
			}
			if !reflect.DeepEqual(g, want) {
				t.Fatalf("account %d changed beyond its switch:\n got %v\nwant %v", i, g, want)
			}
		}
		// same permissions as before (tokens live in this file). On Unix that's the 0600 it
		// was written with; Windows has no Unix permissions and reports both as 0666.
		if info, _ := os.Stat(p); info.Mode().Perm() != before.Mode().Perm() {
			t.Fatalf("permissions changed from %v to %v", before.Mode().Perm(), info.Mode().Perm())
		}
	})
}

func TestSetEnabledRefuses(t *testing.T) {
	dir := t.TempDir()
	writeAccounts(t, dir, "telegram", `{"accounts":[{"accountId":"main","enabled":true}]}`)
	for _, c := range []struct{ ch, acct string }{{"../../etc", "main"}, {"Telegram", "main"}, {"telegram", "nope"}, {"discord", "main"}} {
		if err := SetEnabled(dir, c.ch, c.acct, false); err == nil {
			t.Errorf("SetEnabled(%q, %q) should fail", c.ch, c.acct)
		}
	}
	// damaged files are refused and left exactly as they were, whatever the damage
	for _, bad := range []string{`not json`, `{"accounts": "nope"}`, `{"accounts":[{"accountId":"main","enabled":true}]`, ``} {
		p := writeAccounts(t, dir, "slack", bad)
		if err := SetEnabled(dir, "slack", "main", true); err == nil {
			t.Errorf("damaged file %q accepted", bad)
		}
		if b, _ := os.ReadFile(p); string(b) != bad {
			t.Errorf("damaged file %q was changed to %q", bad, b)
		}
	}
}

func TestPairChecksInputBeforeAskingLetta(t *testing.T) {
	called := false
	l := Letter(func(ctx context.Context, args ...string) ([]byte, error) {
		called = true
		return []byte(`{"success": true}`), nil
	})
	for _, c := range []struct{ code, agent string }{{"", "agent-1"}, {"PHK8XY; rm -rf /", "agent-1"}, {"PHK8XY", ""}, {"PHK8XY", "--evil"}} {
		called = false
		if err := Pair(context.Background(), l, "telegram", "", c.code, c.agent); err == nil || called {
			t.Errorf("Pair(%q, %q): err=%v, asked Letta=%v", c.code, c.agent, err, called)
		}
	}
	var got []string
	l = func(ctx context.Context, args ...string) ([]byte, error) {
		got = args
		return []byte(`{"success": true}`), nil
	}
	if err := Pair(context.Background(), l, "telegram", "main", " phk8xy ", "agent-1"); err != nil {
		t.Fatal(err)
	}
	if s := strings.Join(got, " "); s != "channels pair --channel telegram --code PHK8XY --agent agent-1 --conversation default --account-id main" {
		t.Fatalf("asked Letta: %s", s)
	}
}

func TestHealthFromLogs(t *testing.T) {
	dir := t.TempDir()
	logs := filepath.Join(dir, "logs", "remote")
	os.MkdirAll(logs, 0o755)
	os.WriteFile(filepath.Join(logs, "2026-11-19T08-00-00-000Z.log"), []byte("[08:00] [ChannelGateway] [Channels] Failed to start fakechat/old: from before\n"), 0o644)
	w := Snapshot(dir)
	os.WriteFile(filepath.Join(logs, "2026-11-19T09-00-00-000Z.log"), []byte(strings.Join([]string{
		"[09:00] [ChannelGateway] [Channels] started adapter for fakechat/good",
		"[09:00] [ChannelGateway] [Channels] Failed to start fakechat/broken: fakechat: told to fail",
		"[09:00] something unrelated about fakechat/good failing",
	}, "\n")), 0o644)
	res := w.Read()
	if res["fakechat/good"] != "" || res["fakechat/broken"] != "fakechat: told to fail" {
		t.Fatalf("got %v", res)
	}
	if _, ok := res["fakechat/old"]; ok {
		t.Fatal("read a log from before we started")
	}
	list := []Channel{{ID: "fakechat", Accounts: []Account{{ID: "good"}, {ID: "broken"}, {ID: "off"}}}}
	res.Apply(list)
	if a := list[0].Accounts; !a[0].Running || a[1].Running || a[1].Problem == "" || a[2].Running || a[2].Problem != "" {
		t.Fatalf("applied: %+v", a)
	}
}

// Local-agent accounts are marked the same way Letta decides it: agentId, or
// binding.agentId, starting with "agent-local-".
func TestMarkLocal(t *testing.T) {
	dir := t.TempDir()
	writeAccounts(t, dir, "discord", `{"accounts":[
		{"accountId":"localbot","agentId":"agent-local-0000","enabled":true},
		{"accountId":"bound","binding":{"agentId":"agent-local-1111"},"enabled":true},
		{"accountId":"cloud","agentId":"agent-2222","enabled":true},
		{"accountId":"unbound","enabled":true}]}`)
	list := []Channel{
		{ID: "discord", Accounts: []Account{{ID: "localbot"}, {ID: "bound"}, {ID: "cloud"}, {ID: "unbound"}, {ID: "not-in-file"}}},
		{ID: "telegram", Accounts: []Account{{ID: "main"}}}, // no file at all
		{ID: "../evil", Accounts: []Account{{ID: "x"}}},
	}
	MarkLocal(dir, list)
	got := []bool{}
	for _, a := range list[0].Accounts {
		got = append(got, a.LocalAgent)
	}
	if !reflect.DeepEqual(got, []bool{true, true, false, false, false}) || list[1].Accounts[0].LocalAgent || list[2].Accounts[0].LocalAgent {
		t.Fatalf("marked: %v / %v / %v", got, list[1].Accounts[0].LocalAgent, list[2].Accounts[0].LocalAgent)
	}
}
