[← goals / human-decisions](index.md)

# PLAT-208 — `report_human_inputs` operator-identity gap is already closed by a shipped platform improvement, for every row going forward

| Field | Value |
|---|---|
| State | closed |
| Priority | - |
| Product | goals |
| Area | human-decisions |
| Summary | P4 — no live defect requiring further work. |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `resolved` — informational closure record, no code change |
| Last synchronized | `2026-08-29` |

- **Priority:** P4 — no live defect requiring further work. The finding
  itself documents that the platform improvement it depends on has already
  shipped, and explicitly states there is no workflow-side action available.
- **Owner:** N/A — `answered_by`/`answered_by_kind`/`answered_session_id`
  are populated by the platform's own human-input-answering path, not by
  any workflow tool.
- **Related:** `db:report_human_inputs:operator-identity` (confida-login,
  low), the finding this closes. `PUL-07B2C9EC` (referenced by the finding
  as a human-deference question that depends on this attribution) is a
  separate workflow-local concern, out of scope here.

## The finding, already carrying its own resolution

*"Materially changed by a platform improvement: recent decisions now carry
`human_ui` provenance and a real session id, so the original claim that no
approval in this table can be proven human is no longer true for anything
answered since 2026-08-12."*

Seven rows answered `2026-08-12` or later carry
`answered_by_kind='human_ui'`, `answered_via='report_ui'`, and a concrete
`answered_session_id`. Only rows answered **before** that date remain
`answered_by_kind='legacy_unattributed'` with an empty session id — a
permanent historical gap for data that predates the provenance-tracking
improvement, not a live, ongoing one.

## Disposition

The finding's own `next_check` already states the correct action: *"No
workflow change available — `answered_by` is populated by the platform,
not by this workflow. Treat pre-2026-08-12 rows as unattributable and stop
citing them as proof of human approval; reopen only if a NEW row appears
with `answered_by_kind='legacy_unattributed'`."* That is a durable,
self-contained closure condition — this ticket exists to give it a PLAT
record and clear the harness row, not to add new work.

## Explicitly not done

- No code change — the provenance-tracking improvement (`human_ui`
  attribution with a real session id) is already live for every row
  answered since 2026-08-12; there is nothing further to build.
- Did not retroactively attribute the pre-2026-08-12
  `legacy_unattributed` rows — the finding is explicit that this is
  impossible by construction (the identity signal was never captured at
  answer time for those rows).

## Verification

- Confirmed via the finding's own SQL reproduction against `db/db.sqlite`'s
  `report_human_inputs`: 7 rows since 2026-08-12 carry `human_ui`
  provenance with a real session id; older rows are
  `legacy_unattributed` by design, not by defect.
