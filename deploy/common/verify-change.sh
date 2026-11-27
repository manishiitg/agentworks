#!/usr/bin/env bash
# Verify one commit on the build host instead of GitHub: the same steps as .github/workflows/verify-change.yml, much faster.
#
#   verify-change.sh --url <https git url> --sha <40-hex> [--builds-dir /srv/_builds]
#
# Runs on the Linux build host (see deploy/common/build-once.sh, verify_change_remote). It
#   1. checks build disk free space, fetches the commit and main into a kept mirror (<builds-dir>/.verify-mirror, only new objects
#      each time), clones it locally, and inspects what changed against main and the Verify-* lines of the commit message;
#   2. takes a slot lock before running heavy checks (it does NOT take the build lock: builds stay unblocked):
#      - Go runs take one of VERIFY_SLOTS (default 4) locks <builds-dir>/.verify.lock.<n> (each owns its own GOCACHE);
#      - frontend-only runs take one of VERIFY_FRONTEND_SLOTS (default 2) locks <builds-dir>/.verify-frontend.lock.<n> so they do
#        not starve or wait on Go slots;
#      - while any deploy holds its lock (<builds-dir>/.deploys/*), all Go and frontend verifies throttle to slot 1;
#   3. Go (when agent_go, workspace, mcp-gateway, mcpagent or multi-llm-provider-go changed, or a Verify-Test line exists):
#      LRU-trims that slot's GOCACHE when over cap (default 3 GiB -> 2 GiB target, keeping 4 slots within the ~12 GiB budget),
#      build ./..., vet the changed packages (each library in its own module), cross-build and vet the macOS CLI (only when
#      something outside agent_go/cmd/server changed: the CLI never imports it), then each Verify-Test line as `go test <line> -count=1`
#      (a line starting with workspace, mcp-gateway, mcpagent or multi-llm-provider-go runs there);
#   4. website (when frontend/ changed or a Verify-Vitest line exists): links cached node_modules keyed by
#      sha256(frontend/package-lock.json) (building under lock on cache miss, pruning old hashes, with per-run writable .tmp/.vite),
#      semantic theme contract, tsc -b, Verify-Vitest files, and `npm run build` with Verify-Frontend-Build: true;
#      when both Go and website checks are needed, they run in parallel with separate non-interleaved failure logs;
#   5. prints VERIFY: PASSED or VERIFY: FAILED.
# Not run here: golangci-lint (new issues only) and Verify-Browser; use scripts/verify-remote.sh (GitHub) when those matter.
# Tests run as an unprivileged account (VERIFY_AS, default nobody): as root, permission tests would pass or fail for the wrong reason.
# Go, npm and node_modules caches live in <builds-dir>/.verify-cache (kept between runs; only verification uses them).
set -euo pipefail
umask 022

URL=""; SHA=""; BUILDS="${BUILDS_DIR:-/srv/_builds}"; INNER=0; WORK=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --url) URL="$2"; shift 2 ;;
    --sha) SHA="$2"; shift 2 ;;
    --builds-dir) BUILDS="$2"; shift 2 ;;
    --inner) INNER=1; shift ;;
    --work) WORK="$2"; shift 2 ;;
    *) echo "Unknown argument: $1" >&2; exit 2 ;;
  esac
done
[[ -n "$URL" && "$SHA" =~ ^[0-9a-f]{40}$ ]] || { echo "--url and a full 40-hex --sha are required" >&2; exit 2; }
USER_AS="${VERIFY_AS:-nobody}"
export PATH="$BUILDS/.toolchain/go/bin:$HOME/.local/go/bin:/usr/local/go/bin:$PATH"
STARTED="${VERIFY_STARTED:-$(date +%s)}"
step() { echo "==> [${SHA:0:8}] $* ($(( $(date +%s) - STARTED ))s)"; }

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

