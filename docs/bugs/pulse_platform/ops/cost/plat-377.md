[← ops / cost](index.md)

# PLAT-377 — RTS workshop child costs lose user attribution

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | ops |
| Area | cost |
| Summary | fixed on main for new workshop child runs; deployment pending. |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | Fixed on main for future runs; RTS historical repair complete where verified; deployment pending |
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
Pulse calls have a user ID; their child rows do not. The initial investigation was read-only. The user subsequently authorized
the historical production data repair recorded below.
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

## Authorized one-time RTS repair — 2026-10-03

Ran `scripts/repair_cost_user_attribution.py` through AWS SSM, first dry run,
then apply. This script matches exact saved parent session / child execution
IDs, checks conflicting actors, and understands the historical
`workshop-workflow-full-<token>` correlation ID. Current ownership, timestamps
and prompt text are never used to invent attribution. The legacy `default`
actor is not silently mapped to today's admin.

- **753 global rows** received verified actors, **$250.51671575** in recorded
  cost (720 rows in the first pass, then 33 zero-cost tool rows whose exact
  parent identities became resolvable).
- The reported September 4–October 3 UTC **$242.348381** bucket is fully
  attributed. Final read-back: **$0 unattributed and 0 unattributed LLM calls**
  in that window. 356 zero-cost tool rows still have no durable actor evidence.
- Corresponding existing records in all five workflow SQLite ledgers were
  reconciled by exact global event ID. No missing workflow cost rows were
  invented or copied into the ledgers.
- Full original-row comparison against the backups verifies every column
  except `user_id` is unchanged, including prices, input/output/cache tokens,
  event identities and runtime links. All six live ledgers pass full SQLite
  `integrity_check`; a final dry run proposes zero additional updates.
- Read-back caught pre-existing damaged unique indexes in the rtslatency
  workflow ledger. Those were backed up and rebuilt without altering row
  data before finishing its attribution repair: [PLAT-384](plat-384.md).
- Global audit plans, SQLite backup images and final verification are in
  `/data/video-studio/docs/_system/cost-attribution-repair-20261003T143216860536Z`.
  Follow-up reconciliation backups are in the sibling directories ending
  `20261003T143821506702Z` and `20261003T144526819626Z`. These directories are
  operator-private; they contain only stored ledger data and evidence paths,
  not copied conversation text.
- **29 older Code rows, $24.9805032**, remain unattributed: rr1/rr2 under the
  legacy `_users/default/Chats/Video Studio/projects/` tree. No exact actor
  evidence was found, so these were left unchanged.
- Services, schedules and application releases were not changed or restarted.

The script defaults to a dry run. With `--apply`, it makes WAL-consistent
SQLite backups, takes a write transaction per ledger, requires full integrity,
checks each UPDATE changed exactly one row, verifies the final actor and all
non-actor fields, and records the plan and result. Exact global event identity
also makes reruns reconcile a workflow copy after a partial multi-ledger run.
Five regression tests cover ambiguity, legacy actors, parent identity checks,
zero-workspace tool ambiguity, backups, financial preservation and idempotence.

## Remaining work

- Deploy and verify a fresh RTS workshop step/background task has the launch
  user in Providers Costs.
- The 29 legacy Code rows and 356 zero-cost tool rows still need original
  actor evidence if further historical attribution is required. Deployment of
  the launch-context fix is still needed to prevent new missing actors.

## Register notes

[PLAT-377](plat-377.md), P2, fixed on main for
new workshop child runs; deployment pending. Authorized RTS historical repair
verified 753 rows ($250.52), including the entire reported $242.35 bucket.
Older legacy Code costs ($24.98) lack actor evidence and stay unattributed.
Detached workshop sessions discarded the launch user and channel, so child
steps/reviewers had workflow attribution but no user attribution.
