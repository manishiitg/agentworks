#!/usr/bin/env bash
#
# Agentic P0 suite for coding-agent STREAMING OUTPUT (Claude Code, Codex).
#
# Why this is agentic, not just deterministic: coding CLIs change behavior with
# every release, and deterministic assertions can pass on visibly-degraded output
# (a Codex build once streamed every assistant line TWICE yet satisfied every
# assert). So this suite also records the REAL streamed output and REQUIRES an
# agent to look at it and sign off.
#
#   capture : run the live tests, record the real output, and RESET every review
#             to pending (verdict ""). A fresh run always starts unreviewed.
#   verify  : cheap gate (no live CLI) — fails until EVERY record is agent-approved.
#   (none)  : capture then verify.
#
# Between capture and verify, the agent running the tests MUST open each
#   pkg/adapters/*/testdata/agent-reviews/*.json
# read `output` (incl. `content_chunks`) against `review_criteria`
# (no duplication, proper formatting, human-readable, correct text<->tool
# interleaving, real work done), and set:
#   agent_review.verdict              = "good"   (or "bad" with issues)
#   agent_review.reviewed_fingerprint = <the record's "fingerprint">
#   agent_review.reviewer             = "<who>"
#
# Requires: a real authenticated `claude` and `codex` CLI, `tmux`, `node`.
set -euo pipefail
cd "$(dirname "$0")/.."
export GOTOOLCHAIN="${GOTOOLCHAIN:-go1.26.0}"

# Codex's entire P0/P1 registry runs over app-server. Keep this path independent
# of tmux and of unrelated providers. Review captures before invoking verify.
if [[ "${2:-}" == "codex-cli" ]]; then
  case "${1:-all}" in
    capture|all)
      regex="$(go run ./cmd/coding-agent-p0-tests -provider codex-cli -package pkg/adapters/codexcli -priority all)"
      MLP_AGENT_REVIEW_CAPTURE=1 go test ./pkg/adapters/codexcli -run "$regex" -coding-cli-p0-live -v -count=1 -timeout 20m
      [[ "${1:-all}" == capture ]] && exit 0
      ;;
    verify) ;;
    *) echo "usage: $0 [capture|verify|all] [codex-cli]" >&2; exit 2 ;;
  esac
  go test ./pkg/adapters/codexcli -run '^TestAgentReviewsApproved$' -count=1
  exit
fi

# Keep live-test tmux sessions out of the user's normal tmux server. The path is
# deliberately stable: if a previous P0 run was killed too abruptly for its
# EXIT trap to run, the next invocation can recover that abandoned server
# before starting. A small lock prevents two P0 suites from sharing the server.
P0_RUNTIME_ROOT="${TMPDIR:-/tmp}/multi-llm-provider-agentic-p0"
P0_LOCK_DIR="${P0_RUNTIME_ROOT}.lock"
P0_TMUX_ROOT="${P0_RUNTIME_ROOT}/tmux"
P0_TEST_PID=""

acquire_p0_runtime() {
  if ! mkdir "$P0_LOCK_DIR" 2>/dev/null; then
    prior_pid="$(cat "$P0_LOCK_DIR/pid" 2>/dev/null || true)"
    if [[ "$prior_pid" =~ ^[0-9]+$ ]] && kill -0 "$prior_pid" 2>/dev/null; then
      echo ">> another agentic P0 suite is already running (pid $prior_pid)" >&2
      exit 1
    fi
    rm -rf "$P0_LOCK_DIR"
    mkdir "$P0_LOCK_DIR"
  fi
  printf '%s\n' "$$" > "$P0_LOCK_DIR/pid"

  mkdir -p "$P0_TMUX_ROOT"
  chmod 700 "$P0_TMUX_ROOT"

  # This can only address the dedicated P0 server because TMUX_TMPDIR is scoped
  # to P0_TMUX_ROOT. It cannot disturb AgentWorks or the user's tmux sessions.
  TMUX_TMPDIR="$P0_TMUX_ROOT" tmux kill-server 2>/dev/null || true
  export TMUX_TMPDIR="$P0_TMUX_ROOT"
}

