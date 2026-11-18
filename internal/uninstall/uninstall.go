// Package uninstall removes Letta Tether and everything it added, and nothing else.
//
// Removed: the start-at-login entry, Letta Tether's own folder (settings, log, and the
// Letta it downloaded, if any), and the app itself where possible.
// Kept: the person's Letta sign-in and settings (~/.letta), any Letta they installed
// themselves, and their agents. Those were never ours.
package uninstall

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tanner-caffrey/letta-tether/internal/autostart"
)

// setAutostart / hasAutostart are the real start-at-login switch. Tests replace them so a
// test run can never remove someone's real entry (one did, once, on a real machine).
var (
	setAutostart = autostart.Set
	hasAutostart = autostart.IsSet
)

// Plan says what will be removed, so the page can show it before anyone clicks.
type Plan struct {
	AppDir    string   // Letta Tether's own folder
	Autostart bool     // whether a start-at-login entry exists
	Exe       string   // the program itself
	Keeps     []string // things we deliberately leave alone
}

func MakePlan(appDir string) Plan {
	exe, _ := os.Executable()
	home, _ := os.UserHomeDir()
	return Plan{
		AppDir:    appDir,
		Autostart: hasAutostart(),
		Exe:       exe,
		Keeps:     []string{filepath.Join(home, ".letta") + " (your Letta sign-in and settings)", "any Letta you installed yourself", "your agents (they live in your Letta account)"},
	}
}

// Run removes everything in the plan. The caller must have stopped Letta first
// (Windows won't delete files that are in use).
func Run(p Plan) error {
	if err := guard(p.AppDir); err != nil {
		return err
	}
	var errs []error
	if err := setAutostart(false); err != nil {
		errs = append(errs, fmt.Errorf("start-at-login entry: %w", err))
	}
	if err := os.RemoveAll(p.AppDir); err != nil {
		errs = append(errs, fmt.Errorf("%s: %w", p.AppDir, err))
	}
	if err := removeSelf(p.Exe); err != nil {
		errs = append(errs, fmt.Errorf("the app itself: %w", err))
	}
	return errors.Join(errs...)
}

// guard refuses to delete anything that isn't plainly our own folder. This is the
// one place a bug could delete someone's files, so it's strict on purpose.
func guard(dir string) error {
	clean := filepath.Clean(dir)
	if !filepath.IsAbs(clean) || filepath.Base(clean) != "letta-tether" {
		return fmt.Errorf("refusing to delete %q: not Letta Tether's folder", dir)
	}
	home, _ := os.UserHomeDir()
	for _, bad := range []string{home, filepath.Join(home, ".letta"), filepath.Dir(home), string(filepath.Separator)} {
		if bad != "" && strings.EqualFold(clean, filepath.Clean(bad)) {
			return fmt.Errorf("refusing to delete %q", dir)
		}
	}
	return nil
}
