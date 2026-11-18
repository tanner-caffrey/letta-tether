package runner

// Sign-in reads Letta's human output. Generate the shapes that output can take
// (and the timings), and check the outcome against what actually happened.

import (
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"pgregory.net/rapid"
)

func signInInstall(t fataler, lines []string, end string) Install {
	// reuse the fake: "say:" lines print text verbatim
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := []string{}
	for _, l := range lines {
		script = append(script, "say:"+l)
	}
	script = append(script, end)
	return Install{Kind: "fake", Node: exe, Script: "-test.run=^$", Env: []string{
		"TETHER_FAKE=1", "TETHER_FAKE_SCRIPT=" + strings.Join(script, ";"),
	}}
}

// The sign-in flow as letta prints it, with plausible variations.
func TestPropSignInFindsLinkAndCode(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		url := "https://app.letta.com/oauth/device?user_code=ABCD-EFGH"
		code := "ABCD-EFGH"
		eol := rapid.SampledFrom([]string{"", "\r"}).Draw(t, "eol")
		lines := []string{"Log file: C:\\Users\\x\\.letta\\logs\\remote\\a.log", "No API key found. Starting OAuth login...", ""}
		order := rapid.Bool().Draw(t, "codeFirst")
		visit := "If it didn't open, visit: " + url
		your := "Your code: " + code
		if order {
			lines = append(lines, your, visit)
		} else {
			lines = append(lines, visit, your)
		}
		lines = append(lines, "", "Waiting for authorization...")
		for i := range lines {
			lines[i] += eol
		}
		approve := rapid.Bool().Draw(t, "approve")
		end := "hang"
		if approve {
			end = "say:Authenticated successfully.;hang"
		}
		s := &SignIn{}
		done := make(chan struct{})
		go func() { s.Run(signInInstall(t, lines, end), "x"); close(done) }()
		if !waitFor(5*time.Second, func() bool { snap := s.Snapshot(); return snap.URL != "" && snap.Code != "" }) {
			t.Fatalf("never showed link+code: phase %q", s.Snapshot().Phase)
		}
		snap := s.Snapshot()
		if snap.URL != url || snap.Code != code {
			t.Fatalf("got url %q code %q", snap.URL, snap.Code)
		}
		if approve {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("approved but never finished")
			}
			if p := s.Snapshot().Phase; p != "done" {
				t.Fatalf("phase %q after approval", p)
			}
		}
	})
}

// Letta is slow to start (first launch on a slow disk, antivirus scanning node.exe...)
// and then asks for a sign-in. We must not have already declared "you're signed in".
func TestSignInSlowStartStillShowsLink(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	s := &SignIn{}
	in := signInInstall(t, nil, "sleep:13000;say:If it didn't open, visit: https://app.letta.com/oauth/device?user_code=SLOW-1;say:Your code: SLOW-1;hang")
	done := make(chan struct{})
	go func() { s.Run(in, "x"); close(done) }()
	if !waitFor(20*time.Second, func() bool { return s.Snapshot().Code != "" || s.Snapshot().Phase == "already" }) {
		t.Fatalf("nothing happened: phase %q", s.Snapshot().Phase)
	}
	if snap := s.Snapshot(); snap.Phase == "already" || snap.Code != "SLOW-1" {
		t.Fatalf("slow Letta that needed a sign-in was reported as phase %q code %q", snap.Phase, snap.Code)
	}
	_ = runtime.GOOS
}
