// Letta Tether: a tray icon that keeps `letta server` running on this computer so your
// Letta agents can use it, with a setup page that explains what's going on.
//
// Start it normally and it opens the setup/settings page. Start it with --background
// (that's how "start when I log in" launches it) and it just sits in the tray.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"fyne.io/systray"

	"github.com/tanner-caffrey/letta-tether/internal/channels"
	"github.com/tanner-caffrey/letta-tether/internal/config"
	"github.com/tanner-caffrey/letta-tether/internal/icons"
	"github.com/tanner-caffrey/letta-tether/internal/runner"
	"github.com/tanner-caffrey/letta-tether/internal/single"
	"github.com/tanner-caffrey/letta-tether/internal/ui"
	"github.com/tanner-caffrey/letta-tether/internal/update"
)

var updates = &update.Manager{}

var (
	cfg config.Config
	run = &runner.Runner{LogPath: config.LogPath()}
	app = &ui.App{Runner: run}
	mu  sync.Mutex
)

var errNoLetta = errors.New("Letta isn't set up yet. Open Settings to finish setup.")

var lock *single.Lock

func main() {
	background := len(os.Args) > 1 && os.Args[1] == "--background"
	// --updated: we were just started by the previous version, right after an update.
	updated := len(os.Args) > 1 && (os.Args[1] == "--updated" || os.Args[1] == "--updated-open")
	if updated {
		background = os.Args[1] == "--updated" // --updated-open: they were on the page, open it again
	}
	_ = os.MkdirAll(runner.AppDir(), 0o755)

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--headless", "--updated-headless", "--download", "--version", "--help", "-h":
			if os.Args[1] == "--version" {
				// answer without touching the lock: the updater runs this on a new
				// version while the old one is still running
				fmt.Println("letta-tether", version)
				os.Exit(0)
			}
			// command-line modes: still only one copy at a time
			var err error
			if os.Args[1] == "--updated-headless" {
				lock, err = single.AcquireWait(filepath.Join(runner.AppDir(), "running.lock"), 20*time.Second)
				os.Args[1] = "--headless"
			} else {
				lock, err = single.Acquire(filepath.Join(runner.AppDir(), "running.lock"))
			}
			if os.Args[1] == "--headless" && errors.Is(err, single.ErrRunning) {
				fmt.Fprintln(os.Stderr, "letta-tether: already running on this computer")
				os.Exit(1)
			}
			if err == nil {
				defer lock.Release()
			}
			os.Exit(cli(os.Args[1], os.Args[2:]))
		}
	}

	// Only one Letta Tether at a time. A second launch just asks the first to open its page.
	// After an update, wait a little for the old version to finish exiting.
	var err error
	if updated {
		lock, err = single.AcquireWait(filepath.Join(runner.AppDir(), "running.lock"), 20*time.Second)
	} else {
		lock, err = single.Acquire(filepath.Join(runner.AppDir(), "running.lock"))
	}
	if errors.Is(err, single.ErrRunning) {
		if !background {
			single.Poke(runner.AppDir())
		}
		return
	}
	if err == nil {
		defer lock.Release()
	}
	update.CleanupAfterUpdate() // remove the previous version's backup, if any

	cfg = config.Load()
	systray.Run(func() { onReady(background) }, onExit)
}