detect_needed_checks() { # changed_file message_file -> sets go_needed, frontend_needed
  local changed="$1" message="$2"
  go_needed=false; frontend_needed=false
  if grep -qE '^(agent_go|workspace|mcp-gateway|mcpagent|multi-llm-provider-go)/' "$changed" || grep -q '^Verify-Test:' "$message"; then
    go_needed=true
  fi
  # The pre-commit hook bumps frontend/src/version.ts on every commit; that alone does not need a website check.
  if { grep '^frontend/' "$changed" || true; } | grep -qvx 'frontend/src/version.ts' || grep -q '^Verify-Vitest:' "$message"; then
    frontend_needed=true
  fi
}

# #3035: check the actual build filesystem before downloads or compilation.
# Embedded in both standalone launchers: neither has a source checkout yet.
check_build_disk() {
python3 - "$BUILDS" <<'DISK_CHECK'
import math, os, shutil, sys
minimum = os.environ.get("BUILD_DISK_MIN_FREE_GIB", "10")
try:
    minimum = float(minimum)
    if not math.isfinite(minimum) or minimum < 0:
        raise ValueError()
except ValueError:
    print("[DISK_CHECK] event=refused reason=invalid_minimum BUILD_DISK_MIN_FREE_GIB must be a nonnegative number", file=sys.stderr)
    raise SystemExit(2)
if "BUILD_DISK_MIN_FREE_GIB" in os.environ:
    print(f"[DISK_CHECK] event=operator_override minimum_free_gib={minimum:g}", file=sys.stderr)
try:
    usage = shutil.disk_usage(sys.argv[1])
except OSError as exc:
    print(f"[DISK_CHECK] event=refused reason=check_failed error={exc}", file=sys.stderr)
    raise SystemExit(2)
free_gib = usage.free / (1024 ** 3)
percent = math.ceil(100 * usage.used / max(1, usage.used + usage.free))
print(f"[DISK_CHECK] used_percent={percent} free_gib={free_gib:.2f}", file=sys.stderr)
if percent >= 85:
    print("WARNING: build disk is at least 85% full. Cleanup is needed.", file=sys.stderr)
if usage.free < minimum * 1024 ** 3:
    print(f"Build not started: less than {minimum:g} GiB free on the build disk. Free space and retry.", file=sys.stderr)
    raise SystemExit(2)
DISK_CHECK
}

# Trim one slot's GOCACHE oldest-first (LRU) down to target_mib when it exceeds max_mib,
# instead of wiping the entire cache to 0 bytes.
trim_gocache() { # dir max_mib target_mib
python3 - "$1" "$2" "$3" <<'GOCACHE_TRIM'
import os, sys

cache_dir = sys.argv[1]
max_bytes = int(float(sys.argv[2]) * 1024 * 1024)
target_bytes = int(float(sys.argv[3]) * 1024 * 1024)
if target_bytes > max_bytes:
    target_bytes = max_bytes
if not os.path.isdir(cache_dir):
    raise SystemExit(0)

entries = []
total = 0

def scan(path, is_root=False):
    global total
    try:
        with os.scandir(path) as it:
            for entry in it:
                if is_root and entry.name in ("README", "trim.txt"):
                    continue
                try:
                    if entry.is_dir(follow_symlinks=False):
                        scan(entry.path, False)
                    elif entry.is_file(follow_symlinks=False):
                        st = entry.stat(follow_symlinks=False)
                        last_used = max(st.st_mtime, st.st_atime)
                        entries.append((last_used, st.st_mtime, st.st_size, entry.path))
                        total += st.st_size
                except OSError:
                    pass
    except OSError:
        pass

scan(cache_dir, True)
if total <= max_bytes:
    raise SystemExit(0)

entries.sort(key=lambda item: (item[0], item[1]))
before = total
removed = 0
for _, _, size, path in entries:
    if total <= target_bytes:
        break
    try:
        os.unlink(path)
        total -= size
        removed += 1
    except OSError:
        pass

gib = 1024 ** 3
print(
    f"[GOCACHE_TRIM] dir={os.path.basename(cache_dir)} "
    f"before_gib={before / gib:.2f} after_gib={total / gib:.2f} removed={removed}",
    file=sys.stderr,
)
GOCACHE_TRIM
}

