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
# It is built from source: the host needs a C compiler, make and awk (EC2 hosts get them from install-system-tools.sh).
for tool in cc make awk; do
  command -v "$tool" >/dev/null || { echo "rsync helper needs $tool on this host; run install-system-tools.sh (or install build-essential and gawk) as root" >&2; exit 1; }
done
stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT
# samba.org is the home; the project's GitHub release carries the same file (same checksum) when it is down.
for source in "https://download.samba.org/pub/rsync/rsync-$version.tar.gz" "https://github.com/RsyncProject/rsync/releases/download/v$version/rsync-$version.tar.gz"; do
  curl -fsSL --connect-timeout 15 --max-time 120 --proto '=https' --tlsv1.2 "$source" -o "$stage/source.tgz" && break
  echo "rsync source unavailable from $source; trying the next one" >&2
  rm -f "$stage/source.tgz"
done
[[ -s "$stage/source.tgz" ]] || { echo 'rsync source could not be downloaded' >&2; exit 1; }
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