func onReady(background bool) {
	systray.SetIcon(icons.For("stopped"))
	systray.SetTitle("")
	systray.SetTooltip("Letta Tether")

	mStatus := systray.AddMenuItem("Stopped", "")
	mStatus.Disable()
	mName := systray.AddMenuItem("", "")
	mName.Disable()
	systray.AddSeparator()
	mStart := systray.AddMenuItem("Start", "Let your agents use this computer")
	mStop := systray.AddMenuItem("Stop", "Your agents can't use this computer until you start it again")
	systray.AddSeparator()
	mOpen := systray.AddMenuItem("Settings…", "Open the Letta Tether page")
	mLog := systray.AddMenuItem("Open log", "What Letta has been saying")
	mRemove := systray.AddMenuItem("Remove Letta Tether…", "Uninstall: opens a page that shows exactly what gets removed")
	mUpdate := systray.AddMenuItem("", "Install the new version of Letta Tether")
	mUpdate.Hide()
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit", "Stop and close Letta Tether")

	show := func(s runner.State) {
		words := map[runner.State]string{
			runner.Connected: "● Connected", runner.Working: "● Working", runner.Starting: "Starting…",
			runner.Reconnecting: "Reconnecting…", runner.Stopped: "Stopped", runner.Error: "Something's wrong (open Settings)",
		}
		mStatus.SetTitle(words[s])
		name := app.Config().ComputerName
		if name == "" {
			name = cfg.ComputerName
		}
		mName.SetTitle("as “" + name + "”")
		systray.SetIcon(icons.For(string(iconFor(s))))
		systray.SetTooltip("Letta Tether: " + words[s])
		if s == runner.Stopped || s == runner.Error {
			mStart.Show()
			mStop.Hide()
		} else {
			mStart.Hide()
			mStop.Show()
		}
	}
	run.OnChange = func(s runner.State, _ string) { show(s) }
	show(runner.Stopped)

	app.Apply = apply
	app.Quit = systray.Quit
	// which channels started: read from Letta's own logs, only those from our latest launch
	var chanWatch *channels.Watch
	var chanMu sync.Mutex
	run.OnStart = func() {
		chanMu.Lock()
		chanWatch = channels.Snapshot(channels.ConfigDir())
		chanMu.Unlock()
	}
	app.ChannelWatch = func() channels.Result {
		chanMu.Lock()
		defer chanMu.Unlock()
		return chanWatch.Read()
	}
	app.Updates = updates

	updates.Version = version
	updates.Auto = func() bool { return app.Config().AutoUpdateOn() }
	updates.Restart = restartIntoNewVersion
	updates.OnChange = func(st update.Status) {
		switch {
		case st.Phase == "downloading":
			mUpdate.SetTitle("Updating Letta Tether…")
			mUpdate.Disable()
			mUpdate.Show()
		case st.Available != "":
			mUpdate.SetTitle("Update to " + st.Available)
			mUpdate.Enable()
			mUpdate.Show()
		default:
			mUpdate.Hide()
		}
	}
	go updates.Run(context.Background())
	url, err := app.Start(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "settings page:", err)
	}
	single.Listen(runner.AppDir(), func() { openBrowser(app.URL()) })
	// The page's address, for agents/scripts helping someone (same-user only, like everything here).
	_ = os.WriteFile(filepath.Join(runner.AppDir(), "settings.url"), []byte(app.URL()), 0o600)

	if cfg.SetupDone && !cfg.Paused {
		if err := pick(cfg); err == nil {
			run.Start()
		} else {
			show(runner.Error)
		}
	}
	if !background || !cfg.SetupDone {
		openBrowser(url)
	}
	systray.SetOnTapped(func() { openBrowser(app.URL()) })

	go func() {
		for {
			select {
			case <-mStart.ClickedCh:
				c := app.Config()
				c.Paused = false
				_ = c.Save()
				app.SetConfig(c)
				if pick(c) == nil {
					run.Start()
				}
			case <-mStop.ClickedCh:
				c := app.Config()
				c.Paused = true
				_ = c.Save()
				app.SetConfig(c)
				go run.Stop()
			case <-mOpen.ClickedCh:
				openBrowser(app.URL())
			case <-mUpdate.ClickedCh:
				go updates.Apply(context.Background())
			case <-mRemove.ClickedCh:
				openBrowser(app.URL() + "#remove")
			case <-mLog.ClickedCh:
				openBrowser(config.LogPath())
			case <-mQuit.ClickedCh:
				systray.Quit()
				return
			}
		}
	}()
}

