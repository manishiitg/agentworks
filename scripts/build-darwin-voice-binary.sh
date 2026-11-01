#!/usr/bin/env bash
# Builds a macOS Go binary WITH the shared speech engine (agent_go/pkg/voicestt)
# and stages the native libraries it links beside it, in <out-dir>/lib.
#
#   scripts/build-darwin-voice-binary.sh <package-dir> <output-binary> [arm64|amd64]
#
# Why: pkg/voicestt links sherpa-onnx's prebuilt dylibs, so the binary must be
# built with cgo — a plain `GOOS=darwin GOARCH=arm64 go build` on a different
# host arch silently turns cgo OFF and ships the stub that answers the mic
# with 503. The dylibs carry @rpath install names and are found through the
# extra rpath baked in here (@executable_path/lib), which is where the desktop
# apps' extraResources put them (Contents/Resources/lib beside the server).
#
# Used by desktop development and CI to
# build agent-server.
set -euo pipefail

PKG_DIR="$1"
OUT="$2"
ARCH="${3:-$(uname -m)}"
case "$ARCH" in
  arm64|aarch64) GOARCH=arm64; LIB_ARCH=aarch64-apple-darwin ;;
  amd64|x86_64)  GOARCH=amd64; LIB_ARCH=x86_64-apple-darwin ;;
  *) echo "unknown arch $ARCH (want arm64 or amd64)" >&2; exit 1 ;;
esac

mkdir -p "$(dirname "$OUT")/lib"
OUT_DIR="$(cd "$(dirname "$OUT")" && pwd)"
OUT="$OUT_DIR/$(basename "$OUT")"

MOD_DIR="$(cd "$PKG_DIR" && go list -m -f '{{.Dir}}' github.com/k2-fsa/sherpa-onnx-go-macos)"
LIB_SRC="$MOD_DIR/lib/$LIB_ARCH"
test -f "$LIB_SRC/libsherpa-onnx-c-api.dylib" || { echo "speech runtime libraries not found under $LIB_SRC" >&2; exit 1; }
install -m 0644 "$LIB_SRC"/libsherpa-onnx-c-api.dylib "$LIB_SRC"/libonnxruntime.dylib "$OUT_DIR/lib/"

(cd "$PKG_DIR" && CGO_ENABLED=1 GOOS=darwin GOARCH="$GOARCH" CGO_LDFLAGS='-Wl,-rpath,@executable_path/lib' go build -o "$OUT" .)

otool -l "$OUT" | grep -A2 LC_RPATH | grep -q '@executable_path/lib' || { echo "built binary lacks the @executable_path/lib rpath" >&2; exit 1; }
file "$OUT"
echo "Staged $(ls "$OUT_DIR/lib" | tr '\n' ' ')in $OUT_DIR/lib"