collect_process_tree() {
  local pid="$1" child
  P0_PROCESS_TREE="$P0_PROCESS_TREE $pid"
  for child in $(pgrep -P "$pid" 2>/dev/null || true); do
    collect_process_tree "$child"
  done
}

cleanup_p0_runtime() {
  local original_status=$? pane_pid pid cleanup_failed=0
  trap - EXIT

  # tmux kill-server alone may leave MCP grandchildren behind. Record each pane
  # process tree while the relationship still exists, then terminate it before
  # removing the dedicated server.
  P0_PROCESS_TREE=""
  if [[ "$P0_TEST_PID" =~ ^[0-9]+$ ]]; then
    collect_process_tree "$P0_TEST_PID"
  fi
  while IFS= read -r pane_pid; do
    [[ "$pane_pid" =~ ^[0-9]+$ ]] && collect_process_tree "$pane_pid"
  done < <(tmux list-panes -a -F '#{pane_pid}' 2>/dev/null || true)

  for pid in $P0_PROCESS_TREE; do
    kill -TERM "$pid" 2>/dev/null || true
  done
  sleep 0.5
  for pid in $P0_PROCESS_TREE; do
    kill -KILL "$pid" 2>/dev/null || true
  done

  if tmux list-sessions >/dev/null 2>&1 && ! tmux kill-server; then
    echo ">> warning: failed to stop the isolated agentic P0 tmux server" >&2
    cleanup_failed=1
  fi
  rm -rf "$P0_RUNTIME_ROOT" "$P0_LOCK_DIR"

  if (( original_status == 0 && cleanup_failed != 0 )); then
    original_status=1
  fi
  exit "$original_status"
}

acquire_p0_runtime
trap cleanup_p0_runtime EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

# Every coding-agent adapter whose contract sets SupportsStructuredStreaming must
# have its live streaming P0 tests captured + agent-approved here. Add a provider
# to STREAM_PKGS the moment it streams structured chunks + has a streaming E2E
# (and its contract flag flips on) — see coding_agent_certification.go
# CertStructuredStreaming. claude/codex/cursor stream by tailing the CLI
# transcript; pi streams via its injected marker hook.
STREAM_PKGS=(
  ./pkg/adapters/claudecode/
  ./pkg/adapters/cursorcli/
  ./pkg/adapters/picli/
)
PKGS=("${STREAM_PKGS[@]}")
# Matches every provider's streaming tests and structured two-turn resume proof.
LIVE='(Transcript|Structured)Streaming|StructuredTwoTurnResume'

capture() {
  echo ">> capture: running live agentic P0 tests; resetting reviews to pending ..."
  MLP_AGENT_REVIEW_CAPTURE=1 go test "${PKGS[@]}" -run "$LIVE" -coding-cli-p0-live -count=1 -timeout 1200s &
  P0_TEST_PID=$!
  wait "$P0_TEST_PID"
  P0_TEST_PID=""
  bash scripts/agentic-p0.sh capture codex-cli
  echo ">> capture done. Records under pkg/adapters/*/testdata/agent-reviews/ now have verdict=\"\" (pending)."
  echo ">> An agent must review each record and set agent_review.verdict=\"good\", then run: $0 verify"
}

verify() {
  echo ">> verify: enforcing agent sign-off (cheap gate, no live CLI) ..."
  go test "${PKGS[@]}" ./pkg/adapters/codexcli/ -run TestAgentReviewsApproved -count=1
  echo ">> verify passed: every recorded output is agent-approved for its current fingerprint."
}

case "${1:-all}" in
  capture) capture ;;
  verify)  verify ;;
  all)     capture; verify ;;
  *) echo "usage: $0 [capture|verify|all]"; exit 2 ;;
esac
