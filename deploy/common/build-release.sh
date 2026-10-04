#!/usr/bin/env bash
# Build a whole release ONCE (PLAT-426); every server then only copies and activates it.
#
#   build-release.sh --source mcp-agent-builder-go <url> <sha> \
#                    --source mcpagent <url> <sha> --source multi-llm-provider-go <url> <sha> \
#                    [--builds-dir /srv/_builds] [--keep 3] [--force]
#
# Runs on the build host (the Hetzner box, x86_64, as root by default: set BUILD_AS=<user> to drop to an unprivileged account that
# owns the builds dir). It
#   1. takes a lock, and returns the existing build when one already holds exactly these three revisions (unless --force);
#   2. fetches the three repositories at the given full commit ids into a scratch folder under <builds-dir>/.work;
#   3. builds into <builds-dir>/<builder-sha8>-<utc timestamp>/ : bin/ (agent with cgo native STT and bin/lib, workspace, gateway,
#      browser, landlock runner, slotctl, slottmux, mcpbridge, workspace-security.test), frontend/, static/, downloads/ (AgentWorks
#      CLI for four targets), packages/, source/ (the three repos without .git), SOURCE_REVISIONS and manifest.json (revisions, arch,
#      glibc, build time, sha256 of every file; deploy/common/release_manifest.py);
#   4. prunes all but the newest --keep builds (never one younger than an hour, never one pinned with `./deploy.sh pin`).
# Product-specific parts (runtime-config.js, brand, MCP catalog, binary names, version.json) are added by each target's activation,
# so the one build serves every product. The last output lines are BUILD_NAME=<name> and BUILD_DIR=<path>.
set -euo pipefail
umask 022

if [[ "$(uname -sm)" != "Linux x86_64" ]]; then echo "Builds run on Linux x86_64 only" >&2; exit 1; fi

BUILDS="${BUILDS_DIR:-/srv/_builds}"
KEEP=3
FORCE=0
INNER=0
WORK=""
NAME=""
STARTED="$(date +%s)"
declare -A URL REV
ORDER=(mcp-agent-builder-go mcpagent multi-llm-provider-go)
while [[ $# -gt 0 ]]; do
  case "$1" in
    --inner) INNER=1; shift ;;
    --work) WORK="$2"; shift 2 ;;
    --name) NAME="$2"; shift 2 ;;
    --started) STARTED="$2"; shift 2 ;;
    --builds-dir) BUILDS="$2"; shift 2 ;;
    --keep) KEEP="$2"; shift 2 ;;
    --force) FORCE=1; shift ;;
    --source) URL[$2]="$3"; REV[$2]="$4"; shift 4 ;;
    *) echo "Unknown argument: $1" >&2; exit 2 ;;
  esac
done
for repo in "${ORDER[@]}"; do
  [[ -n "${URL[$repo]:-}" && "${REV[$repo]:-}" =~ ^[0-9a-f]{40}$ ]] || { echo "--source $repo <url> <40-hex sha> is required" >&2; exit 2; }
done
SELF="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/$(basename -- "${BASH_SOURCE[0]}")"
PASS=(--builds-dir "$BUILDS" --keep "$KEEP" --started "$STARTED")
[[ "$FORCE" == 1 ]] && PASS+=(--force)
for repo in "${ORDER[@]}"; do PASS+=(--source "$repo" "${URL[$repo]}" "${REV[$repo]}"); done

if [[ "$INNER" == 0 ]]; then
  # ---- outer: lock, reuse, toolchain, fetch, then hand over to the script inside the fetched checkout --------------------------
  if [[ "$(id -u)" == 0 && -n "${BUILD_AS:-}" ]]; then
    install -d -m 0755 -o "$BUILD_AS" "$BUILDS"
    exec runuser -u "$BUILD_AS" -- env BUILD_AS= bash "$SELF" "${PASS[@]}"
  fi
  install -d -m 0755 "$BUILDS"
  exec 9>"$BUILDS/.lock"
  flock -w 3600 9 || { echo "Another build has held $BUILDS/.lock for an hour" >&2; exit 1; }

  existing=""
  if [[ "$FORCE" == 0 ]]; then
    existing="$(python3 - "$BUILDS" "${REV[mcp-agent-builder-go]}" "${REV[mcpagent]}" "${REV[multi-llm-provider-go]}" <<'PY'
