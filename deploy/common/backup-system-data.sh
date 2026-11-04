#!/usr/bin/env bash
# Daily backup of the server's own databases (owner, 2026-11-04): chat events, costs, the AgentBridge mailbox,
# schedules, sign-in stores and the Vault audit log. Each live SQLite file is copied with SQLite's online backup
# (safe while the server writes, WAL included), gzip-compressed, into $STATE_ROOT/backups/daily/<date>/, mode 0600.
# Seven days are kept. Run by the <prefix>-system-backup.timer that activate_release installs.
set -euo pipefail
: "${DOCS_ROOT:?}" "${STATE_ROOT:?}"
KEEP_DAYS="${BACKUP_KEEP_DAYS:-7}"
command -v sqlite3 >/dev/null || { echo "system backup: sqlite3 is not installed; nothing backed up" >&2; exit 1; }
umask 077
day="$(date -u +%Y-%m-%d)"
out="$STATE_ROOT/backups/daily/$day"
mkdir -p "$out"
chmod 0700 "$STATE_ROOT/backups" "$STATE_ROOT/backups/daily" "$out"

# The live stores only: one level deep in each folder, so one-off operator and migration copies are not copied again.
failed=0 copied=0
while IFS= read -r src; do
  [[ -n "$src" ]] || continue
  name="$(printf '%s' "${src#/}" | tr '/' '_')"
  tmp="$out/$name.partial"
  if sqlite3 "$src" ".timeout 30000" ".backup '$tmp'" && gzip -f "$tmp"; then
    mv -f "$tmp.gz" "$out/$name.gz"
    copied=$((copied + 1))
  else
    rm -f "$tmp" "$tmp.gz"
    echo "system backup: could not copy $src" >&2
    failed=$((failed + 1))
  fi
done < <(
  for dir in "$STATE_ROOT" "$STATE_ROOT/auth" "$STATE_ROOT/vault" "$DOCS_ROOT/_system" "$DOCS_ROOT/_system/agent_messages"; do
    [[ -d "$dir" ]] && find "$dir" -maxdepth 1 -type f -name '*.sqlite'
  done | sort -u
)
find "$STATE_ROOT/backups/daily" -mindepth 1 -maxdepth 1 -type d -mtime "+$((KEEP_DAYS - 1))" -exec rm -rf {} +
echo "system backup: $day copied=$copied failed=$failed size=$(du -sh "$out" | cut -f1) kept_days=$KEEP_DAYS"
[[ "$failed" -eq 0 ]]