# Acquire a slot lock on fd 8 according to the commit's workload:
#   - go:       one of VERIFY_SLOTS (default 4) locks .verify.lock.<n>
#   - frontend: one of VERIFY_FRONTEND_SLOTS (default 2) locks .verify-frontend.lock.<n>
#   - none:     no slot lock needed (docs / Verify-Deploy only)
# While any deploy holds its lock (<builds>/.deploys/*), both Go and frontend throttle to .verify.lock.1.
acquire_verify_slot() { # mode (go|frontend|none) -> sets slot and opens fd 8 when locked
  local mode="$1"
  slot=""
  [[ "$mode" != none ]] || return 0
  local waited=0 announced=0 wait_step="${VERIFY_SLOT_WAIT_SECONDS:-5}" wait_timeout="${VERIFY_SLOT_TIMEOUT_SECONDS:-1800}"
  while [[ -z "$slot" ]]; do
    local deploy_active=false slots lock_prefix
    if compgen -G "$BUILDS/.deploys/*" >/dev/null; then
      deploy_active=true
    fi
    if [[ "$deploy_active" == true ]]; then
      slots=1
      lock_prefix="$BUILDS/.verify.lock"
    elif [[ "$mode" == frontend ]]; then
      slots="${VERIFY_FRONTEND_SLOTS:-2}"
      lock_prefix="$BUILDS/.verify-frontend.lock"
    else
      slots="${VERIFY_SLOTS:-4}"
      lock_prefix="$BUILDS/.verify.lock"
    fi
    for n in $(seq 1 "$slots"); do
      exec 8>"${lock_prefix}.${n}"
      if flock -n 8; then
        if [[ "$mode" == frontend && "$deploy_active" == false ]]; then
          slot="frontend-$n"
        else
          slot="$n"
        fi
        break
      fi
      exec 8>&-
    done
    if [[ -z "$slot" ]]; then
      if [[ "$announced" == 0 ]]; then
        step "Waiting for a free $mode verify slot ($slots in use)"
        announced=1
      fi
      waited="$(python3 -c 'import sys; print(float(sys.argv[1]) + float(sys.argv[2]))' "$waited" "$wait_step")"
      if python3 -c 'import sys; raise SystemExit(0 if float(sys.argv[1]) >= float(sys.argv[2]) else 1)' "$waited" "$wait_timeout"; then
        echo "No free verify slot for ${wait_timeout}s" >&2
        exit 1
      fi
      sleep "$wait_step"
    fi
  done
  step "Acquired verify slot $slot"
}

# Create/own only top-level cache directories (O(1), never recursive chown -R over $CACHE).
ensure_cache_dirs() { # user group dir...
  local user="$1" group="$2"
  shift 2
  local d
  for d in "$@"; do
    if [[ "$(id -u)" == 0 ]]; then
      install -d -o "$user" -g "$group" -m 0755 "$d"
    else
      mkdir -p "$d"
    fi
  done
}

