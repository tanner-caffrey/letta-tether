package channels

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// The test binary doubles as a fake Letta wizard, shaped like the real Telegram one:
// readline prompts without newlines, a token, a defaulted choice, a final "saved" line.
func TestMain(m *testing.M) {
	switch os.Getenv("FAKE_WIZARD") {
	case "telegram":
		fakeTelegramWizard()
		return
	case "stall":
		fmt.Print("Enter your bot token: ")
		time.Sleep(time.Hour)
		return
	}
	os.Exit(m.Run())
}

func fakeTelegramWizard() {
	in := bufio.NewReader(os.Stdin)
	ask := func(q string) string {
		fmt.Print(q)
		l, _ := in.ReadString('\n')
		return strings.TrimSpace(l)
	}
	fmt.Println("\n🤖 Telegram Bot Setup\n\nYou'll need a bot token from @BotFather on Telegram.")
	tok := ask("Enter your Telegram bot token: ")
	if tok == "" {
		fmt.Println("No token provided. Setup cancelled.")
		return
	}
	fmt.Println("\nValidating token...")
	if !strings.Contains(tok, ":") {
		fmt.Fprintln(os.Stderr, "✗ Invalid token: Call to 'getMe' failed! (401: Unauthorized)")
		return
	}
	fmt.Println("✓ Connected to @test_bot")
	pol := ask("DM policy [pairing]: ")
	if pol == "" {
		pol = "pairing"
	}
	agent := ask("\nAgent ID to bind this bot to (required for DM and @mention routing): ")
	fmt.Printf("\n✓ Telegram configured (dm policy: %s, token ends %s, agent %s)\n", pol, tok[len(tok)-2:], agent)
}

func fakeStarter(kind string) Starter {
	return func(ctx context.Context, channel string) *exec.Cmd {
		exe, _ := os.Executable()
		cmd := exec.CommandContext(ctx, exe, "-test.run=^$")
		cmd.Env = append(os.Environ(), "FAKE_WIZARD="+kind)
		return cmd
	}
}

