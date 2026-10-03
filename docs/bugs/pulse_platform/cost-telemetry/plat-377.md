[← Pulse platform index](../../pulse_platform_issue_register.md)

# PLAT-377 — RTS workshop child costs lose user attribution

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | Fixed on main for future runs; deployment and historical repair pending |
| Last synchronized | 2026-10-03 |

**Priority:** P2. Costs are recorded but the user breakdown is misleading.

## Evidence

Read-only AWS SSM queries of RTS `/data/video-studio/docs/_system/costs.sqlite`
found 1,092 rows with an empty user ID, totaling **$242.348381** from
September 4 through October 3 UTC. 516 LLM rows carry real workflow identities;
576 tool rows have zero recorded cost. This matches the reported rounded $242.

| Workflow | Execution and Pulse cost with missing user |
|---|---:|
| rtslatency | $124.48491675 |
| rtsaws | $38.50848475 |
| rtssprinttracking | $34.61227625 |
| automationtesting | $34.51353875 |
| rtsprreviweer | $10.22916450 |

Latest missing-user entries on October 3 are live workshop message-sequence
steps and background tasks, not only imported legacy rows. Parent scheduled
Pulse calls have a user ID; their child rows do not. No production records,
credentials, schedules or services were changed during this investigation.
The browser was signed out; the diagnosis is from the server ledger, not a
verified screenshot of the user's current date filter.

## Cause and fix

`NewWorkshopChatSession` built its session context from `context.Background()`.
`newExecContext` derived every workshop child from that context and copied only
execution-parent identity. `attachCostObserver` reads `common.UserIDKey`, so
it recorded an empty actor for those children. Providers correctly labels the
empty actor as Unattributed even though workflow attribution exists.

The server now puts its authorized user and source channel in WorkshopConfig.
The detached session retains these; typed context identity is a fallback for
non-server callers. Request cancellation still does not stop background work;
session Close still stops children and refuses later launches. No owner-based
identity is invented and no old cost amounts are changed.

## Verification

- Regression tests check child actor/channel propagation, request versus
  session cancellation, rejection after stop and unknown-identity fallback.
- Existing orchestrator observer tests verify user-bearing child context is
  written to the ledger.

## Remaining work

- Deploy and verify a fresh RTS workshop step/background task has the launch
  user in Providers Costs.
- Historical rows remain Unattributed. Repair only where durable launch/session
  evidence unambiguously identifies the actor; changing current owner labels
  or bulk assigning to an admin would misrepresent historical usage.
