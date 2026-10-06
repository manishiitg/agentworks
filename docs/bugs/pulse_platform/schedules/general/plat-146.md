[← schedules / general](index.md)

# PLAT-146 — a manual launch preserves schedule prose but not a typed safety mode, so a “market close” schedule can execute ordinary entry logic at midday

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P0 |
| Product | schedules |
| Area | general |
| Summary | P0 for schedules capable of trades, payments, publishing, or |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `implemented` — typed close-only mode reaches the script boundary and blocks entries; live fake-broker P0 remains follow-up |
| Last synchronized | `2026-08-19` |

- **Priority:** P0 for schedules capable of trades, payments, publishing, or
  destructive external actions.
- **Owner:** schedule schema/execution context, manual-trigger path, workflow
  route/input binding, and workflow-builder validation.
- **Observed on:** Tectonic USA Day Trading market-close schedule.

## Live evidence

The market-close schedule was manually launched at approximately 11:51 ET,
before the 15:30 entry cutoff. Its message described a past-cutoff
manage/flatten-only operation, but `scripts/place_trades.py` independently reads
wall-clock time. Before 15:30 it follows the ordinary intraday placement path and
can open a new position.

No position was opened in this incident: account snapshots remained at zero
open positions. That makes this a confirmed unsafe reachable path, not a claim
of financial loss.

## Root cause

Critical execution semantics exist only in natural-language schedule text and
an assumed launch time. A cron launch happens near the assumed time; the same
schedule's manual Run button can execute at any time. The runtime passes no
immutable typed value proving the requested operation is close-only, so the
script falls back to the clock and ordinary route behavior.

## Fix reasoning

Schedule configuration must carry typed, immutable execution inputs, for
example:

```text
execution_mode = close_only
route_selections = { trading_mode: close_only }
```

The manual and cron paths must materialize the identical mode in the run
manifest and agent/tool environment. The trading boundary must enforce it:

- reject every new-entry action;
- permit only manage, cancel, flatten, and verify operations;
- fail closed if the required mode is missing or altered;
- record the effective mode in durable run evidence.

This is intentionally stronger than prompt guidance. Safety must be enforced at
the side-effect boundary even if an agent misunderstands the prose.

## Acceptance

- Manually launch the close schedule at midday and prove no entry action can be
  proposed or executed.
- Cron and manual launches persist the same effective execution mode.
- Missing/unknown mode fails before any external action.
- The mode cannot be overridden by a later sequence message.
- A P0 test reaches the real trading command boundary with a fake broker and
  asserts zero entry requests.
