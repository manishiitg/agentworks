#!/usr/bin/env bash
# Nightly full server test runner on the Linux build host (#4340).
#
#   nightly-tests.sh --url <https git url> [--branch main] [--sha <40-hex>]
#                    [--builds-dir /srv/_builds] [--packages './...']
#                    [--timeout 30m] [--no-save-state]
#
# Designed never to slow down people's `./deploy.sh verify` runs or deploys:
#   - takes its own single-instance lock (<builds>/.nightly-test.lock), never any verify slot lock (.verify.lock.1..4);
#   - waits if a deploy lock (<builds>/.deploys/*) is active before starting compilation;
#   - uses its own bounded GOCACHE (<builds>/.verify-cache/go-build-nightly, LRU-trimmed 2 GiB -> 1.5 GiB);
#   - runs under Nice=19, IOSchedulingClass=idle, CPUQuota=200%, MemoryMax=6G (via build-once.sh systemd-run),
#     and caps Go compilation/test concurrency (GOMAXPROCS=2, -p 2 -parallel 2);
#   - checks disk free space with a stricter floor (default 15 GiB) and exits 2 without modifying saved state if low;
#   - runs tests as an unprivileged account (VERIFY_AS, default nobody) with AGENTWORKS_LINUX_TEST_ROOT and landlock-runner;
#   - parses `go test -json`, diffs against <builds>/.nightly-tests/last-run.json, saves history (last 14 runs),
#     keeps failing output logs for 7 days, and prints `NIGHTLY_REPORT_JSON={...}` on stdout.
#
# Never deploys anything.
set -euo pipefail
umask 022

URL=""; BRANCH="main"; SHA=""; BUILDS="${BUILDS_DIR:-/srv/_builds}"
PACKAGES="${NIGHTLY_TEST_PACKAGES:-./...}"; TIMEOUT="${NIGHTLY_TEST_TIMEOUT:-30m}"
NO_SAVE_STATE=0; INNER=0; WORK=""
REPORT_SCRIPT="${NIGHTLY_REPORT_SCRIPT:-}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --url) URL="$2"; shift 2 ;;
    --branch) BRANCH="$2"; shift 2 ;;
    --sha) SHA="$2"; shift 2 ;;
    --builds-dir) BUILDS="$2"; shift 2 ;;
    --packages) PACKAGES="$2"; shift 2 ;;
    --timeout) TIMEOUT="$2"; shift 2 ;;
    --no-save-state) NO_SAVE_STATE=1; shift ;;
    --report-script) REPORT_SCRIPT="$2"; shift 2 ;;
    --inner) INNER=1; shift ;;
    --work) WORK="$2"; shift 2 ;;
    *) echo "Unknown argument: $1" >&2; exit 2 ;;
  esac
done

[[ -n "$URL" ]] || { echo "--url is required" >&2; exit 2; }
[[ -z "$SHA" || "$SHA" =~ ^[0-9a-f]{40}$ ]] || { echo "--sha must be a 40-hex commit" >&2; exit 2; }
USER_AS="${VERIFY_AS:-nobody}"
export PATH="$BUILDS/.toolchain/go/bin:$HOME/.local/go/bin:/usr/local/go/bin:$PATH"
STARTED="${NIGHTLY_STARTED:-$(date +%s)}"
step() { echo "==> [nightly] $* ($(( $(date +%s) - STARTED ))s)" >&2; }

check_nightly_disk() {
python3 - "$BUILDS" <<'DISK_CHECK'
import math, os, shutil, sys
minimum = os.environ.get("NIGHTLY_DISK_MIN_FREE_GIB", os.environ.get("BUILD_DISK_MIN_FREE_GIB", "15"))
try:
    minimum = float(minimum)
    if not math.isfinite(minimum) or minimum < 0:
        raise ValueError()
except ValueError:
    print("[DISK_CHECK] event=refused reason=invalid_minimum", file=sys.stderr)
    raise SystemExit(2)
try:
    usage = shutil.disk_usage(sys.argv[1])
except OSError as exc:
    print(f"[DISK_CHECK] event=refused reason=check_failed error={exc}", file=sys.stderr)
    raise SystemExit(2)
free_gib = usage.free / (1024 ** 3)
percent = math.ceil(100 * usage.used / max(1, usage.used + usage.free))
print(f"[DISK_CHECK] used_percent={percent} free_gib={free_gib:.2f}", file=sys.stderr)
if usage.free < minimum * 1024 ** 3:
    print(f"Nightly tests skipped: less than {minimum:g} GiB free on build disk.", file=sys.stderr)
    raise SystemExit(2)
DISK_CHECK
}

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
print(f"[GOCACHE_TRIM] dir={os.path.basename(cache_dir)} before_gib={before / gib:.2f} after_gib={total / gib:.2f} removed={removed}", file=sys.stderr)
GOCACHE_TRIM
}

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

