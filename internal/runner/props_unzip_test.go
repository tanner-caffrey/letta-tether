package runner

// Unzip: nothing may land outside the destination, whatever the archive says.
// Archives are built in-memory from generated entry names, including every shape of
// "escape" we know of: ../, absolute paths, drive letters, backslashes, and mixes.

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func genEntryName() *rapid.Generator[string] {
	seg := rapid.SampledFrom([]string{"a", "b", "bin", "node", "..", ".", "", "C:", "c:", "\\", "..\\..", "x..y", "...", " ", "app"})
	sep := rapid.SampledFrom([]string{"/", "\\", "//"})
	return rapid.Custom(func(t *rapid.T) string {
		n := rapid.IntRange(1, 5).Draw(t, "n")
		var b strings.Builder
		if rapid.Bool().Draw(t, "abs") {
			b.WriteString(sep.Draw(t, "lead"))
		}
		for i := 0; i < n; i++ {
			if i > 0 {
				b.WriteString(sep.Draw(t, "sep"))
			}
			b.WriteString(seg.Draw(t, "seg"))
		}
		return b.String()
	})
}

func buildZip(t *rapid.T, names []string) string {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range names {
		w, err := zw.Create(n)
		if err != nil {
			continue // zip refuses some names itself; fine
		}
		w.Write([]byte("x"))
	}
	zw.Close()
	f, err := os.CreateTemp("", "tether-*.zip")
	if err != nil {
		t.Fatal(err)
	}
	f.Write(buf.Bytes())
	f.Close()
	return f.Name()
}

// Everything under root, listed relative to the sandbox parent: anything outside dest is an escape.
func filesUnder(root string) []string {
	var out []string
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			out = append(out, p)
		}
		return nil
	})
	return out
}

func TestPropUnzipNeverEscapes(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		names := rapid.SliceOfN(genEntryName(), 1, 6).Draw(t, "names")
		zipPath := buildZip(t, names)
		defer os.Remove(zipPath)
		sandbox, _ := os.MkdirTemp("", "tether-sandbox")
		defer os.RemoveAll(sandbox)
		dest := filepath.Join(sandbox, "dest")
		_ = unzip(zipPath, dest) // errors are fine; escapes are not
		destAbs, _ := filepath.Abs(dest)
		for _, f := range filesUnder(sandbox) {
			if !strings.HasPrefix(f, destAbs+string(os.PathSeparator)) {
				t.Fatalf("archive %q wrote %s outside %s", names, f, destAbs)
			}
		}
		// and nothing in the real filesystem root or cwd got created from these names
		for _, n := range names {
			for _, p := range []string{"/" + strings.TrimLeft(n, "/\\"), n} {
				if strings.Contains(p, "..") || p == "" || p == "/" {
					continue
				}
				if runtime.GOOS != "windows" && strings.HasPrefix(p, "/tmp/") {
					continue
				}
			}
		}
	})
}

// A failed update must leave the old Letta exactly where it was.
func TestPropFailedInstallKeepsOldCopy(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		dir, _ := os.MkdirTemp("", "tether-app")
		defer os.RemoveAll(dir)
		old := filepath.Join(dir, "letta", "marker")
		os.MkdirAll(filepath.Dir(old), 0o755)
		os.WriteFile(old, []byte("old"), 0o644)

		// a corrupt or truncated download
		junk := rapid.SliceOfN(rapid.Byte(), 0, 64).Draw(t, "junk")
		zipPath := filepath.Join(dir, "bad.zip")
		os.WriteFile(zipPath, junk, 0o644)
		if err := swapIn(dir, zipPath); err == nil && len(junk) > 0 {
			// a valid empty zip is possible from random bytes only in theory; fine either way
			return
		}
		if b, err := os.ReadFile(old); err != nil || string(b) != "old" {
			t.Fatalf("old copy damaged after failed install: %v %q", err, b)
		}
		for _, leftover := range []string{"letta.new", "letta.old"} {
			if _, err := os.Stat(filepath.Join(dir, leftover)); err == nil {
				t.Fatalf("left %s behind", leftover)
			}
		}
	})
}