import json, sys
from pathlib import Path
root, *shas = sys.argv[1:]
want = dict(zip(["mcp-agent-builder-go", "mcpagent", "multi-llm-provider-go"], shas))
for manifest in sorted(Path(root).glob("*/manifest.json"), reverse=True):
    try:
        if json.loads(manifest.read_text()).get("revisions") == want:
            print(manifest.parent.name)
            break
    except ValueError:
        pass
PY
)"
  fi
  if [[ -n "$existing" ]]; then
    echo "Reusing build $existing (same three revisions)"
    echo "BUILD_NAME=$existing"
    echo "BUILD_DIR=$BUILDS/$existing"
    exit 0
  fi

  export PATH="$BUILDS/.toolchain/go/bin:$HOME/.local/go/bin:/usr/local/go/bin:$PATH"
  if ! command -v go >/dev/null; then
    echo "Installing the pinned Go toolchain into $BUILDS/.toolchain"
    install -d -m 0755 "$BUILDS/.toolchain"
    tmp="$(mktemp -d "$BUILDS/.toolchain/dl.XXXXXX")"
    curl --fail --location --silent --show-error https://go.dev/dl/go1.27.1.linux-amd64.tar.gz -o "$tmp/go.tar.gz"
    curl --fail --location --silent --show-error 'https://go.dev/dl/?mode=json&include=all' -o "$tmp/go-releases.json"
    python3 - "$tmp" <<'PY'
import hashlib, json, pathlib, sys
p = pathlib.Path(sys.argv[1])
expected = next(f['sha256'] for v in json.loads((p/'go-releases.json').read_text()) for f in v['files'] if f['filename'] == 'go1.27.1.linux-amd64.tar.gz')
assert hashlib.sha256((p/'go.tar.gz').read_bytes()).hexdigest() == expected, 'Go checksum mismatch'
PY
    tar -xzf "$tmp/go.tar.gz" -C "$BUILDS/.toolchain"
    rm -rf "$tmp"
  fi

  NAME="${REV[mcp-agent-builder-go]:0:8}-$(date -u +%Y%m%d%H%M%S)"
  WORK="$BUILDS/.work/$NAME"
  rm -rf "$WORK"; mkdir -p "$WORK/source"
  trap 'rm -rf "$WORK"' EXIT
  for repo in "${ORDER[@]}"; do
    echo "Fetching $repo at ${REV[$repo]:0:10}"
    dir="$WORK/source/$repo"
    git init -q "$dir"
    git -C "$dir" remote add origin "${URL[$repo]}"
    GIT_TERMINAL_PROMPT=0 git -C "$dir" -c protocol.file.allow=always fetch -q --depth 1 origin "${REV[$repo]}" \
      || GIT_TERMINAL_PROMPT=0 git -C "$dir" -c protocol.file.allow=always fetch -q origin
    git -C "$dir" -c advice.detachedHead=false checkout -q --detach "${REV[$repo]}"
    [[ "$(git -C "$dir" rev-parse HEAD)" == "${REV[$repo]}" ]] || { echo "$repo: checked out the wrong commit" >&2; exit 1; }
  done
  # Not exec: the trap must still remove the scratch folder, and the lock (fd 9) stays held by this shell.
  bash "$WORK/source/mcp-agent-builder-go/deploy/common/build-release.sh" --inner --work "$WORK" --name "$NAME" "${PASS[@]}"
  exit $?
fi