wait_for_deploys() {
  local waited=0 announced=0 step_s="${NIGHTLY_DEPLOY_WAIT_SECONDS:-15}" timeout_s="${NIGHTLY_DEPLOY_TIMEOUT_SECONDS:-1800}"
  while compgen -G "$BUILDS/.deploys/*" >/dev/null; do
    if [[ "$announced" == 0 ]]; then
      step "Deferring start while a deploy is active"
      announced=1
    fi
    if python3 -c 'import sys; raise SystemExit(0 if float(sys.argv[1]) >= float(sys.argv[2]) else 1)' "$waited" "$timeout_s"; then
      echo "Deploy stayed active for ${timeout_s}s; skipping nightly run" >&2
      exit 2
    fi
    sleep "$step_s"
    waited="$(python3 -c 'import sys; print(float(sys.argv[1]) + float(sys.argv[2]))' "$waited" "$step_s")"
  done
}

if [[ "$INNER" == 0 ]]; then
  install -d -m 0755 "$BUILDS"
  check_nightly_disk
  command -v go >/dev/null || { echo "No Go toolchain on this host: run ./deploy.sh build once." >&2; exit 2; }
  if [[ "$(id -u)" == 0 ]]; then
    id -u "$USER_AS" >/dev/null 2>&1 || { echo "Account $USER_AS does not exist; set VERIFY_AS" >&2; exit 2; }
  fi

  # Single-instance lock dedicated to nightly runs; never contends with .verify.lock.1..4.
  exec 8>"$BUILDS/.nightly-test.lock"
  if ! flock -n 8; then
    echo "Another nightly server test run is already in progress; exiting." >&2
    exit 75
  fi

  wait_for_deploys

  NIGHTLY_DIR="$BUILDS/.nightly-tests"
  LOG_DIR="$NIGHTLY_DIR/logs"
  HIST_DIR="$NIGHTLY_DIR/runs"
  STATE_FILE="$NIGHTLY_DIR/last-run.json"
  install -d -m 0755 "$NIGHTLY_DIR" "$LOG_DIR" "$HIST_DIR"
  find "$LOG_DIR" -type f -mtime +7 -delete 2>/dev/null || true

  MIRROR="$BUILDS/.verify-mirror"
  step "Fetching $BRANCH"
  (
    flock -w 600 9 || { echo "The verify mirror has been busy for 10 minutes" >&2; exit 2; }
    [[ -d "$MIRROR/objects" ]] || git init -q --bare "$MIRROR"
    git -C "$MIRROR" remote get-url origin >/dev/null 2>&1 || git -C "$MIRROR" remote add origin "$URL"
    git -C "$MIRROR" remote set-url origin "$URL"
    if [[ -n "$SHA" ]]; then
      GIT_TERMINAL_PROMPT=0 git -C "$MIRROR" fetch -q origin "+refs/heads/$BRANCH:refs/heads/$BRANCH" "$SHA"
    else
      GIT_TERMINAL_PROMPT=0 git -C "$MIRROR" fetch -q origin "+refs/heads/$BRANCH:refs/heads/$BRANCH"
    fi
  ) 9>"$BUILDS/.verify-mirror.lock"

  if [[ -z "$SHA" ]]; then
    SHA="$(git -C "$MIRROR" rev-parse "refs/heads/$BRANCH")"
  fi
  [[ "$SHA" =~ ^[0-9a-f]{40}$ ]] || { echo "Could not resolve commit for $BRANCH" >&2; exit 2; }

  WORK="$BUILDS/.work/nightly-${SHA:0:8}-$(date -u +%Y%m%d%H%M%S)-$$"
  CACHE="$BUILDS/.verify-cache"
  mkdir -p "$WORK"
  trap 'rm -rf "$WORK"' EXIT

  git clone -q --shared --no-checkout "$MIRROR" "$WORK/repo"
  git -C "$WORK/repo" -c advice.detachedHead=false checkout -q --detach "$SHA"
  [[ "$(git -C "$WORK/repo" rev-parse HEAD)" == "$SHA" ]] || { echo "Checked out the wrong commit" >&2; exit 2; }

  GROUP_AS=""
  if [[ "$(id -u)" == 0 ]]; then
    GROUP_AS="$(id -gn "$USER_AS")"
  fi
  ensure_cache_dirs "$USER_AS" "$GROUP_AS" "$CACHE" "$CACHE/gomod" "$CACHE/go-build-nightly"
  if [[ "$(id -u)" == 0 ]]; then
    chown -R "$USER_AS:$GROUP_AS" "$WORK"
  fi

  cp "$0" "$WORK/nightly-tests.sh"
  chmod 0755 "$WORK/nightly-tests.sh"
  if [[ "$(id -u)" == 0 ]]; then
    chown "$USER_AS:$GROUP_AS" "$WORK/nightly-tests.sh"
  fi

  step "Running full server tests on ${SHA:0:10} ($PACKAGES, timeout=$TIMEOUT)"
  run_rc=0
  if [[ "$(id -u)" == 0 ]]; then
    runuser -u "$USER_AS" -- env \
      "PATH=$PATH" \
      "HOME=$WORK/home" \
      "NIGHTLY_STARTED=$STARTED" \
      "VERIFY_CACHE=$CACHE" \
      "NIGHTLY_GOMAXPROCS=${NIGHTLY_GOMAXPROCS:-2}" \
      "NIGHTLY_GO_TEST_P=${NIGHTLY_GO_TEST_P:-2}" \
      "NIGHTLY_GO_TEST_PARALLEL=${NIGHTLY_GO_TEST_PARALLEL:-2}" \
      "NIGHTLY_GOCACHE_MAX_MIB=${NIGHTLY_GOCACHE_MAX_MIB:-2048}" \
      "NIGHTLY_GOCACHE_TARGET_MIB=${NIGHTLY_GOCACHE_TARGET_MIB:-1536}" \
      bash "$WORK/nightly-tests.sh" --inner --work "$WORK" --url "$URL" --sha "$SHA" --builds-dir "$BUILDS" --packages "$PACKAGES" --timeout "$TIMEOUT" || run_rc=$?
  else
    env \
      "PATH=$PATH" \
      "HOME=$WORK/home" \
      "NIGHTLY_STARTED=$STARTED" \
      "VERIFY_CACHE=$CACHE" \
      "NIGHTLY_GOMAXPROCS=${NIGHTLY_GOMAXPROCS:-2}" \
      "NIGHTLY_GO_TEST_P=${NIGHTLY_GO_TEST_P:-2}" \
      "NIGHTLY_GO_TEST_PARALLEL=${NIGHTLY_GO_TEST_PARALLEL:-2}" \
      "NIGHTLY_GOCACHE_MAX_MIB=${NIGHTLY_GOCACHE_MAX_MIB:-2048}" \
      "NIGHTLY_GOCACHE_TARGET_MIB=${NIGHTLY_GOCACHE_TARGET_MIB:-1536}" \
      bash "$WORK/nightly-tests.sh" --inner --work "$WORK" --url "$URL" --sha "$SHA" --builds-dir "$BUILDS" --packages "$PACKAGES" --timeout "$TIMEOUT" || run_rc=$?
  fi

  if [[ ! -f "$WORK/events.jsonl" ]]; then
    echo "Nightly test runner did not produce events.jsonl (rc=$run_rc)" >&2
    exit 2
  fi

  STAMP="$(date -u +%Y%m%d-%H%M%S)"
  KEPT_LOG=""
  if [[ "$run_rc" != 0 ]]; then
    KEPT_LOG="$LOG_DIR/${STAMP}-${SHA:0:8}.log"
    {
      echo "=== go test stderr ==="
      cat "$WORK/go-stderr.log" 2>/dev/null || true
      echo "=== failing test output ==="
      python3 - "$WORK/events.jsonl" <<'EXTRACT_FAILS'
import json, sys
from collections import defaultdict
out_by_test = defaultdict(list)
failed = set()
pkg_out = defaultdict(list)
pkg_failed = set()
with open(sys.argv[1], "r", encoding="utf-8", errors="replace") as fh:
    for line in fh:
        try:
            ev = json.loads(line)
        except json.JSONDecodeError:
            continue
        act = ev.get("Action") or ""
        pkg = ev.get("Package") or ev.get("ImportPath") or ""
        test = ev.get("Test") or ""
        out = ev.get("Output") or ""
        if act in ("output", "build-output") and out:
            if test:
                out_by_test[(pkg, test)].append(out)
            else:
                pkg_out[pkg].append(out)
        elif act in ("fail", "build-fail"):
            if test:
                failed.add((pkg, test))
            else:
                pkg_failed.add(pkg)
for pkg, test in sorted(failed):
    print(f"--- FAIL {pkg}::{test} ---")
    for chunk in out_by_test[(pkg, test)][-80:]:
        sys.stdout.write(chunk)
for pkg in sorted(pkg_failed):
    if not any(p == pkg for p, _ in failed):
        print(f"--- PKG FAIL {pkg} ---")
        for chunk in pkg_out[pkg][-80:]:
            sys.stdout.write(chunk)
EXTRACT_FAILS
    } > "$KEPT_LOG"
  fi

  if [[ -z "$REPORT_SCRIPT" ]]; then
    if [[ -f "$WORK/repo/deploy/common/nightly_tests_report.py" ]]; then
      REPORT_SCRIPT="$WORK/repo/deploy/common/nightly_tests_report.py"
    elif [[ -f "$BUILDS/.nightly_tests_report.py" ]]; then
      REPORT_SCRIPT="$BUILDS/.nightly_tests_report.py"
    else
      echo "Missing nightly_tests_report.py" >&2
      exit 2
    fi
  fi

  DURATION="$(( $(date +%s) - STARTED ))"
  report_args=(
    parse-and-diff
    --events "$WORK/events.jsonl"
    --stderr "$WORK/go-stderr.log"
    --state "$STATE_FILE"
    --sha "$SHA"
    --duration "$DURATION"
    --log-path "$KEPT_LOG"
    --history-dir "$HIST_DIR"
  )
  [[ "$NO_SAVE_STATE" != 1 ]] || report_args+=(--no-save-state)

  step "Diffing results against previous run"
  python3 "$REPORT_SCRIPT" "${report_args[@]}"
  exit 0
