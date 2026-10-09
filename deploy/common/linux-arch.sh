#!/usr/bin/env bash
# Native Linux release builds keep the cgo libraries and manifest on the same architecture.
case "$(uname -sm)" in
  'Linux x86_64') NATIVE_BUILD_ARCH=amd64 ;;
  'Linux aarch64') NATIVE_BUILD_ARCH=arm64 ;;
  *) echo 'Expected Linux x86_64 or aarch64.' >&2; exit 1 ;;
esac
BUILD_ARCH="${BUILD_ARCH:-$NATIVE_BUILD_ARCH}"
[[ "$BUILD_ARCH" == "$NATIVE_BUILD_ARCH" ]] || {
  echo "A $BUILD_ARCH release needs a native $BUILD_ARCH Linux build host (this host is $NATIVE_BUILD_ARCH)." >&2
  exit 1
}
export BUILD_ARCH
