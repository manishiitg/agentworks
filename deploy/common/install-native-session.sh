#!/usr/bin/env bash
# Root-owned, argument-free installer for the active release's slotctl and native-session (the "root step" after a deploy).
# The deploy account may run exactly this file through one sudoers line:
#   agents ALL=(root) NOPASSWD: /usr/local/libexec/agentworks/install-native-session ""
# It installs only binaries whose SHA-256 matches the root-owned build manifest of the live release (the deploy account can
# write the release folder, not the build host's manifest), copying each into a root-only folder first and checking that copy,
# so a swapped or half-written file is refused. Settings are fixed below when root installs this file; nothing is read
# from the caller. Then provision-slots.sh native-session (also installed by root) installs the checked copies.
set -euo pipefail
PRODUCT=agents
APP_DIR=/srv/agents
BUILDS_DIR=/srv/_builds
LIBEXEC_DIR=/usr/local/libexec/agentworks
[[ $# -eq 0 ]] || { echo "install-native-session takes no arguments" >&2; exit 2; }
[[ $EUID -eq 0 ]] || { echo "install-native-session must run as root" >&2; exit 2; }
export PATH=/usr/sbin:/usr/bin:/sbin:/bin
release="$(readlink -f "$APP_DIR/current")"
manifest=""
for build in "$BUILDS_DIR"/*/; do
  [[ -f "$build/SOURCE_REVISIONS" && -f "$build/manifest.json" ]] || continue
  if cmp -s "$build/SOURCE_REVISIONS" "$release/SOURCE_REVISIONS"; then manifest="$build/manifest.json"; break; fi
done
[[ -n "$manifest" ]] || { echo "refused: no build on this host matches the live release $release" >&2; exit 1; }
[[ "$(stat -c %u "$manifest")" == 0 ]] || { echo "refused: $manifest is not root-owned" >&2; exit 1; }
work="$(mktemp -d /root/.native-session.XXXXXX)"
trap 'rm -rf "$work"' EXIT
for name in slotctl native-session; do
  install -o root -g root -m 0755 "$release/bin/$name" "$work/$name"
  want="$(python3 -I -c 'import json,sys; print(json.load(open(sys.argv[1]))["files"]["bin/"+sys.argv[2]]["sha256"])' "$manifest" "$name")"
  have="$(sha256sum "$work/$name" | cut -d' ' -f1)"
  [[ "$want" == "$have" ]] || { echo "refused: bin/$name differs from the build manifest" >&2; exit 1; }
done
PRODUCT="$PRODUCT" APP_DIR="$APP_DIR" NATIVE_SOURCE_DIR="$work" bash "$LIBEXEC_DIR/provision-slots.sh" native-session
echo "native session helper and slotctl installed from $(basename "$release")"
