#!/usr/bin/env bash
# Package the native CLI beside a local development server's downloads.
set -euo pipefail

REPO_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
DEST="${1:?usage: package-agentworks-cli-local.sh ABSOLUTE_DOWNLOAD_DIR}"
[[ "$DEST" = /* ]] || { echo "download directory must be absolute" >&2; exit 2; }

case "$(uname -s)" in
  Darwin) OS=darwin ;;
  Linux) OS=linux ;;
  *) echo "unsupported local CLI platform" >&2; exit 2 ;;
esac
case "$(uname -m)" in
  arm64|aarch64) ARCH=arm64 ;;
  x86_64|amd64) ARCH=amd64 ;;
  *) echo "unsupported local CLI architecture" >&2; exit 2 ;;
esac

mkdir -p "$DEST"
STAGE="$(mktemp -d "${DEST}.tmp.XXXXXX")"
trap 'rm -rf "$STAGE"' EXIT
NAME="agentworks-$OS-$ARCH"
REVISION="$(git -C "$REPO_ROOT" rev-parse HEAD 2>/dev/null || echo dev)"
if [[ "$REVISION" != dev ]] && [[ -n "$(git -C "$REPO_ROOT" status --porcelain 2>/dev/null)" ]]; then
  REVISION="${REVISION}+dirty"
fi

(cd "$REPO_ROOT/agent_go" && GOOS="$OS" GOARCH="$ARCH" CGO_ENABLED=0 go build -ldflags "-s -w -X main.cliVersion=$REVISION" -o "$STAGE/$NAME" ./cmd/agentworks)
chmod +x "$STAGE/$NAME"
gzip -n -9 -c "$STAGE/$NAME" > "$STAGE/$NAME.gz"
(cd "$STAGE" && if command -v sha256sum >/dev/null 2>&1; then sha256sum "$NAME" > "$NAME.sha256"; else shasum -a 256 "$NAME" > "$NAME.sha256"; fi)
cp "$REPO_ROOT/scripts/install-agentworks-cli.sh" "$STAGE/install-agentworks.sh"
printf '{"version":"%s","release":"local"}\n' "$REVISION" > "$STAGE/version.json"
for file in "$NAME" "$NAME.gz" "$NAME.sha256" install-agentworks.sh version.json; do
  mv "$STAGE/$file" "$DEST/$file"
done
# A local development server offers the native executable it just built.
rm -f "$DEST/cli-download-base.txt" "$DEST/cli-archives.sha256"
echo "Packaged local AgentWorks CLI: $DEST/$NAME"
