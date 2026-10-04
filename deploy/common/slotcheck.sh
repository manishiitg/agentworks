#!/usr/bin/env bash
# Deploy self-test of the per-user slot chain (PLAT-478). Read-only. Runs as the product's service account on the
# server, at the end of every deploy of a slot-enabled host (both build-and-activate scripts) and on its own:
#
#   ./deploy.sh slotcheck <server>                      # from a laptop
#   bash <app>/current/slotcheck.sh --app <app> --docs <docs root> --product <product>   # on the server
#
# What it does:
#   1. reads the slot settings the workspace service really runs with (from the running process, else <app>/.env):
#      AGENTWORKS_SLOTS, the slotctl launcher/config and slot table paths, the browser profile settings;
#   2. on a host with slots, runs <release>/bin/slotcheck: the slotctl allow-list (docs root, allowed_cwd,
#      allowed_exec), the slot table (root:<service group> 0640, readable by the service), every folder from / to
#      the Landlock launcher traversable by each slot, and for each assigned slot plus one unassigned test slot a real
#      `pwd` through sudo + slotctl + the launcher, with the shell tool's grant builder, in the docs root, a workflow,
#      a Crew project and a Code project. Prints a PASS/FAIL table and `FAIL <what> -- fix: <how>` lines;
#   3. the secret admission scan (names only) as a WARN section; it never fails the deploy.
# Exit 1 when any slot check fails (activation has already happened: nothing is rolled back, the deploy is loud).
# It changes nothing on the server.
set -uo pipefail

APP="" DOCS="" PRODUCT="" RELEASE="" LEVEL="${SECURITY_CHECKS_LEVEL:-basic}"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --app) APP="$2"; shift 2 ;;
    --docs) DOCS="$2"; shift 2 ;;
    --product) PRODUCT="$2"; shift 2 ;;
    --release) RELEASE="$2"; shift 2 ;;
    --level) LEVEL="$2"; shift 2 ;;
    *) echo "usage: slotcheck.sh --app <app> --docs <docs root> --product <product> [--release <release dir>] [--level basic|full]" >&2; exit 2 ;;
  esac
done
[[ -n "$APP" && -n "$DOCS" && -n "$PRODUCT" ]] || { echo "usage: slotcheck.sh --app <app> --docs <docs root> --product <product> [--release <release dir>]" >&2; exit 2; }
[[ "$LEVEL" == basic || "$LEVEL" == full ]] || { echo "slotcheck: --level must be basic or full (got $LEVEL)" >&2; exit 2; }
[[ -n "$RELEASE" ]] || RELEASE="$(readlink -f "$APP/current")"
export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"

# The keys the slot chain reads. Values are paths and switches, never secrets; nothing else is taken from the service.
SLOT_KEYS="AGENTWORKS_SLOTS AGENTWORKS_SLOT_PREFIX AGENTWORKS_SLOTCTL AGENTWORKS_SLOTCTL_CONFIG AGENTWORKS_SLOTS_FILE AGENT_BROWSER_SHARED_PROFILE AGENT_BROWSER_PROFILE_ROOT AGENTWORKS_BROWSER_SESSION_PREFIX AGENT_BROWSER_EXECUTABLE_PATH SANDBOX_EXTRA_SYSTEM_PATHS AGENTWORKS_STATE_ROOT WORKSPACE_DOCS_PATH"

main_pid() { systemctl --user show "$1" -p MainPID --value 2>/dev/null || echo 0; }

# service_env PID ENV_FILE: KEY=value lines for SLOT_KEYS, from the running process, else from the env file.
service_env() {
  python3 - "$1" "$2" $SLOT_KEYS <<'PY'
import sys
pid, env_file, keys = sys.argv[1], sys.argv[2], set(sys.argv[3:])
values = {}
try:
    if pid and pid != "0":
        for entry in open(f"/proc/{pid}/environ", "rb").read().decode(errors="replace").split("\0"):
            k, sep, v = entry.partition("=")
            if sep and k in keys:
                values[k] = v
except OSError:
    pass
if not values:
    try:
        for line in open(env_file):
            k, sep, v = line.strip().partition("=")
            if sep and k in keys:
                values[k] = v.strip().strip('"').strip("'")
    except OSError:
        pass
for k, v in values.items():
    if "\n" not in v:
        print(f"{k}={v}")
PY
}

echo "==> [$PRODUCT] slot self-test (read-only; release $(basename "$RELEASE"))"
ENV_LINES=()
slots_mode="" table="/etc/agentworks/slots.json"
while IFS= read -r line; do
  [[ -n "$line" ]] || continue
  ENV_LINES+=("$line")
  case "$line" in
    AGENTWORKS_SLOTS=*) slots_mode="$(printf '%s' "${line#*=}" | tr '[:upper:]' '[:lower:]')" ;;
    AGENTWORKS_SLOTS_FILE=*) table="${line#*=}" ;;
  esac
done < <(service_env "$(main_pid "$PRODUCT-workspace")" "$APP/.env")
rc=0
case "$slots_mode" in
  on|optin)
    if [[ ! -x "$RELEASE/bin/slotcheck" ]]; then
      echo "FAIL slotcheck-binary: $RELEASE/bin/slotcheck is missing -- fix: deploy a release built with deploy/common/slots.sh slots_build"
      rc=1
    else
      env -i PATH=/usr/bin:/bin HOME="${HOME:-/}" ${ENV_LINES[@]+"${ENV_LINES[@]}"} "$RELEASE/bin/slotcheck" --docs "$DOCS" --app "$APP" --level "$LEVEL" || rc=$?
    fi
    ;;
  *)
    if [[ -e "$table" ]]; then
      echo "WARN slots-mode: a slot table exists ($table) but the workspace service runs without AGENTWORKS_SLOTS: slot checks skipped"
    else
      echo "slots are not enabled on this host: slot checks skipped"
    fi
    ;;
esac

echo "==> [$PRODUCT] secret admission scan (names only, warnings never fail the deploy)"
if [[ -f "$RELEASE/admission_scan.py" ]]; then
  python3 "$RELEASE/admission_scan.py" --docs "$DOCS" --agent-pid "$(main_pid "$PRODUCT-agent")" --env-file "$APP/.env" || true
else
  echo "WARN secret admission scan is not in this release"
fi

if [[ "$rc" != 0 ]]; then
  echo "==> [$PRODUCT] SLOT SELF-TEST FAILED: the release is active, but slot users' shell commands will fail as listed above." >&2
fi
exit "$rc"
