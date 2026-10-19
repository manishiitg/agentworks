#!/usr/bin/env python3
"""One-off Gmail inbox schema migration. Run before starting the new server.

Old product addresses stop receiving work. Their target IDs, optional conditions,
activity and conversations remain; choose a personal inbox and reconfigure each
binding through Builder, then approve its saved configuration in the website.
"""
import argparse
import datetime
import os
import json
from pathlib import Path
import sqlite3

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("database", help="Private gmail-inbound/email.db file")
parser.add_argument("--workspace-root", required=True, help="Filesystem root of workspace documents")
args = parser.parse_args()
if not os.path.isfile(args.database):
    print("No Gmail inbox database; nothing to migrate.")
    raise SystemExit(0)
db = sqlite3.connect(args.database, timeout=30)
schema = db.execute("SELECT sql FROM sqlite_master WHERE name='routes' AND type='table'").fetchone()
if not schema:
    db.close()
    print("No Gmail routes table; nothing to migrate.")
    raise SystemExit(0)
columns = {row[1] for row in db.execute("PRAGMA table_info(routes)")}
if not {"id", "owner", "workspace", "connection", "address", "data", "enabled"} <= columns:
    raise SystemExit("Unrecognized Gmail routes schema; migration stopped.")
# Detect the retired constraint by index columns, independent of SQL formatting.
unique_address = False
for index in db.execute("PRAGMA index_list(routes)").fetchall():
    if index[2]:
        name = index[1].replace('"', '""')
        if [row[2] for row in db.execute(f'PRAGMA index_info("{name}")')] == ["address"]:
            unique_address = True
if not unique_address:
    db.close()
    print("Gmail personal inbox schema already current; nothing to migrate.")
    raise SystemExit(0)
# An empty old store still needs its unique-address constraint removed so its
# first future personal inbox can be bound to more than one product.
root = Path(args.workspace_root).resolve(strict=True)
# Resolve and inspect before making any change. Old Gmail schedule entries are
# removed, because their public address field is no longer a valid binding.
# Private route IDs/history remain; Builder saves each chosen binding afresh.
workflow_ids = {}
for route_id, workspace, raw in db.execute("SELECT id,workspace,data FROM routes"):
    route = json.loads(raw)
    if route.get("workflow_trigger"):
        target = (root / workspace / "workflow.json").resolve()
        if not target.is_relative_to(root):
            raise SystemExit("A workflow path is outside the workspace root; no migration performed.")
        if target.is_file():
            workflow_ids.setdefault(target, set()).add(route_id)
changes = []
for target, route_ids in workflow_ids.items():
    original = target.read_bytes()
    manifest = json.loads(original)
    kept = [schedule for schedule in manifest.get("schedules", []) if not (schedule.get("kind") == "gmail" and schedule.get("id") in route_ids)]
    if len(kept) != len(manifest.get("schedules", [])):
        manifest["schedules"] = kept
        changes.append((target, original, (json.dumps(manifest, indent=2) + "\n").encode()))
stamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%SZ")
backup = args.database + ".before-personal-inbox-" + stamp
fd = os.open(backup, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
os.close(fd)
with sqlite3.connect(backup) as saved:
    db.backup(saved)
backup_root = Path(backup + ".workflows")
if changes:
    backup_root.mkdir(mode=0o700)
for target, original, _ in changes:
    saved = backup_root / target.relative_to(root)
    saved.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    fd = os.open(saved, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    with os.fdopen(fd, "wb") as handle:
        handle.write(original)
try:
    for target, _, updated in changes:
        target.write_bytes(updated)
    db.executescript("""
BEGIN IMMEDIATE;
ALTER TABLE routes RENAME TO product_address_routes;
CREATE TABLE routes(id TEXT PRIMARY KEY,owner TEXT NOT NULL,workspace TEXT NOT NULL,connection TEXT NOT NULL,address TEXT NOT NULL,data TEXT NOT NULL,enabled INTEGER NOT NULL,UNIQUE(owner,workspace));
INSERT INTO routes SELECT id,owner,workspace,connection,address,json_set(data,'$.enabled',json('false')),0 FROM product_address_routes;
DROP TABLE product_address_routes;
CREATE TABLE IF NOT EXISTS inboxes(id TEXT PRIMARY KEY,owner TEXT NOT NULL UNIQUE,connection TEXT NOT NULL,address TEXT NOT NULL UNIQUE);
DELETE FROM sender_consents;
UPDATE deliveries SET status='rejected',error='Personal inbox setup requires a new address and owner approval' WHERE status IN ('staged','pending','reply');
COMMIT;
""")
except BaseException:
    db.rollback()
    for target, original, _ in changes:
        target.write_bytes(original)
    raise
finally:
    db.close()
print("Migrated. Private SQLite backup:", backup)
print("Removed old Gmail schedule entries from", len(changes), "workflow manifests; private copies are beside the database backup.")
print("In My Vault choose a Gmail inbox. Ask Builder to reconnect each chosen trigger, then review and approve it in chat or Incoming email.")
