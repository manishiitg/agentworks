#!/bin/bash
set -e

# Get the directory where this script is located
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"

echo "Using Project Root: $PROJECT_ROOT"

if command -v tmux >/dev/null 2>&1; then
    TMUX_VERSION="$(tmux -V 2>/dev/null || true)"
    TMUX_MAJOR="$(printf '%s\n' "$TMUX_VERSION" | sed -E 's/^tmux ([0-9]+).*/\1/')"
else
    TMUX_VERSION=""
    TMUX_MAJOR=""
fi

if ! [ "$TMUX_MAJOR" -ge 3 ] 2>/dev/null; then
    if command -v brew >/dev/null 2>&1; then
        echo "Installing/upgrading Claude Code experimental runtime dependency..."
        brew upgrade tmux || brew install tmux
    else
        echo "Warning: Claude Code experimental mode requires tmux 3.x or newer. Install it with: brew install tmux"
    fi
else
    echo "Claude Code experimental runtime dependency available: $TMUX_VERSION"
fi

# Create resources directory if it doesn't exist
mkdir -p "$SCRIPT_DIR/resources"

echo "Building agent-server..."
cd "$PROJECT_ROOT/agent_go"
if [ "$(uname -s)" = Darwin ]; then
    MACOSX_DEPLOYMENT_TARGET=12.0 CGO_ENABLED=1 go build -tags novoice -o "$SCRIPT_DIR/resources/agent-server" .
    cd "$PROJECT_ROOT"
    bash scripts/build-darwin-voice-binary.sh agent_go desktop/resources/voice/agent-server "$(uname -m)"
    VOICE_MINIMUM="$(python3 scripts/desktop-minimum-os.py desktop/resources/voice)"
    node -e 'require("fs").writeFileSync(process.argv[1], JSON.stringify({minimumSystemVersion: process.argv[2]}))' "$SCRIPT_DIR/resources/voice/minimum-macos.json" "$VOICE_MINIMUM"
else
    go build -o "$SCRIPT_DIR/resources/agent-server" .
fi

echo "Building workspace-server..."
cd "$PROJECT_ROOT/workspace"
go build -o "$SCRIPT_DIR/resources/workspace-server" .

echo "Installing Electron dependencies..."
cd "$SCRIPT_DIR"
npm install

echo "Setup complete."
echo "To run the app: cd desktop && npm start"