if [[ "$INNER" == 0 ]]; then
  # ---- outer (root): check disk, fetch, pick slot, hand checkout to an unprivileged account --------------------------------
  install -d -m 0755 "$BUILDS"
  check_build_disk
  if [[ "$(id -u)" == 0 ]]; then
    id -u "$USER_AS" >/dev/null 2>&1 || { echo "Account $USER_AS does not exist; set VERIFY_AS" >&2; exit 1; }
  fi
  WORK="$BUILDS/.work/verify-${SHA:0:8}-$(date -u +%Y%m%d%H%M%S)-$$"
  CACHE="$BUILDS/.verify-cache"
  mkdir -p "$WORK"
  # Full logs of failing steps stay for two days (the scratch folder is deleted with the run).
  install -d -m 1777 "$BUILDS/.verify-logs"
  find "$BUILDS/.verify-logs" -type f -mtime +2 -delete 2>/dev/null || true
  trap 'rm -rf "$WORK"' EXIT
  step "Fetching the commit and main"
  MIRROR="$BUILDS/.verify-mirror"
  (
    flock -w 600 9 || { echo "The verify mirror has been busy for 10 minutes" >&2; exit 1; }
    [[ -d "$MIRROR/objects" ]] || git init -q --bare "$MIRROR"
    git -C "$MIRROR" remote get-url origin >/dev/null 2>&1 || git -C "$MIRROR" remote add origin "$URL"
    git -C "$MIRROR" remote set-url origin "$URL"
    GIT_TERMINAL_PROMPT=0 git -C "$MIRROR" fetch -q origin "+refs/heads/main:refs/heads/main" "$SHA"
  ) 9>"$BUILDS/.verify-mirror.lock"
  # --shared reads objects from the mirror; the mirror only grows, so no object can vanish under a running verification.
  git clone -q --shared --no-checkout "$MIRROR" "$WORK/repo"
  git -C "$WORK/repo" -c advice.detachedHead=false checkout -q --detach "$SHA"
  [[ "$(git -C "$WORK/repo" rev-parse HEAD)" == "$SHA" ]] || { echo "Checked out the wrong commit" >&2; exit 1; }

  # Classify the commit before queueing so frontend-only runs do not hold or wait on Go slots.
  base="$(git -C "$WORK/repo" merge-base HEAD origin/main)"
  git -C "$WORK/repo" diff --name-only "$base" HEAD > "$WORK/changed.txt"
  git -C "$WORK/repo" log -1 --format=%B > "$WORK/message.txt"
  detect_needed_checks "$WORK/changed.txt" "$WORK/message.txt"

  slot_mode="none"
  if [[ "$go_needed" == true ]]; then
    command -v go >/dev/null || { echo "No Go toolchain on this host: run ./deploy.sh build once (it installs one)." >&2; exit 1; }
    slot_mode="go"
  elif [[ "$frontend_needed" == true ]]; then
    slot_mode="frontend"
  fi
  acquire_verify_slot "$slot_mode"

  GROUP_AS=""
  if [[ "$(id -u)" == 0 ]]; then
    GROUP_AS="$(id -gn "$USER_AS")"
  fi
  cache_dirs=("$CACHE" "$CACHE/gomod" "$CACHE/npm" "$CACHE/node_modules")
  [[ "$slot_mode" != go ]] || cache_dirs+=("$CACHE/go-build-$slot")
  ensure_cache_dirs "$USER_AS" "$GROUP_AS" "${cache_dirs[@]}"

  # Only chown the fresh scratch checkout, never recursively walk $CACHE.
  if [[ "$(id -u)" == 0 ]]; then
    chown -R "$USER_AS:$GROUP_AS" "$WORK"
  fi
  # A queued caller may replace the shared launcher; test this commit's script.
  cp "$WORK/repo/deploy/common/verify-change.sh" "$WORK/verify-change.sh"; chmod 0755 "$WORK/verify-change.sh"
  if [[ "$(id -u)" == 0 ]]; then
    chown "$USER_AS:$GROUP_AS" "$WORK/verify-change.sh"
  fi
  step "Verifier hashes: checkout=$(sha256_file "$WORK/repo/deploy/common/verify-change.sh") inner=$(sha256_file "$WORK/verify-change.sh")"
  # Not exec: the trap must still remove the scratch folder and the lock (fd 8) stays held.
  slot_env="${slot:-none}"
  if [[ "$(id -u)" == 0 ]]; then
    runuser -u "$USER_AS" -- env "PATH=$PATH" "HOME=$WORK/home" "VERIFY_STARTED=$STARTED" "VERIFY_CACHE=$CACHE" "VERIFY_SLOT=$slot_env" \
      bash "$WORK/verify-change.sh" --inner --work "$WORK" --url "$URL" --sha "$SHA" --builds-dir "$BUILDS"
  else
    env "PATH=$PATH" "HOME=$WORK/home" "VERIFY_STARTED=$STARTED" "VERIFY_CACHE=$CACHE" "VERIFY_SLOT=$slot_env" \
      bash "$WORK/verify-change.sh" --inner --work "$WORK" --url "$URL" --sha "$SHA" --builds-dir "$BUILDS"
  fi
  exit $?
