#!/bin/sh
# install-agentworks.sh — install the AgentWorks CLI from your server and log in.
#
# Served at https://SERVER/api/downloads/cli/install-agentworks.sh and
# shown prefilled on the Connect tab (Setup → Integrations). SERVER is your
# AgentWorks address (the one in your browser); write it out in full. Single command:
#
#   curl -fsSL https://SERVER/api/downloads/cli/install-agentworks.sh | \
#     sh -s -- --server https://SERVER
#
# Browser sign-in grants read and run access without copying a credential.
# Existing --token / AGENTWORKS_TOKEN installations remain supported.
set -eu

SERVER="${AGENTWORKS_SERVER:-}"
TOKEN="${AGENTWORKS_TOKEN:-}"
INSTALL_DIR="${HOME:-/tmp}/.local/bin"

usage() {
  echo "usage: sh install-agentworks.sh --server https://YOUR-SERVER-ADDRESS [--dir DIR] [--no-login]" >&2
}

while [ $# -gt 0 ]; do
  case "$1" in
    --server) SERVER="${2:-}"; shift 2 ;;
    --token) TOKEN="${2:-}"; shift 2 ;;
    --dir) INSTALL_DIR="${2:-}"; shift 2 ;;
    --no-login) NO_LOGIN=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage; exit 2 ;;
  esac
done

[ -n "$SERVER" ] || { echo "missing --server (or AGENTWORKS_SERVER)" >&2; usage; exit 2; }
case "$SERVER" in
  *"<"*|*">"*|*" "*|*SERVER*|*YOUR-SERVER*|*example.com*)
    echo "The server address \"$SERVER\" is a placeholder, not a real address. Use the address you open AgentWorks at in your browser, for example https://agents.mycompany.com." >&2
    exit 2 ;;
esac
case "$SERVER" in https://*|http://localhost*|http://127.0.0.1*) ;; *) echo "The server address must start with https:// (got \"$SERVER\")." >&2; exit 2 ;; esac
if [ -n "$TOKEN" ]; then
  case "$TOKEN" in aw_pat_*) ;; *) echo "expected an AgentWorks access token (aw_pat_...)" >&2; exit 2 ;; esac
fi
command -v curl >/dev/null 2>&1 || { echo "curl is required to install the AgentWorks CLI" >&2; exit 1; }

OS="$(uname -s)"
ARCH="$(uname -m)"
case "$OS" in
  Darwin) OS=darwin ;;
  Linux) OS=linux ;;
  *) echo "unsupported OS: $OS (need macOS or Linux)" >&2; exit 1 ;;
esac
case "$ARCH" in
  arm64|aarch64) ARCH=arm64 ;;
  x86_64|amd64) ARCH=amd64 ;;
  *) echo "unsupported architecture: $ARCH" >&2; exit 1 ;;
esac

ASSET="agentworks-${OS}-${ARCH}"
BASE_URL="${SERVER%/}/api/downloads/cli"
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT INT TERM

echo "Downloading $ASSET ..."
curl -fsSL -o "$STAGE/$ASSET" "$BASE_URL/$ASSET"
curl -fsSL -o "$STAGE/$ASSET.sha256" "$BASE_URL/$ASSET.sha256"

echo "Verifying checksum ..."
if command -v sha256sum >/dev/null 2>&1; then
  (cd "$STAGE" && sha256sum -c "$ASSET.sha256")
elif command -v shasum >/dev/null 2>&1; then
  (cd "$STAGE" && shasum -a 256 -c "$ASSET.sha256")
else
  echo "need sha256sum or shasum to verify the download; refusing to install unverified" >&2
  exit 1
fi

mkdir -p "$INSTALL_DIR"
chmod +x "$STAGE/$ASSET"
mv "$STAGE/$ASSET" "$INSTALL_DIR/agentworks"
echo "Installed $INSTALL_DIR/agentworks"

if command -v agentworks >/dev/null 2>&1; then
  FOUND="$(command -v agentworks)"
  if [ "$FOUND" != "$INSTALL_DIR/agentworks" ]; then
    echo "note: another agentworks shadows this install at $FOUND" >&2
  fi
else
  # A new Mac or Linux terminal does not look in this folder yet: add it to the shell's startup file once.
  PATH_LINE="export PATH=\"$INSTALL_DIR:\$PATH\""
  case "${SHELL:-}" in
    */zsh) RC="${HOME:-}/.zshrc" ;;
    */bash) if [ "$OS" = darwin ]; then RC="${HOME:-}/.bash_profile"; else RC="${HOME:-}/.bashrc"; fi ;;
    *) RC="${HOME:-}/.profile" ;;
  esac
  if [ -n "${HOME:-}" ] && [ -z "${AGENTWORKS_NO_PATH_EDIT:-}" ]; then
    if ! grep -qsF "$PATH_LINE" "$RC"; then
      printf '\n# Added by the AgentWorks installer\n%s\n' "$PATH_LINE" >> "$RC"
    fi
    echo "Added $INSTALL_DIR to your PATH in $RC. Open a new terminal window, or run this now:"
  else
    echo "$INSTALL_DIR is not on your PATH. Add it with:"
  fi
  echo "  $PATH_LINE"
fi

if [ "${NO_LOGIN:-0}" != 1 ]; then
  echo "Signing in to $SERVER ..."
  if [ -n "$TOKEN" ]; then
    printf '%s' "$TOKEN" | "$INSTALL_DIR/agentworks" login --server "$SERVER" --token-stdin
  else
    "$INSTALL_DIR/agentworks" login --server "$SERVER"
  fi
fi

echo "Done. Try: $INSTALL_DIR/agentworks workflows list"
echo "For AI assistants, paste the MCP command from the Connect tab."
