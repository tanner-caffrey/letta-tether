package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tanner-caffrey/letta-tether/internal/channels"
	"github.com/tanner-caffrey/letta-tether/internal/config"
)

func post(t *testing.T, base, token, path string, body any) map[string]any {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", base+path+"?t="+token, strings.NewReader(string(b)))
	req.Header.Set("X-Tether-Token", token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return out
}

func TestChannelEndpoints(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("LETTA_CONFIG_DIR", cfgDir) // Letta's folder for this test, never the real ~/.letta
	acc := filepath.Join(cfgDir, "channels", "telegram", "accounts.json")
	os.MkdirAll(filepath.Dir(acc), 0o755)
	os.WriteFile(acc, []byte(`{"accounts":[{"channel":"telegram","accountId":"main","enabled":false,"token":"123:abc"}]}`), 0o600)

	a, base, token, rec := startTestApp(t)
	var asked [][]string
	a.letta = func(ctx context.Context, args ...string) ([]byte, error) {
		asked = append(asked, args)
		switch args[0] + " " + args[1] {
		case "channels status":
			b, _ := os.ReadFile(acc)
			on := strings.Contains(string(b), `"enabled": true`)
			return []byte(`{"telegram":{"configured":true,"runtimeInstalled":true,"accounts":[{"accountId":"main","displayName":"my bot","enabled":` + map[bool]string{true: "true", false: "false"}[on] + `}]},"slack":{"configured":false}}`), nil
		case "channels pair":
			return []byte(`{"success": true}`), nil
		case "agents list":
			return []byte(`{"options":{},"body":[{"id":"agent-2","name":"zed"},{"id":"agent-3","name":"Helper-sleeptime"},{"id":"agent-1","name":"Helper"}]}`), nil
		}
		return nil, nil
	}
	var applied []config.Config
	a.Apply = func(c config.Config) error { applied = append(applied, c); rec.applied++; return nil }

	// switching one account on: Letta's file flips, channels turn on, Letta restarts
	if r := post(t, base, token, "api/channel-switch", map[string]any{"channel": "telegram", "account": "main", "on": true}); r["ok"] != true {
		t.Fatalf("switch: %v", r)
	}
	b, _ := os.ReadFile(acc)
	if !strings.Contains(string(b), `"enabled": true`) || !strings.Contains(string(b), `"token": "123:abc"`) {
		t.Fatalf("accounts.json after switch: %s", b)
	}
	if len(applied) != 1 || !applied[0].Channels {
		t.Fatalf("expected one restart with channels on, got %+v", applied)
	}

	// a bad request changes nothing and restarts nothing
	before, _ := os.ReadFile(acc)
	if r := post(t, base, token, "api/channel-switch", map[string]any{"channel": "../telegram", "account": "main", "on": false}); r["ok"] == true {
		t.Fatal("bad channel name accepted")
	}
	if after, _ := os.ReadFile(acc); string(after) != string(before) || len(applied) != 1 {
		t.Fatal("a refused switch still changed something")
	}

	// pairing goes to Letta with the agent picked, then restarts Letta so it reads it
	if r := post(t, base, token, "api/channel-pair", map[string]any{"Channel": "telegram", "Account": "main", "Code": "phk8xy", "Agent": "agent-1"}); r["ok"] != true {
		t.Fatalf("pair: %v", r)
	}
	last := strings.Join(asked[len(asked)-1], " ")
	if !strings.Contains(last, "--code PHK8XY --agent agent-1") || len(applied) != 2 {
		t.Fatalf("pair asked Letta %q, restarts %d", last, len(applied))
	}

	// the list shows what Letta has and whether it actually started
	a.ChannelWatch = func() channels.Result { return channels.Result{"telegram/main": "401: Unauthorized"} }
	c := a.Config()
	c.Channels = true
	a.SetConfig(c)
	resp, _ := http.Get(base + "api/channels?t=" + token)
	var list struct {
		On       bool
		Channels []channels.Channel
	}
	json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	if !list.On || list.Channels[0].ID != "telegram" || list.Channels[0].Accounts[0].Problem != "401: Unauthorized" {
		t.Fatalf("list: %+v", list)
	}
}

func TestAgentPickerSkipsSleeptime(t *testing.T) {
	a, base, token, _ := startTestApp(t)
	a.letta = func(ctx context.Context, args ...string) ([]byte, error) {
		return []byte(`{"options":{},"body":[{"id":"agent-2","name":"zed"},{"id":"agent-3","name":"Helper-sleeptime"},{"id":"agent-1","name":"Helper"}]}`), nil
	}
	resp, _ := http.Get(base + "api/agents?t=" + token)
	var out struct{ Agents []struct{ ID, Name string } }
	json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	if len(out.Agents) != 2 || out.Agents[0].Name != "Helper" || out.Agents[1].Name != "zed" {
		t.Fatalf("picker list: %+v", out.Agents)
	}
}
