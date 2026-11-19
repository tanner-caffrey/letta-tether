package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/tanner-caffrey/letta-tether/internal/ziputil"
)

// The bundled copy is Letta's own official build with Node bundled in, published on PyPI
// as a "wheel" (which is just a zip). We don't need Python: we download the zip for this
// computer, check its checksum against PyPI, and unpack it into AppDir(). Nothing else
// on the computer changes.

const pypiJSON = "https://pypi.org/pypi/letta/json"

// wheelTag is the part of the filename that matches this computer.
func wheelTag() (string, error) {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "windows/amd64":
		return "win_amd64", nil
	case "darwin/arm64":
		return "macosx_14_0_arm64", nil
	case "darwin/amd64":
		return "macosx_14_0_x86_64", nil
	case "linux/amd64":
		return "manylinux_2_28_x86_64", nil
	case "linux/arm64":
		return "manylinux_2_28_aarch64", nil
	}
	return "", fmt.Errorf("Letta doesn't make a bundled build for %s/%s yet", runtime.GOOS, runtime.GOARCH)
}

// Release is one downloadable build.
type Release struct {
	Version string
	URL     string
	SHA256  string
	Size    int64
}

// LatestBundled asks PyPI for the newest bundled build for this computer.
func LatestBundled(ctx context.Context) (Release, error) {
	tag, err := wheelTag()
	if err != nil {
		return Release{}, err
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", pypiJSON, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return Release{}, fmt.Errorf("PyPI said %s", resp.Status)
	}
	var meta struct {
		Info struct{ Version string } `json:"info"`
		URLs []struct {
			Filename string            `json:"filename"`
			URL      string            `json:"url"`
			Size     int64             `json:"size"`
			Digests  map[string]string `json:"digests"`
		} `json:"urls"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return Release{}, err
	}
	for _, u := range meta.URLs {
		if strings.HasSuffix(u.Filename, tag+".whl") {
			return Release{Version: meta.Info.Version, URL: u.URL, SHA256: u.Digests["sha256"], Size: u.Size}, nil
		}
	}
	return Release{}, fmt.Errorf("no Letta %s build for this computer (%s)", meta.Info.Version, tag)
}

// InstallBundled downloads rel and unpacks it, replacing any older bundled copy only once
// the new one is fully in place. progress(done, total) is called as bytes arrive.
func InstallBundled(ctx context.Context, rel Release, progress func(done, total int64)) error {
	dir := AppDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmpZip := filepath.Join(dir, "download.zip.part")
	defer os.Remove(tmpZip)
	if err := download(ctx, rel, tmpZip, progress); err != nil {
		return err
	}
	return swapIn(dir, tmpZip)
}

// swapIn unpacks zipPath next to the current copy, then swaps it in. If anything
// fails, the current copy is left exactly as it was and no half-built folders remain.
func swapIn(dir, zipPath string) error {
	staging := filepath.Join(dir, "letta.new")
	_ = os.RemoveAll(staging)
	if err := unzip(zipPath, staging); err != nil {
		_ = os.RemoveAll(staging)
		return fmt.Errorf("unpacking Letta: %w", err)
	}
	final := filepath.Join(dir, "letta")
	old := filepath.Join(dir, "letta.old")
	_ = os.RemoveAll(old)
	if _, err := os.Stat(final); err == nil {
		if err := os.Rename(final, old); err != nil {
			_ = os.RemoveAll(staging)
			return fmt.Errorf("couldn't move the old copy aside (is Letta still running?): %w", err)
		}
	}
	if err := os.Rename(staging, final); err != nil {
		_ = os.RemoveAll(staging)
		_ = os.Rename(old, final)
		return err
	}
	_ = os.RemoveAll(old)
	return nil
}

func download(ctx context.Context, rel Release, path string, progress func(done, total int64)) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", rel.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download failed: %s", resp.Status)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	total := rel.Size
	if total <= 0 {
		total = resp.ContentLength
	}
	var done int64
	buf := make([]byte, 256*1024)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := f.Write(buf[:n]); err != nil {
				return err
			}
			h.Write(buf[:n])
			done += int64(n)
			if progress != nil {
				progress(done, total)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	if rel.SHA256 != "" && hex.EncodeToString(h.Sum(nil)) != rel.SHA256 {
		return errors.New("the download didn't match Letta's published checksum, so it wasn't used")
	}
	return nil
}

// safeEntryName and unzip are kept as names here for the tests; the work is in ziputil.
var safeEntryName = ziputil.SafeName

func unzip(src, dest string) error {
	return ziputil.Unzip(src, dest, func(name string) bool {
		// the bundled node must be executable, and so must native modules
		return strings.Contains(name, "/_payload/bin/") || strings.HasSuffix(name, ".node")
	})
}
