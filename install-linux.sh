#!/bin/sh
# Letta Tether for Linux: keeps your Letta agents able to use this computer,
# as a systemd user service (no tray, no root).
#
#   curl -fsSL https://raw.githubusercontent.com/tanner-caffrey/letta-tether/main/install-linux.sh | sh
#
# What it does (read it first; it's short):
#   1. downloads the letta-tether program for your CPU into ~/.local/bin
#   2. if you don't have Letta, downloads Letta's official all-in-one build
#   3. signs you in to Letta if needed (opens a link you approve in a browser)
#   4. adds a systemd user service so it starts on login and restarts if it crashes
#
# Remove everything:  sh install-linux.sh --uninstall
set -eu

REPO="tanner-caffrey/letta-tether"
BIN="$HOME/.local/bin/letta-tether"
UNIT_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
UNIT="$UNIT_DIR/letta-tether.service"
APP_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/letta-tether"

say() { printf '\033[1m%s\033[0m\n' "$*"; }
die() { printf 'letta-tether: %s\n' "$*" >&2; exit 1; }

if [ "${1:-}" = "--uninstall" ]; then
  say "Removing Letta Tether"
  systemctl --user disable --now letta-tether.service 2>/dev/null || true
  rm -f "$UNIT" "$BIN"
  systemctl --user daemon-reload 2>/dev/null || true
  case "$APP_DIR" in */letta-tether) rm -rf "$APP_DIR" ;; esac
  say "Done. Your Letta sign-in (~/.letta) and your agents were left alone."
  exit 0
fi

command -v systemctl >/dev/null 2>&1 || die "this installer needs systemd. Run 'letta-tether --headless' under your own supervisor instead."

case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) die "no build for $(uname -m) yet" ;;
esac

NAME="${LETTA_TETHER_NAME:-$(hostname) (tether)}"

say "1/4 Downloading letta-tether ($ARCH)"
mkdir -p "$(dirname "$BIN")"
URL="https://github.com/$REPO/releases/latest/download/letta-tether-linux-$ARCH"
curl -fsSL "$URL" -o "$BIN.new" || die "download failed: $URL"
curl -fsSL "$URL.sha256" -o "$BIN.sha256" || die "checksum download failed"
( cd "$(dirname "$BIN")" && echo "$(cut -d' ' -f1 "$BIN.sha256")  letta-tether.new" | sha256sum -c --quiet - ) || { rm -f "$BIN.new" "$BIN.sha256"; die "checksum didn't match, not installing"; }
rm -f "$BIN.sha256"
chmod +x "$BIN.new" && mv "$BIN.new" "$BIN"

say "2/4 Checking for Letta"
if command -v letta >/dev/null 2>&1; then
  echo "Using the Letta you have: $(letta --version 2>/dev/null | tail -1)"
  USE=existing
else
  echo "No Letta found, downloading Letta's official all-in-one build (about 80 MB)"
  "$BIN" --download
  USE=bundled
fi

say "3/4 Signing in to Letta (skipped if you already are)"
if [ ! -f "$HOME/.letta/settings.json" ] || ! grep -q '"refreshToken"' "$HOME/.letta/settings.json" 2>/dev/null; then
  echo "A sign-in link will appear below. Open it in any browser, approve, and come back."
  if [ "$USE" = existing ]; then letta server --computer-name "$NAME" & else
    "$APP_DIR/letta/letta_code/_payload/bin/node" "$APP_DIR/letta/letta_code/_payload/app/letta.js" server --computer-name "$NAME" & fi
  PID=$!
  until grep -q '"refreshToken"' "$HOME/.letta/settings.json" 2>/dev/null; do
    kill -0 "$PID" 2>/dev/null || die "sign-in didn't finish"
    sleep 2
  done
  kill "$PID" 2>/dev/null || true
  wait "$PID" 2>/dev/null || true
fi

say "4/4 Starting it on login"
mkdir -p "$UNIT_DIR"
# systemd reads % and $ as its own codes, and " ends the quoted name: escape them
UNIT_NAME=$(printf '%s' "$NAME" | sed -e 's/%/%%/g' -e 's/\$/$$/g' -e 's/"/\\"/g')
cat > "$UNIT" <<EOF
[Unit]
Description=Letta Tether: lets your Letta agents use this computer
After=network-online.target

[Service]
ExecStart=$BIN --headless --use $USE --name "$UNIT_NAME"
Restart=on-failure
RestartSec=10

[Install]
WantedBy=default.target
EOF
systemctl --user daemon-reload
systemctl --user enable --now letta-tether.service

if command -v loginctl >/dev/null 2>&1 && [ "$(loginctl show-user "$USER" -p Linger --value 2>/dev/null)" != yes ]; then
  echo
  echo "Note: it runs while you're logged in. To keep it running after you log out"
  echo "(e.g. on a server), run:  sudo loginctl enable-linger $USER"
fi
echo
say "Done. In Letta, pick the computer \"$NAME\" for your agent."
echo "Status:  systemctl --user status letta-tether    Logs:  journalctl --user -u letta-tether -f"
