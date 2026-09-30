#!/usr/bin/env bash
# Install Agy from Google's release manifest, verifying the payload before
# replacing the managed binary. Configure the service home for the existing
# GEMINI_API_KEY; settings contain no credentials. Runs on the target host.
set -euo pipefail
prefix="${1:?usage: install-agy.sh <prefix> <service-home>}"
service_home="${2:?usage: install-agy.sh <prefix> <service-home>}"
[[ "$(uname -s)" == Linux ]] || { echo 'install-agy: Linux required' >&2; exit 1; }
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) echo 'install-agy: unsupported architecture' >&2; exit 1 ;;
esac
platform="linux_$arch"
if [[ -f /lib/libc.musl-x86_64.so.1 || -f /lib/libc.musl-aarch64.so.1 ]]; then platform+='_musl'; fi
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 \
  "https://antigravity-cli-auto-updater-974169037036.us-central1.run.app/manifests/$platform.json" -o "$tmp/manifest.json"
mapfile -t release < <(python3 - "$tmp/manifest.json" <<'PY'
import json, re, sys
d = json.load(open(sys.argv[1]))
assert re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", d["version"])
assert d["url"].startswith("https://storage.googleapis.com/antigravity-public/")
assert re.fullmatch(r"[a-fA-F0-9]{128}", d["sha512"])
print(d["version"]); print(d["url"]); print(d["sha512"])
PY
)
[[ "${#release[@]}" == 3 ]] || { echo 'install-agy: invalid release manifest' >&2; exit 1; }
curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 "${release[1]}" -o "$tmp/payload"
printf '%s  %s\n' "${release[2]}" "$tmp/payload" | sha512sum -c - >/dev/null
case "${release[1]}" in
  *.tar.gz*) tar -xzf "$tmp/payload" -C "$tmp" antigravity ;;
  *) mv "$tmp/payload" "$tmp/antigravity" ;;
esac
chmod 0755 "$tmp/antigravity"
HOME="$service_home" "$tmp/antigravity" --version
install -d -m 0755 "$prefix/bin"
install -m 0755 "$tmp/antigravity" "$prefix/bin/agy.new"
mv -f "$prefix/bin/agy.new" "$prefix/bin/agy"
python3 - "$service_home" <<'PY'
import json, os, pathlib, sys, tempfile
path = pathlib.Path(sys.argv[1]) / ".gemini/antigravity-cli/settings.json"
path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
settings = json.loads(path.read_text()) if path.exists() else {}
settings["modelProvider"] = "gemini"
fd, temp = tempfile.mkstemp(dir=path.parent, prefix=".settings-")
try:
    with os.fdopen(fd, "w") as f:
        json.dump(settings, f, indent=2); f.write("\n")
    os.replace(temp, path)
finally:
    if os.path.exists(temp): os.unlink(temp)
PY
echo "    agy: ${release[0]} installed (Gemini API-key mode)"
