#!/usr/bin/env bash
# Native, pinned sources: Homebrew bottles target the builder's newer macOS.
# Static libevent/utf8proc plus system ncurses keep older Intel Macs usable.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="${1:?usage: build-desktop-tmux.sh <resources>}"
mkdir -p "$OUT"
OUT="$(cd "$OUT" && pwd)"
STAGE="$(mktemp -d -t agentworks-tmux-build)"
trap 'rm -rf "$STAGE"' EXIT
fetch() {
  curl -fsSL --retry 3 "$1" -o "$STAGE/$2"
  printf '%s  %s\n' "$3" "$STAGE/$2" | shasum -a 256 -c -
  tar -xzf "$STAGE/$2" -C "$STAGE"
}
fetch https://github.com/libevent/libevent/releases/download/release-2.1.12-stable/libevent-2.1.12-stable.tar.gz event.tgz 92e6de1be9ec176428fd2367677e61ceffc2ee1cb119035037a27d346b0403bb
fetch https://github.com/JuliaStrings/utf8proc/archive/refs/tags/v2.10.0.tar.gz utf8proc.tgz 6f4f1b639daa6dca9f80bc5db1233e9cbaa31a67790887106160b33ef743f136
fetch https://github.com/tmux/tmux/releases/download/3.8/tmux-3.8.tar.gz tmux.tgz e79c699c7e949dccd0a4a125e17b8d1261e311b16979dbc8eb34542f3966d82e
export MACOSX_DEPLOYMENT_TARGET=12.0
(
  cd "$STAGE/libevent-2.1.12-stable"
  ./configure --prefix="$STAGE/prefix" --disable-shared --enable-static --disable-openssl --disable-samples --disable-libevent-regress
  make -j2
  make install
)
(
  cd "$STAGE/utf8proc-2.10.0"
  make -j2 libutf8proc.a
)
SDK="$(xcrun --show-sdk-path)"
(
  cd "$STAGE/tmux-3.8"
  PKG_CONFIG=false \
    LIBEVENT_CORE_CFLAGS="-I$STAGE/prefix/include" LIBEVENT_CORE_LIBS="$STAGE/prefix/lib/libevent_core.a" \
    LIBTINFOW_CFLAGS="-I$SDK/usr/include" LIBTINFOW_LIBS="$SDK/usr/lib/libncurses.tbd" \
    LIBUTF8PROC_CFLAGS="-I$STAGE/utf8proc-2.10.0" LIBUTF8PROC_LIBS="$STAGE/utf8proc-2.10.0/libutf8proc.a" \
    ./configure --prefix="$STAGE/prefix" --enable-utf8proc --disable-jemalloc
  make -j2
  make install
)
PATH="$STAGE/prefix/bin:$PATH" python3 "$ROOT/scripts/bundle-desktop-tmux.py" "$OUT"
for dependency in libevent-2.1.12-stable utf8proc-2.10.0 tmux-3.8; do
  mkdir -p "$OUT/licenses/$dependency"
  for license in LICENSE LICENSE.md COPYING; do
    if [ -f "$STAGE/$dependency/$license" ]; then cp "$STAGE/$dependency/$license" "$OUT/licenses/$dependency/"; fi
  done
done