func waitState(t *testing.T, s *Setup, ok func(SetupState) bool) SetupState {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if st := s.State(); ok(st) {
			return st
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out; state: %+v", s.State())
	return SetupState{}
}

func TestSetupRelaysAQuestionAtATime(t *testing.T) {
	var s Setup
	if err := s.Begin(fakeStarter("telegram"), "telegram"); err != nil {
		t.Fatal(err)
	}
	st := waitState(t, &s, func(st SetupState) bool { return st.Waiting })
	if st.Question != "Enter your Telegram bot token:" || !st.Secret {
		t.Fatalf("first question: %+v", st)
	}
	if !strings.Contains(strings.Join(st.Lines, "\n"), "@BotFather") {
		t.Fatalf("intro text not relayed: %q", st.Lines)
	}
	if err := s.Begin(fakeStarter("telegram"), "telegram"); err != ErrBusy {
		t.Fatalf("second setup at once: %v", err)
	}
	if err := s.Answer("123456:SECRET-abcdef"); err != nil {
		t.Fatal(err)
	}
	st = waitState(t, &s, func(st SetupState) bool { return st.Waiting && strings.HasPrefix(st.Question, "DM policy") })
	if st.Secret || st.Default != "pairing" {
		t.Fatalf("second question: %+v", st)
	}
	if strings.Contains(strings.Join(st.Lines, "\n"), "SECRET") {
		t.Fatal("the token was shown back in the transcript")
	}
	if err := s.Answer(""); err != nil { // "use default"
		t.Fatal(err)
	}
	st = waitState(t, &s, func(st SetupState) bool { return st.Waiting && st.AskAgent })
	if st.Secret || st.Default != "" {
		t.Fatalf("agent question: %+v", st)
	}
	s.Answer("agent-123")
	st = waitState(t, &s, func(st SetupState) bool { return st.Done })
	if !st.OK || !strings.Contains(strings.Join(st.Lines, "\n"), "dm policy: pairing, token ends ef, agent agent-123") {
		t.Fatalf("finished: %+v", st)
	}
	if err := s.Answer("late"); err != ErrNotAsked {
		t.Fatalf("answer after the end: %v", err)
	}
}

func TestSetupReportsLettasRefusal(t *testing.T) {
	var s Setup
	s.Begin(fakeStarter("telegram"), "telegram")
	waitState(t, &s, func(st SetupState) bool { return st.Waiting })
	s.Answer("not-a-token")
	st := waitState(t, &s, func(st SetupState) bool { return st.Done })
	if st.OK || !strings.Contains(st.Error, "Invalid token") || strings.Contains(st.Error, "exit status") {
		t.Fatalf("refused token should end not-ok, with Letta's reason shown: %+v", st)
	}
}

func TestSetupCancel(t *testing.T) {
	var s Setup
	s.Begin(fakeStarter("stall"), "telegram")
	waitState(t, &s, func(st SetupState) bool { return st.Waiting })
	s.Cancel()
	st := s.State()
	if !st.Done || st.OK || st.Running {
		t.Fatalf("after cancel: %+v", st)
	}
	// a new one can start right away, and the old one's output never leaks into it
	if err := s.Begin(fakeStarter("telegram"), "discord"); err != nil {
		t.Fatal(err)
	}
	st = waitState(t, &s, func(st SetupState) bool { return st.Waiting })
	if st.Channel != "discord" || st.Done {
		t.Fatalf("new setup: %+v", st)
	}
	s.Cancel()
}

func TestSetupRefusesOddChannelNames(t *testing.T) {
	var s Setup
	for _, ch := range []string{"", "../x", "Telegram", "a b", "x;rm"} {
		if err := s.Begin(fakeStarter("telegram"), ch); err == nil {
			s.Cancel()
			t.Errorf("started setup for %q", ch)
		}
	}
}

// Output arrives in arbitrary pieces; the relay must see the same lines and question
// however it's split.
func TestSetupFeedIsSplitIndependent(t *testing.T) {
	full := "intro\r\n\x1b[32m✓ ok\x1b[0m\nline two\nDM policy [pairing]: "
	var ref Setup
	ref.feed(full)
	for cut := 0; cut <= len(full); cut++ {
		var s Setup
		s.feed(full[:cut])
		s.feed(full[cut:])
		if strings.Join(s.lines, "|") != strings.Join(ref.lines, "|") || clean(s.partial) != clean(ref.partial) || s.waiting != ref.waiting {
			t.Fatalf("split at %d: %q / %q vs %q / %q", cut, s.lines, s.partial, ref.lines, ref.partial)
		}
	}
	if strings.Join(ref.lines, "|") != "intro|✓ ok|line two" || !ref.waiting {
		t.Fatalf("parsed: %q waiting=%v", ref.lines, ref.waiting)
	}
}

// Before Letta has printed anything the page still gets a list, never null (the page
// crashed on null and stopped following the setup).
func TestSetupStateLinesNeverNull(t *testing.T) {
	var s Setup
	if b, _ := json.Marshal(s.State()); !strings.Contains(string(b), `"lines":[]`) {
		t.Fatalf("empty state: %s", b)
	}
}

// The install step runs first, with its progress on the page; its failure ends setup
// with the reason; and Cancel during it stops before Letta's questions ever start.
func TestSetupInstallsFirst(t *testing.T) {
	var s Setup
	installed := make(chan struct{})
	prep := func(ctx context.Context, ch string) error { close(installed); return nil }
	if err := s.BeginPrepared(prep, fakeStarter("telegram"), "telegram"); err != nil {
		t.Fatal(err)
	}
	st := waitState(t, &s, func(st SetupState) bool { return st.Waiting })
	<-installed
	if !strings.Contains(strings.Join(st.Lines, "\n"), "Getting what this channel needs") {
		t.Fatalf("install step not shown: %q", st.Lines)
	}
	s.Cancel()

	var f Setup
	f.BeginPrepared(func(ctx context.Context, ch string) error { return fmt.Errorf("npm said no") }, fakeStarter("telegram"), "telegram")
	st = waitState(t, &f, func(st SetupState) bool { return st.Done })
	if st.OK || !strings.Contains(st.Error, "npm said no") {
		t.Fatalf("failed install: %+v", st)
	}

	var c Setup
	started := false
	slow := func(ctx context.Context, ch string) error { <-ctx.Done(); return ctx.Err() }
	c.BeginPrepared(slow, func(ctx context.Context, ch string) *exec.Cmd { started = true; return fakeStarter("telegram")(ctx, ch) }, "telegram")
	time.Sleep(50 * time.Millisecond)
	c.Cancel()
	st = waitState(t, &c, func(st SetupState) bool { return st.Done })
	time.Sleep(50 * time.Millisecond)
	if st.OK || st.Error != "Cancelled." || started {
		t.Fatalf("cancel during install: %+v, Letta started=%v", st, started)
	}
}
