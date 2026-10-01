#!/usr/bin/env bash
# Prepare Citymall's dedicated Ubuntu host before its domain/auth configuration.
# Run as root on 52.66.201.227; no application service or public site is started.
set -euo pipefail

[[ $EUID -eq 0 ]] || { echo 'Run as root.' >&2; exit 1; }
[[ $(uname -sm) == 'Linux x86_64' ]] || { echo 'Expected Linux x86_64.' >&2; exit 1; }
# shellcheck source=/dev/null
. /etc/os-release
[[ ${ID:-} == ubuntu ]] || { echo 'Expected Ubuntu.' >&2; exit 1; }

export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y --no-install-recommends \
  ca-certificates curl git rsync jq unzip zip xz-utils file openssl \
  build-essential pkg-config libsqlite3-dev python3-pip python3-venv \
  tmux dbus-user-session uidmap slirp4netns fuse-overlayfs \
  libnspr4 libnss3 libatk1.0-0t64 libatk-bridge2.0-0t64 libcups2t64 \
  libdrm2 libxkbcommon0 libxcomposite1 libxdamage1 libxfixes3 libxrandr2 \
  libgbm1 libasound2t64 fonts-liberation

if ! id citymall >/dev/null 2>&1; then
  useradd --create-home --home-dir /srv/citymall --shell /bin/bash citymall
fi
[[ $(getent passwd citymall | cut -d: -f6) == /srv/citymall ]] || {
  echo 'Existing citymall account has an unexpected home.' >&2; exit 1;
}
if id -nG citymall | tr ' ' '\n' | grep -Eq '^(sudo|docker)$'; then
  echo 'Citymall must be an unprivileged service account.' >&2; exit 1
fi
chmod 750 /srv/citymall
for directory in home home/Downloads home/.local home/.config tools tools/bin \
  builds releases logs state state/agent-tmp data data/docs data/docs/Downloads \
  .config .config/systemd .config/systemd/user; do
  install -d -o citymall -g citymall -m 700 "/srv/citymall/$directory"
done
loginctl enable-linger citymall
service_uid=$(id -u citymall)
systemctl start "user@$service_uid.service"

# Generate deployment-specific secrets once; never borrow another customer's
# credentials, never print them, and never replace them on a repeated setup.
if [[ ! -e /srv/citymall/.env ]]; then
  umask 077
  {
    printf 'AUTH_SECRET=%s\n' "$(openssl rand -hex 32)"
    printf 'ACCESS_PASSWORD=%s\n' "$(openssl rand -hex 32)"
    printf 'GOG_KEYRING_PASSWORD=%s\n' "$(openssl rand -hex 32)"
    cat <<'ENV'
GOG_KEYRING_BACKEND=file
HOME=/srv/citymall/home
PATH=/srv/citymall/tools/node/bin:/srv/citymall/tools/bin:/srv/citymall/home/.local/bin:/usr/local/bin:/usr/bin:/bin
WORKSPACE_DOCS_PATH=/srv/citymall/data/docs
WORKSPACE_API_URL=http://127.0.0.1:25001
MCP_API_URL=http://127.0.0.1:25000
STATIC_DIR=/srv/citymall/current/frontend
GATEWAY_ADDR=127.0.0.1:25080
AGENTWORKS_CLI_LANDLOCK=on
AGENTWORKS_CLI_FULL=on
AGENT_BROWSER_CDP_ENABLED=false
ENV
  } > /srv/citymall/.env
  chown citymall:citymall /srv/citymall/.env
fi
[[ $(stat -c %U /srv/citymall/.env) == citymall ]] || {
  echo 'Citymall environment file has an unexpected owner.' >&2; exit 1;
}
chmod 600 /srv/citymall/.env

# Authorize the same public key used by the supplied ubuntu SSH login.
# Only public authorized_keys entries are copied, never the private PEM.
if [[ -s /home/ubuntu/.ssh/authorized_keys && ! -e /srv/citymall/.ssh/authorized_keys ]]; then
  install -d -o citymall -g citymall -m 700 /srv/citymall/.ssh
  install -o citymall -g citymall -m 600 /home/ubuntu/.ssh/authorized_keys /srv/citymall/.ssh/authorized_keys
fi

# Use the same checksum-pinned Node runtime as Confida. The dedicated
# service owns it; npm installs never need root or the Ubuntu account.
if [[ ! -x /srv/citymall/tools/node-v24.21.0/bin/node ]]; then
  node_stage=$(mktemp -d /srv/citymall/tools/.node-install.XXXXXX)
  trap 'rm -rf "$node_stage"' EXIT
  curl -fsSL https://nodejs.org/dist/v24.21.0/node-v24.21.0-linux-x64.tar.xz \
    -o "$node_stage/node.tar.xz"
  printf '%s  %s\n' fd8e59d5a511510f6a298afb548f18c7d2b1be404d8b4a27d94fbe49f56cb2d6 \
    "$node_stage/node.tar.xz" | sha256sum -c -
  tar -xJf "$node_stage/node.tar.xz" -C "$node_stage"
  mv "$node_stage/node-v24.21.0-linux-x64" /srv/citymall/tools/node-v24.21.0
  chown -R citymall:citymall /srv/citymall/tools/node-v24.21.0
  rm -rf "$node_stage"
  trap - EXIT
fi
ln -sfn /srv/citymall/tools/node-v24.21.0 /srv/citymall/tools/node
chown -h citymall:citymall /srv/citymall/tools/node
runuser -u citymall -- /srv/citymall/tools/node/bin/node --version
runuser -u citymall -- env PATH=/srv/citymall/tools/node/bin:/usr/bin:/bin npm --version

runuser -u citymall -- env "XDG_RUNTIME_DIR=/run/user/$service_uid" \
  "DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/$service_uid/bus" \
  systemctl --user is-system-running
runuser -u citymall -- python3 -m pip --version
runuser -u citymall -- bash -s <<'VERIFY'
set -euo pipefail
stage=$(mktemp -d /srv/citymall/builds/venv-check.XXXXXX)
trap 'rm -rf "$stage"' EXIT
python3 -m venv "$stage/venv"
"$stage/venv/bin/python" -m pip --version
VERIFY
echo 'Citymall base host ready. Domain, sign-in settings and app scope are needed before deploying.'
