#!/usr/bin/env bash
# Every server deploy installs/updates the complete coding-provider catalog.
# One managed prefix; no conditional "already installed" skips. Authentication
# remains separate. Optional Gemini mode uses the service's existing key.
set -euo pipefail
prefix="${1:?usage: install-coding-clis.sh <prefix> <service-home> [auto|gemini]}"
service_home="${2:?usage: install-coding-clis.sh <prefix> <service-home> [auto|gemini]}"
agy_auth_mode="${3:-auto}"
case "$agy_auth_mode" in auto|gemini) ;; *) echo 'Invalid Agy authentication mode' >&2; exit 1 ;; esac
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
CODING_CLI_BINARIES=(claude codex cursor-agent pi muse agy)
install -d -m 0755 "$prefix/bin" "$service_home/.local/bin"
export PATH="$prefix/node/bin:$prefix/bin:$service_home/.local/bin:$PATH"
# Clean abandoned npm staging copies that can otherwise leave the old binary
# on PATH after an interrupted update (RTS 2026-09-25).
rm -rf "$prefix"/lib/node_modules/@anthropic-ai/.claude-code-* \
  "$prefix"/lib/node_modules/@earendil-works/.pi-coding-agent-* \
  "$prefix"/lib/node_modules/@openai/.codex-*
HOME="$service_home" npm install -g --prefix "$prefix" --include=optional \
  --allow-scripts=@anthropic-ai/claude-code,esbuild,protobufjs,@google/genai \
  @anthropic-ai/claude-code@latest @openai/codex@latest @earendil-works/pi-coding-agent@latest
curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 https://cursor.com/install | HOME="$service_home" bash
test -x "$service_home/.local/bin/cursor-agent"
if [[ "$prefix/bin" != "$service_home/.local/bin" ]]; then
  ln -sfn "$service_home/.local/bin/cursor-agent" "$prefix/bin/cursor-agent"
fi
curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 https://dev.meta.ai/install.sh \
  | HOME="$service_home" MUSE_INSTALL_DIR="$prefix/bin" MUSE_NO_MODIFY_PATH=1 bash
if [[ "$agy_auth_mode" == gemini ]]; then
  bash "$script_dir/install-agy.sh" "$prefix" "$service_home"
else
  HOME="$service_home" bash "$script_dir/install-agy.sh" "$prefix"
fi
for cli in "${CODING_CLI_BINARIES[@]}"; do
  resolved="$(command -v "$cli")"
  case "$resolved" in
    "$prefix/bin/"*|"$service_home/.local/bin/"*) ;;
    *) echo "FATAL: $cli resolves outside the managed installation" >&2; exit 1 ;;
  esac
  # Installation is only successful if the binary actually launches. This
  # catches npm packages that publish successfully without a Linux binary.
  timeout 45 "$resolved" --version
done
