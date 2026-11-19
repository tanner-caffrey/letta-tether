// Package channels lets Letta Tether run Letta's messaging channels (Telegram, Discord,
// Slack, WhatsApp, Signal, and custom ones) alongside the listener.
//
// Letta does the real work. What we rely on, all checked against Letta Code's source and
// tested with a fake channel:
//   - `letta channels status` lists every channel and account (JSON).
//   - LETTA_RESTORE_ENABLED_CHANNELS=1 makes `letta server` start every account whose
//     `enabled` is true, and a broken account only breaks itself. (Passing --channels by
//     name instead makes ONE broken account take the whole connection down.)
//   - So the on/off switch per account is Letta's own `enabled` field in
//     <letta config>/channels/<channel>/accounts.json.
//   - Start failures are only written to Letta's session log, <letta config>/logs/remote.
package channels

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Letter runs a letta subcommand and returns its stdout (the runner package provides it).
type Letter func(ctx context.Context, args ...string) ([]byte, error)

type Account struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	// Problem is the last start failure Letta logged for this account, "" if none.
	Problem string `json:"problem,omitempty"`
	// Running is true when Letta logged that it started this account in the current run.
	Running bool `json:"running"`
	// LocalAgent: this account answers with a local agent (agent-local-...). Letta only
	// runs those for local-agent servers, never alongside a cloud connection like ours,
	// so Tether can't run it.
	LocalAgent bool `json:"local_agent"`
}

type Channel struct {
	ID               string    `json:"id"`
	Configured       bool      `json:"configured"`
	RuntimeInstalled bool      `json:"runtime_installed"`
	Accounts         []Account `json:"accounts"`
}

// Builtin are the channels Letta ships with, in the order we show them.
var Builtin = []string{"telegram", "discord", "slack", "whatsapp", "signal"}

// List asks Letta which channels exist and how they're set up.
func List(ctx context.Context, letta Letter) ([]Channel, error) {
	out, err := letta(ctx, "channels", "status")
	if err != nil {
		return nil, err
	}
	return parseStatus(out)
}

