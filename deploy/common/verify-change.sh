#!/usr/bin/env bash
# Verify one commit on the build host instead of GitHub: the same steps as .github/workflows/verify-change.yml, much faster.
#
#   verify-change.sh --url <https git url> --sha <40-hex> [--builds-dir /srv/_builds]
#
# Runs on the Linux build host (see deploy/common/build-once.sh, verify_change_remote). It
#   1. takes one of VERIFY_SLOTS (default 2) slot locks <builds-dir>/.verify.lock.<n>, so at most that many verifications run at
#      once (it does NOT take the build lock: builds stay unblocked);
#   2. fetches the commit and main into a kept mirror (<builds-dir>/.verify-mirror, only new objects each time), clones it locally,
#      and reads what changed against main and the Verify-* lines of the commit message;
#   3. Go (when agent_go, workspace or mcp-gateway changed, or a Verify-Test line exists): build ./..., vet the changed packages,
#      cross-build and vet the macOS CLI (only when something outside agent_go/cmd/server changed: the CLI never imports it),
#      the Verify-Deploy checks, then each Verify-Test line as `go test <line> -count=1`;
#   4. website (when frontend/ changed or a Verify-Vitest line exists): npm ci, tsc -b, Verify-Vitest files, and `npm run build`
#      with Verify-Frontend-Build: true;
#   5. prints VERIFY: PASSED or VERIFY: FAILED.
# Not run here: golangci-lint (new issues only) and Verify-Browser; use scripts/verify-remote.sh (GitHub) when those matter.
# Tests run as an unprivileged account (VERIFY_AS, default nobody): as root, permission tests would pass or fail for the wrong reason.
# Go and npm caches live in <builds-dir>/.verify-cache (kept between runs; only verification uses them).
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

if [[ "$INNER" == 0 ]]; then
  # ---- outer (root): lock, fetch, hand the checkout to an unprivileged account ----------------------------------------------
  install -d -m 0755 "$BUILDS"
  # Up to VERIFY_SLOTS verifications at once; each keeps its own CPU and memory limits (build-once.sh).
  slots="${VERIFY_SLOTS:-2}"; slot=""; waited=0
  while [[ -z "$slot" ]]; do
    for n in $(seq 1 "$slots"); do
      exec 8>"$BUILDS/.verify.lock.$n"
      if flock -n 8; then slot="$n"; break; fi
      exec 8>&-
    done
    if [[ -z "$slot" ]]; then
      [[ "$waited" -gt 0 ]] || step "Waiting for a free verify slot ($slots in use)"
      (( waited += 5 )); [[ "$waited" -lt 1800 ]] || { echo "No free verify slot for 30 minutes" >&2; exit 1; }
      sleep 5
    fi
  done
  command -v go >/dev/null || { echo "No Go toolchain on this host: run ./deploy.sh build once (it installs one)." >&2; exit 1; }
  id -u "$USER_AS" >/dev/null 2>&1 || { echo "Account $USER_AS does not exist; set VERIFY_AS" >&2; exit 1; }
  WORK="$BUILDS/.work/verify-${SHA:0:8}-$(date -u +%Y%m%d%H%M%S)"
  CACHE="$BUILDS/.verify-cache"
  mkdir -p "$WORK" "$CACHE/go-build-$slot" "$CACHE/gomod" "$CACHE/npm"
  # Full logs of failing steps stay for two days (the scratch folder is deleted with the run).
  install -d -m 1777 "$BUILDS/.verify-logs"
  find "$BUILDS/.verify-logs" -type f -mtime +2 -delete 2>/dev/null || true
  trap 'rm -rf "$WORK"' EXIT
  step "Fetching the commit and main (slot $slot)"
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
  GROUP_AS="$(id -gn "$USER_AS")"
  chown -R "$USER_AS:$GROUP_AS" "$WORK" "$CACHE"
  SELF="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/$(basename -- "${BASH_SOURCE[0]}")"
  cp "$SELF" "$WORK/verify-change.sh"; chmod 0755 "$WORK/verify-change.sh"; chown "$USER_AS:$GROUP_AS" "$WORK/verify-change.sh"
  # Not exec: the trap must still remove the scratch folder and the lock (fd 8) stays held.
  runuser -u "$USER_AS" -- env "PATH=$PATH" "HOME=$WORK/home" "VERIFY_STARTED=$STARTED" "VERIFY_CACHE=$CACHE" "VERIFY_SLOT=$slot" \
    bash "$WORK/verify-change.sh" --inner --work "$WORK" --url "$URL" --sha "$SHA" --builds-dir "$BUILDS"
  exit $?
fi

# ---- inner (unprivileged): the checks ------------------------------------------------------------------------------------------
mkdir -p "$WORK/home"
CACHE="${VERIFY_CACHE:?}"
# One Go build cache per verify slot: the slot lock is held for the whole run, so a wipe never removes objects a concurrent
# verification is still linking (a shared cache wiped mid-build failed with "no such file", 2026-10-29).
export GOCACHE="$CACHE/go-build-${VERIFY_SLOT:?}" GOMODCACHE="$CACHE/gomod" npm_config_cache="$CACHE/npm"
# Test binaries grow the Go cache past the disk (67G filled the build host; on 2026-10-28 a day of runs reached 56G with nothing a
# day old, the disk filled and the server's own service crash-looped). Over 10G per slot, wipe that slot's cache.
if [[ -d "$GOCACHE" ]] && (( $(du -sk "$GOCACHE" | cut -f1) > 10 * 1024 * 1024 )); then
  find "$GOCACHE" -mindepth 1 -delete 2>/dev/null || true
