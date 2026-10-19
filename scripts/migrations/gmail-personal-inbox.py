#!/usr/bin/env python3
"""One-off Gmail inbox schema migration. Run before starting the new server.

Old product addresses stop receiving work. Their target IDs, optional conditions,
activity and conversations remain; choose a personal inbox and reconfigure each
binding through Builder, then approve its saved configuration in the website.
"""
import argparse
import datetime
import os
import sqlite3

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("database", help="Private gmail-inbound/email.db file")
args = parser.parse_args()
if not os.path.isfile(args.database):
    parser.error("The inbox database does not exist; a new installation needs no migration.")
db = sqlite3.connect(args.database, timeout=30)
schema = db.execute("SELECT sql FROM sqlite_master WHERE name='routes' AND type='table'").fetchone()
if not schema or "address TEXT NOT NULL UNIQUE" not in schema[0]:
    raise SystemExit("The product-address schema is not present; no migration performed.")
stamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%SZ")
backup = args.database + ".before-personal-inbox-" + stamp
fd = os.open(backup, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
os.close(fd)
with sqlite3.connect(backup) as saved:
    db.backup(saved)
try:
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
    raise
finally:
    db.close()
print("Migrated. Private backup:", backup)
print("In My Vault choose a Gmail inbox. Ask Builder to reconnect each chosen trigger, then review and approve it in chat or Incoming email.")
