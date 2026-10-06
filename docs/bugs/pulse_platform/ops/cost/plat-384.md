[← platform / cost-telemetry](index.md)

# PLAT-384 — RTS latency workflow cost ledger has damaged unique indexes

| Field | Value |
|---|---|
| State | open |
| Priority | P2 |
| Product | ops |
| Area | cost |
| Summary | RTS indexes backed up, rebuilt and integrity verified. |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | RTS data repaired; corruption origin remains unconfirmed |
| Last synchronized | 2026-10-03 |

**Priority:** P2. Event lookups could omit existing cost records.

## Evidence

During the user-authorized [PLAT-377](plat-377.md) historical actor repair,
`Workflow/rtslatency/costs/costs.sqlite` passed `quick_check` but failed full
`integrity_check` with 100 reported errors, including missing rows in
`sqlite_autoindex_cost_events_1` (event ID) and `_2` (idempotency key).
An exact event lookup returned no row, while `NOT INDEXED` found that same
row. The first attribution pass therefore could not actually update 25
existing records despite finding them during the repair's reads.

The global Providers ledger and the other four workflow ledgers passed full
integrity checks. There is no evidence that this task caused the index damage;
its origin has not been identified. Do not infer a missing record from an
indexed lookup alone when the database's integrity has not been checked.

## One-time repair and verification

- Backed up the live database with SQLite's backup API, including WAL data,
  to `/data/video-studio/docs/_system/cost-index-repair-20261003T144342Z/before.sqlite`.
- Required index-only reported errors and verified no duplicate event IDs or
  idempotency keys through table scans before rebuilding.
- Ran `REINDEX cost_events` inside a write transaction. A SHA-256 fingerprint
  over **every row and column, using NOT INDEXED**, matched before and after.
- Full `integrity_check` then returned `ok`. The attribution fixer could now
  update and verify the 25 records. Final read-back checks every original row
  against the pre-attribution backup; only authorized user IDs differ.
- Reusable operational helper: `scripts/repair_cost_ledger_indexes.py`, dry run
  by default, `--ledger <path> --backup-root <private-directory> --apply` for a
  confirmed index repair. It refuses table damage, duplicate keys and any
  rebuild that changes row contents or fails integrity.
- `repair_cost_user_attribution.py` now requires full integrity and checks the
  affected row count plus actual final actor. `quick_check` alone is insufficient
  to establish that unique index contents agree with the table.

## Remaining work

The historic source of the index damage remains unknown. Investigate if full
integrity checks fail again; no automated recurring repair or service restart
was introduced. This ticket records the unresolved cause separately from the
completed, verified production data repair.

## Register notes

[PLAT-384](plat-384.md), P2, RTS indexes backed up,
rebuilt and integrity verified. Rows and financial values were preserved.
The original source of the index damage remains unconfirmed.
