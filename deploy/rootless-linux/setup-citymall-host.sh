#!/usr/bin/env bash
# Prepare Citymall's dedicated Ubuntu host (13.206.199.45; see citymall.md). Idempotent: ./deploy.sh citymall runs it
# as root (sudo through the ubuntu account) before every deploy. It installs packages, the unprivileged citymall account,
# its owner-only .env (generated once), the systemd --user units and the nginx site. It starts no application service:
# the deploy activates the release and restarts the units.
set -euo pipefail

[[ $EUID -eq 0 ]] || { echo 'Run as root.' >&2; exit 1; }
[[ $(uname -sm) == 'Linux x86_64' ]] || { echo 'Expected Linux x86_64.' >&2; exit 1; }
# shellcheck source=/dev/null
. /etc/os-release
[[ ${ID:-} == ubuntu ]] || { echo 'Expected Ubuntu.' >&2; exit 1; }

export DEBIAN_FRONTEND=noninteractive
packages=(ca-certificates curl git rsync jq unzip zip xz-utils file openssl
  build-essential pkg-config libsqlite3-dev python3-pip python3-venv
  tmux dbus-user-session uidmap slirp4netns fuse-overlayfs
  libnspr4 libnss3 libatk1.0-0t64 libatk-bridge2.0-0t64 libcups2t64
  libdrm2 libxkbcommon0 libxcomposite1 libxdamage1 libxfixes3 libxrandr2
  libgbm1 libasound2t64 fonts-liberation nginx certbot python3-certbot-nginx)
if ! dpkg-query -W -f='${Status}\n' "${packages[@]}" 2>/dev/null | grep -c '^install ok installed$' | grep -qx "${#packages[@]}"; then
  apt-get update -qq
  apt-get install -y --no-install-recommends "${packages[@]}"
fi

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
  builds prebuilt releases logs state state/agent-tmp data data/docs data/docs/Downloads \
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
  systemctl --user is-system-running || true  # "degraded" when a unit failed; never block a redeploy
runuser -u citymall -- python3 -m pip --version
runuser -u citymall -- bash -s <<'VERIFY'
set -euo pipefail
stage=$(mktemp -d /srv/citymall/builds/venv-check.XXXXXX)
trap 'rm -rf "$stage"' EXIT
python3 -m venv "$stage/venv"
"$stage/venv/bin/python" -m pip --version
VERIFY
# Non-secret settings the deploy's preflight needs before the first release. The public URL is already the final HTTPS
# one: cookies are Secure, and https://agents.citymall.live is what OAuth callbacks and links must use once certbot runs.
grep -q '^PUBLIC_URL=' /srv/citymall/.env || echo 'PUBLIC_URL=https://agents.citymall.live' >> /srv/citymall/.env

# systemd --user units (rewritten when they differ; the deploy owns their drop-ins and restarts them).
unit_dir=/srv/citymall/.config/systemd/user
install -d -o citymall -g citymall -m 755 "$unit_dir/default.target.wants"
write_unit() { # name, content
  local path="$unit_dir/$1.service"
  if [[ ! -f "$path" ]] || [[ "$(cat "$path")" != "$2" ]]; then
    printf '%s\n' "$2" > "$path.next"
    chown citymall:citymall "$path.next"
    mv "$path.next" "$path"
    echo "installed $path"
  fi
  ln -sfn "../$1.service" "$unit_dir/default.target.wants/$1.service"
  chown -h citymall:citymall "$unit_dir/default.target.wants/$1.service"
}
write_unit citymall-workspace "[Unit]
Description=Citymall workspace API
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=/srv/citymall/.env
Environment=HOME=/srv/citymall/home
WorkingDirectory=/srv/citymall/current
ExecStart=/srv/citymall/current/bin/citymall-workspace server --host 127.0.0.1 --port 25001 --docs-dir /srv/citymall/data/docs
StandardOutput=append:/srv/citymall/logs/workspace.log
StandardError=append:/srv/citymall/logs/workspace.log
Restart=always
RestartSec=5
LimitNOFILE=65536

