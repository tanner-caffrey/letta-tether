// Package update checks GitHub for a newer Letta Tether and installs it.
//
// Safety, in order:
//   - Only this repo's releases, only over HTTPS, only non-prerelease ("latest").
//   - Every download is checked against SHA256SUMS.txt from the same release.
//   - The new program is swapped in with a rename, keeping the old one as a backup, and
//     is run once with --version before we trust it. If anything fails, the old program
//     stays exactly where it was.
//   - Never downgrades; never updates a dev build.
package update

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/tanner-caffrey/letta-tether/internal/runner"
)

// Repo is where releases come from.
const Repo = "tanner-caffrey/letta-tether"

// API is the GitHub API base. Tests swap it; end-to-end test builds set it at build
// time (-X .../update.testAPI=http://127.0.0.1:port). Release builds never set it.
var API = "https://api.github.com"

var testAPI string

func init() {
	if testAPI != "" {
		API = testAPI
	}
}

var client = &http.Client{Timeout: 5 * time.Minute}

// Release is what we need from a GitHub release.
type Release struct {
	Tag    string
	Notes  string
	URL    string // the release page, for "what's new"
	Assets map[string]string
}

// AssetName is the release file for this computer.
func AssetName() (string, error) {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "windows/amd64":
		return "letta-tether.exe", nil
	case "darwin/amd64", "darwin/arm64":
		return "letta-tether-mac.zip", nil
	case "linux/amd64":
		return "letta-tether-linux-amd64", nil
	case "linux/arm64":
		return "letta-tether-linux-arm64", nil
	}
	return "", fmt.Errorf("no Letta Tether build for %s/%s", runtime.GOOS, runtime.GOARCH)
}

// Latest asks GitHub for the newest real (non-prerelease) release.
func Latest(ctx context.Context) (Release, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", API+"/repos/"+Repo+"/releases/latest", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return Release{}, fmt.Errorf("GitHub said %s", resp.Status)
	}
	var r struct {
		Tag        string `json:"tag_name"`
		Body       string `json:"body"`
		HTMLURL    string `json:"html_url"`
		Prerelease bool   `json:"prerelease"`
		Draft      bool   `json:"draft"`
		Assets     []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r); err != nil {
		return Release{}, err
	}
	if r.Prerelease || r.Draft {
		return Release{}, errors.New("latest release is a prerelease")
	}
	out := Release{Tag: r.Tag, Notes: r.Body, URL: r.HTMLURL, Assets: map[string]string{}}
	for _, a := range r.Assets {
		out.Assets[a.Name] = a.URL
	}
	return out, nil
}

// Newer reports whether release tag is newer than the running version.
// A dev build (not a release) never updates itself.
func Newer(running, tag string) bool {
	if running == "" || running == "dev" {
		return false
	}
	return runner.Older(running, tag)
}

// Download fetches this computer's file from rel into dir and checks it against the
// release's SHA256SUMS.txt. It returns the downloaded file's path.
func Download(ctx context.Context, rel Release, dir string, progress func(done, total int64)) (string, error) {
	name, err := AssetName()
	if err != nil {
		return "", err
	}
	fileURL, sumsURL := rel.Assets[name], rel.Assets["SHA256SUMS.txt"]
	if fileURL == "" {
		return "", fmt.Errorf("release %s has no %s", rel.Tag, name)
	}
	if sumsURL == "" {
		return "", fmt.Errorf("release %s has no checksums, so it can't be verified", rel.Tag)
	}
	for _, u := range []string{fileURL, sumsURL} {
		if !strings.HasPrefix(u, "https://github.com/"+Repo+"/releases/download/") && !strings.HasPrefix(u, API) {
			return "", fmt.Errorf("unexpected download location: %s", u)
		}
	}
	want, err := expectedSum(ctx, sumsURL, name)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name+".download")
	if err := fetch(ctx, fileURL, path, want, progress); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

func expectedSum(ctx context.Context, url, name string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("checksums: %s", resp.Status)
	}
	sc := bufio.NewScanner(io.LimitReader(resp.Body, 1<<16))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name && len(f[0]) == 64 {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("no checksum listed for %s", name)
}

func fetch(ctx context.Context, url, path, wantSum string, progress func(done, total int64)) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download: %s", resp.Status)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	h := sha256.New()
	var done int64
	buf := make([]byte, 128*1024)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := f.Write(buf[:n]); err != nil {
				f.Close()
				return err
			}
			h.Write(buf[:n])
			done += int64(n)
			if progress != nil {
				progress(done, resp.ContentLength)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			return rerr
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != wantSum {
		return errors.New("the download didn't match the release's checksum, so it wasn't used")
	}
	return nil
}

// Verify runs a downloaded program once with --version and checks it says the
// expected version, so a broken file never replaces a working one.
func Verify(program, tag string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, program, "--version")
	hide(cmd)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("the new version didn't run: %w", err)
	}
	if got := strings.TrimSpace(string(out)); got != "letta-tether "+tag {
		return fmt.Errorf("the new version says %q, expected %s", got, tag)
	}
	return nil
}

// Swap puts newProgram (a file or an .app folder) where current is, keeping the old
// one as current+".old". If the swap fails halfway, the old one is put back.
// Windows allows renaming a running .exe (just not deleting it), which is what makes this work.
func Swap(current, newProgram string) error {
	old := current + ".old"
	_ = os.RemoveAll(old)
	if err := os.Rename(current, old); err != nil {
		return fmt.Errorf("couldn't move the old version aside: %w", err)
	}
	if err := os.Rename(newProgram, current); err != nil {
		_ = os.Rename(old, current)
		return fmt.Errorf("couldn't put the new version in place: %w", err)
	}
	return nil
}
