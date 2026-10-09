#!/usr/bin/env bash
# Builds the Video Studio agent for linux/amd64 or linux/arm64 WITH cgo and stages it, plus the
# native speech-to-text libraries it links, under "$BUILD_DIR/bin".
#
#   build-linux-agent.sh <build-dir> <agent_go-dir> <workspace-root>
#
# <build-dir> must already hold the go.work to build under (build-and-activate.sh
# writes one) and receives bin/video-studio-agent and bin/lib/*.so. Every host
# path is mounted into the container at the SAME path so the go.work's absolute
# `use` entries resolve unchanged.
#
# Why a container: pkg/voicestt links sherpa-onnx's prebuilt Linux libraries,
# so the agent needs CGO_ENABLED=1 for the mic control to work on the server,
# and a Mac has no linux/amd64 C toolchain. The image (Dockerfile next to this
# script) is native arm64 Go plus the Debian x86-64 cross gcc — no emulation.
#
# The binary gets an extra rpath of $ORIGIN/lib, so it finds the libraries in
# bin/lib beside itself on the server. The rpath baked in by sherpa-onnx-go
# (the build machine's module cache) is simply skipped there.
set -euo pipefail

BUILD_DIR="$1"
AGENT_DIR="$2"
WORKSPACE_ROOT="$3"
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
IMAGE="video-studio-linux-amd64-builder"
ARCH="${BUILD_ARCH:-amd64}"
case "$ARCH" in
  amd64) LIB_TRIPLE=x86_64-unknown-linux-gnu; ELF_ARCH=x86-64; NATIVE_ARCH=x86_64 ;;
  arm64) LIB_TRIPLE=aarch64-unknown-linux-gnu; ELF_ARCH='ARM aarch64'; NATIVE_ARCH=aarch64 ;;
  *) echo "Unsupported agent architecture: $ARCH" >&2; exit 2 ;;
esac

test -f "$BUILD_DIR/go.work"
mkdir -p "$BUILD_DIR/bin/lib"
if [[ "$(uname -sm)" == "Linux $NATIVE_ARCH" ]]; then
  echo "Building agent natively on the Linux server"
  (cd "$WORKSPACE_ROOT" && GOWORK="$BUILD_DIR/go.work" GOOS=linux GOARCH="$ARCH" CGO_ENABLED=1 CGO_LDFLAGS='-Wl,-rpath,$ORIGIN/lib' go build -o "$BUILD_DIR/bin/video-studio-agent" "$AGENT_DIR")
else
[[ "$ARCH" == amd64 ]] || { echo 'The cgo ARM agent needs a native Linux aarch64 build host.' >&2; exit 1; }
command -v docker >/dev/null || { echo "Missing docker (needed for the cgo linux/amd64 agent build)" >&2; exit 1; }
test -f "$BUILD_DIR/go.work" || { echo "Expected $BUILD_DIR/go.work" >&2; exit 1; }

docker image inspect "$IMAGE" >/dev/null 2>&1 \
  || docker build --platform linux/arm64 -t "$IMAGE" -f "$SCRIPT_DIR/Dockerfile.linux-amd64-cgo" "$SCRIPT_DIR"

HOST_MODCACHE="$(go env GOMODCACHE)"
mkdir -p "$BUILD_DIR/bin/lib"

docker run --rm --platform linux/arm64 \
  -v "$WORKSPACE_ROOT:$WORKSPACE_ROOT" \
  -v "$BUILD_DIR:$BUILD_DIR" \
  -v "$HOST_MODCACHE:/go/pkg/mod" \
  -v video-studio-linux-amd64-gocache:/root/.cache/go-build \
  -e GOWORK="$BUILD_DIR/go.work" \
  -e GOFLAGS=-buildvcs=false \
  -e GOTOOLCHAIN=auto \
  -e CGO_LDFLAGS='-Wl,-rpath,$ORIGIN/lib' \
  -w "$WORKSPACE_ROOT" \
  "$IMAGE" go build -o "$BUILD_DIR/bin/video-studio-agent" "$AGENT_DIR"

fi

# The exact module version the build linked against, from the same go.work.
SHERPA_DIR="$(cd "$AGENT_DIR" && GOWORK="$BUILD_DIR/go.work" go list -m -f '{{.Dir}}' github.com/k2-fsa/sherpa-onnx-go-linux)"
LIB_SRC="$SHERPA_DIR/lib/$LIB_TRIPLE"
test -f "$LIB_SRC/libsherpa-onnx-c-api.so" || { echo "Native STT libraries not found under $LIB_SRC" >&2; exit 1; }
install -m 0644 "$LIB_SRC"/libsherpa-onnx-c-api.so "$LIB_SRC"/libonnxruntime.so "$BUILD_DIR/bin/lib/"

file "$BUILD_DIR/bin/video-studio-agent" | grep -q "$ELF_ARCH" || { echo "Agent binary is not linux/$ARCH" >&2; exit 1; }
echo "Built $BUILD_DIR/bin/video-studio-agent (cgo, native STT) with $(ls "$BUILD_DIR/bin/lib" | tr '\n' ' ')"
