#!/usr/bin/env bash
# One-time root setup for agents.excellencetechnologies.in on the shared
# rootless host (the part deploy/rootless-linux does not do: see its README,
# "Requirements this template assumes"). Idempotent: re-running changes
# nothing that is already in place. Run as root ON the host:
#
#   ssh -p 2299 root@116.202.210.102 'bash -s' < deploy/rootless-linux/setup-agents-host.sh
#
# Already done by hand on 2026-09-28: the `agents` system account
# (home /srv/agents, linger on, its directories) and /srv/agents/.env
# (fresh secrets, Supabase sign-in, ADMIN_USERS). This script checks both.
set -euo pipefail

PRODUCT=agents
HOME_DIR=/srv/agents
DOMAIN=agents.excellencetechnologies.in
UNIT_DIR="$HOME_DIR/.config/systemd/user"
CADDYFILE=/etc/caddy/Caddyfile

[[ $EUID -eq 0 ]] || { echo "Run as root." >&2; exit 1; }
id "$PRODUCT" >/dev/null || { echo "Account $PRODUCT is missing." >&2; exit 1; }
[[ -f "$HOME_DIR/.env" ]] || { echo "$HOME_DIR/.env is missing." >&2; exit 1; }
[[ "$(loginctl show-user "$PRODUCT" -p Linger --value)" == yes ]] || loginctl enable-linger "$PRODUCT"

install -d -o "$PRODUCT" -g "$PRODUCT" -m 700 "$HOME_DIR/.config" "$HOME_DIR/.config/systemd" "$UNIT_DIR"
install -d -o "$PRODUCT" -g "$PRODUCT" -m 755 "$UNIT_DIR/default.target.wants"

write_unit() { # name, content (only when absent: the deploy manages drop-ins)
  local path="$UNIT_DIR/$1.service"
  if [[ ! -e "$path" ]]; then
    printf '%s\n' "$2" > "$path"
    chown "$PRODUCT:$PRODUCT" "$path"
    echo "installed $path"
  fi
  ln -sfn "../$1.service" "$UNIT_DIR/default.target.wants/$1.service"
  chown -h "$PRODUCT:$PRODUCT" "$UNIT_DIR/default.target.wants/$1.service"
}

write_unit agents-workspace "[Unit]
Description=Agents (Code) workspace API
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=$HOME_DIR/.env
Environment=HOME=$HOME_DIR/home
WorkingDirectory=$HOME_DIR/current
ExecStart=$HOME_DIR/current/bin/agents-workspace server --host 127.0.0.1 --port 24001 --docs-dir $HOME_DIR/data/docs
StandardOutput=append:$HOME_DIR/logs/workspace.log
StandardError=append:$HOME_DIR/logs/workspace.log
Restart=always
RestartSec=5
LimitNOFILE=65536

[Install]
WantedBy=default.target"

write_unit agents-agent "[Unit]
Description=Agents (Code) agent API
After=network-online.target agents-workspace.service
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=$HOME_DIR/.env
Environment=HOME=$HOME_DIR/home
Environment=MCP_BRIDGE_BINARY=$HOME_DIR/current/bin/mcpbridge
WorkingDirectory=$HOME_DIR/current
ExecStart=$HOME_DIR/current/bin/agents-agent server --host 127.0.0.1 --port 24000 --log-level info --log-file $HOME_DIR/logs/agent.log --mcp-config $HOME_DIR/current/configs/mcp_servers_agents.json --max-turns 100
StandardOutput=append:$HOME_DIR/logs/agent.log
StandardError=append:$HOME_DIR/logs/agent.log
Restart=always
RestartSec=5
LimitNOFILE=65536

[Install]
WantedBy=default.target"

write_unit agents-gateway "[Unit]
Description=Agents (Code) cookie-auth gateway
After=network-online.target agents-agent.service agents-workspace.service
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=$HOME_DIR/.env
Environment=HOME=$HOME_DIR/home
WorkingDirectory=$HOME_DIR/current
ExecStart=$HOME_DIR/current/bin/agents-gateway
StandardOutput=append:$HOME_DIR/logs/gateway.log
StandardError=append:$HOME_DIR/logs/gateway.log
Restart=always
RestartSec=3

[Install]
WantedBy=default.target"

# Caddy: add the site once, validate the whole file, then reload gracefully.
# A failed validation restores the previous file and changes nothing live.
if ! grep -q "^$DOMAIN {" "$CADDYFILE"; then
  backup="$CADDYFILE.bak-agents-$(date +%Y%m%d%H%M%S)"
  cp -p "$CADDYFILE" "$backup"
  printf '\n%s {\n    encode zstd gzip\n    reverse_proxy 127.0.0.1:24080\n}\n' "$DOMAIN" >> "$CADDYFILE"
  if ! caddy validate --config "$CADDYFILE" --adapter caddyfile; then
    cp -p "$backup" "$CADDYFILE"
    echo "Caddyfile did not validate; restored $backup." >&2
    exit 1
  fi
  systemctl reload caddy
  echo "Caddy site $DOMAIN added (backup: $backup)."
fi

echo "Host setup for $PRODUCT is in place. Next, from the repo: ./deploy.sh agents"
