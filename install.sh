#!/usr/bin/env bash
# Install the desktop app and bundled CLI. No source builds or automatic updates.
set -euo pipefail
REPO="manishiitg/agentworks"
APP_DIR="/Applications"
BIN_DIR="${HOME}/.local/bin"
VERSION="${RUNLOOP_VERSION:-}"
DMG="${RUNLOOP_DMG_PATH:-}"
CHECKSUM=""
UPDATE=false
LAUNCH=true
log() { printf '[agentworks] %s\n' "$*"; }
die() { printf '[agentworks] %s\n' "$*" >&2; exit 1; }
usage() {
  cat <<'HELP'
Install AgentWorks for Mac and its agentworks command, then open the app.
An existing app is opened without updating it. Sign-ins are never copied.

Usage: bash install.sh [options]
  --update                 Explicitly replace an existing installation
  --version vX.Y.Z         Install an exact release
  --app-dir PATH           App destination (default: /Applications)
  --bin-dir PATH           CLI destination (default: ~/.local/bin)
  --dmg PATH               Test a locally built DMG; requires --sha256-file
  --sha256-file PATH       Checksum file for that DMG
  --no-open                Install without launching
  --help                   Show this help
HELP
}
while [ $# -gt 0 ]; do
  case "$1" in
    --update) UPDATE=true; shift ;;
    --no-open) LAUNCH=false; shift ;;
    --version|--app-dir|--bin-dir|--dmg|--sha256-file)
      [ $# -ge 2 ] && [ -n "$2" ] || die "Missing value for $1"
      case "$1" in
        --version) VERSION="$2" ;; --app-dir) APP_DIR="$2" ;;
        --bin-dir) BIN_DIR="$2" ;; --dmg) DMG="$2" ;;
        --sha256-file) CHECKSUM="$2" ;;
      esac
      shift 2 ;;
    --help|-h) usage; exit 0 ;;
    *) die "Unknown option: $1. Use --help." ;;
  esac
done
[ "$(uname -s)" = Darwin ] || die "The desktop installer currently supports macOS. The standalone CLI remains available from your server."
[ "$(uname -m)" = arm64 ] || die "This release supports Apple Silicon Macs. Use the source development launcher on other architectures."
case "$VERSION" in '') ;; *) [[ "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "Version must be vX.Y.Z" ;; esac
mkdir -p "$APP_DIR" "$BIN_DIR" || die "Choose writable --app-dir and --bin-dir locations; no sudo is needed."
APP_DIR="$(cd "$APP_DIR" && pwd -P)"
BIN_DIR="$(cd "$BIN_DIR" && pwd -P)"
for destination in "$APP_DIR" "$BIN_DIR"; do
  ancestor="$destination"
  while [ "$ancestor" != / ]; do
    [ ! -e "$ancestor/.git" ] || die "Choose an install location outside your development checkout. Keep using ./run_agentworks for development."
    ancestor="$(dirname "$ancestor")"
  done
done
APP="$APP_DIR/AgentWorks.app"
CLI="$APP/Contents/Resources/bin/agentworks"
LINK="$BIN_DIR/agentworks"
link_cli() {
  [ -x "$CLI" ] || die "This app does not include the CLI. Use --update with a combined app release."
  if [ -L "$LINK" ]; then
    case "$(readlink "$LINK")" in
      */AgentWorks.app/Contents/Resources/bin/agentworks) rm "$LINK" ;;
      *) log "Kept existing $LINK. The bundled CLI is at $CLI"; return ;;
    esac
  elif [ -e "$LINK" ]; then
    # PLAT-856-434: preserve standalone CLIs unless an update was requested.
    # Check the Go module marker before executing an existing binary.
    if $UPDATE && LC_ALL=C grep -aq 'github.com/manishiitg/coding-agent-loop/agent_go/cmd/agentworks' "$LINK" &&
       "$LINK" --version 2>/dev/null | grep -q '^agentworks version '; then
      rm "$LINK"
    else
      log "Kept existing $LINK. Use the bundled CLI at $CLI; --update can replace an older AgentWorks CLI."
      return
    fi
  fi
  ln -s "$CLI" "$LINK"
  log "CLI installed: $LINK"
  case ":$PATH:" in *":$BIN_DIR:"*) ;; *) printf 'Add the CLI to your PATH: export PATH=%q:"$PATH"\n' "$BIN_DIR" ;; esac
}
open_app() { if $LAUNCH; then /usr/bin/open "$APP"; fi; }
if [ -e "$APP" ] && ! $UPDATE; then
  link_cli
  log "Opening the installed app. Use --update when you want a newer version."
  open_app
  exit 0