# ---- inner: the build itself, from the checkout the outer step fetched ------------------------------------------------------------
[[ -n "$WORK" && -n "$NAME" ]] || { echo "--inner needs --work and --name" >&2; exit 2; }
WORKSPACE_ROOT="$WORK/source"
REPO_ROOT="$WORKSPACE_ROOT/mcp-agent-builder-go"
OUT="$BUILDS/$NAME.partial"
JOBS="${BUILD_JOBS:-$(nproc)}"
export PATH="$BUILDS/.toolchain/go/bin:$HOME/.local/go/bin:/usr/local/go/bin:$PATH"
export GOMAXPROCS="$JOBS" GOFLAGS="-p=$JOBS" NODE_OPTIONS=--max-old-space-size=4096
export GOCACHE="$BUILDS/.cache/go-build" GOMODCACHE="$BUILDS/.cache/gomod" npm_config_cache="$BUILDS/.cache/npm"
mkdir -p "$GOCACHE" "$GOMODCACHE" "$npm_config_cache"
for command in git go gcc npm node python3 file sha256sum; do command -v "$command" >/dev/null || { echo "Missing $command" >&2; exit 1; }; done
rm -rf "$OUT"
mkdir -p "$OUT/bin" "$OUT/frontend" "$OUT/static" "$OUT/downloads" "$OUT/packages"
trap 'rm -rf "$WORK" "$OUT"' EXIT
step() { echo "==> [$NAME] $* ($(( $(date +%s) - STARTED ))s)"; }

# Build exactly these checkouts, resolving the shared sibling modules from the scratch workspace rather than any checked-in go.work.
DEPLOY_GOWORK="$OUT/go.work"
(cd "$OUT" && go work init "$REPO_ROOT/agent_go" "$REPO_ROOT/workspace" "$WORKSPACE_ROOT/mcpagent" "$WORKSPACE_ROOT/multi-llm-provider-go")
{
  for repo in "${ORDER[@]}"; do printf '%s=%s\n' "$repo" "${REV[$repo]}"; done
} > "$OUT/SOURCE_REVISIONS"

step "Saving the source (no .git, dependency or build folders)"
for repo in "${ORDER[@]}"; do
  mkdir -p "$OUT/source/$repo"
  tar -C "$WORKSPACE_ROOT/$repo" --exclude=.git --exclude=node_modules --exclude=dist -cf - . | tar -C "$OUT/source/$repo" -xf -
done

gobuild() { # gobuild OUTPUT PACKAGE [GOOS GOARCH CGO]
  (cd "$WORKSPACE_ROOT" && GOWORK="$DEPLOY_GOWORK" GOOS="${3:-linux}" GOARCH="${4:-amd64}" CGO_ENABLED="${5:-0}" go build -o "$1" "$2")
}

step "Building binaries (native linux/amd64, cgo agent with sherpa-onnx)"
# The agent links sherpa-onnx (voice/STT) with cgo and an $ORIGIN/lib rpath; the libraries are staged in bin/lib beside it.
bash "$REPO_ROOT/deploy/aws-ec2/build/build-linux-agent.sh" "$OUT" "$REPO_ROOT/agent_go" "$WORKSPACE_ROOT"
mv "$OUT/bin/video-studio-agent" "$OUT/bin/agent"
gobuild "$OUT/bin/workspace" "$REPO_ROOT/workspace"
gobuild "$OUT/bin/browser" "$REPO_ROOT/workspace/cmd/shared-browser"
# Literal filename: workspace/security/landlock_policy.go resolves its sandbox launcher by this exact name for every product.
gobuild "$OUT/bin/video-studio-landlock-runner" "$REPO_ROOT/workspace/cmd/landlock-runner"
(cd "$WORKSPACE_ROOT" && GOWORK="$DEPLOY_GOWORK" GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c -o "$OUT/bin/workspace-security.test" "$REPO_ROOT/workspace/security")
# Per-user accounts: slotctl and slottmux (deploy/common/slots.sh, shared with both activation scripts).
source "$REPO_ROOT/deploy/common/slots.sh"
slots_build "$WORKSPACE_ROOT" "$DEPLOY_GOWORK" "$REPO_ROOT" "$OUT"
gobuild "$OUT/bin/mcpbridge" ./mcpagent/cmd/mcpbridge
gobuild "$OUT/bin/gateway" "$REPO_ROOT/deploy/aws-ec2/server/auth-gateway.go"
install -m 0755 "$REPO_ROOT/deploy/aws-ec2/server/update-coding-clis.sh" "$OUT/bin/update-coding-clis"

