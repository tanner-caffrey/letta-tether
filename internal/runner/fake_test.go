package runner

// A fake Letta listener for tests. The test binary re-runs itself with
// TETHER_FAKE=1 and behaves like `letta server --lifecycle-output jsonl`,
// following a tiny script from TETHER_FAKE_SCRIPT:
//
//	print:<state>   print a lifecycle line
//	sleep:<ms>      wait
//	exit:<code>     exit
//	hang            wait forever (until killed)
//	child           start a grandchild that sleeps forever (like a shell the agent opened)
//
// Every fake (and grandchild) writes its pid into TETHER_FAKE_PIDS, so tests can
// check which are still alive without trusting the code under test.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	switch os.Getenv("TETHER_FAKE") {
	case "1":
		fakeListener()
		return
	case "child":
		recordPid()
		time.Sleep(time.Hour)
		return
	}
	os.Exit(m.Run())
}

func recordPid() {
	if dir := os.Getenv("TETHER_FAKE_PIDS"); dir != "" {
		_ = os.WriteFile(filepath.Join(dir, strconv.Itoa(os.Getpid())), nil, 0o644)
	}
}

func fakeListener() {
	recordPid()
	for _, step := range strings.Split(os.Getenv("TETHER_FAKE_SCRIPT"), ";") {
		op, arg, _ := strings.Cut(step, ":")
		switch op {
		case "print":
			fmt.Printf(`{"lettaLifecycleProtocol":1,"state":%q}`+"\n", arg)
		case "say":
			fmt.Println(arg)
		case "sleep":
			ms, _ := strconv.Atoi(arg)
			time.Sleep(time.Duration(ms) * time.Millisecond)
		case "exit":
			code, _ := strconv.Atoi(arg)
			os.Exit(code)
		case "hang":
			time.Sleep(time.Hour)
		case "child":
			c := exec.Command(os.Args[0], "-test.run=^$")
			c.Env = append(os.Environ(), "TETHER_FAKE=child")
			_ = c.Start()
		}
	}
	time.Sleep(time.Hour) // a real listener keeps running
}

// fakeInstall returns an Install that runs the fake with the given script.
type fataler interface{ Fatal(...any) }

func fakeInstall(t fataler, pids, script string) Install {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Install{Kind: "fake", Version: "0.0.0", Node: exe, Script: "-test.run=^$", Env: []string{
		"TETHER_FAKE=1", "TETHER_FAKE_SCRIPT=" + script, "TETHER_FAKE_PIDS=" + pids,
	}}
}

// alivePids lists recorded processes that are still running.
func alivePids(_ any, dir string) []int {
	ents, _ := os.ReadDir(dir)
	var out []int
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if processAlive(pid) {
			out = append(out, pid)
		} else {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
	return out
}

// waitFor polls until cond is true or the deadline passes.
func waitFor(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

func killAll(dir string) {
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if pid, err := strconv.Atoi(e.Name()); err == nil {
			if p, err := os.FindProcess(pid); err == nil {
				_ = p.Kill()
			}
		}
	}
}