fi

# ---- inner (unprivileged): the checks ------------------------------------------------------------------------------------------
mkdir -p "$WORK/home"
CACHE="${VERIFY_CACHE:?}"
export GOMODCACHE="$CACHE/gomod" npm_config_cache="$CACHE/npm"
export GOWORK=off GOTOOLCHAIN=local GOFLAGS="-modcacherw" NODE_OPTIONS=--max-old-space-size=4096
cd "$WORK/repo"
base="$(git merge-base HEAD origin/main)"
git diff --name-only "$base" HEAD > "$WORK/changed.txt"
git log -1 --format=%B > "$WORK/message.txt"
echo "changed files:"; sed 's/^/  /' "$WORK/changed.txt"

fail() { echo; echo "VERIFY: FAILED at: $*" >&2; exit 1; }
run() { # run <label> <command...>: prints the last lines of the output when it fails
  local label="$1" log="${VERIFY_STEP_LOG:-$WORK/step.log}"; shift
  step "$label"
  if ! "$@" >"$log" 2>&1; then
    # The first failures and the name of a hanging test come before the long tail, so print them first.
    grep -E '^(--- FAIL|    --- FAIL|FAIL|panic: |running tests:|	Test[A-Za-z0-9_]+ \()' "$log" | head -n 60 >&2 || true
    echo "---- last lines ----" >&2
    tail -n 40 "$log" >&2
    kept="$BUILDS/.verify-logs/${SHA:0:8}-$(date +%H%M%S)-${VERIFY_TRACK:-main}-$$.log"
    cp "$log" "$kept" 2>/dev/null && echo "full log kept on the build host: $kept" >&2
    fail "$label"
  fi
  # Focused tests may publish non-sensitive measurements explicitly. Keep
  # these visible on success too: step.log is overwritten by the next step.
  grep -E 'VERIFY-METRIC: ' "$log" || true
}