func iconFor(s runner.State) runner.State {
	if s == runner.Starting {
		return runner.Reconnecting
	}
	return s
}

// pick chooses which Letta to run from the settings.
func pick(c config.Config) error {
	mu.Lock()
	defer mu.Unlock()
	var in runner.Install
	var ok bool
	if c.Install == "existing" {
		in, ok = runner.FindExisting()
	} else {
		in, ok = runner.FindBundled()
	}
	if !ok && c.Install == "existing" {
		return errors.New("Couldn't find your Letta install anymore. Open Settings and pick “download Letta for me”.")
	}
	if !ok {
		return errNoLetta
	}
	run.Install = in
	run.ComputerName = c.ComputerName
	run.Channels = c.Channels
	app.Channels = in // channel commands run with the same Letta
	return nil
}

// apply saves new settings and restarts the listener with them.
func apply(c config.Config) error {
	if err := c.Save(); err != nil {
		return err
	}
	run.Stop()
	if err := pick(c); err != nil {
		return err
	}
	if !c.Paused {
		run.Start()
	}
	return nil
}

// restartIntoNewVersion stops Letta, lets go of the one-copy lock, starts the new
// version (which waits for the lock), and quits. The new version picks up the same
// settings, so it reconnects under the same name without asking anything.
func restartIntoNewVersion(reopen bool) error {
	t, err := update.CurrentTarget()
	if err != nil {
		return err
	}
	run.Stop()
	lock.Release()
	flag := "--updated"
	if reopen {
		flag = "--updated-open"
	}
	if runtime.GOOS == "darwin" {
		// relaunch the app bundle, so macOS treats it as the app (menu bar icon and all)
		err = update.Relaunch("open", "-n", t.Replace, "--args", flag)
	} else {
		err = update.Relaunch(t.Program, flag)
	}
	if err != nil {
		// couldn't launch the new one: keep running the old one rather than vanish
		lock, _ = single.Acquire(filepath.Join(runner.AppDir(), "running.lock"))
		if pick(app.Config()) == nil && !app.Config().Paused {
			run.Start()
		}
		return fmt.Errorf("couldn't start the new version: %w", err)
	}
	systray.Quit()
	return nil
}

// version is set at build time: -ldflags "-X main.version=v0.1.0"
var version = "dev"

func cli(mode string, args []string) int {
	switch mode {
	case "--version":
		fmt.Println("letta-tether", version)
		return 0
	case "--download":
		rel, err := runner.LatestBundled(context.Background())
		if err != nil {
			fmt.Fprintln(os.Stderr, "letta-tether:", err)
			return 1
		}
		fmt.Printf("Downloading Letta %s (%.0f MB)...\n", rel.Version, float64(rel.Size)/1e6)
		last := -1
		err = runner.InstallBundled(context.Background(), rel, func(d, t int64) {
			if t > 0 {
				if p := int(d * 100 / t); p/10 != last/10 {
					last = p
					fmt.Printf("  %d%%\n", p)
				}
			}
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, "letta-tether:", err)
			return 1
		}
		c := config.Load()
		c.Install = "bundled"
		_ = c.Save()
		fmt.Println("Done. Letta Tether will use it.")
		return 0
	case "--headless":
		return runHeadless(args)
	}
	fmt.Println(`letta-tether: run your Letta agents on this computer.

  letta-tether                 tray icon + setup page (desktop)
  letta-tether --headless      no tray: keep Letta running, print status (servers, systemd)
      --name "my server"       the computer name to show in Letta
      --use existing|bundled   which Letta to run
  letta-tether --download      fetch Letta's official all-in-one build (no Node needed)
  letta-tether --version`)
	return 0
}

func onExit() {
	run.Stop()
}

func openBrowser(target string) {
	if target == "" {
		return
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	case "darwin":
		cmd = exec.Command("open", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	_ = cmd.Start()
}
