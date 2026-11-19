// Package ziputil unpacks zip files safely: nothing can land outside the destination.
package ziputil

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// SafeName accepts only plain relative paths written with forward slashes, like every
// real build uses: no backslashes, drive letters, leading slash, or "." / ".." parts,
// and no empty parts except a trailing slash on a folder.
func SafeName(name string) bool {
	if name == "" || strings.ContainsAny(name, "\\:\x00") || strings.HasPrefix(name, "/") {
		return false
	}
	for _, p := range strings.Split(strings.TrimSuffix(name, "/"), "/") {
		if p == "" || p == "." || p == ".." {
			return false
		}
	}
	return true
}

// Unzip unpacks src into dest. needsExec says which entries must be executable even if
// the archive forgot (zips don't always keep the bit).
func Unzip(src, dest string, needsExec func(name string) bool) error {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer zr.Close()
	root, _ := filepath.Abs(dest)
	for _, f := range zr.File {
		if !SafeName(f.Name) {
			return fmt.Errorf("unsafe path in archive: %q", f.Name)
		}
		target := filepath.Join(root, filepath.FromSlash(f.Name))
		if !strings.HasPrefix(target, root+string(os.PathSeparator)) {
			return fmt.Errorf("unsafe path in archive: %q", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if f.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("links aren't allowed in archive: %q", f.Name)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		mode := f.Mode().Perm()
		if needsExec != nil && needsExec(f.Name) {
			mode |= 0o755
		}
		if mode == 0 {
			mode = 0o644
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(out, rc)
		rc.Close()
		out.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
