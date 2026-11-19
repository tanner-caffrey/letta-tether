**New: Letta Tether now updates itself.** It checks twice a day and installs new versions on its own (each one checked against the release's checksums and run once before it replaces the old one). Turn it off in Settings if you'd rather update by hand; then the tray menu shows **Update to vX** when there's one.

If you have 0.1.0, download this version once by hand (0.1.0 had no updater). After that it's automatic.

**Windows:** download `letta-tether.exe` and run it. Windows will warn that it's from an unknown publisher (it isn't code-signed): **More info → Run anyway**.

**Mac (untested):** download `letta-tether-mac.zip`, unzip, right-click the app → **Open** → **Open**. Please tell us if it works.

**Linux:** `curl -fsSL https://raw.githubusercontent.com/tanner-caffrey/letta-tether/main/install-linux.sh | sh` (already installed? it updates itself.)

`SHA256SUMS.txt` has checksums for every file.
