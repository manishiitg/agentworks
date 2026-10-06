[← goals / learnings](index.md)

# PLAT-076 — learning and scripted metadata record claims instead of runtime facts

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | goals |
| Area | learnings |
| Summary | P1 — incorrect provenance can drive locking, maturity and |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented` — runtime reverify pending |
| Last synchronized | `2026-08-10` |

- **Priority:** P1 — incorrect provenance can drive locking, maturity and
  optimization decisions
- **Owner:** learning detection and scripted metadata persistence
- **Findings:** build-in-public `03f6ed727fb67c9a`,
  `9d1c0fe414871e37`, `10eb995c50fb52c0`

## Evidence and root causes

The full workflow-local records and retained artifacts reproduced four defects:

1. `step-route-selector/script_metadata.json` recorded four successful
   `scripted_fast_path` executions under `successful_runs.agentic`. The writer
   literally incremented the `"agentic"` key for every validated saved-script
   execution.
2. Seven of nine `.learning_metadata.json` files retained creation-time
   `step_path` values after plan reordering. Both `step-route-selector` and
   `step-reddit-scan-draft` claimed `step-1`. The writer only assigned identity
   when creating or recovering an unreadable file.
3. `has_new_learning` was inferred from phrases such as “files changed” in the
   agent's response. A KB/DB-only change could therefore be recorded as a
   learning change even when the managed learning tree was byte-identical.
4. `script_version` and `relearn_count` increased every time execution code was
   copied back, including when `main.py` was byte-identical. The copy routine
   already held both versions but incremented before comparing them.

## Fix

- Normalize saved-script success provenance to the single `"scripted"` key.
  Existing mirrored legacy keys use `total_runs - failed_runs`, avoiding double
  counting.
- Refresh `step_id` and the current positional `step_path` on every learning
  metadata write.
- Derive `has_new_learning` from before/after hashes of the complete managed
  learning artifact tree for normal reflection, recovered reflection and
  message-sequence learning turns. Agent prose is no longer evidence of a
  write.
- Increment script version/relearn count only when the saved script artifact
  bytes actually differ, including added or removed helper files.

Regression tests use the captured build-in-public metadata shapes and cover
legacy mirrored counters, stale identity, identical scripts and real artifact
changes.

## Verification

- Focused regression tests: pass.
- `go build ./...`: pass.
- Required broad suite: exactly 22 known baseline failures; no additional
  failures.
- Runtime reverify: pending server restart and a scripted plus learning-writing
  run.

Ready to close after runtime verification: `03f6ed727fb67c9a`,
`9d1c0fe414871e37`, `10eb995c50fb52c0`.

Social-media `dd2a48047c4d7993` required no second fix: PLAT-061 already removed
the dead-field shape, and the current v1.0.22 workflow no longer contains
`global_skill_objective`. It is ready to close as covered by PLAT-061 after the
normal post-restart check.

