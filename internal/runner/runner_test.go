package runner

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseLifecycle(t *testing.T) {
	cases := map[string]State{
		`{"lettaLifecycleProtocol":1,"state":"connected"}`:    Connected,
		`{"lettaLifecycleProtocol":1,"state":"idle"}`:         Connected,
		`{"lettaLifecycleProtocol":1,"state":"working"}`:      Working,
		`{"lettaLifecycleProtocol":1,"state":"reconnecting"}`: Reconnecting,
		`{"lettaLifecycleProtocol":1,"state":"error"}`:        Error,
	}
	for line, want := range cases {
		got, ok := ParseLifecycle(line)
		if !ok || got != want {
			t.Errorf("%s: got %q %v, want %q", line, got, ok, want)
		}
	}
	for _, junk := range []string{"", "hello", `{"state":"connected"}`, `{"lettaLifecycleProtocol":1,"state":"party"}`} {
		if _, ok := ParseLifecycle(junk); ok {
			t.Errorf("%q should not parse", junk)
		}
	}
}

func TestCleanEnvDropsDebug(t *testing.T) {
	out := cleanEnv([]string{"PATH=/bin", "LETTA_DEBUG=1", "debug=x", "HOME=/h"})
	if len(out) != 2 || out[0] != "PATH=/bin" || out[1] != "HOME=/h" {
		t.Fatalf("got %v", out)
	}
}

func TestOlder(t *testing.T) {
	yes := [][2]string{{"0.34.26", "0.34.64"}, {"0.34.64", "0.35.0"}, {"0.9.9", "1.0.0"}, {"v0.34.1", "0.34.2"}}
	no := [][2]string{{"0.34.64", "0.34.64"}, {"0.35.0", "0.34.64"}, {"junk", "0.34.64"}, {"0.34", "0.34.64"}, {"", ""}}
	for _, c := range yes {
		if !Older(c[0], c[1]) {
			t.Errorf("%s should be older than %s", c[0], c[1])
		}
	}
	for _, c := range no {
		if Older(c[0], c[1]) {
			t.Errorf("%s should not be older than %s", c[0], c[1])
		}
	}
}

func TestCommandStartsInHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	cmd := Install{Node: "node", Script: "letta.js"}.command(context.Background(), "--version")
	if cmd.Dir != home {
		t.Fatalf("Dir = %q, want %q", cmd.Dir, home)
	}
}

// Regression: a prerelease used to compare equal to its release, so someone on
// 0.35.0-beta.1 was never told 0.35.0 was out.
func TestOlderPrereleaseBeforeRelease(t *testing.T) {
	if !Older("0.35.0-beta.1", "0.35.0") {
		t.Fatal("0.35.0-beta.1 should be older than 0.35.0")
	}
	if Older("0.35.0", "0.35.0-beta.1") {
		t.Fatal("0.35.0 should not be older than 0.35.0-beta.1")
	}
	if !Older("0.34.65", "0.35.0-beta.1") {
		t.Fatal("0.34.65 should be older than 0.35.0-beta.1")
	}
}

// Regression: Stop killed the listener but not processes it started (a shell the
// agent opened, a dev server...), which kept running after "Stop".
func TestStopKillsWholeProcessTree(t *testing.T) {
	dir := t.TempDir()
	defer killAll(dir)
	r := &Runner{Install: fakeInstall(t, dir, "print:connected;child;print:idle"), RetryWait: time.Hour}
	r.Start()
	if !waitFor(3*time.Second, func() bool { return len(alivePids(nil, dir)) == 2 }) {
		t.Fatalf("listener and child never both started: %v", alivePids(nil, dir))
	}
	r.Stop()
	if !waitFor(time.Second, func() bool { return len(alivePids(nil, dir)) == 0 }) {
		t.Fatalf("still alive after Stop: %v", alivePids(nil, dir))
	}
}

// Regression: an entry like `a\node//..` resolved to the destination folder itself on
// Mac/Linux and was written as a file there. Backslashes, drive letters and absolute
// names never appear in a real Letta build, so they're refused on every OS.
func TestUnzipRejectsOddNames(t *testing.T) {
	for _, name := range []string{`a\node//..`, `..\..\evil`, `C:\evil`, `c:evil`, `/etc/evil`, `a/../..`, `a/..`, `.`} {
		dir := t.TempDir()
		zipPath := filepath.Join(dir, "x.zip")
		f, _ := os.Create(zipPath)
		zw := zip.NewWriter(f)
		w, _ := zw.Create(name)
		w.Write([]byte("x"))
		zw.Close()
		f.Close()
		dest := filepath.Join(dir, "dest")
		if err := unzip(zipPath, dest); err == nil {
			t.Errorf("unzip accepted entry %q", name)
		}
		if st, err := os.Stat(dest); err == nil && !st.IsDir() {
			t.Errorf("entry %q turned the destination into a file", name)
		}
	}
}

// Regression: if Letta can't start at all (node uninstalled, file deleted), the
// state said "reconnecting" forever. It should say something is wrong, with why.
func TestStartFailureIsAnError(t *testing.T) {
	r := &Runner{Install: Install{Kind: "fake", Node: filepath.Join(t.TempDir(), "no-such-node"), Script: "x"}, RetryWait: time.Hour}
	r.Start()
	defer r.Stop()
	if !waitFor(3*time.Second, func() bool { s, _ := r.State(); return s == Error }) {
		s, d := r.State()
		t.Fatalf("state = %q (%s), want error", s, d)
	}
	if _, d := r.State(); d == "" {
		t.Fatal("error state should say why")
	}
}

// Same for a listener that keeps dying instantly: after a few tries in a row, say so.
func TestRepeatedInstantCrashIsAnError(t *testing.T) {
	dir := t.TempDir()
	defer killAll(dir)
	r := &Runner{Install: fakeInstall(t, dir, "exit:3"), RetryWait: 10 * time.Millisecond}
	r.Start()
	defer r.Stop()
	if !waitFor(5*time.Second, func() bool { s, _ := r.State(); return s == Error }) {
		s, d := r.State()
		t.Fatalf("state = %q (%s) after repeated instant crashes, want error", s, d)
	}
}

// Regression: sign-in assumed "already signed in" after 12 quiet seconds, so a slow
// first start (antivirus scanning node.exe) skipped the sign-in it actually needed.
func TestSignInWaitsForSlowStart(t *testing.T) {
	if testing.Short() {
		t.Skip("takes 13s")
	}
	exe, _ := os.Executable()
	in := Install{Kind: "fake", Node: exe, Script: "-test.run=^$", Env: []string{"TETHER_FAKE=1",
		"TETHER_FAKE_SCRIPT=sleep:13000;say:If it didn't open, visit: https://example/x;say:Your code: SLOW-1;hang"}}
	s := &SignIn{}
	go s.Run(in, "x")
	if !waitFor(20*time.Second, func() bool { p := s.Snapshot(); return p.Code != "" || p.Phase == "already" }) {
		t.Fatal("nothing happened")
	}
	if p := s.Snapshot(); p.Phase == "already" || p.Code != "SLOW-1" {
		t.Fatalf("slow Letta that needed a sign-in ended as phase %q code %q", p.Phase, p.Code)
	}
}