# Populate $CACHE/node_modules/<sha256>/node_modules under lock on miss, prune old hashes,
# and link top-level entries into $WORK/repo/frontend/node_modules (keeping .tmp/.vite writable per run).
prepare_frontend_node_modules() {
  local frontend_dir="$1"
  local lockfile="$frontend_dir/package-lock.json"
  if [[ ! -f "$lockfile" ]]; then
    run "npm ci" npm ci --no-audit --no-fund
    return 0
  fi
  local nm_root="$CACHE/node_modules"
  mkdir -p "$nm_root"
  local lock_hash node_ver entry_dir ready_file cached_nm keep min_prune_age cache_status
  lock_hash="$(sha256_file "$lockfile")"
  node_ver="$(node -v)"
  entry_dir="$nm_root/$lock_hash"
  ready_file="$entry_dir/.verify-ready"
  cached_nm="$entry_dir/node_modules"
  keep="${VERIFY_NODE_MODULES_KEEP:-2}"
  min_prune_age="${VERIFY_NODE_MODULES_PRUNE_AGE_SECONDS:-1800}"

  cache_status="hit"
  exec 7>"$nm_root/.lock"
  flock -w 600 7 || { exec 7>&-; fail "node_modules cache lock busy for 10 minutes"; }

  # Clean stale staging directories from interrupted runs.
  python3 - "$nm_root" <<'STALE_STAGE'
import os, shutil, sys, time
root = sys.argv[1]
now = time.time()
try:
    with os.scandir(root) as it:
        for entry in it:
            if entry.name.startswith(".stage-") and entry.is_dir(follow_symlinks=False):
                try:
                    if now - entry.stat(follow_symlinks=False).st_mtime > 3600:
                        shutil.rmtree(entry.path, ignore_errors=True)
                except OSError:
                    pass
except OSError:
    pass
STALE_STAGE

  if [[ -f "$ready_file" && -d "$cached_nm" && "$(cat "$ready_file" 2>/dev/null || true)" == "$node_ver" ]]; then
    touch "$ready_file"
  else
    cache_status="miss"
    local stage="$nm_root/.stage-${lock_hash:0:12}-$$"
    rm -rf "$stage"
    mkdir -p "$stage"
    cp "$frontend_dir/package.json" "$lockfile" "$stage/"
    if ! ( cd "$stage" && run "npm ci (cache build ${lock_hash:0:12})" npm ci --no-audit --no-fund ); then
      rm -rf "$stage"
      exec 7>&-
      exit 1
    fi
    printf '%s\n' "$node_ver" > "$stage/.verify-ready"
    if [[ -e "$entry_dir" ]]; then
      local old="$nm_root/.old-${lock_hash:0:12}-$$"
      mv "$entry_dir" "$old" 2>/dev/null || rm -rf "$entry_dir"
      mv "$stage" "$entry_dir"
      rm -rf "$old"
    else
      mv "$stage" "$entry_dir"
    fi
  fi

  # Prune old lockfile hashes beyond keep (default 2), skipping recently used hashes still needed by concurrent runs.
  python3 - "$nm_root" "$lock_hash" "$keep" "$min_prune_age" <<'NM_PRUNE'
import os, re, shutil, sys, time

root, current, keep, min_age = sys.argv[1], sys.argv[2], int(sys.argv[3]), float(sys.argv[4])
now = time.time()
hex64 = re.compile(r"^[0-9a-f]{64}$")
entries = []
try:
    with os.scandir(root) as it:
        for entry in it:
            if not entry.is_dir(follow_symlinks=False) or not hex64.match(entry.name):
                continue
            ready = os.path.join(entry.path, ".verify-ready")
            try:
                mtime = os.stat(ready).st_mtime if os.path.isfile(ready) else entry.stat(follow_symlinks=False).st_mtime
            except OSError:
                mtime = 0.0
            entries.append((mtime, entry.name, entry.path))
except OSError:
    raise SystemExit(0)

entries.sort(key=lambda item: item[0], reverse=True)
for idx, (mtime, name, path) in enumerate(entries):
    if name == current or idx < keep:
        continue
    if now - mtime < min_age:
        continue
    shutil.rmtree(path, ignore_errors=True)
    print(f"[NODE_MODULES_CACHE] pruned={name[:12]}", file=sys.stderr)
NM_PRUNE
  exec 7>&-

  # Real node_modules dir with symlinks to top-level cached packages so .tmp (tsc -b) and .vite stay isolated per run.
  rm -rf "$frontend_dir/node_modules"
  mkdir -p "$frontend_dir/node_modules"
  python3 - "$cached_nm" "$frontend_dir/node_modules" <<'NM_LINK'
import os, sys

src, dst = sys.argv[1], sys.argv[2]
skip = {".tmp", ".vite", ".vite-temp", ".cache", ".verify-ready"}
with os.scandir(src) as it:
    for entry in it:
        if entry.name in skip:
            continue
        os.symlink(entry.path, os.path.join(dst, entry.name))
NM_LINK
  step "frontend node_modules cache $cache_status (${lock_hash:0:12})"
}