fi

# ---- inner (unprivileged): run full server tests with capped resources -------------------------
mkdir -p "$WORK/home" "$WORK/linux-test-root"
CACHE="${VERIFY_CACHE:?}"
export GOMODCACHE="$CACHE/gomod"
export GOCACHE="$CACHE/go-build-nightly"
export GOWORK=off GOTOOLCHAIN=local GOFLAGS="-modcacherw"
export GOMAXPROCS="${NIGHTLY_GOMAXPROCS:-2}"
export AGENTWORKS_LINUX_TEST_ROOT="$WORK/linux-test-root"
mkdir -p "$GOCACHE"
trim_gocache "$GOCACHE" "${NIGHTLY_GOCACHE_MAX_MIB:-2048}" "${NIGHTLY_GOCACHE_TARGET_MIB:-1536}"

(cd "$WORK/repo/workspace" && go build -o "$WORK/landlock-runner" ./cmd/landlock-runner) >/dev/null 2>&1 || true
[[ ! -x "$WORK/landlock-runner" ]] || export AGENTWORKS_LANDLOCK_RUNNER="$WORK/landlock-runner"

cd "$WORK/repo/agent_go"
# shellcheck disable=SC2086
go test -json -count=1 -timeout "$TIMEOUT" -p "${NIGHTLY_GO_TEST_P:-2}" -parallel "${NIGHTLY_GO_TEST_PARALLEL:-2}" $PACKAGES >"$WORK/events.jsonl" 2>"$WORK/go-stderr.log"
