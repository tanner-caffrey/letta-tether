package update

// Tests run against a fake GitHub (httptest). The "new version" is this test binary
// itself, re-run with TETHER_FAKE_VERSION so it answers --version like a release would.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func TestMain(m *testing.M) {
	if v := os.Getenv("TETHER_FAKE_VERSION"); v != "" && len(os.Args) > 1 && os.Args[len(os.Args)-1] == "--version" {
		fmt.Println("letta-tether " + v)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type fakeGitHub struct {
	srv        *httptest.Server
	tag        string
	prerelease bool
	file       []byte // the asset for this OS
	sums       string // SHA256SUMS.txt; "" = missing
	noAsset    bool
	downloads  int
}

func newFake(t *testing.T, tag string, file []byte) *fakeGitHub {
	f := &fakeGitHub{tag: tag, file: file}
	name, _ := AssetName()
	sum := sha256.Sum256(file)
	f.sums = hex.EncodeToString(sum[:]) + "  " + name + "\n"
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			assets := []map[string]string{}
			if !f.noAsset {
				assets = append(assets, map[string]string{"name": name, "browser_download_url": f.srv.URL + "/dl/" + name})
			}
			if f.sums != "" {
				assets = append(assets, map[string]string{"name": "SHA256SUMS.txt", "browser_download_url": f.srv.URL + "/dl/SHA256SUMS.txt"})
			}
			json.NewEncoder(w).Encode(map[string]any{"tag_name": f.tag, "prerelease": f.prerelease, "html_url": "https://example/r", "body": "notes", "assets": assets})
		case strings.HasSuffix(r.URL.Path, "/SHA256SUMS.txt"):
			fmt.Fprint(w, f.sums)
		case strings.HasPrefix(r.URL.Path, "/dl/"):
			f.downloads++
			w.Write(f.file)
		default:
			http.NotFound(w, r)
		}
	}))
	old := API
	API = f.srv.URL
	t.Cleanup(func() { API = old; f.srv.Close() })
	return f
}

// a "release" that is really this test binary answering --version as tag
func fakeProgram(t *testing.T) []byte {
	exe, _ := os.Executable()
	b, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPropNewer(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a := fmt.Sprintf("v%d.%d.%d", rapid.IntRange(0, 2).Draw(t, "a1"), rapid.IntRange(0, 3).Draw(t, "a2"), rapid.IntRange(0, 3).Draw(t, "a3"))
		b := fmt.Sprintf("v%d.%d.%d", rapid.IntRange(0, 2).Draw(t, "b1"), rapid.IntRange(0, 3).Draw(t, "b2"), rapid.IntRange(0, 3).Draw(t, "b3"))
		if Newer(a, b) && Newer(b, a) {
			t.Fatalf("%s and %s both newer than each other", a, b)
		}
		if Newer(a, a) {
			t.Fatalf("%s newer than itself", a)
		}
		if Newer("dev", b) || Newer("", b) {
			t.Fatal("a dev build must never update itself")
		}
	})
}

func TestDownloadChecksSum(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("mac release is a zip; covered by install test")
	}
	f := newFake(t, "v9.9.9", []byte("the real thing"))
	rel, err := Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Download(context.Background(), rel, t.TempDir(), nil); err != nil {
		t.Fatalf("good download refused: %v", err)
	}
	f.file = []byte("tampered")
	dir := t.TempDir()
	if _, err := Download(context.Background(), rel, dir, nil); err == nil {
		t.Fatal("tampered download accepted")
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Fatalf("a refused download must leave nothing behind, have %d files", len(ents))
	}
}

func TestRefusesWithoutChecksums(t *testing.T) {
	f := newFake(t, "v9.9.9", []byte("x"))
	f.sums = ""
	rel, _ := Latest(context.Background())
	if _, err := Download(context.Background(), rel, t.TempDir(), nil); err == nil {
		t.Fatal("downloaded a release with no checksums")
	}
}

func TestPrereleaseIsNotOffered(t *testing.T) {
	f := newFake(t, "v9.9.9-beta", []byte("x"))
	f.prerelease = true
	if _, err := Latest(context.Background()); err == nil {
		t.Fatal("prerelease offered as an update")
	}
}

func TestRefusesForeignDownloadURL(t *testing.T) {
	rel := Release{Tag: "v9.9.9", Assets: map[string]string{}}
	name, _ := AssetName()
	rel.Assets[name] = "https://evil.example/" + name
	rel.Assets["SHA256SUMS.txt"] = "https://evil.example/SHA256SUMS.txt"
	if _, err := Download(context.Background(), rel, t.TempDir(), nil); err == nil || !strings.Contains(err.Error(), "unexpected download location") {
		t.Fatalf("followed a download link outside the repo: %v", err)
	}
}

// The whole thing: fake release, real download, real verify, real swap.
func TestInstallSwapsAndKeepsBackup(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("mac bundle path tested in CI on macOS separately")
	}
	t.Setenv("TETHER_FAKE_VERSION", "v9.9.9")
	newFake(t, "v9.9.9", fakeProgram(t))
	rel, err := Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	current := filepath.Join(dir, "letta tether.exe") // a space, like real install paths
	os.WriteFile(current, []byte("old version"), 0o755)
	if err := Install(context.Background(), rel, Target{Replace: current, Program: current}, nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(current + ".old"); string(b) != "old version" {
		t.Fatal("old version not kept as a backup")
	}
	if err := Verify(current, "v9.9.9"); err != nil {
		t.Fatalf("new version in place doesn't run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".letta-tether-update")); !os.IsNotExist(err) {
		t.Fatal("staging folder left behind")
	}
}

// A download that doesn't run (or says the wrong version) must never replace a working program.
func TestBrokenDownloadNeverReplaces(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("see above")
	}
	for name, setup := range map[string]func(f *fakeGitHub){
		"not a program":  func(f *fakeGitHub) { setSum(f, []byte("#!/bin/false\nnot really a program")) },
		"wrong version":  func(f *fakeGitHub) { t.Setenv("TETHER_FAKE_VERSION", "v1.0.0") },
		"truncated file": func(f *fakeGitHub) { setSum(f, f.file[:len(f.file)/2]) },
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("TETHER_FAKE_VERSION", "v9.9.9")
			f := newFake(t, "v9.9.9", fakeProgram(t))
			setup(f)
			rel, _ := Latest(context.Background())
			dir := t.TempDir()
			current := filepath.Join(dir, "letta-tether")
			os.WriteFile(current, []byte("old version"), 0o755)
			if err := Install(context.Background(), rel, Target{Replace: current, Program: current}, nil); err == nil {
				t.Fatal("broken update was installed")
			}
			if b, _ := os.ReadFile(current); string(b) != "old version" {
				t.Fatal("old version damaged")
			}
		})
	}
}

func setSum(f *fakeGitHub, file []byte) {
	f.file = file
	name, _ := AssetName()
	sum := sha256.Sum256(file)
	f.sums = hex.EncodeToString(sum[:]) + "  " + name + "\n"
}

// If the new version can't be moved into place, the old one goes back where it was.
func TestSwapRollsBackWhenNewCantMove(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "letta-tether")
	os.WriteFile(current, []byte("old version"), 0o755)
	missing := filepath.Join(dir, "not-there") // renaming this fails
	if err := Swap(current, missing); err == nil {
		t.Fatal("swap with a missing new version succeeded")
	}
	if b, err := os.ReadFile(current); err != nil || string(b) != "old version" {
		t.Fatalf("old version not restored: %v %q", err, b)
	}
}
