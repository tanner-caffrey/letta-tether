package channels

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Setup runs Letta's own channel setup (`letta channels configure <channel>`) hidden in
// the background and relays it: what it prints goes to the settings page, the person's
// answers come back from it. Letta's wizards are plain question-and-answer (no terminal
// tricks), checked for all five channels, so this works for any of them.
//
// One setup at a time. Answers are never logged or kept: they go straight to Letta.
type Setup struct {
	mu      sync.Mutex
	channel string
	lines   []string // what Letta has printed (whole lines)
	partial string   // text since the last newline: usually the open question
	waiting bool     // Letta is waiting for an answer (partial ends like a question)
	secret  bool     // the open question is for a token or password
	done    bool
	ok      bool
	err     string
	stdin   io.WriteCloser
	cancel  context.CancelFunc
	gen     int
}

// SetupState is what the page shows.
type SetupState struct {
	Channel  string   `json:"channel"`
	Lines    []string `json:"lines"`
	Question string   `json:"question"`
	Waiting  bool     `json:"waiting"`
	Secret   bool     `json:"secret"`
	Default  string   `json:"default"` // "pairing" from "DM policy [pairing]: "
	// AskAgent: Letta wants an agent ID (Discord asks which agent the bot belongs to);
	// the page offers a picker instead of making someone paste an ID.
	AskAgent bool   `json:"ask_agent"`
	Done     bool   `json:"done"`
	OK       bool   `json:"ok"`
	Error    string `json:"error"`
	Running  bool   `json:"running"`
}

// Starter builds the hidden `letta channels configure <channel>` command.
type Starter func(ctx context.Context, channel string) *exec.Cmd

var (
	ErrBusy     = errors.New("a channel setup is already going; finish or cancel it first")
	ErrNotAsked = errors.New("Letta isn't waiting for an answer right now")
	secretRe    = regexp.MustCompile(`(?i)\b(token|secret|password|api key|passphrase)\b`)
	agentAskRe  = regexp.MustCompile(`(?i)\bagent id\b`)
	defaultRe   = regexp.MustCompile(`\[([^\]\n]{1,40})\]\s*:?\s*$`)
	// a question ends with ": " or "? " or "] " and no newline (readline prompts)
	questionRe = regexp.MustCompile(`[:?\]]\s*$`)
	ansiRe     = regexp.MustCompile("\x1b\\[[0-9;?]*[A-Za-z]")
)

const maxLines = 400

// Preparer installs a channel's extra pieces before its questions start. Letta's own
// setup installs them itself, but its installer (npm) inherits the setup's input, which we
// hold open for answers, and on Windows it waits on that and hangs. So we install first,
// with no input attached, and Letta's setup then finds them already there.
type Preparer func(ctx context.Context, channel string) error

// Begin starts setup for channel. Only one runs at a time.
func (s *Setup) Begin(start Starter, channel string) error {
	return s.BeginPrepared(nil, start, channel)
}

// BeginPrepared is Begin with an install step first (see Preparer).
func (s *Setup) BeginPrepared(prepare Preparer, start Starter, channel string) error {
	if !safeID.MatchString(channel) {
		return errors.New("not a channel name")
	}
	s.mu.Lock()
	if s.cancel != nil && !s.done {
		s.mu.Unlock()
		return ErrBusy
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	s.gen++ // output from an older setup is ignored from here on
	gen := s.gen
	s.channel, s.stdin, s.cancel = channel, nil, cancel
	s.lines, s.partial = nil, ""
	s.waiting, s.secret, s.done, s.ok, s.err = false, false, false, false, ""
	s.mu.Unlock()

	go s.run(ctx, cancel, gen, prepare, start, channel)
	return nil
}

func (s *Setup) finish(gen int, ok bool, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gen != gen || s.done {
		return
	}
	s.flushPartial()
	s.done, s.waiting, s.ok, s.err = true, false, ok, msg
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *Setup) say(gen int, line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gen == gen {
		s.addLine(line)
	}
}

func (s *Setup) run(ctx context.Context, cancel context.CancelFunc, gen int, prepare Preparer, start Starter, channel string) {
	if prepare != nil {
		s.say(gen, "Getting what this channel needs (one time; can take a minute or two)...")
		if err := prepare(ctx, channel); err != nil {
			if ctx.Err() != nil {
				s.finish(gen, false, timeoutOrCancel(ctx))
			} else {
				s.finish(gen, false, "Couldn't install what this channel needs: "+err.Error())
			}
			return
		}
		s.say(gen, "Done.")
		s.say(gen, "")
	}
	cmd := start(ctx, channel)
	stdin, err1 := cmd.StdinPipe()
	stdout, err2 := cmd.StdoutPipe()
	if err := errors.Join(err1, err2); err != nil {
		s.finish(gen, false, err.Error())
		return
	}
	cmd.Stderr = cmd.Stdout // errors are part of the conversation ("Invalid token")
	if err := cmd.Start(); err != nil {
		s.finish(gen, false, "Couldn't start Letta's setup: "+err.Error())
		return
	}
	s.mu.Lock()
	if s.gen != gen || s.done { // cancelled while installing
		s.mu.Unlock()
		cancel()
		_ = cmd.Wait()
		return
	}
	s.stdin = stdin
	s.mu.Unlock()

	read := make(chan struct{})
	go func() { s.read(gen, stdout); close(read) }()
	func() {
		<-read // everything Letta said, including its last words, before Wait closes the pipe
		err := cmd.Wait()
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.gen != gen {
			return
		}
		s.flushPartial()
		s.done, s.waiting = true, false
		s.ok = err == nil && !s.failedText()
		switch {
		case s.ok:
		case ctx.Err() != nil:
			s.err = timeoutOrCancel(ctx)
		default:
			// Letta's own last words say why ("Invalid token: ... 401"); an exit code doesn't
			s.err = "Letta's setup didn't finish: " + s.lastWords()
		}
		cancel()
	}()
}

func timeoutOrCancel(ctx context.Context) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "Setup took too long (30 minutes) and was stopped."
	}
	return "Cancelled."
}