fi
export GOWORK=off GOTOOLCHAIN=local GOFLAGS="-modcacherw" NODE_OPTIONS=--max-old-space-size=4096
cd "$WORK/repo"
base="$(git merge-base HEAD origin/main)"
git diff --name-only "$base" HEAD > "$WORK/changed.txt"
git log -1 --format=%B > "$WORK/message.txt"
echo "changed files:"; sed 's/^/  /' "$WORK/changed.txt"

fail() { echo; echo "VERIFY: FAILED at: $*" >&2; exit 1; }
run() { # run <label> <command...>: prints the last lines of the output when it fails
  local label="$1" log="$WORK/step.log"; shift
  step "$label"
  if ! "$@" >"$log" 2>&1; then
    # The first failures and the name of a hanging test come before the long tail, so print them first.
    grep -E '^(--- FAIL|    --- FAIL|FAIL|panic: |running tests:|	Test[A-Za-z0-9_]+ \()' "$log" | head -n 60 >&2 || true
    echo "---- last lines ----" >&2
    tail -n 40 "$log" >&2
    kept="$BUILDS/.verify-logs/${SHA:0:8}-$(date +%H%M%S).log"
    cp "$log" "$kept" 2>/dev/null && echo "full log kept on the build host: $kept" >&2
    fail "$label"
  fi
}

go_needed=false; frontend_needed=false
{ grep -qE '^(agent_go|workspace|mcp-gateway)/' "$WORK/changed.txt" || grep -q '^Verify-Test:' "$WORK/message.txt"; } && go_needed=true
# The pre-commit hook bumps frontend/src/version.ts on every commit; that alone does not need a website check.
{ grep '^frontend/' "$WORK/changed.txt" | grep -qvx 'frontend/src/version.ts' || grep -q '^Verify-Vitest:' "$WORK/message.txt"; } && frontend_needed=true

if [[ "$go_needed" == true ]]; then
  cd "$WORK/repo/agent_go"
  run "go build ./..." go build ./...
  pkgs="$(sed -n 's#^agent_go/\(.*\)/[^/]*\.go$#./\1#p' "$WORK/changed.txt" | sort -u)"
  existing=""
  for pkg in $pkgs; do [[ -d "$pkg" ]] && existing="$existing $pkg"; done
  [[ -z "$existing" ]] || run "go vet$existing" go vet $existing
  if grep -E '^agent_go/' "$WORK/changed.txt" | grep -qvE '^agent_go/cmd/server/'; then
    for arch in arm64 amd64; do
      run "macOS CLI build and vet ($arch)" env GOOS=darwin GOARCH="$arch" CGO_ENABLED=0 bash -c 'go build -o /dev/null ./cmd/agentworks && go vet ./cmd/agentworks ./pkg/agentworksclient'
    done
  else
    echo "macOS CLI skipped: nothing outside agent_go/cmd/server changed (the laptop CLI does not import it)."
  fi
  while IFS= read -r file; do
    [[ -n "$file" ]] || continue
    [[ "$file" =~ ^deploy/(common|rootless-linux)/test_[a-z0-9_]+\.py$ && -f "$WORK/repo/$file" ]] || fail "Invalid deployment check: $file"
    run "deployment check $file" python3 "$WORK/repo/$file"
  done < <(grep -E '^Verify-Deploy:' "$WORK/message.txt" | sed 's/^Verify-Deploy:[[:space:]]*//' || true)
  (cd "$WORK/repo/workspace" && go build -o "$WORK/landlock-runner" ./cmd/landlock-runner) >/dev/null 2>&1 || true
  [[ ! -x "$WORK/landlock-runner" ]] || export AGENTWORKS_LANDLOCK_RUNNER="$WORK/landlock-runner"
  while IFS= read -r line; do
    [[ -n "$line" ]] || continue
    case "$line" in
      'workspace '*|'mcp-gateway '*)
        module="${line%% *}"; args="${line#* }"
        # The line goes in through the environment, not the -c string, so its own quotes survive (same eval as the GitHub workflow).
        run "go test ($module) $args" env VERIFY_DIR="$WORK/repo/$module" VERIFY_ARGS="$args" bash -c 'cd "$VERIFY_DIR" && eval "go test $VERIFY_ARGS -count=1"' ;;
      *) run "go test $line" env VERIFY_ARGS="$line" bash -c 'eval "go test $VERIFY_ARGS -count=1"' ;;
    esac
  done < <(grep -E '^Verify-Test:' "$WORK/message.txt" | sed 's/^Verify-Test:[[:space:]]*//' || true)
fi

if [[ "$frontend_needed" == true ]]; then
  cd "$WORK/repo/frontend"
  run "npm ci" npm ci --no-audit --no-fund
  run "tsc -b" npx tsc -b
  files="$(grep -E '^Verify-Vitest:' "$WORK/message.txt" | sed 's/^Verify-Vitest:[[:space:]]*//' | tr '\n' ' ' || true)"
  if [[ -n "${files// }" ]]; then run "vitest $files" npx vitest run $files; else echo "No Verify-Vitest lines; type check only."; fi
  if grep -q '^frontend/' "$WORK/changed.txt" && grep -q 'Verify-Frontend-Build: true' "$WORK/message.txt"; then
    run "website build" npm run build
  fi
fi

[[ "$go_needed" == true || "$frontend_needed" == true ]] || echo "Nothing to verify: no Go or website changes and no Verify-Test or Verify-Vitest line."
echo
step "VERIFY: PASSED"
