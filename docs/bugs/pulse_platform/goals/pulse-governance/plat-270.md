[← goals / pulse-governance](index.md)

# PLAT-270 — Protected live SQLite databases made configured backups impossible and wrongly suppressed publish

| Field | Value |
|---|---|
| State | in progress |
| Priority | - |
| Product | goals |
| Area | pulse-governance |
| Summary | backup/data durability, severity high. |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented; runtime reverify pending` |
| Last synchronized | `2026-09-01` |

- **Priority:** backup/data durability, severity high.
- **Origin:** the `rtslatency` scheduled finalizer reported a partial backup
  because `db/db.sqlite` returned `Operation not permitted`; it then skipped
  an otherwise valid report publish. The manifest explicitly declared
  `db-sqlite` coverage, while the same platform correctly forbids agents from
  reading the live SQLite file and WAL sidecars directly.

## Problem

The backup contract told the parent agent to stage a protected runtime file.
That could never work reliably: direct shell/file access is intentionally
blocked, and copying only the main file can omit committed rows still present
in WAL. The finalizer also coupled unrelated operations by stating that it
must not publish after backup failure, leaving public reports stale even when
their own verification and deployment path were healthy.

## Resolution

- Added trusted workspace operation `POST /api/db/backup-snapshot` and the
  no-argument Builder/Pulse tool `create_workflow_database_snapshot`.
- The workspace service resolves only `Workflow/<id>/db/db.sqlite`, creates a
  SQLite-engine snapshot with `VACUUM INTO`, includes committed WAL state,
  runs `PRAGMA integrity_check`, hashes and fsyncs the image, then atomically
  publishes the fixed restore artifact at `backup/database/db.sqlite` plus a
  stable checksum sidecar. Callers cannot choose a destination.
- Backup freshness hashing includes the managed snapshot rather than the
  protected live DB, so DB-only changes cannot be incorrectly skipped as
  already current. Legacy repositories remove live DB/WAL/SHM paths from the
  Git index without deleting them and track the managed snapshot instead.
- Scheduled/manual finalization deterministically creates that image before
  the agent backup when the due backup destination covers `db-sqlite`; the
  agent stages the snapshot and never the live database.
- Backup, Publish, and Notify retain ordered receipts but independent failure
  semantics. A partial/failed backup no longer suppresses a valid publish or
  notification.

## Verification

- Workspace tests cover committed WAL rows, integrity, read-only output,
  atomic replacement on the next snapshot, and arbitrary-path rejection.
- Tool tests cover registry exposure and denial inside workflow step sessions.
- Scheduler contract tests cover trigger/coverage selection and ensure both
  Pulse and ordinary finalizers explicitly decouple Publish from Backup.
