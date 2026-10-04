#!/usr/bin/env bash
# Per-user Linux accounts ("slots") for a rootless product host. Run as root ON the host:
#
#   ssh -p 2299 root@<host> 'PRODUCT=agents bash -s -- init'            < deploy/common/provision-slots.sh
#   ssh -p 2299 root@<host> 'PRODUCT=agents bash -s -- adduser <email> [role] [products]' < deploy/common/provision-slots.sh
#   ssh -p 2299 root@<host> 'PRODUCT=agents bash -s -- assign <user-id>' < deploy/common/provision-slots.sh
#
# What it sets up (idempotent):
#   - slot01..slotNN accounts, each with its own group; the product's service account joins every
#     slot group once, so adding a person later needs no service restart;
#   - the one program the service account may run as a slot (slotctl, root-owned) and a sudoers rule
#     that allows exactly that;
#   - /etc/agentworks/slots.json (user id -> slot), readable by the service account and changed only
#     here, so nothing the service runs can reassign slots;
#   - a user's tree under data/docs/_users/<id> belongs to that user's slot group, with no access
#     for anyone else.
# Signing in never provisions anything: an administrator runs `assign`.
set -euo pipefail

# PRODUCT is the service account. The layout defaults to the rootless-linux products (/srv/<product>);
# another host sets APP_DIR (releases, current, slots, .env), DOCS and SERVICE_HOME, for example the
# RTS EC2 host: PRODUCT=video-studio APP_DIR=/var/lib/video-studio/video-studio DOCS=/data/video-studio/docs
# SERVICE_HOME=/var/lib/video-studio
PRODUCT="${PRODUCT:-agents}"
HOME_DIR="${APP_DIR:-/srv/$PRODUCT}"
DOCS="${DOCS:-$HOME_DIR/data/docs}"
SERVICE_HOME="${SERVICE_HOME:-$HOME_DIR/home}"
SLOT_COUNT="${SLOT_COUNT:-50}"
# Products that share one host each get their own slot accounts, launcher, config, table and sudo rule, so a
# product's service account is only ever in its own slot groups. The default prefix "slot" keeps the original
# host-wide names (the first product on a host, excellence's `agents`); any other prefix puts everything under
# a per-product name: accounts <prefix>01.., /usr/local/libexec/agentworks/<product>/, /etc/agentworks/<product>/,
# /etc/sudoers.d/agentworks-slots-<product>. Set the same prefix in the product's service environment
# (AGENTWORKS_SLOT_PREFIX, AGENTWORKS_SLOTCTL, AGENTWORKS_SLOTCTL_CONFIG, AGENTWORKS_SLOTS_FILE).
SLOT_PREFIX="${SLOT_PREFIX:-slot}"
[[ "$SLOT_PREFIX" =~ ^[a-z][a-z0-9]{0,15}$ ]] || { echo "SLOT_PREFIX must be lowercase letters and digits, starting with a letter." >&2; exit 2; }
if [[ "$SLOT_PREFIX" == slot ]]; then
  LIBEXEC=/usr/local/libexec/agentworks
  ETC=/etc/agentworks
  SUDOERS=/etc/sudoers.d/agentworks-slots
else
  LIBEXEC="/usr/local/libexec/agentworks/$PRODUCT"
  ETC="/etc/agentworks/$PRODUCT"
  SUDOERS="/etc/sudoers.d/agentworks-slots-$PRODUCT"
fi
TABLE="$ETC/slots.json"
SLOTCTL_CONFIG="$LIBEXEC/slotctl.json"
# sudoers alias names are shared by every file under /etc/sudoers.d: one per product.
if [[ "$SLOT_PREFIX" == slot ]]; then ALIAS=AGENTWORKS_SLOTS; else ALIAS="AGENTWORKS_SLOTS_$(printf '%s' "$PRODUCT" | tr 'a-z-' 'A-Z_')"; fi

[[ $EUID -eq 0 ]] || { echo "Run as root." >&2; exit 1; }
id "$PRODUCT" >/dev/null || { echo "Account $PRODUCT is missing." >&2; exit 1; }

slot_name() { printf '%s%02d' "$SLOT_PREFIX" "$1"; }

