#!/usr/bin/env bash
# Download a published build and check it (PLAT-426). Runs ON THE TARGET SERVER (a product account, or video-studio on RTS); needs curl, tar, gzip, python3.
#
#   fetch-build.sh <tag> <asset> <expected-manifest-sha256> <dest>
#
#   tag      build-<builder8>-<mcpagent8>-<provider8>, a release of github.com/manishiitg/agentworks-builds (public: no credential)
#   asset    build.tar.gz (the whole build) or build-rts.tar.gz (the trimmed copy for RTS)
#   hash     sha256 of the build's manifest.json as announced by the build host (deploy.sh reads it there)
#   dest     the folder to create; it must not exist
#
# Downloads with resume, retries and timeouts, extracts, and refuses unless manifest.json inside hashes to the announced value; the
# normal manifest verification (every file) runs later, unchanged, in the activation script. A failed, tampered or wrong download
# leaves nothing behind: everything happens in a scratch folder next to dest, and dest appears only at the very end.
# BUILDS_DOWNLOAD_BASE overrides https://github.com/manishiitg/agentworks-builds/releases/download (tests).
set -euo pipefail

[[ $# -eq 4 ]] || { echo "Usage: fetch-build.sh <tag> <asset> <expected-manifest-sha256> <dest>" >&2; exit 2; }
TAG="$1"; ASSET="$2"; WANT="$3"; DEST="$4"
BASE="${BUILDS_DOWNLOAD_BASE:-https://github.com/manishiitg/agentworks-builds/releases/download}"
[[ "$TAG" =~ ^build-[0-9a-f]{8}-[0-9a-f]{8}-[0-9a-f]{8}(-arm64)?$ ]] || { echo "fetch-build: invalid tag: $TAG" >&2; exit 2; }
[[ "$ASSET" == build.tar.gz || "$ASSET" == build-rts.tar.gz ]] || { echo "fetch-build: asset must be build.tar.gz or build-rts.tar.gz" >&2; exit 2; }
[[ "$WANT" =~ ^[0-9a-f]{64}$ ]] || { echo "fetch-build: the expected manifest sha256 must be 64 hex characters" >&2; exit 2; }
[[ ! -e "$DEST" ]] || { echo "fetch-build: $DEST already exists" >&2; exit 2; }
command -v curl >/dev/null && command -v python3 >/dev/null || { echo "fetch-build: needs curl and python3" >&2; exit 1; }

PARENT="$(dirname -- "$DEST")"
mkdir -p "$PARENT"
WORK="$(mktemp -d "$PARENT/.fetch.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT
FILE="$WORK/$ASSET"
URL="$BASE/$TAG/$ASSET"

echo "fetch-build: downloading $URL"
got=0
for attempt in 1 2 3 4 5 6 7 8; do
  rc=0
  curl --fail --silent --show-error --location --connect-timeout 20 --max-time 1800 --speed-limit 20000 --speed-time 60 \
    --retry 3 --retry-delay 2 --continue-at - --output "$FILE" "$URL" || rc=$?
  if [[ "$rc" == 0 ]]; then got=1; break; fi
  if [[ "$rc" == 33 ]] && gzip -t "$FILE" 2>/dev/null; then got=1; break; fi  # range refused because the file is already whole
  if [[ "$rc" == 22 ]]; then break; fi                                           # HTTP error (404: no such release): resuming cannot help
  echo "fetch-build: attempt $attempt failed (curl exit $rc); resuming" >&2
  sleep 2
done
[[ "$got" == 1 ]] || { echo "fetch-build: could not download $URL" >&2; exit 1; }

gzip -t "$FILE" 2>/dev/null || { echo "fetch-build: the download is not a complete gzip file (truncated?)" >&2; exit 1; }
# Every entry must sit under one top folder; no absolute or .. paths.
tar -tzf "$FILE" | python3 -c '
import sys
tops = set()
for line in sys.stdin:
    p = line.rstrip("\n")
    if p.startswith("/") or ".." in p.split("/"):
        sys.exit("fetch-build: unsafe path in the archive: " + p)
    tops.add(p.split("/")[0])
if len(tops) != 1:
    sys.exit("fetch-build: the archive must hold exactly one top folder, found %d" % len(tops))
print(tops.pop())' > "$WORK/top" || exit 1
TOP="$(cat "$WORK/top")"
mkdir "$WORK/x"
tar -xzf "$FILE" -C "$WORK/x"
rm -f "$FILE"
[[ -f "$WORK/x/$TOP/manifest.json" ]] || { echo "fetch-build: the download has no manifest.json" >&2; exit 1; }
have="$(python3 -c 'import hashlib,sys; print(hashlib.sha256(open(sys.argv[1],"rb").read()).hexdigest())' "$WORK/x/$TOP/manifest.json")"
if [[ "$have" != "$WANT" ]]; then
  echo "fetch-build: REFUSING the download: manifest.json hash is $have, expected $WANT" >&2
  exit 1
fi
mv "$WORK/x/$TOP" "$DEST"
echo "fetch-build: $TAG/$ASSET is in $DEST (manifest.json sha256 $have)"
