[← goals / human-decisions](index.md)

# PLAT-207 — `report_human_inputs` consumed_at drain is already complete; correction/closure record

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | goals |
| Area | human-decisions |
| Summary | P4 — no live defect. |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `resolved` — correction/closure record only, no code change |
| Last synchronized | `2026-08-29` |

- **Priority:** P4 — no live defect. The finding's own reopen condition was
  already satisfied in the resolved direction when it was filed.
- **Owner:** N/A — no code change made or needed by this ticket.
- **Related:** `db:report_human_inputs:consumed_at` (confida-login, low),
  the finding this closes. Related to PLAT-092 (the drain mechanism this
  finding measures).

## The finding, already self-resolving

*"Reopen condition satisfied in the resolved direction: both named rows are
now consumed, and a full sweep of report_human_inputs finds zero
answered-but-unconsumed rows."* Filed as a **low-severity, evidence-wait**
finding whose own `next_check` was *"Nothing pending. Re-open only if a
future pass observes a row with answered_at set and consumed_at empty."*

## What the evidence shows

Live query against `db/db.sqlite`'s `report_human_inputs` (per the
finding's own reproduction steps): 18 rows carry `answered_at`, all
`status=consumed` with a `consumed_at` timestamp and an `outcome_summary`.
The only 2 non-terminal rows are `status=dismissed` (never answered, so not
part of the consumed/unconsumed drain question at all). Zero rows in
`status=answered`. This is a pure "the thing being measured already
finished" state — the finding was filed as its own confirmation that
nothing is outstanding, not as a report of a gap.

## Explicitly not done

- No code change — there was nothing to fix. This ticket exists only to
  give the finding a durable, closed PLAT record and remove its row from
  the platform harness table, matching this register's convention of not
  leaving accurate-but-fully-resolved findings sitting open indefinitely.

## Verification

- Direct SQL confirmation of the finding's own evidence, matching its
  reported row counts and statuses exactly.
