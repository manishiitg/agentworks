#!/usr/bin/env bash
# Per-user Linux accounts ("slots") for a rootless product host. Run as root ON the host:
#
#   ssh -p 2299 root@<host> 'PRODUCT=agents bash -s -- init'            < deploy/rootless-linux/provision-slots.sh
#   ssh -p 2299 root@<host> 'PRODUCT=agents bash -s -- assign <user-id>' < deploy/rootless-linux/provision-slots.sh
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

PRODUCT="${PRODUCT:-agents}"
HOME_DIR="/srv/$PRODUCT"
DOCS="$HOME_DIR/data/docs"
SLOT_COUNT="${SLOT_COUNT:-50}"
LIBEXEC=/usr/local/libexec/agentworks
ETC=/etc/agentworks
TABLE="$ETC/slots.json"
SLOTCTL_CONFIG="$LIBEXEC/slotctl.json"
SUDOERS=/etc/sudoers.d/agentworks-slots

[[ $EUID -eq 0 ]] || { echo "Run as root." >&2; exit 1; }
id "$PRODUCT" >/dev/null || { echo "Account $PRODUCT is missing." >&2; exit 1; }

slot_name() { printf 'slot%02d' "$1"; }

cmd_init() {
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
  # Slots must be able to walk to their own files (they cannot list anything they have no access to).
  chmod 0751 "$HOME_DIR"

  # The one program the service account may run as a slot.
  install -d -o root -g root -m 0755 "$LIBEXEC"
  install -o root -g root -m 0755 "$slotctl_src" "$LIBEXEC/slotctl"
  install -d -o root -g "$PRODUCT" -m 0750 "$ETC"
  cat > "$SLOTCTL_CONFIG.new" <<JSON
{
  "allowed_exec": ["$HOME_DIR/releases/*/bin/video-studio-landlock-runner", "/usr/bin/tmux"],
  "allowed_cwd": ["$HOME_DIR/data/docs", "$HOME_DIR/slots"],
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
Runas_Alias AGENTWORKS_SLOTS = $names
$PRODUCT ALL=(AGENTWORKS_SLOTS) NOPASSWD: $LIBEXEC/slotctl exec, $LIBEXEC/slotctl exec --request-file *
SUDO
  visudo -cf "$SUDOERS.new" >/dev/null || { echo "sudoers did not validate; nothing installed." >&2; rm -f "$SUDOERS.new"; exit 1; }
  install -o root -g root -m 0440 "$SUDOERS.new" "$SUDOERS" && rm -f "$SUDOERS.new"

  # Nobody but the service account reads another person's tree, even before it is assigned.
  if [[ -d "$DOCS/_users" ]]; then
    chmod 0711 "$DOCS/_users"
    find "$DOCS/_users" -mindepth 1 -maxdepth 1 -type d -exec chmod o-rwx {} +
  fi
  [[ -d "$DOCS/config" ]] && chmod 0700 "$DOCS/config"
  echo "Slots 1..$SLOT_COUNT are in place for $PRODUCT."
  echo "The service account joined the slot groups just now: restart its services (or the user manager) once so they pick that up."
}

user_tree() { printf '%s/_users/%s' "$DOCS" "$1"; }

cmd_assign() {
  local user_id="${1:-}" slot="${2:-}"
  [[ "$user_id" =~ ^[A-Za-z0-9_.-]+$ ]] || { echo "usage: assign <user-id> [slotNN]" >&2; exit 2; }
  [[ -z "$slot" || "$slot" =~ ^slot[0-9]{2,3}$ ]] || { echo "usage: assign <user-id> [slotNN]: $slot is not a slot name" >&2; exit 2; }
  [[ $# -le 2 ]] || { echo "usage: assign <user-id> [slotNN]" >&2; exit 2; }
  [[ -f "$TABLE" ]] || { echo "Run init first." >&2; exit 1; }
  slot="$(python3 - "$TABLE" "$user_id" "$slot" "$SLOT_COUNT" <<'PY'
import json, os, sys, fcntl
path, user, want, count = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4])
with open(path + ".lock", "a") as lock:
    fcntl.flock(lock, fcntl.LOCK_EX)
    table = json.load(open(path))
    slots = table.setdefault("slots", {})
    held = next((s for s, u in slots.items() if u == user), None)
    if held:
        print(held); sys.exit(0)
    free = want or next(("slot%02d" % n for n in range(1, count + 1) if "slot%02d" % n not in slots), None)
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
  echo "service account: $PRODUCT; slot accounts: $(getent passwd | grep -c '^slot[0-9]')"
  [[ -f "$TABLE" ]] && python3 -c 'import json,sys; t=json.load(open(sys.argv[1])).get("slots",{}); print("assigned:", len(t)); [print(" ",s,"->",u) for s,u in sorted(t.items())]' "$TABLE"
  ls -l "$LIBEXEC/slotctl" "$SLOTCTL_CONFIG" "$SUDOERS" 2>&1 | sed 's/^/  /'
}

case "${1:-}" in
  init) cmd_init ;;
  assign) shift; cmd_assign "$@" ;;
  release) shift; cmd_release "$@" ;;
  status) cmd_status ;;
  *) echo "usage: provision-slots.sh init | assign <user-id> [slot] | release <user-id> | status" >&2; exit 2 ;;
esac