cmd_init() {
  command -v setfacl >/dev/null || { echo "Installing the acl package (setfacl)"; DEBIAN_FRONTEND=noninteractive apt-get install -y acl >/dev/null; }
  command -v setfacl >/dev/null || { echo "setfacl is missing and could not be installed." >&2; exit 1; }
  local slotctl_src
  slotctl_src="$(readlink -f "$HOME_DIR/current/bin/slotctl" 2>/dev/null || true)"
  [[ -x "$slotctl_src" ]] || { echo "No slotctl in $HOME_DIR/current/bin: deploy a release that builds it first." >&2; exit 1; }

  local n slot names=""
  for n in $(seq 1 "$SLOT_COUNT"); do
    slot="$(slot_name "$n")"
    getent group "$slot" >/dev/null || groupadd "$slot"
    id "$slot" >/dev/null 2>&1 || useradd -M -N -g "$slot" -d "$HOME_DIR/slots/home/$slot" -s /usr/sbin/nologin -c "AgentWorks slot" "$slot"
    id -nG "$PRODUCT" | tr ' ' '\n' | grep -qx "$slot" || usermod -aG "$slot" "$PRODUCT"
    names="${names:+$names, }$slot"
  done

  # Folders the slots live in. The service account owns them; each slot's group gets in.
  install -d -o "$PRODUCT" -g "$PRODUCT" -m 0711 "$HOME_DIR/slots" "$HOME_DIR/slots/home" "$HOME_DIR/slots/state" "$HOME_DIR/slots/run"
  for n in $(seq 1 "$SLOT_COUNT"); do
    slot="$(slot_name "$n")"
    install -d -o "$slot" -g "$slot" -m 0700 "$HOME_DIR/slots/home/$slot"
    install -d -o "$PRODUCT" -g "$slot" -m 2770 "$HOME_DIR/slots/state/$slot" "$HOME_DIR/slots/run/$slot"
  done
  install -d -o "$PRODUCT" -g "$PRODUCT" -m 0700 "$HOME_DIR/slots/run/.sessions"
  # Slots must be able to walk to their own files (they cannot list anything they have no access to):
  # search-only for everyone on the folders above the app and the docs, nothing else.
  local walk dir
  for walk in "$HOME_DIR" "$DOCS"; do
    for dir in "$walk" $(python3 -c 'import os,sys
p=os.path.dirname(sys.argv[1])
while p not in ("/", ""):
    print(p); p=os.path.dirname(p)' "$walk"); do
      [[ -d "$dir" && "$dir" != /srv && "$dir" != /var && "$dir" != /var/lib && "$dir" != /data ]] || continue
      [[ "$(stat -c %a "$dir")" =~ [1357]$ ]] || chmod o+x "$dir"
    done
  done

  # The one program the service account may run as a slot.
  install -d -o root -g root -m 0755 "$(dirname "$LIBEXEC")" "$LIBEXEC"
  install -o root -g root -m 0755 "$slotctl_src" "$LIBEXEC/slotctl"
  install -d -o root -g "$PRODUCT" -m 0750 "$ETC"
  # A product's folder below /etc/agentworks is only reachable if every parent is searchable: /etc/agentworks
  # belongs to the first product's group (excellence's agents, 0750), which would hide another product's folder
  # from its own service ("slot table unavailable: permission denied", Confida 2026-10-01). Search-only for
  # everyone on that one parent; each product's own folder and table stay closed to the others. Always, not only for
  # the other products: running init for the first product itself resets the folder to 0750 (excellence's init on
  # 2026-10-02 took Confida's table away again).
  chmod o+x /etc/agentworks
  local docker_flag
  docker_flag="$(slot_docker_enabled)"
  cat > "$SLOTCTL_CONFIG.new" <<JSON
{
  "slot_prefix": "$SLOT_PREFIX",
  "slot_docker": $docker_flag,
  "allowed_exec": ["$HOME_DIR/releases/*/bin/video-studio-landlock-runner", "/usr/bin/tmux", "/usr/bin/chmod"],
  "allowed_cwd": ["$DOCS", "$HOME_DIR/slots"],
  "slot_run_root": "$HOME_DIR/slots/run",
  "slot_state_root": "$HOME_DIR/slots/state",
  "docs_root": "$DOCS",
  "slot_table": "$TABLE"
}
JSON
  install -o root -g root -m 0644 "$SLOTCTL_CONFIG.new" "$SLOTCTL_CONFIG" && rm -f "$SLOTCTL_CONFIG.new"
  [[ -f "$TABLE" ]] || printf '{"slots":{}}\n' | install -o root -g "$PRODUCT" -m 0640 /dev/stdin "$TABLE"

  cat > "$SUDOERS.new" <<SUDO
# Managed by provision-slots.sh. The service account may run exactly one program as a slot.
Defaults:$PRODUCT !requiretty
Defaults:$PRODUCT env_reset
Defaults:$PRODUCT secure_path="/usr/bin:/bin"
Runas_Alias $ALIAS = $names
$PRODUCT ALL=($ALIAS) NOPASSWD: $LIBEXEC/slotctl exec, $LIBEXEC/slotctl exec --request-file *
SUDO
  visudo -cf "$SUDOERS.new" >/dev/null || { echo "sudoers did not validate; nothing installed." >&2; rm -f "$SUDOERS.new"; exit 1; }
  install -o root -g root -m 0440 "$SUDOERS.new" "$SUDOERS" && rm -f "$SUDOERS.new"

  # Nobody but the service account reads another person's tree, even before it is assigned.
  if [[ -d "$DOCS/_users" ]]; then
    chmod 0711 "$DOCS/_users"
    find "$DOCS/_users" -mindepth 1 -maxdepth 1 -type d -exec chmod o-rwx {} +
  fi
  [[ -d "$DOCS/config" ]] && chmod 0700 "$DOCS/config"
  cmd_shared
  echo "Slots 1..$SLOT_COUNT are in place for $PRODUCT."
  echo "The service account joined the slot groups just now: restart its services (or the user manager) once so they pick that up."
}

# Folders every account shares (workflows, downloads, skills): they belong to the service account, and a slot
# account could not even enter a workflow's folder, so every shell command a workflow ran as a slot failed
# ("fork/exec ...: permission denied", Confida and RTS, 2026-10-01). One group per product holds the service
# account and every slot; the shared folders get that group, group read/write and setgid, so what the service
# writes there stays reachable by the slots and the other way round. Private trees stay closed to other slots.
# SHARED_DIRS (below DOCS) can be overridden. Safe to run again; run it after adding slots.
cmd_shared() {
  local group="${SLOT_PREFIX}shared" n dir
  getent group "$group" >/dev/null || groupadd "$group"
  for n in $(seq 1 "$SLOT_COUNT"); do usermod -aG "$group" "$(slot_name "$n")"; done
  usermod -aG "$group" "$PRODUCT"
  for dir in ${SHARED_DIRS:-Workflow Downloads skills subagents tmp}; do
    [[ -d "$DOCS/$dir" ]] || continue
    chgrp -R "$group" "$DOCS/$dir"
    chmod -R g+rwX "$DOCS/$dir"
    find "$DOCS/$dir" -type d -exec chmod g+s {} +
    echo "shared folder: $DOCS/$dir -> group $group"
  done
  echo "The service account joined $group just now: restart its services (or the user manager) once."
}

# Whether this host gives every slot its own Docker (slot_docker in the slotctl config): "true" or "false".
slot_docker_enabled() {
  python3 - "$SLOTCTL_CONFIG" <<'PY' 2>/dev/null || echo false
import json, sys
try:
    print("true" if json.load(open(sys.argv[1])).get("slot_docker") else "false")
except Exception:
    print("false")
PY
}

alloc_subid() {
  local file="$1" option="$2" account="$3" start
  grep -q "^${account}:" "$file" && return
  start="$(awk -F: 'BEGIN { max=100000; block=65536 } NF == 3 { end=$2+$3; if (end > max) max=end } END { print int((max+block-1)/block)*block }' /etc/subuid /etc/subgid)"
  usermod "$option" "$start-$((start + 65535))" "$account"
}

# A private rootless Docker for one slot: the slot's commands reach it through DOCKER_HOST (set by the platform,
# see slot_docker), so a user can run containers without the platform's own Docker, which their account cannot
# reach, and without seeing or stopping anyone else's containers. Each slot keeps its own images and volumes
# under its home: mind the disk, and the memory (a running daemon is roughly 100-150 MB). Safe to run again.
enable_slot_docker() {
  local slot="$1" uid home env
  uid="$(id -u "$slot")"
  home="$(getent passwd "$slot" | cut -d: -f6)"
  alloc_subid /etc/subuid --add-subuids "$slot"
  alloc_subid /etc/subgid --add-subgids "$slot"
  loginctl enable-linger "$slot"
  systemctl start "user@${uid}.service"
  env="HOME=$home XDG_RUNTIME_DIR=/run/user/$uid DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/$uid/bus"
  runuser -u "$slot" -- env $env systemctl --user daemon-reload
  if ! runuser -u "$slot" -- env $env systemctl --user is-active --quiet docker.service 2>/dev/null; then
    runuser -u "$slot" -- env $env dockerd-rootless-setuptool.sh install >/dev/null
    runuser -u "$slot" -- env $env systemctl --user enable --now docker.service
  fi
  local i
  for i in $(seq 1 30); do [[ -S "/run/user/$uid/docker.sock" ]] && break; sleep 1; done
  [[ -S "/run/user/$uid/docker.sock" ]] || { echo "$slot: its Docker socket did not appear" >&2; return 1; }
  runuser -u "$slot" -- env $env DOCKER_HOST="unix:///run/user/$uid/docker.sock" docker info --format '{{.SecurityOptions}}' | grep -q rootless \
    || { echo "$slot: its Docker is not rootless" >&2; return 1; }
  echo "docker ready: $slot (uid $uid)"
}

cmd_docker() {
  if [[ "$(sysctl -n kernel.apparmor_restrict_unprivileged_userns 2>/dev/null || echo 0)" == 1 && -z "${FORCE_DOCKER:-}" ]]; then
    echo "Unprivileged user namespaces are restricted by AppArmor on this host: rootless Docker needs an exception first (FORCE_DOCKER=1 tries anyway)." >&2
    exit 1
  fi
  command -v dockerd-rootless-setuptool.sh >/dev/null || { echo "docker-ce-rootless-extras (and uidmap) are not installed." >&2; exit 1; }
  [[ -f "$TABLE" ]] || { echo "Run init first." >&2; exit 1; }
  local -a targets=("$@") slot
  if [[ ${#targets[@]} -eq 0 ]]; then
    while IFS= read -r slot; do targets+=("$slot"); done < <(python3 -c 'import json,sys; print("\n".join(sorted(json.load(open(sys.argv[1])).get("slots",{}))))' "$TABLE")
  fi
  for slot in "${targets[@]}"; do
    [[ "$slot" =~ ^${SLOT_PREFIX}[0-9]{2,3}$ ]] || { echo "not a slot name: $slot" >&2; exit 2; }
    enable_slot_docker "$slot"
  done
  python3 - "$SLOTCTL_CONFIG" <<'PY'
import json, os, sys
path = sys.argv[1]
cfg = json.load(open(path))
cfg["slot_docker"] = True
tmp = path + ".new"
json.dump(cfg, open(tmp, "w"), indent=2)
os.chmod(tmp, 0o644)
os.replace(tmp, path)
PY
  echo "slot_docker is on for $PRODUCT: commands run as a slot now use that slot's own Docker."
}

user_tree() { printf '%s/_users/%s' "$DOCS" "$1"; }

cmd_assign() {
  local user_id="${1:-}" slot="${2:-}"
  [[ "$user_id" =~ ^[A-Za-z0-9_.-]+$ ]] || { echo "usage: assign <user-id> [slotNN]" >&2; exit 2; }
  [[ -z "$slot" || "$slot" =~ ^${SLOT_PREFIX}[0-9]{2,3}$ ]] || { echo "usage: assign <user-id> [${SLOT_PREFIX}NN]: $slot is not a slot name" >&2; exit 2; }
  [[ $# -le 2 ]] || { echo "usage: assign <user-id> [slotNN]" >&2; exit 2; }
  [[ -f "$TABLE" ]] || { echo "Run init first." >&2; exit 1; }
  slot="$(python3 - "$TABLE" "$user_id" "$slot" "$SLOT_COUNT" "$SLOT_PREFIX" <<'PY'
import json, os, sys, fcntl
path, user, want, count, prefix = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4]), sys.argv[5]
with open(path + ".lock", "a") as lock:
    fcntl.flock(lock, fcntl.LOCK_EX)
    table = json.load(open(path))
    slots = table.setdefault("slots", {})
    held = next((s for s, u in slots.items() if u == user), None)
    if held:
        print(held); sys.exit(0)
    free = want or next(("%s%02d" % (prefix, n) for n in range(1, count + 1) if "%s%02d" % (prefix, n) not in slots), None)
    if not free:
        sys.exit("no free slot: raise SLOT_COUNT and run init again")
    if free in slots and slots[free] != user:
        sys.exit("%s is held by another user" % free)
    slots[free] = user
    tmp = path + ".new"
    json.dump(table, open(tmp, "w"), indent=2)
    os.chmod(tmp, 0o640)
    os.replace(tmp, path)
    print(free)
PY
)"
  local tree
  tree="$(user_tree "$user_id")"
  getent group "$slot" >/dev/null || { echo "$slot has no account: run init." >&2; exit 1; }
  mkdir -p "$tree"
  chown "$PRODUCT:$slot" "$tree"
  chgrp -R "$slot" "$tree"
  chmod -R g+rwX,o-rwx "$tree"
  find "$tree" -type d -exec chmod g+s {} +
  # The slot's own runtime and state areas are group-owned too (created at init).
  chown root:"$PRODUCT" "$TABLE"; chmod 0640 "$TABLE"
  echo "$user_id -> $slot (tree $tree is group $slot, closed to everyone else)"
  if [[ "$(slot_docker_enabled)" == true ]]; then enable_slot_docker "$slot"; fi
}

# Add a person (an account an administrator provisions) and give them a slot, in one step. Signing in
# never creates an account. The account is created by the server's own `add-user` command with the
# service environment, so it has the same shape as one made in the Users panel.
cmd_adduser() {
  local email="${1:-}" role="${2:-editor}" products="${3:-code}"
  [[ "$email" =~ ^[^[:space:]@]+@[^[:space:]@]+\.[^[:space:]@]+$ ]] || { echo "usage: adduser <email> [admin|creator|editor|viewer] [products]" >&2; exit 2; }
  [[ "$role" =~ ^(admin|creator|editor|viewer)$ ]] || { echo "role must be admin, creator, editor or viewer" >&2; exit 2; }
  [[ "$products" =~ ^[a-z0-9_,-]+$ ]] || { echo "products must be a comma-separated list like code" >&2; exit 2; }
  [[ -x "$HOME_DIR/current/bin/$PRODUCT-agent" ]] || { echo "No $PRODUCT-agent in $HOME_DIR/current/bin." >&2; exit 1; }
  local out id
  out="$(runuser -u "$PRODUCT" -- env HOME="$SERVICE_HOME" bash -c 'set -a; . "$1/.env"; set +a; exec "$1/current/bin/$2-agent" server add-user --email "$3" --role "$4" --products "$5"' _ "$HOME_DIR" "$PRODUCT" "$email" "$role" "$products")"
  printf '%s\n' "$out" | tail -1
  id="$(printf '%s' "$out" | tail -1 | python3 -c 'import json,sys; print(json.loads(sys.stdin.read())["id"])')"
  cmd_assign "$id"
}

cmd_release() {
  local user_id="${1:-}"
  [[ -n "$user_id" ]] || { echo "usage: release <user-id>" >&2; exit 2; }
  python3 - "$TABLE" "$user_id" <<'PY'
import json, os, sys, fcntl
path, user = sys.argv[1], sys.argv[2]
with open(path + ".lock", "a") as lock:
    fcntl.flock(lock, fcntl.LOCK_EX)
    table = json.load(open(path))
    slots = table.get("slots", {})
    for s in [s for s, u in slots.items() if u == user]:
        del slots[s]
    tmp = path + ".new"
    json.dump(table, open(tmp, "w"), indent=2)
    os.chmod(tmp, 0o640)
    os.replace(tmp, path)
PY
  echo "$user_id released (files keep their group; clear them before reusing the slot)"
}

cmd_status() {
  echo "service account: $PRODUCT; slot accounts: $(getent passwd | grep -c "^${SLOT_PREFIX}[0-9]")"
  [[ -f "$TABLE" ]] && python3 -c 'import json,sys; t=json.load(open(sys.argv[1])).get("slots",{}); print("assigned:", len(t)); [print(" ",s,"->",u) for s,u in sorted(t.items())]' "$TABLE"
  ls -l "$LIBEXEC/slotctl" "$SLOTCTL_CONFIG" "$SUDOERS" 2>&1 | sed 's/^/  /'
}

case "${1:-}" in
  init) cmd_init ;;
  shared) cmd_shared ;;
  docker) shift; cmd_docker "$@" ;;
  assign) shift; cmd_assign "$@" ;;
  adduser) shift; cmd_adduser "$@" ;;
  release) shift; cmd_release "$@" ;;
  status) cmd_status ;;
  *) echo "usage: provision-slots.sh init | shared | docker [slotNN ...] | adduser <email> [role] [products] | assign <user-id> [slot] | release <user-id> | status" >&2; exit 2 ;;
esac
