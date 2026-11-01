#!/usr/bin/env bash
# Build the pinned native-session delta helper; no system rsync is replaced.
# Release builders package this binary beside the app backend.
set -euo pipefail
prefix="${1:?usage: install-rsync.sh <prefix>}"
version=3.5.1
sha=c55f9c9dc10fb8bec397b399a0fdded53cc9a2d8e30891bb0d63724d25c37bef
if [[ -x "$prefix/bin/rsync" ]]; then
  # Consume all output: head under pipefail makes rsync exit 141 (SIGPIPE),
  # which previously redownloaded an already-installed helper on every deploy.
  installed="$("$prefix/bin/rsync" --version)"
  if [[ "${installed%%$'\n'*}" == *"version $version "* ]]; then exit 0; fi
fi
stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT
curl -fsSL --connect-timeout 15 --max-time 120 --proto '=https' --tlsv1.2 "https://download.samba.org/pub/rsync/rsync-$version.tar.gz" -o "$stage/source.tgz"
actual="$(shasum -a 256 "$stage/source.tgz" | cut -d' ' -f1)"
[[ "$actual" == "$sha" ]] || { echo 'rsync source checksum mismatch' >&2; exit 1; }
tar -xzf "$stage/source.tgz" -C "$stage"
cd "$stage/rsync-$version"
./configure --prefix="$prefix" --disable-openssl --disable-xxhash --disable-zstd --disable-lz4 --disable-md2man --disable-idn
make -j2
install -d -m0755 "$prefix/bin"
install -m0755 rsync "$prefix/bin/rsync.new"
mv "$prefix/bin/rsync.new" "$prefix/bin/rsync"
install -d -m0755 "$prefix/share/licenses/rsync"
install -m0644 COPYING "$prefix/share/licenses/rsync/COPYING"