step "Building AgentWorks CLI downloads"
for target in darwin-arm64 darwin-amd64 linux-amd64 linux-arm64; do
  os="${target%-*}"; arch="${target#*-}"; name="agentworks-$target"
  (cd "$WORKSPACE_ROOT" && GOWORK="$DEPLOY_GOWORK" GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 go build -ldflags "-X main.cliVersion=${REV[mcp-agent-builder-go]}" -o "$OUT/downloads/$name" "$REPO_ROOT/agent_go/cmd/agentworks")
  chmod +x "$OUT/downloads/$name"
  (cd "$OUT/downloads" && sha256sum "$name" > "$name.sha256" && sha256sum -c "$name.sha256")
  case "$target" in darwin-*) expected=Mach-O ;; linux-*) expected=ELF ;; esac
  actual="$(file -b "$OUT/downloads/$name")"
  [[ "$actual" == *"$expected"* ]] || { echo "Invalid $name binary: $actual" >&2; exit 1; }
done
install -m 0644 "$REPO_ROOT/scripts/install-agentworks-cli.sh" "$OUT/downloads/install-agentworks.sh"
bash -n "$OUT/downloads/install-agentworks.sh"

step "Building frontend"
(cd "$REPO_ROOT/frontend" && npm ci)
(cd "$REPO_ROOT/frontend" && VITE_API_BASE_URL='' VITE_WORKSPACE_API_URL=/api/wp npm run build)
cp -R "$REPO_ROOT/frontend/dist/." "$OUT/frontend/"
node "$REPO_ROOT/frontend/scripts/check-release-assets.mjs" "$OUT/frontend"
# The frontend build writes report-preview.js here, into the checkout, never into dist; the agent serves it from ./static/.
cp -R "$REPO_ROOT/agent_go/cmd/server/static/." "$OUT/static/"
python3 "$REPO_ROOT/scripts/build-playwright-packages.py" "$OUT/packages" >/dev/null

# A binary that cannot find a shared library must never be announced as a build.
missing="$(LD_LIBRARY_PATH="$OUT/bin/lib" ldd "$OUT/bin/agent" | grep 'not found' || true)"
[[ -z "$missing" ]] || { echo "agent has unresolved libraries: $missing" >&2; exit 1; }
rm -f "$OUT/go.work" "$OUT/go.work.sum"
chmod -R u+rwX,go+rX,go-w "$OUT"

step "Writing manifest.json"
elapsed=$(( $(date +%s) - STARTED ))
revision_args=()
for repo in "${ORDER[@]}"; do revision_args+=(--rev "$repo=${REV[$repo]}"); done
python3 "$REPO_ROOT/deploy/common/release_manifest.py" create "$OUT" --name "$NAME" "${revision_args[@]}" --build-seconds "$elapsed" \
  --note "go=$(go env GOVERSION)" --note "node=$(node --version)" --note "builder=$(id -un)"
python3 "$REPO_ROOT/deploy/common/release_manifest.py" verify "$OUT" >/dev/null
chmod 0644 "$OUT/manifest.json"
mv "$OUT" "$BUILDS/$NAME"
chmod 0755 "$BUILDS/$NAME"

# Keep the newest $KEEP builds; never remove one younger than an hour (an activation may still be copying from it) and clear stale scratch.
python3 - "$BUILDS" "$KEEP" <<'PY'
import re, shutil, sys, time
from pathlib import Path
root, keep = Path(sys.argv[1]), int(sys.argv[2])
# A pinned build (marker file in <builds>/.pinned/, `./deploy.sh pin <build>`) is a known-good one to deploy later: it is never
# removed and does not count toward the newest $KEEP.
pinned = {m.name for m in (root / ".pinned").glob("*")} if (root / ".pinned").is_dir() else set()
builds = sorted((d for d in root.iterdir() if re.fullmatch(r"[0-9a-f]{8}-\d{14}", d.name) and (d / "manifest.json").is_file() and d.name not in pinned), key=lambda d: d.name, reverse=True)
for old in builds[keep:]:
    if time.time() - (old / "manifest.json").stat().st_mtime > 3600:
        print(f"pruning old build {old.name}")
        shutil.rmtree(old)
for stale in list(root.glob("*.partial")) + list((root / ".work").glob("*")):
    if time.time() - stale.stat().st_mtime > 86400:
        shutil.rmtree(stale, ignore_errors=True)
PY
trap 'rm -rf "$WORK"' EXIT
step "Build complete"
echo "BUILD_NAME=$NAME"
echo "BUILD_DIR=$BUILDS/$NAME"