run_go_checks() {
  export VERIFY_TRACK="go"
  export VERIFY_STEP_LOG="$WORK/step-go.log"
  # One Go build cache per verify slot: the slot lock is held for the whole run, so LRU trimming never removes objects
  # a concurrent verification is still linking (a shared cache wiped mid-build failed with "no such file", 2026-10-29).
  export GOCACHE="$CACHE/go-build-${VERIFY_SLOT:?}"
  mkdir -p "$GOCACHE"
  # Keep 4 Go slots bounded to the same ~12 GiB disk budget as the old 2x6 GiB cap (#3035), trimming oldest files first
  # down to target size instead of wiping the whole cache to zero.
  trim_gocache "$GOCACHE" "${VERIFY_GOCACHE_MAX_MIB:-3072}" "${VERIFY_GOCACHE_TARGET_MIB:-2048}"

  cd "$WORK/repo/agent_go"
  run "go build ./..." go build ./...
  pkgs="$(sed -n 's#^agent_go/\(.*\)/[^/]*\.go$#./\1#p' "$WORK/changed.txt" | sort -u)"
  existing=""
  nested_mods=""
  for pkg in $pkgs; do
    [[ -d "$pkg" ]] || continue
    # Skip packages inside a nested go.mod (e.g. agent_go/pkg/mcpoauth) from the parent module vet
    # and vet them in their own module directory instead.
    dir="$pkg"
    nested=""
    while [[ "$dir" != "." && "$dir" != "/" ]]; do
      if [[ -f "$dir/go.mod" ]]; then nested="$dir"; break; fi
      dir="$(dirname "$dir")"
    done
    if [[ -n "$nested" ]]; then
      nested_mods="$nested_mods $nested"
    else
      existing="$existing $pkg"
    fi
  done
  [[ -z "$existing" ]] || run "go vet$existing" go vet $existing
  for mod in $(printf '%s\n' $nested_mods | sort -u); do
    run "go vet (agent_go/${mod#./})" env VERIFY_DIR="$WORK/repo/agent_go/${mod#./}" bash -c 'cd "$VERIFY_DIR" && go vet ./...'
  done
  # The engine libraries are their own modules (agent_go/go.mod replaces them with these folders): vet their changed packages there.
  for module in mcpagent multi-llm-provider-go mcp-gateway; do
    pkgs="$(sed -n "s#^$module/\(.*\)/[^/]*\.go\$#./\1#p; s#^$module/[^/]*\.go\$#.#p" "$WORK/changed.txt" | sort -u)"
    existing=""
    for pkg in $pkgs; do [[ -d "$WORK/repo/$module/$pkg" ]] && existing="$existing $pkg"; done
    [[ -z "$existing" ]] || run "go vet ($module)$existing" env VERIFY_DIR="$WORK/repo/$module" bash -c 'cd "$VERIFY_DIR" && go vet "$@"' bash $existing
  done
  # The laptop CLI also builds shared workspace packages (crewroot and others), so a change there builds it too.
  if grep -E '^(agent_go|workspace|mcpagent|multi-llm-provider-go)/' "$WORK/changed.txt" | grep -qvE '^agent_go/cmd/server/'; then
    for arch in arm64 amd64; do
      run "macOS CLI build and vet ($arch)" env GOOS=darwin GOARCH="$arch" CGO_ENABLED=0 bash -c 'go build -o /dev/null ./cmd/agentworks && go vet ./cmd/agentworks ./pkg/agentworksclient'
    done
  else
    echo "macOS CLI skipped: nothing outside agent_go/cmd/server changed in agent_go or workspace (the laptop CLI does not import it)."
  fi
  (cd "$WORK/repo/workspace" && go build -o "$WORK/landlock-runner" ./cmd/landlock-runner) >/dev/null 2>&1 || true
  [[ ! -x "$WORK/landlock-runner" ]] || export AGENTWORKS_LANDLOCK_RUNNER="$WORK/landlock-runner"
  # Exercise hosted-folder access outside the launcher's private /tmp grant.
  mkdir -p "$WORK/linux-test-root"
  export AGENTWORKS_LINUX_TEST_ROOT="$WORK/linux-test-root"
  step "Verifier inner: sha256=$(sha256_file "${BASH_SOURCE[0]}") AGENTWORKS_LINUX_TEST_ROOT=$AGENTWORKS_LINUX_TEST_ROOT"
  while IFS= read -r line; do
    [[ -n "$line" ]] || continue
    case "$line" in
      'workspace '*|'mcp-gateway '*|'mcpagent '*|'multi-llm-provider-go '*|'agent_go/pkg/mcpoauth '*)
        module="${line%% *}"; args="${line#* }"
        # The line goes in through the environment, not the -c string, so its own quotes survive (same eval as the GitHub workflow).
        run "go test ($module) $args" env VERIFY_DIR="$WORK/repo/$module" VERIFY_ARGS="$args" bash -c 'cd "$VERIFY_DIR" && eval "go test $VERIFY_ARGS -count=1"' ;;
      *) run "go test $line" env VERIFY_ARGS="$line" bash -c 'eval "go test $VERIFY_ARGS -count=1"' ;;
    esac
  done < <(grep -E '^Verify-Test:' "$WORK/message.txt" | sed 's/^Verify-Test:[[:space:]]*//' || true)
}