[Install]
WantedBy=default.target"
write_unit citymall-agent "[Unit]
Description=Citymall agent API
After=network-online.target citymall-workspace.service
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=/srv/citymall/.env
Environment=HOME=/srv/citymall/home
Environment=MCP_BRIDGE_BINARY=/srv/citymall/current/bin/mcpbridge
WorkingDirectory=/srv/citymall/current
ExecStart=/srv/citymall/current/bin/citymall-agent server --host 127.0.0.1 --port 25000 --log-level info --log-file /srv/citymall/logs/agent.log --mcp-config /srv/citymall/current/configs/mcp_servers_citymall.json --max-turns 100
StandardOutput=append:/srv/citymall/logs/agent.log
StandardError=append:/srv/citymall/logs/agent.log
Restart=always
RestartSec=5
LimitNOFILE=65536

[Install]
WantedBy=default.target"
write_unit citymall-gateway "[Unit]
Description=Citymall gateway (frontend and per-user token check)
After=network-online.target citymall-agent.service citymall-workspace.service
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=/srv/citymall/.env
Environment=HOME=/srv/citymall/home
WorkingDirectory=/srv/citymall/current
ExecStart=/srv/citymall/current/bin/citymall-gateway
StandardOutput=append:/srv/citymall/logs/gateway.log
StandardError=append:/srv/citymall/logs/gateway.log
Restart=always
RestartSec=3

[Install]
WantedBy=default.target"
runuser -u citymall -- env "XDG_RUNTIME_DIR=/run/user/$service_uid" \
  "DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/$service_uid/bus" systemctl --user daemon-reload

# nginx: the site file comes from the repository (products/citymall/nginx-site.conf, passed by deploy.sh as
# NGINX_SITE_B64). Validate the whole configuration and restore the previous site if it does not pass.
if [[ -n "${NGINX_SITE_B64:-}" ]]; then
  # HTTPS once certbot has the certificate (nginx-site.conf includes this folder; empty means plain HTTP).
  install -d -m 0755 /etc/nginx/citymall-tls.d
  cert=/etc/letsencrypt/live/agents.citymall.live
  if [[ -s "$cert/fullchain.pem" && -s "$cert/privkey.pem" ]]; then
    cat > /etc/nginx/citymall-tls.d/tls.conf <<TLS
listen 443 ssl;
listen [::]:443 ssl;
ssl_certificate $cert/fullchain.pem;
ssl_certificate_key $cert/privkey.pem;
include /etc/letsencrypt/options-ssl-nginx.conf;
ssl_dhparam /etc/letsencrypt/ssl-dhparams.pem;
# Plain http on the domain goes to https (the bare IP keeps plain http: the certificate does not cover it).
set \$citymall_redirect "\$scheme:\$host";
if (\$citymall_redirect = "http:agents.citymall.live") { return 301 https://agents.citymall.live\$request_uri; }
TLS
  else
    rm -f /etc/nginx/citymall-tls.d/tls.conf
  fi
  site=/etc/nginx/sites-available/citymall
  printf '%s' "$NGINX_SITE_B64" | base64 -d > "$site.next"
  if [[ ! -f "$site" ]] || ! cmp -s "$site.next" "$site"; then
    [[ -f "$site" ]] && cp -p "$site" "$site.prev"
    mv "$site.next" "$site"
    ln -sfn "$site" /etc/nginx/sites-enabled/citymall
    rm -f /etc/nginx/sites-enabled/default
    if ! nginx -t; then
      if [[ -f "$site.prev" ]]; then cp -p "$site.prev" "$site"; else rm -f "$site" /etc/nginx/sites-enabled/citymall; fi
      echo 'nginx configuration did not validate; previous site restored.' >&2
      exit 1
    fi
    echo "nginx site $site installed"
  else
    rm -f "$site.next"
  fi
  systemctl enable --now nginx >/dev/null
  systemctl reload nginx
fi
echo 'Citymall host ready.'
