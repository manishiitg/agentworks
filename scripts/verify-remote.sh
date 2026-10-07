#!/usr/bin/env bash
# Verify the current commit on GitHub instead of building on this machine.
# Pushes HEAD to verify/<name>, waits for the "Verify change" workflow, prints
# the failing log on failure, deletes the branch, and exits with the result.
# Name the tests in the commit message (see .github/workflows/verify-change.yml):
#   Verify-Test: ./cmd/server -run '^TestCodeChatAsk'
#   Verify-Vitest: src/components/PanelSwitcher.test.tsx
# Usage: scripts/verify-remote.sh [name]
set -euo pipefail

name="${1:-$(git rev-parse --short HEAD)}"
branch="verify/${name}"
sha="$(git rev-parse HEAD)"

git push -q -f origin "HEAD:refs/heads/${branch}"
cleanup() { git push -q origin --delete "${branch}" >/dev/null 2>&1 || true; }
trap cleanup EXIT

run_id=""
for _ in $(seq 1 36); do
  run_id="$(gh run list --workflow verify-change.yml --branch "${branch}" --json databaseId,headSha \
    -q ".[] | select(.headSha == \"${sha}\") | .databaseId" 2>/dev/null | head -n 1)"
  [ -n "${run_id}" ] && break
  sleep 5
done
if [ -z "${run_id}" ]; then
  echo "verify: no workflow run appeared for ${sha} on ${branch}" >&2
  exit 2
fi

echo "verify: run ${run_id} for ${sha:0:9} on ${branch}"
if gh run watch "${run_id}" --exit-status --interval 20 >/dev/null; then
  echo "verify: PASSED"
  exit 0
fi
echo "verify: FAILED; failing steps:" >&2
gh run view "${run_id}" --log-failed 2>/dev/null | tail -n 80 >&2 || true
exit 1
