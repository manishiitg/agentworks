#!/usr/bin/env bash
# Reuse healthy managed tools. Vendor upgrades are explicit maintenance; a
# missing/broken binary or a changed installation policy still triggers repair.
set -euo pipefail
prefix="${1:?usage: install-coding-clis.sh <prefix> <service-home> [auto|gemini] [0|1]}"
service_home="${2:?usage: install-coding-clis.sh <prefix> <service-home> [auto|gemini] [0|1]}"
agy_auth_mode="${3:-auto}"
refresh="${4:-0}"
case "$agy_auth_mode" in auto|gemini) ;; *) echo 'Invalid Agy authentication mode' >&2; exit 1 ;; esac
case "$refresh" in 0|1) ;; *) echo 'Tool refresh must be 0 or 1' >&2; exit 1 ;; esac
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
sdk_installer="${5:-$script_dir/../../agent_go/internal/codingsdks/coding-sdks.cjs}"
CODING_CLI_BINARIES=(claude codex cursor-agent pi muse agy)
install -d -m 0755 "$prefix/bin" "$service_home/.local/bin"
export PATH="$prefix/node/bin:$prefix/bin:$service_home/.local/bin:$PATH"
export HOME="$service_home"
# The prefix survives releases. Serialize maintenance and write receipts only
# after successful launches so interrupted installs cannot become a cache hit.
previous_umask="$(umask)"
umask 077
exec 9>"$prefix/.coding-cli-install.lock"
umask "$previous_umask"
flock -w 300 9
state="$prefix/.coding-cli-state"
install -d -m 0700 "$state"
policy="$(sha256sum "$script_dir/install-coding-clis.sh" "$script_dir/install-agy.sh" "$script_dir/coding-cli-state.py" "$sdk_installer" | cut -d ' ' -f 1 | sha256sum | cut -d ' ' -f 1)"
policy+=":$(node --version):$(npm --version):$(uname -m)"
tool_policy() {
  if [[ "$1" == agy ]]; then printf '%s:agy:%s' "$policy" "$agy_auth_mode"; else printf '%s:%s' "$policy" "$1"; fi
}
tool_version() {
  local resolved
  resolved="$(command -v "$1")" || return 1
  case "$resolved" in "$prefix/bin/"*|"$service_home/.local/bin/"*) ;; *) return 1 ;; esac
  timeout 45 "$resolved" --version 2>/dev/null
}
record_tool() {
  local version
  version="$(tool_version "$1")" && [[ -n "$version" ]] || {
    echo "FATAL: $1 does not launch from the managed installation" >&2; return 1;
  }
  python3 "$script_dir/coding-cli-state.py" write "$state/$1.json" "$(tool_policy "$1")" "$version" || return 1
  printf '    %s: %s\n' "$1" "$version"
}
needs_install() {
  local version result
  [[ "$refresh" == 0 ]] || return 0
  version="$(tool_version "$1")" && [[ -n "$version" ]] || return 0
  if [[ "$1" == agy && "$agy_auth_mode" == gemini ]]; then
    python3 - "$service_home/.gemini/antigravity-cli/settings.json" <<'PY' || return 0
import json, sys
try:
    assert json.load(open(sys.argv[1]))["modelProvider"] == "gemini"
except (OSError, ValueError, KeyError, AssertionError):
    sys.exit(1)
PY
  fi
  result=0
  python3 "$script_dir/coding-cli-state.py" check "$state/$1.json" "$(tool_policy "$1")" "$version" || result=$?
  # Adopt healthy managed installations from before receipts existed. A
  # corrupt/stale receipt requires repair rather than being adopted.
  [[ "$result" == 0 || "$result" == 2 ]] || return 0
  # Leave the successful receipt's date intact on reuse; otherwise an old
  # tool could never reach the maintenance warning threshold.
  if [[ "$result" == 2 ]]; then record_tool "$1" || return 0; fi
  printf '    %s: %s\n' "$1" "$version"
  echo "    $1: unchanged; installer skipped"
  return 1
}
install_node_cli() {
  local cli="$1" package="$2"
  needs_install "$cli" || return 0
  case "$cli" in
    claude) rm -rf "$prefix"/lib/node_modules/@anthropic-ai/.claude-code-* ;;
    codex) rm -rf "$prefix"/lib/node_modules/@openai/.codex-* ;;
    pi) rm -rf "$prefix"/lib/node_modules/@earendil-works/.pi-coding-agent-* ;;
  esac
  local attempt
  for attempt in 1 2 3 4; do
    npm install -g --prefix "$prefix" --include=optional \
      --allow-scripts=@anthropic-ai/claude-code,esbuild,protobufjs,@google/genai,agent-browser "$package@latest"
    if record_tool "$cli"; then return 0; fi
    # Latest can appear before Codex's platform binary; retry just that tool.
    [[ "$cli" == codex && "$attempt" != 4 ]] || return 1
    echo 'codex platform package not ready; waiting before repair' >&2
    sleep 60
    rm -rf "$prefix/lib/node_modules/@openai/codex"
  done
}
install_node_cli claude @anthropic-ai/claude-code
install_node_cli codex @openai/codex
install_node_cli pi @earendil-works/pi-coding-agent

# Same version/export contract as the provisioner embedded in local/Desktop.
sdk_args=(--prefix "$prefix")
[[ "$refresh" == 0 ]] || sdk_args+=(--force)
node "$sdk_installer" "${sdk_args[@]}"

if needs_install cursor-agent; then
  curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 https://cursor.com/install | bash
  test -x "$service_home/.local/bin/cursor-agent"
  if [[ "$prefix/bin" != "$service_home/.local/bin" ]]; then
    ln -sfn "$service_home/.local/bin/cursor-agent" "$prefix/bin/cursor-agent"
  fi
  record_tool cursor-agent
fi
if needs_install muse; then
  curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 https://dev.meta.ai/install.sh \
    | MUSE_INSTALL_DIR="$prefix/bin" MUSE_NO_MODIFY_PATH=1 bash
  record_tool muse
fi
if needs_install agy; then
  if [[ "$agy_auth_mode" == gemini ]]; then
    bash "$script_dir/install-agy.sh" "$prefix" "$service_home"
  else
    bash "$script_dir/install-agy.sh" "$prefix"
  fi
  record_tool agy
fi
install_node_cli agent-browser agent-browser
# Check the complete provider catalog even when no installer was called.
for cli in "${CODING_CLI_BINARIES[@]}" agent-browser; do
  tool_version "$cli" >/dev/null || { echo "FATAL: $cli is unavailable" >&2; exit 1; }
done
