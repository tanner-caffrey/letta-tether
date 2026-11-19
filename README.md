# Letta Tether

**Run your Letta agents on your own computer, without opening a terminal.**

Your Letta agent has two parts: its memory and conversations, which live in your Letta account, and a *computer* it works on (where it runs commands, reads files, uses tools). Letta can rent you a cloud computer for that. Letta Tether lets your agent use **your own computer** instead, for free, and keeps that connection running in the background.

It puts a small icon by your clock. Green means your agent can use this computer.

> Letta Tether is not made by Letta. It's a small open-source helper; everything it does is in this repository, and you (or your agent) can read all of it.

## Install

### Windows

1. Download **`letta-tether.exe`** from the [latest release](https://github.com/tanner-caffrey/letta-tether/releases/latest).
2. Double-click it. Windows will probably warn you that it's from an unknown publisher ("Windows protected your PC"). That's because it isn't code-signed, which costs money. Click **More info → Run anyway**.
3. A page opens in your browser and walks you through setup: it explains what's happening, gets Letta ready, signs you in, and names this computer.
4. Look for the ring icon near your clock. If you don't see it, click the little **^** arrow; you can drag the icon out so it always shows.

Then in [chat.letta.com](https://chat.letta.com), open your agent, click the computer button at the bottom left of the message box, and pick the name you chose.

### Mac (untested)

Download **`letta-tether-mac.zip`** from the [latest release](https://github.com/tanner-caffrey/letta-tether/releases/latest), unzip it, and open the app. Because it isn't signed, macOS will refuse the first time: right-click it → **Open** → **Open**. The rest is the same as Windows, with the icon in the menu bar.

We don't have a Mac to test on yet. If you try it, please [open an issue](https://github.com/tanner-caffrey/letta-tether/issues) and say whether it worked.

### Linux

For desktops and servers, as a systemd user service (no tray, no root):

```sh
curl -fsSL https://raw.githubusercontent.com/tanner-caffrey/letta-tether/main/install-linux.sh | sh
```

[Read the script first](install-linux.sh) if you like; it's short. Set `LETTA_TETHER_NAME="my server"` before it to choose the computer name. Remove it with `sh install-linux.sh --uninstall`.

## Things to know

- **Your agent can use this computer while it's connected.** It can run commands and read and write files as you. Only connect agents you trust.
- **When this computer is off or asleep, your agent can't use it.** Messages wait until it's back.
- **Switching to your own computer may not stop cloud charges right away.** We've seen a Letta cloud computer for an agent keep running (and billing) for a while after the agent moved to a personal machine. Check your [usage page](https://chat.letta.com/preferences/usage) after switching. We've reported it to Letta.
- It uses the same Letta sign-in as everything else on this computer (`~/.letta`), so if you already use Letta here, you won't need to sign in again.

## Which Letta does it run?

- **If you already have Letta installed** (with npm), Letta Tether uses that one. If there's a newer version, the settings page says so; update it from Letta, then press **Restart**.
- **If you don't**, it downloads Letta's own official all-in-one version (from [PyPI](https://pypi.org/project/letta/), with Node built in), checks it against Letta's published checksum, and keeps it in Letta Tether's own folder. You don't need to install Node or anything else. This version comes out a little after the newest one.

You can switch between them in settings.

## Messaging channels

Letta can let people talk to your agent from Telegram, Discord, Slack, WhatsApp or Signal. Letta Tether can keep those running on this computer, alongside everything else.

1. Set the channel up once with Letta's own setup, in a terminal: `letta channels configure telegram` (or discord, slack, whatsapp, signal). It asks for things like a bot token.
2. In Letta Tether's settings, turn on **Run messaging channels on this computer**. Each channel you've set up gets its own switch, and shows whether it's running or has a problem.
3. When someone new messages your bot, they get a pairing code back. Paste it under that channel, pick which agent answers, and press **Connect**.

Channels you already set up with Letta on this computer show up by themselves. If you were running them some other way (`letta server --channels ...` in a terminal or a service), stop that first, or two copies will fight over the same bot.

If one channel has a problem (an expired bot token, say), only that channel stops; your agent can still use this computer. Channels tied to a *local* agent can't run here (Letta Tether runs cloud agents); they're listed, without a switch.

Command line: `letta-tether --headless --channels`.

## Updates

Letta Tether updates itself: it checks GitHub twice a day, and when there's a new version it downloads it, checks it, and restarts into it within a few seconds. Your agents reconnect by themselves. You can turn this off in Settings ("Update Letta Tether automatically"); then the tray menu and the settings page offer **Update to vX** when there's one.

Each update is checked against the release's published checksums and run once before it replaces the current version. The previous version is kept until the new one has started. Only full releases from this repository are installed, never test builds.

(Version 0.1.0 had no updater, so if you have it, download the newest version once by hand.)

This is separate from Letta itself: see "Which Letta does it run?" above.

## Removing it

Click the icon → **Remove Letta Tether…**. The page lists exactly what it removes:

- the "start when I log in" entry
- Letta Tether's own folder (its settings, its log, and the Letta it downloaded, if any)
- the program itself (Windows; on Mac, drag the app to the Trash afterwards)

It does **not** touch your Letta sign-in and settings (`~/.letta`), any Letta you installed yourself, or your agents.

## What it does, exactly

For anyone (or any agent) reviewing it:

- It runs `letta server --lifecycle-output jsonl --computer-name "<your name>"`, hidden, starting in your home folder. That's the same command Letta's docs tell you to run in a terminal. The status icon reads Letta's own status lines; it never guesses.
- If Letta crashes, it restarts it, waiting longer each time (up to 5 minutes). Stopping it stops Letta **and everything Letta started** (on Windows via a job object, elsewhere via a process group).
- It won't start a second copy of itself, and it picks its own computer name (`<computer> (tether)`) so it doesn't collide with a `letta server` you run by hand. Letta itself also refuses to run two listeners with the same name.
- The setup/settings page is served only on `127.0.0.1`, on a random port, behind a random token in the link, so other programs and websites can't use it.
- Files: Letta Tether keeps its settings, log, and (optionally) downloaded Letta in `%AppData%\letta-tether` (Windows), `~/Library/Application Support/letta-tether` (Mac), or `~/.config/letta-tether` (Linux). Start at login is one registry value under `HKCU\...\Run` (visible in Task Manager → Startup), a LaunchAgent (Mac), or an XDG autostart entry / systemd user service (Linux).
- Network: Letta itself talks to Letta. Letta Tether additionally asks npm for the newest Letta version, PyPI when downloading Letta, and GitHub (this repository's releases) for its own updates. Nothing else.

## Command line

```
letta-tether                 tray icon + setup page
letta-tether --headless      no tray: keep Letta running, print status
    --name "my server"       computer name
    --use existing|bundled   which Letta to run
    --no-auto-update         don't update Letta Tether by itself
letta-tether --download      fetch Letta's official all-in-one build
letta-tether --version
```

## Building and testing

Go 1.26+. `go test ./...` runs everything, including property-based tests ([rapid](https://github.com/flyingmutant/rapid)) that drive the runner with a fake Letta through random start/stop/crash histories, and fuzz the unzip, settings, version, and lifecycle-parsing code. CI runs them on Windows, macOS and Linux.

```sh
go test -race ./...                        # Mac/Linux
go build -ldflags "-H windowsgui" ./cmd/letta-tether   # Windows app (no console window)
```

## License

MIT