func parseStatus(out []byte) ([]Channel, error) {
	// Letta may print warnings before the JSON.
	i := strings.IndexByte(string(out), '{')
	if i < 0 {
		return nil, errors.New("Letta didn't list any channels")
	}
	var raw map[string]struct {
		Configured       bool `json:"configured"`
		RuntimeInstalled bool `json:"runtimeInstalled"`
		Accounts         []struct {
			AccountID   string `json:"accountId"`
			DisplayName string `json:"displayName"`
			Enabled     bool   `json:"enabled"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(out[i:], &raw); err != nil {
		return nil, fmt.Errorf("couldn't read Letta's channel list: %w", err)
	}
	var list []Channel
	for id, c := range raw {
		ch := Channel{ID: id, Configured: c.Configured, RuntimeInstalled: c.RuntimeInstalled}
		for _, a := range c.Accounts {
			name := a.DisplayName
			if name == "" {
				name = a.AccountID
			}
			ch.Accounts = append(ch.Accounts, Account{ID: a.AccountID, Name: name, Enabled: a.Enabled})
		}
		list = append(list, ch)
	}
	order := map[string]int{}
	for i, id := range Builtin {
		order[id] = i + 1
	}
	sort.Slice(list, func(a, b int) bool {
		oa, ob := order[list[a].ID], order[list[b].ID]
		if oa == 0 {
			oa = 100
		}
		if ob == 0 {
			ob = 100
		}
		if oa != ob {
			return oa < ob
		}
		return list[a].ID < list[b].ID
	})
	return list, nil
}

// IsLocalAgent is Letta's own rule (isLocalAgentId in Letta Code).
func IsLocalAgent(agentID string) bool { return strings.HasPrefix(agentID, "agent-local-") }

// MarkLocal reads which agent each account is tied to from Letta's account files (the
// same fields Letta checks: agentId, or binding.agentId) and marks the local ones.
func MarkLocal(configDir string, list []Channel) {
	for ci := range list {
		c := &list[ci]
		if !safeID.MatchString(c.ID) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(configDir, "channels", c.ID, "accounts.json"))
		if err != nil {
			continue
		}
		var doc struct {
			Accounts []struct {
				AccountID string `json:"accountId"`
				AgentID   string `json:"agentId"`
				Binding   *struct {
					AgentID string `json:"agentId"`
				} `json:"binding"`
			} `json:"accounts"`
		}
		if json.Unmarshal(b, &doc) != nil {
			continue
		}
		bound := map[string]string{}
		for _, a := range doc.Accounts {
			id := strings.TrimSpace(a.AgentID)
			if id == "" && a.Binding != nil {
				id = strings.TrimSpace(a.Binding.AgentID)
			}
			bound[a.AccountID] = id
		}
		for ai := range c.Accounts {
			c.Accounts[ai].LocalAgent = IsLocalAgent(bound[c.Accounts[ai].ID])
		}
	}
}

// ConfigDir is Letta's own settings folder (where channels live).
func ConfigDir() string {
	if d := strings.TrimSpace(os.Getenv("LETTA_CONFIG_DIR")); d != "" && filepath.IsAbs(d) {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".letta")
}

var safeID = regexp.MustCompile(`^[a-z0-9_-]{1,64}$`)

// SetEnabled flips one account's `enabled` in Letta's accounts.json. Everything else in
// the file (tokens, Letta's own settings) is kept exactly as it was.
func SetEnabled(configDir, channelID, accountID string, on bool) error {
	if !safeID.MatchString(channelID) {
		return fmt.Errorf("not a channel name: %q", channelID)
	}
	path := filepath.Join(configDir, "channels", channelID, "accounts.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(b, &doc); err != nil {
		return fmt.Errorf("couldn't read %s: %w", path, err)
	}
	var accounts []map[string]json.RawMessage
	if err := json.Unmarshal(doc["accounts"], &accounts); err != nil {
		return fmt.Errorf("couldn't read the accounts in %s: %w", path, err)
	}
	found := false
	for _, a := range accounts {
		var id string
		_ = json.Unmarshal(a["accountId"], &id)
		if id == accountID {
			a["enabled"] = json.RawMessage(fmt.Sprint(on))
			a["updatedAt"], _ = json.Marshal(time.Now().UTC().Format(time.RFC3339Nano))
			found = true
		}
	}
	if !found {
		return fmt.Errorf("%s has no account %q", channelID, accountID)
	}
	doc["accounts"], _ = json.Marshal(accounts)
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	info, _ := os.Stat(path)
	mode := os.FileMode(0o600)
	if info != nil {
		mode = info.Mode().Perm()
	}
	tmp := path + ".tether-tmp"
	if err := os.WriteFile(tmp, append(out, '\n'), mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Install fetches a channel's extra pieces (e.g. Telegram's library). Quick, small.
func Install(ctx context.Context, letta Letter, channelID string) error {
	if !safeID.MatchString(channelID) {
		return fmt.Errorf("not a channel name: %q", channelID)
	}
	_, err := letta(ctx, "channels", "install", channelID)
	return err
}

var codeRe = regexp.MustCompile(`^[A-Za-z0-9]{4,12}$`)

// Pair approves the code a channel sent to someone, connecting that chat to an agent.
func Pair(ctx context.Context, letta Letter, channelID, accountID, code, agentID string) error {
	code = strings.TrimSpace(code)
	switch {
	case !safeID.MatchString(channelID):
		return fmt.Errorf("not a channel name: %q", channelID)
	case !codeRe.MatchString(code):
		return errors.New("that doesn't look like a pairing code (it's a few letters and numbers, like PHK8XY)")
	case !strings.HasPrefix(agentID, "agent-"):
		return errors.New("pick which agent should answer")
	}
	args := []string{"channels", "pair", "--channel", channelID, "--code", strings.ToUpper(code), "--agent", agentID, "--conversation", "default"}
	if accountID != "" {
		args = append(args, "--account-id", accountID)
	}
	out, err := letta(ctx, args...)
	if err != nil {
		return err
	}
	if !strings.Contains(string(out), `"success": true`) && !strings.Contains(string(out), `"success":true`) {
		return fmt.Errorf("Letta didn't accept the code: %s", firstLine(out))
	}
	return nil
}

func firstLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
