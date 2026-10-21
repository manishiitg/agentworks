#!/usr/bin/env python3
"""One-off cleanup of retired Pulse tables in canonical Crew databases.

Preview with --docs. Apply with --apply --backup-dir outside the documents
root. Each changed database is backed up through SQLite before any drop.
Nonempty tables are retained and reported, except Pulse's migration ledger.
Goals and other product directories are never scanned. Retire this tool after
the last installation has been cleaned.
"""

import argparse
from contextlib import closing
import datetime
import json
from pathlib import Path
import sqlite3


def quote(name):
    return '"' + name.replace('"', '""') + '"'


def tables(db):
    return [row[0] for row in db.execute(
        "SELECT name FROM sqlite_master WHERE type='table' ORDER BY name")]


def cleanup(docs, backup_dir=None):
    docs = docs.resolve()
    if backup_dir is not None:
        backup_dir = backup_dir.resolve()
        if backup_dir == docs or docs in backup_dir.parents:
            raise ValueError("Keep backups outside the documents root")
        backup_dir.mkdir(mode=0o700, parents=True, exist_ok=False)
    result = {"checked_at_utc": datetime.datetime.now(datetime.timezone.utc).isoformat(),
              "apply": backup_dir is not None, "databases": [], "errors": []}
    for manifest in sorted((docs / "Crew").glob("*/product.json")):
        path = manifest.parent / "db/db.sqlite"
        if not path.is_file():
            continue
        try:
            if manifest.parent.is_symlink() or not path.resolve().is_relative_to(docs / "Crew"):
                raise ValueError("Crew database is outside its canonical storage")
            if json.loads(manifest.read_text()).get("product") != "work":
                raise ValueError("Not a Crew manifest")
            relative = path.relative_to(docs).as_posix()
            with closing(sqlite3.connect(path.as_uri() + "?mode=ro", uri=True)) as db:
                db.execute("PRAGMA query_only=ON")
                before = tables(db)
                counts = {name: db.execute("SELECT count(*) FROM " + quote(name)).fetchone()[0]
                          for name in before if name.startswith("pulse_")}
            drop = [name for name, count in counts.items()
                    if count == 0 or name == "pulse_schema_migrations"]
            record = {"path": relative, "tables_before": before, "pulse_rows": counts,
                      "drop": drop, "retained_nonempty": {
                          name: count for name, count in counts.items() if name not in drop}}
            if backup_dir is not None and drop:
                backup = backup_dir / relative
                backup.parent.mkdir(mode=0o700, parents=True)
                with closing(sqlite3.connect(path.as_uri() + "?mode=rw", uri=True)) as db:
                    db.execute("PRAGMA busy_timeout=5000")
                    with closing(sqlite3.connect(backup)) as saved:
                        db.backup(saved)
                    backup.chmod(0o600)
                    # Recheck emptiness in the transaction that removes the schema.
                    with db:
                        db.execute("BEGIN IMMEDIATE")
                        for name in drop:
                            count = db.execute("SELECT count(*) FROM " + quote(name)).fetchone()[0]
                            if count and name != "pulse_schema_migrations":
                                record["retained_nonempty"][name] = count
                                continue
                            db.execute("DROP TABLE " + quote(name))
                    record["tables_after"] = tables(db)
                    record["dropped"] = [name for name in drop if name not in record["tables_after"]]
                    record["backup"] = str(backup)
            result["databases"].append(record)
        except (ValueError, OSError, sqlite3.Error) as error:
            result["errors"].append({"path": str(path.relative_to(docs)), "error": str(error)})
    result["database_count"] = len(result["databases"])
    result["affected_count"] = sum(bool(row["drop"]) for row in result["databases"])
    result["dropped_count"] = sum(len(row.get("dropped", [])) for row in result["databases"])
    if backup_dir is not None:
        (backup_dir / "cleanup.json").write_text(json.dumps(result, indent=2) + "\n")
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--docs", type=Path, required=True)
    parser.add_argument("--apply", action="store_true")
    parser.add_argument("--backup-dir", type=Path)
    args = parser.parse_args()
    if args.apply and args.backup_dir is None:
        parser.error("--apply requires --backup-dir")
    if not (args.docs / "Crew").is_dir():
        parser.error("--docs must contain the Crew directory")
    result = cleanup(args.docs, args.backup_dir if args.apply else None)
    print(json.dumps(result, indent=2))
    return 1 if result["errors"] else 0


if __name__ == "__main__":
    raise SystemExit(main())
