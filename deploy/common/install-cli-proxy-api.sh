#!/usr/bin/env bash
# Install the pinned model proxy (CLIProxyAPI) that the server runs for company logins used from the Mac app
# (PLAT-856-441). The server starts it itself when an admin turns a program on.
set -euo pipefail
prefix="${1:?usage: install-cli-proxy-api.sh <prefix>}"
version=8.0.49
case "$(uname -m)" in
  x86_64) arch=amd64; sha=f61d5e27ed46c1eebfdd240eb50cf15d6d6be38b22159e9618a667f671f22d94 ;;
  aarch64|arm64) arch=aarch64; sha=685bbc0a99a6f8b4ddfaa1666f446a0d7a6ee36ea53216b29e2fb7e0c6a5711b ;;
  *) echo "cli-proxy-api: unsupported machine $(uname -m); skipping" >&2; exit 0 ;;
esac
if [[ -x "$prefix/bin/cli-proxy-api" && "$(cat "$prefix/share/cli-proxy-api.version" 2>/dev/null)" == "$version" ]]; then exit 0; fi
stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT
curl -fsSL --connect-timeout 15 --max-time 120 --proto '=https' --tlsv1.2 \
  "https://github.com/router-for-me/CLIProxyAPI/releases/download/v$version/CLIProxyAPI_${version}_linux_${arch}.tar.gz" -o "$stage/proxy.tgz"
actual="$(sha256sum "$stage/proxy.tgz" | cut -d' ' -f1)"
[[ "$actual" == "$sha" ]] || { echo 'cli-proxy-api checksum mismatch' >&2; exit 1; }
tar -xzf "$stage/proxy.tgz" -C "$stage" cli-proxy-api LICENSE
install -d -m0755 "$prefix/bin" "$prefix/share/licenses/cli-proxy-api"
install -m0755 "$stage/cli-proxy-api" "$prefix/bin/cli-proxy-api.new"
mv "$prefix/bin/cli-proxy-api.new" "$prefix/bin/cli-proxy-api"
install -m0644 "$stage/LICENSE" "$prefix/share/licenses/cli-proxy-api/LICENSE"
printf '%s\n' "$version" > "$prefix/share/cli-proxy-api.version"