fi
STAGE="$(mktemp -d -t agentworks-install)"
MOUNT="$STAGE/mount"
mounted=false
cleanup() { if $mounted; then hdiutil detach -quiet "$MOUNT" || true; fi; rm -rf "$STAGE"; }
trap cleanup EXIT
if [ -z "$DMG" ]; then
  if [ -z "$VERSION" ]; then
    curl -fsSL "https://api.github.com/repos/$REPO/releases?per_page=50" -o "$STAGE/releases.json"
    # Use the macOS JSON parser; other apps also publish tags in this repository.
    VERSION="$(osascript -l JavaScript - "$STAGE/releases.json" <<'JXA'
ObjC.import('Foundation');
function run(args) {
  const text = $.NSString.stringWithContentsOfFileEncodingError(args[0], $.NSUTF8StringEncoding, null).js;
  const releases = JSON.parse(text).filter(r => !r.draft && !r.prerelease && /^v\d+\.\d+\.\d+$/.test(r.tag_name));
  releases.sort((a,b) => { const x=a.tag_name.slice(1).split('.').map(Number), y=b.tag_name.slice(1).split('.').map(Number); return y[0]-x[0] || y[1]-x[1] || y[2]-x[2]; });
  if (!releases.length) throw new Error('No stable AgentWorks release found');
  return releases[0].tag_name;
}
JXA
)"
  fi
  ASSET="AgentWorks-${VERSION#v}-arm64.dmg"
  BASE="https://github.com/$REPO/releases/download/$VERSION"
  DMG="$STAGE/$ASSET"
  log "Downloading AgentWorks $VERSION"
  curl -fL --progress-bar "$BASE/$ASSET" -o "$DMG"
fi
[ -f "$DMG" ] || die "DMG not found: $DMG"
if [ -z "$CHECKSUM" ]; then
  [ -n "$VERSION" ] || die "Local DMGs require --sha256-file."
  CHECKSUM="$STAGE/app.sha256"
  curl -fsSL "https://github.com/$REPO/releases/download/$VERSION/AgentWorks-${VERSION#v}-arm64.dmg.sha256" -o "$CHECKSUM"
fi
[ -f "$CHECKSUM" ] || die "Checksum file not found: $CHECKSUM"
EXPECTED="$(awk 'NR == 1 {print $1}' "$CHECKSUM")"
[[ "$EXPECTED" =~ ^[0-9a-fA-F]{64}$ ]] || die "Invalid SHA-256 file."
ACTUAL="$(shasum -a 256 "$DMG" | awk '{print $1}')"
[ "$(printf %s "$EXPECTED" | tr A-F a-f)" = "$ACTUAL" ] || die "Checksum mismatch. Nothing was installed."
mkdir "$MOUNT"
hdiutil attach -quiet -readonly -nobrowse -noautoopen -mountpoint "$MOUNT" "$DMG"
mounted=true
SOURCE="$MOUNT/AgentWorks.app"
for helper in agentworks mcpbridge tmux; do
  [ -x "$SOURCE/Contents/Resources/bin/$helper" ] || die "The release is missing $helper. Nothing was installed."
done
for helper in agent-server workspace-server; do
  [ -x "$SOURCE/Contents/Resources/$helper" ] || die "The release is missing $helper. Nothing was installed."
done
if ps -axo comm= | grep -Fq "$APP/Contents/MacOS/"; then
  die "Quit this installed AgentWorks app, then run --update again. Your development app can stay open."
fi
if [ -e "$APP" ]; then
  [ -f "$APP/Contents/Resources/app.asar" ] || die "This is not a packaged AgentWorks app; choose another --app-dir."
fi
# Copy before replacing: a full disk must not remove the working installation.
SWAP="$(mktemp -d "$APP_DIR/.agentworks-install.XXXXXX")"
if ! ditto "$SOURCE" "$SWAP/AgentWorks.app"; then
  rm -rf "$SWAP"
  die "Could not copy the download. Your existing app is unchanged."
fi
if [ -e "$APP" ]; then mv "$APP" "$SWAP/previous.app"; fi
if ! mv "$SWAP/AgentWorks.app" "$APP"; then
  if [ -e "$SWAP/previous.app" ]; then mv "$SWAP/previous.app" "$APP" || die "Restore the previous app from $SWAP/previous.app"; fi
  rm -rf "$SWAP"
  die "Could not replace the app. Your previous app was restored."
fi
rm -rf "$SWAP"
if ! spctl --assess --type execute "$APP" >/dev/null 2>&1; then
  log "This build is unsigned/not notarized. The checksum verifies the download, not publisher identity. Clearing quarantine."
  /usr/bin/xattr -dr com.apple.quarantine "$APP" 2>/dev/null || true
fi
link_cli
log "Installed AgentWorks and its CLI. Existing sign-ins and projects are unchanged."
open_app
