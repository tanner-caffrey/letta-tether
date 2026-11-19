package update

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// Manager as a state machine: random checks/applies against a release feed that can
// be newer, older, the same, or unreachable, and installs that can fail.
func TestPropManager(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		var mu sync.Mutex
		tag := "v0.1.0"
		reachable, installOK := true, true
		installs, restarts := 0, 0
		m := &Manager{
			Version: "v0.1.0",
			Restart: func(bool) error { mu.Lock(); restarts++; mu.Unlock(); return nil },
			latest: func(context.Context) (Release, error) {
				mu.Lock()
				defer mu.Unlock()
				if !reachable {
					return Release{}, errors.New("offline")
				}
				return Release{Tag: tag}, nil
			},
			install: func(context.Context, Release, func(int64, int64)) error {
				mu.Lock()
				defer mu.Unlock()
				installs++
				if !installOK {
					return errors.New("disk full")
				}
				return nil
			},
		}
		t.Repeat(map[string]func(*rapid.T){
			"release": func(t *rapid.T) {
				mu.Lock()
				tag = rapid.SampledFrom([]string{"v0.0.9", "v0.1.0", "v0.1.1", "v0.2.0", "v0.2.0-beta"}).Draw(t, "tag")
				mu.Unlock()
			},
			"network":  func(t *rapid.T) { mu.Lock(); reachable = rapid.Bool().Draw(t, "up"); mu.Unlock() },
			"diskfull": func(t *rapid.T) { mu.Lock(); installOK = rapid.Bool().Draw(t, "ok"); mu.Unlock() },
			"check": func(t *rapid.T) {
				mu.Lock()
				up, cur := reachable, tag
				mu.Unlock()
				got := m.Check(context.Background())
				want := up && Newer(m.Version, cur)
				if got != want {
					t.Fatalf("Check() = %v with release %s reachable=%v", got, cur, up)
				}
				if s := m.Status(); up && (s.Available != "") != want {
					t.Fatalf("status shows available %q for release %s", s.Available, cur)
				}
			},
			"apply": func(t *rapid.T) {
				mu.Lock()
				before, cur, up, ok := installs, tag, reachable, installOK
				r0 := restarts
				mu.Unlock()
				err := m.Apply(context.Background())
				mu.Lock()
				did, rs := installs-before, restarts-r0
				mu.Unlock()
				should := up && Newer(m.Version, cur)
				if should != (did == 1) {
					t.Fatalf("release %s up=%v: installed %d times", cur, up, did)
				}
				if should && ok && (err != nil || rs != 1 || m.Status().Phase != "restarting") {
					t.Fatalf("good update: err=%v restarts=%d phase=%q", err, rs, m.Status().Phase)
				}
				if should && !ok && (err == nil || rs != 0 || m.Status().Phase != "failed") {
					t.Fatalf("failed install: err=%v restarts=%d phase=%q", err, rs, m.Status().Phase)
				}
				if !should && rs != 0 {
					t.Fatal("restarted without an update")
				}
				if m.Status().Phase == "restarting" {
					m.set(func(s *Status) { s.Phase = "" }) // the real app would be gone now
				}
			},
		})
	})
}

// Run (the real loop) never auto-installs when auto-update is off, and does when it's on.
func TestRunRespectsAutoSetting(t *testing.T) {
	for _, auto := range []bool{false, true} {
		installed := make(chan struct{}, 4)
		checked := make(chan struct{}, 4)
		m := &Manager{Version: "v0.1.0", Auto: func() bool { return auto }, First: time.Millisecond, Every: time.Hour,
			latest: func(context.Context) (Release, error) {
				select {
				case checked <- struct{}{}:
				default:
				}
				return Release{Tag: "v0.2.0"}, nil
			},
			install: func(context.Context, Release, func(int64, int64)) error { installed <- struct{}{}; return nil }}
		ctx, cancel := context.WithCancel(context.Background())
		go m.Run(ctx)
		select {
		case <-checked:
		case <-time.After(2 * time.Second):
			t.Fatal("Run never checked")
		}
		time.Sleep(50 * time.Millisecond)
		cancel()
		select {
		case <-installed:
			if !auto {
				t.Fatal("Run installed with auto-update off")
			}
		default:
			if auto {
				t.Fatal("Run didn't install with auto-update on")
			}
		}
		if s := m.Status(); !auto && s.Available != "v0.2.0" {
			t.Fatalf("with auto off, the update should still be shown as available, got %q", s.Available)
		}
	}
}

// A dev build never checks or updates.
func TestDevBuildNeverUpdates(t *testing.T) {
	called := false
	m := &Manager{Version: "dev", latest: func(context.Context) (Release, error) { called = true; return Release{Tag: "v9.0.0"}, nil }}
	if m.Check(context.Background()) || called {
		t.Fatal("dev build checked for updates")
	}
}

// Everyone watching hears that a download started, before any progress arrives.
func TestApplyAnnouncesDownloading(t *testing.T) {
	var phases []string
	m := &Manager{Version: "v0.1.0", OnChange: func(s Status) { phases = append(phases, s.Phase) },
		latest:  func(context.Context) (Release, error) { return Release{Tag: "v0.2.0"}, nil },
		install: func(context.Context, Release, func(int64, int64)) error { return nil }}
	m.Apply(context.Background())
	if len(phases) == 0 || phases[0] != "downloading" {
		t.Fatalf("first thing announced: %v", phases)
	}
}