// failedText: Letta's wizards exit 0 even when they give up ("Setup cancelled",
// "Invalid token"), so the last lines decide.
func (s *Setup) failedText() bool {
	tail := strings.ToLower(strings.Join(lastN(s.lines, 4), "\n"))
	for _, w := range []string{"cancelled", "canceled", "invalid", "failed", "error"} {
		if strings.Contains(tail, w) {
			return true
		}
	}
	return false
}

// lastWords is Letta's last non-empty line, trimmed of its ✗ / ✓ marks.
func (s *Setup) lastWords() string {
	for i := len(s.lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(strings.TrimLeft(s.lines[i], "✗✓ ")); l != "" {
			if len(l) > 200 {
				l = l[:200]
			}
			return l
		}
	}
	return "it stopped without saying why"
}

func lastN(l []string, n int) []string {
	if len(l) > n {
		return l[len(l)-n:]
	}
	return l
}

func (s *Setup) read(gen int, r io.Reader) {
	br := bufio.NewReader(r)
	buf := make([]byte, 4096)
	for {
		n, err := br.Read(buf)
		if n > 0 {
			s.mu.Lock()
			if s.gen == gen {
				s.feed(string(buf[:n]))
			}
			s.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// feed takes raw output: whole lines are kept, the rest is the (maybe) open question.
func (s *Setup) feed(chunk string) {
	// keep raw text until a line is complete: a color code or \r\n can be cut in half
	// across two pieces of output
	text := s.partial + chunk
	parts := strings.Split(text, "\n")
	for _, l := range parts[:len(parts)-1] {
		s.addLine(clean(l))
	}
	s.partial = parts[len(parts)-1]
	q := clean(s.partial)
	s.waiting = q != "" && questionRe.MatchString(q)
	s.secret = s.waiting && secretRe.MatchString(q)
}

func clean(l string) string {
	return strings.TrimRight(ansiRe.ReplaceAllString(l, ""), "\r")
}

func (s *Setup) addLine(l string) {
	l = strings.TrimRight(l, " \r")
	if !utf8.ValidString(l) {
		l = strings.ToValidUTF8(l, "?")
	}
	s.lines = append(s.lines, l)
	if len(s.lines) > maxLines {
		s.lines = s.lines[len(s.lines)-maxLines:]
	}
}

func (s *Setup) flushPartial() {
	if strings.TrimSpace(clean(s.partial)) != "" {
		s.addLine(clean(s.partial))
	}
	s.partial = ""
}

// Answer sends one answer to the open question.
func (s *Setup) Answer(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stdin == nil || s.done || !s.waiting {
		return ErrNotAsked
	}
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r", ""), "\n", " ")
	// show the question and the answer in the transcript, but never a secret
	shown := text
	if s.secret && text != "" {
		shown = "••••••••"
	}
	s.addLine(clean(s.partial) + shown)
	s.partial, s.waiting, s.secret = "", false, false
	_, err := io.WriteString(s.stdin, text+"\n")
	return err
}

// Cancel stops the setup. Letta's wizards only write their settings at the very end, so
// stopping midway leaves nothing behind.
func (s *Setup) Cancel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	if !s.done {
		s.flushPartial()
		s.done, s.waiting, s.ok, s.err = true, false, false, "Cancelled."
	}
}

// State is a copy for the page.
func (s *Setup) State() SetupState {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := SetupState{Channel: s.channel, Lines: append([]string{}, s.lines...), Waiting: s.waiting, Secret: s.secret,
		Done: s.done, OK: s.ok, Error: s.err, Running: s.cancel != nil && !s.done}
	if s.waiting {
		q := clean(s.partial)
		st.Question = strings.TrimSpace(q)
		st.AskAgent = agentAskRe.MatchString(q)
		if m := defaultRe.FindStringSubmatch(q); m != nil {
			st.Default = m[1]
		}
	}
	return st
}
