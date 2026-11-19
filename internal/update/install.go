package update

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/tanner-caffrey/letta-tether/internal/ziputil"
)

// Target is what an update replaces: the program file (Windows, Linux) or the whole
// app bundle (Mac). Program is what gets run.
type Target struct {
	Replace string // file or .app folder to swap
	Program string // the executable inside it (same as Replace except on Mac)
}

// CurrentTarget works out what to replace for the running program.
func CurrentTarget() (Target, error) {
	exe, err := os.Executable()
	if err != nil {
		return Target{}, err
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	if runtime.GOOS == "darwin" {
		// .../Letta Tether.app/Contents/MacOS/letta-tether
		macos := filepath.Dir(exe)
		contents := filepath.Dir(macos)
		app := filepath.Dir(contents)
		if filepath.Base(macos) != "MacOS" || filepath.Base(contents) != "Contents" || !strings.HasSuffix(app, ".app") {
			return Target{}, errors.New("not running from an app bundle, so it can't update itself")
		}
		return Target{Replace: app, Program: exe}, nil
	}
	return Target{Replace: exe, Program: exe}, nil
}

// Install downloads rel, checks it, and swaps it in for t. Afterwards the files on disk
// are the new version; the running process is still the old one until it restarts.
func Install(ctx context.Context, rel Release, t Target, progress func(done, total int64)) error {
	dir := filepath.Dir(t.Replace)
	staging := filepath.Join(dir, ".letta-tether-update")
	_ = os.RemoveAll(staging)
	defer os.RemoveAll(staging)
	file, err := Download(ctx, rel, staging, progress)
	if err != nil {
		return err
	}
	newReplace, newProgram := file, file
	if strings.HasSuffix(file, ".zip.download") { // Mac: unpack the .app
		unpacked := filepath.Join(staging, "unpacked")
		if err := ziputil.Unzip(file, unpacked, func(name string) bool { return strings.Contains(name, "/Contents/MacOS/") }); err != nil {
			return err
		}
		newReplace = filepath.Join(unpacked, "Letta Tether.app")
		newProgram = filepath.Join(newReplace, "Contents", "MacOS", "letta-tether")
	}
	if err := Verify(newProgram, rel.Tag); err != nil {
		return err
	}
	// the new copy must sit on the same disk as the old one for the swap to be a rename
	return Swap(t.Replace, newReplace)
}

// CleanupAfterUpdate removes the previous version's backup. Call it at startup.
// The old version may still be exiting (Windows won't delete a running .exe), so it
// keeps trying for a little while in the background.
func CleanupAfterUpdate() {
	t, err := CurrentTarget()
	if err != nil {
		return
	}
	old := t.Replace + ".old"
	if _, err := os.Stat(old); err != nil {
		return
	}
	go func() {
		for i := 0; i < 30; i++ {
			if os.RemoveAll(old) == nil {
				if _, err := os.Stat(old); os.IsNotExist(err) {
					return
				}
			}
			time.Sleep(2 * time.Second)
		}
	}()
}
