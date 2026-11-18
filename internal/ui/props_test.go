package ui

// The settings page only listens on 127.0.0.1, but any program or web page on the
// computer can reach that. The token is what keeps them out. Property: without the
// exact token, nothing can be read or changed, by any method, path, or header combo.

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/tanner-caffrey/letta-tether/internal/config"
	"github.com/tanner-caffrey/letta-tether/internal/runner"
)

func init() {
	// set once: each App starts a background version check that reads this
	fetchLatest = func(context.Context) (string, error) { return "0.0.1", nil }
	// and never touch the real start-at-login entry
	setAutostart = func(bool) error { return nil }
	hasAutostart = func() bool { return false }
}

type recorder struct{ applied, started, stopped int }

func startTestApp(t *testing.T) (*App, string, string, *recorder) {
	t.Helper()
	os.Setenv("XDG_CONFIG_HOME", t.TempDir())
	os.Setenv("APPDATA", t.TempDir())
	rec := &recorder{}
	a := &App{Runner: &runner.Runner{}, Apply: func(config.Config) error { rec.applied++; return nil }}
	u, err := a.Start(config.Config{ComputerName: "test", Install: "bundled"})
	if err != nil {
		t.Fatal(err)
	}
	pu, _ := url.Parse(u)
	return a, pu.Scheme + "://" + pu.Host + "/", pu.Query().Get("t"), rec
}

func TestPropTokenGuardsEverything(t *testing.T) {
	_, base, token, rec := startTestApp(t)
	rapid.Check(t, func(rt *rapid.T) {
		path := rapid.SampledFrom([]string{"", "api/status", "api/save", "api/start", "api/stop", "api/install", "api/signin", "api/nope"}).Draw(rt, "path")
		method := rapid.SampledFrom([]string{"GET", "POST", "PUT", "DELETE"}).Draw(rt, "method")
		// a wrong token: missing, empty, almost right, a prefix, the right one in the wrong place
		bad := rapid.SampledFrom([]string{"", "x", token[:len(token)-1], token + "x", strings.ToUpper(token), " " + token}).Draw(rt, "bad")
		inQuery := rapid.Bool().Draw(rt, "inQuery")
		inHeader := rapid.Bool().Draw(rt, "inHeader")
		// HTTP trims spaces around header values in transit, so " <token>" in a header
		// arrives as the real token. That's the same value, not a bypass.
		if inHeader && strings.TrimSpace(bad) == token {
			bad = "x" + token
		}
		u := base + path
		if inQuery {
			u += "?t=" + url.QueryEscape(bad)
		}
		req, _ := http.NewRequest(method, u, strings.NewReader(`{"computer_name":"pwned","setup_done":true}`))
		if inHeader {
			req.Header.Set("X-Tether-Token", bad)
		}
		before := *rec
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			rt.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode < 400 {
			rt.Fatalf("%s %s with token %q (query=%v header=%v) got %d: %.80s", method, path, bad, inQuery, inHeader, resp.StatusCode, body)
		}
		if *rec != before {
			rt.Fatalf("%s %s with a bad token changed something", method, path)
		}
	})
}

// A web page in the browser can send a POST with the token in the URL (if it somehow
// learned it) but can't set our header without permission. So changes need the header.
func TestChangesNeedTheHeader(t *testing.T) {
	_, base, token, rec := startTestApp(t)
	for _, p := range []string{"api/save", "api/start", "api/stop", "api/install", "api/signin"} {
		resp, err := http.Post(base+p+"?t="+token, "text/plain", strings.NewReader(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode < 400 {
			t.Errorf("POST %s with token only in URL got %d", p, resp.StatusCode)
		}
	}
	if rec.applied != 0 {
		t.Errorf("settings were applied without the header")
	}
}

// And the right token does work (so the tests above aren't passing because everything fails).
func TestRightTokenWorks(t *testing.T) {
	_, base, token, _ := startTestApp(t)
	resp, err := http.Get(base + "api/status?t=" + token)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status with the right token: %d", resp.StatusCode)
	}
	req, _ := http.NewRequest("POST", base+"api/save?t="+token, strings.NewReader(`{"computer_name":"ok"}`))
	req.Header.Set("X-Tether-Token", token)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("save with the right token: %d", resp.StatusCode)
	}
}
