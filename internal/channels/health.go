package channels

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Watch finds out which channel accounts started and which failed, from Letta's session
// logs. Every Letta on the computer writes into the same folder, so it only reads log
// files that appeared after our Letta started (Snapshot first, then Read).
type Watch struct {
	dir    string
	before map[string]bool
	since  time.Time
}

// Snapshot remembers which logs already exist. Call it right before starting Letta.
func Snapshot(configDir string) *Watch {
	w := &Watch{dir: filepath.Join(configDir, "logs", "remote"), before: map[string]bool{}, since: time.Now().Add(-2 * time.Second)}
	ents, _ := os.ReadDir(w.dir)
	for _, e := range ents {
		w.before[e.Name()] = true
	}
	return w
}

var (
	startedRe = regexp.MustCompile(`\[Channels\] started adapter for ([a-z0-9_-]+)/(\S+)`)
	failedRe  = regexp.MustCompile(`\[Channels\] Failed to start ([a-z0-9_-]+)/(\S+?): (.*)$`)
)

// Result maps "channel/account" to what happened ("" = started fine).
type Result map[string]string

// Read scans new logs for channel start lines. Later lines win (a restart that worked
// clears an earlier failure).
func (w *Watch) Read() Result {
	res := Result{}
	if w == nil {
		return res
	}
	ents, _ := os.ReadDir(w.dir)
	var files []string
	for _, e := range ents {
		info, err := e.Info()
		if err != nil || e.IsDir() || w.before[e.Name()] || info.ModTime().Before(w.since) {
			continue
		}
		files = append(files, filepath.Join(w.dir, e.Name()))
	}
	// file names are timestamps, so sorted = oldest first
	sortStrings(files)
	for _, f := range files {
		scanFile(f, res)
	}
	return res
}

func scanFile(path string, res Result) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !strings.Contains(line, "[Channels]") {
			continue
		}
		if m := failedRe.FindStringSubmatch(line); m != nil {
			res[m[1]+"/"+m[2]] = strings.TrimSpace(m[3])
		} else if m := startedRe.FindStringSubmatch(line); m != nil {
			res[m[1]+"/"+m[2]] = ""
		}
	}
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// Apply fills in Running/Problem on a channel list from a Result.
func (r Result) Apply(list []Channel) {
	for ci := range list {
		for ai := range list[ci].Accounts {
			a := &list[ci].Accounts[ai]
			if msg, ok := r[list[ci].ID+"/"+a.ID]; ok {
				a.Running, a.Problem = msg == "", msg
			}
		}
	}
}