run_frontend_checks() {
  export VERIFY_TRACK="frontend"
  export VERIFY_STEP_LOG="$WORK/step-frontend.log"
  cd "$WORK/repo/frontend"
  prepare_frontend_node_modules "$WORK/repo/frontend"
  run "semantic theme contract" npm run check:theme-contract
  run "tsc -b" npx tsc -b
  files="$(grep -E '^Verify-Vitest:' "$WORK/message.txt" | sed 's/^Verify-Vitest:[[:space:]]*//' | tr '\n' ' ' || true)"
  if [[ -n "${files// }" ]]; then run "vitest $files" npx vitest run $files; else echo "No Verify-Vitest lines; type check only."; fi
  if grep -q '^frontend/' "$WORK/changed.txt" && grep -q 'Verify-Frontend-Build: true' "$WORK/message.txt"; then
    run "website build" npm run build
  fi
}

detect_needed_checks "$WORK/changed.txt" "$WORK/message.txt"

while IFS= read -r file; do
  [[ -n "$file" ]] || continue
  [[ "$file" =~ ^deploy/(common|rootless-linux)/test_[a-z0-9_]+\.py$ && -f "$WORK/repo/$file" ]] || fail "Invalid deployment check: $file"
  run "deployment check $file" python3 "$WORK/repo/$file"
done < <(grep -E '^Verify-Deploy:' "$WORK/message.txt" | sed 's/^Verify-Deploy:[[:space:]]*//' || true)

if [[ "$go_needed" == true && "$frontend_needed" == true ]]; then
  step "Running Go and frontend checks in parallel"
  fe_out="$WORK/track-frontend.out"
  fe_pid=""
  trap '[[ -z "${fe_pid:-}" ]] || kill "$fe_pid" 2>/dev/null || true' EXIT
  ( run_frontend_checks ) >"$fe_out" 2>&1 &
  fe_pid=$!
  go_rc=0
  ( run_go_checks ) || go_rc=$?
  fe_rc=0
  wait "$fe_pid" || fe_rc=$?
  fe_pid=""
  trap - EXIT
  if [[ "$fe_rc" != 0 ]]; then
    echo "--- frontend checks (failed) ---" >&2
    cat "$fe_out" >&2
  else
    echo "--- frontend checks ---"
    cat "$fe_out"
  fi
  if [[ "$go_rc" != 0 && "$fe_rc" != 0 ]]; then
    fail "Go and frontend checks"
  elif [[ "$go_rc" != 0 ]]; then
    exit "$go_rc"
  elif [[ "$fe_rc" != 0 ]]; then
    exit "$fe_rc"
  fi
elif [[ "$go_needed" == true ]]; then
  ( run_go_checks )
elif [[ "$frontend_needed" == true ]]; then
  ( run_frontend_checks )
fi

[[ "$go_needed" == true || "$frontend_needed" == true ]] || grep -q '^Verify-Deploy:' "$WORK/message.txt" || echo "Nothing to verify: no Go or website changes and no Verify-Test or Verify-Vitest line."
echo
step "VERIFY: PASSED"
