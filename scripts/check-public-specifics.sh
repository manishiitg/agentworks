#!/usr/bin/env bash
# Keeps company and customer specifics (names, domains, host addresses, SSH ports) out of this public repository (PLAT-719).
# The block list is private, in the deployments repository (public-repo-blocklist.txt: one case-insensitive extended regular
# expression per line, # comments), so this file names nothing. Only what is being added is checked:
#   scripts/check-public-specifics.sh --staged          lines added by the staged change
#   scripts/check-public-specifics.sh --message FILE    a commit message
# Without the private list (a fork, a machine without the deployments checkout) it does nothing.
# deploy.sh still names its servers in its own logic; it is skipped until that moves to the private config.
set -uo pipefail
root="$(git rev-parse --show-toplevel)"
list="${AGENTWORKS_DEPLOYMENTS_DIR:-$root/../deployments}/public-repo-blocklist.txt"
[[ -f "$list" ]] || exit 0
pattern="$(grep -v -E '^[[:space:]]*(#|$)' "$list" | paste -sd'|' -)"
[[ -n "$pattern" ]] || exit 0
case "${1:-}" in
  --staged) text="$(git diff --cached -U0 --no-color -- . ':(exclude)deploy.sh' | grep '^+' | grep -v '^+++')" ;;
  --message) text="$(grep -v '^#' "${2:?commit message file}")" ;;
  *) echo "usage: $0 --staged | --message FILE" >&2; exit 2 ;;
esac
hits="$(printf '%s\n' "$text" | grep -i -E "$pattern" | head -10)"
if [[ -n "$hits" ]]; then
  echo "❌ This would put company or customer specifics into the public repository (PLAT-719):" >&2
  printf '%s\n' "$hits" | cut -c1-200 | sed 's/^/   /' >&2
  echo "   Use generic names (example.com, \"a customer\", roles) and placeholders for hosts and ports;" >&2
  echo "   keep the specifics in the private deployments repository." >&2
  exit 1
fi
exit 0
