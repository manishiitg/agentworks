[← ops / cost](index.md)

# PLAT-184 — Pulse and the Workflow Builder cannot access the cost ledger at all, including everything PLAT-166/167 added

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | ops |
| Area | cost |
| Summary | P1 — Pulse's own guidance (`pulse-gate.md`) assigns it a |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `fixed` — per-workspace ledger shipped (`5552fbe71`, `main`); see "Fix implemented" below for what's covered and what's deliberately still open |
| Last synchronized | `2026-08-23` |

- **Priority:** P1 — Pulse's own guidance (`pulse-gate.md`) assigns it a
  `model_cost_fitness` review lens ("model/tier/reasoning/fallback choices,
  quality-cost fit, and cost attribution"), and PLAT-090 records the
  operator's own request ("we should measure pulse time, cost..."). Neither
  is actually possible today: Pulse cannot read the data it's supposed to
  reason about.
- **Owner:** cost ledger storage (`pkg/costledger`), server startup
  (`cmd/server/server.go:1551-1560`), cost observer attribution
  (`pkg/costobserver`).
- **Related:** [PLAT-166](plat-166.md)/[PLAT-167](plat-167.md) (added the
  phase/item-level cost breakdown this ticket's data is entirely invisible
  for — see below), [PLAT-090](plat-090.md) (the standing "measure Pulse
  cost" requirement this blocks), [PLAT-088](plat-088.md) (scope
  attribution — confirmed unrelated to this ticket, see Non-goals).

## Symptom / how this was found

While reviewing whether PLAT-166/167's cost-tracking work actually satisfied
its own requirements, a user asked directly: can the Workflow Builder agent
read `_system/costs.sqlite`? Traced through folder-guard scoping and
confirmed: **no**. Every workflow step's (and even the broadest
"generic agent"/Pulse-specialist) read scope is bounded by
`baseWorkspacePath = hcpo.GetWorkspacePath()`
(`pkg/orchestrator/agents/workflow/step_based_workflow/controller_agent_factory.go:388,433,439`),
which resolves to that workflow's own folder (e.g. `Workflow/social-media`).
`_system/` is a sibling of `Workflow/`, not a descendant, so it sits outside
every possible grant regardless of role. `execute_shell_command` enforces
the same boundary at the OS level via Landlock, so there's no route around
it through shell either.

**No tool exists to compensate for this.** Unlike Pulse's own findings/state
(`_system/pulse-platform.sqlite`), which is mediated through a family of
dedicated tools (`record_pulse_review_focus` and siblings,
`cmd/server/pulse_worklist.go:2024` →
`pkg/orchestrator/agents/workflow/step_based_workflow/pulse_finding_details.go`),
grepping the entire codebase for any `get_costs`/`query_costs`/similar
tool registration found nothing. The cost ledger has zero agent-facing
surface — not even an indirect one.

**Consequence for PLAT-166/167 specifically:** those tickets added phase
(`reflection`) and message-sequence item-level (`item:<id>`) cost
attribution to `pkg/costledger` — the exact hierarchy (`↳ execute and
verify`, `↳ ... reflection`, `↳ automatic final validation repair N`) a
human sees in the Cost Analysis popup. None of that new granularity exists
in the older, file-based cost system
(`costs/execution/`, `costs/phase/`, read via
`pkg/orchestrator/base_orchestrator_tokens.go`,
`context_aware_bridge.go`) that agents *can* read — PLAT-167's own work is
entirely invisible to Pulse and the Workflow Builder, not just historically
but by construction, since it only ever wrote to the SQLite ledger.

## Why the ledger is a single global file, and what that costs

`costDBPath := filepath.Join(fsutil.WorkspaceDocsRoot(), "_system",
"costs.sqlite")` (`server.go:1551`) is opened once at server startup and
published process-wide via `costledger.SetDefaultLedger` (`server.go:1557`).
Every workflow's cost events land in one shared `cost_events` table, keyed
by a `workflow_id` column. This is the *only* reason the data is
unreachable by any agent — the older file-based system already lives inside
each workflow's own folder and is readable exactly because of that.

## Migration scoping (investigated, not a design decision)

A full writer/reader catalog was done against the live ledger
(`workspace-docs/_system/costs.sqlite`, 23MB, 28,276 rows) to assess whether
moving to one SQLite file per workspace (e.g.
`Workflow/<name>/costs/costs.sqlite`) is feasible:

**Writers** — mostly already workspace-scoped:
- `pkg/orchestrator/base_orchestrator_cost.go:48,73` (`WithAttribution(...,
  bo.GetWorkspacePath(), ...)`) — every workflow-step/background-agent
  attach point already carries the workspace path. Clean migration target.
- `cmd/server/virtual-tools/tool_costs.go:127-238` (priced media tools) —
  already dual-writes: the global ledger row, *and* a per-workflow JSON
  file, when `inferWorkflowCostTarget` can resolve a `Workflow/<name>/...`
  segment from the tool's own output path.
- `cmd/server/server.go:5745-5751` (the `/api/query` chat/builder path) —
  genuinely ambiguous by design: `req.SelectedFolder` is legitimately empty
  for plain chat or a workflow-builder session before any workflow folder
  is chosen. This writer needs a real decision (a separate landing place
  for workspace-less events), not a mechanical fix.

**Readers** — all but one already workflow-scoped:
- `cmd/server/workflow_review_data.go:127,198`, `cmd/server/scheduler.go:2378`,
  `pkg/orchestrator/agents/workflow/step_based_workflow/pulse_agent_metrics.go:112`
  — all single-workflow already, or trivially made so (the metrics one has
  `workspacePath` in scope at the call site, just doesn't pass it down yet).
- `cmd/server/cost_routes.go:41` (`GET /api/cost/summary`, backs
  `frontend/src/components/CostDashboard.tsx`) is the one genuinely
  cross-workflow reader — **but `CostDashboard` is not imported or rendered
  anywhere else in the frontend** (confirmed: `grep -rl "CostDashboard"
  frontend/src/` returns only its own file). This looks like dead/unmounted
  UI, not a live constraint on the migration.

**The empty per-workflow `costs.sqlite` red herring, resolved:**
`Workflow/social-media/costs/costs.sqlite` (0 bytes) is the *only* file of
its shape anywhere under any workflow folder, and nothing in the codebase
references that path pattern outside test tempdirs. It is not a half-built
migration attempt — no TODO, no scaffold, no evidence of intent. It's a
stray artifact, most likely a one-off `os.Create`-then-never-written side
effect from something unrelated; safe to delete whenever convenient, not
meaningful to this ticket's design.

**The apparent "54% of the ledger is unattributed" finding, resolved as a
non-issue:** an initial pass found 15,299 of 28,276 rows (54%) have
`workflow_id=''`. Investigated and confirmed **not a live defect**: 15,235
of those rows have `scope='unknown'` and every single one has
`event_id LIKE 'legacy-%'` — they are a one-time historical import of the
pre-SQLite JSONL cost log
(`server.go:1560`, `costledger.MigrateLegacyJSONL(legacyCostPath)`, runs
on every startup but is idempotent via
`idempotency_key = "legacy-jsonl:" + sha256(line)`,
`pkg/costledger/sqlite.go:443,461-477`). Confirmed via direct query: every
one of these rows is timestamped `2026-05-03` through `2026-07-12`, with
**zero** rows since — over a month before this investigation. Already
independently documented: commit `e74710d0d`'s message states this
verbatim, and PLAT-090 (`plat-090.md:111-116`) cites the identical 15,235
figure and already excludes it from live trend analysis. Once this dead
bucket is excluded, live cost data is essentially fully workflow-attributed
already — this finding, once resolved, makes the migration case *stronger*,
not weaker.

## Two fix directions (neither implemented)

Not mutually exclusive, and meaningfully different in cost/risk:

1. **Migrate to one SQLite ledger per workspace.** Matches the existing
   file-based system's pattern, gives every agent normal folder-guard read
   access with zero new tool surface, and per the scoping above is mostly
   mechanical for the parts that matter — but is a real data migration
   (splitting/rewriting existing rows by `workflow_id`, deciding where
   workspace-less events land, handling the schema init path per file
   rather than once).
2. **Build a dedicated cost-query tool**, mirroring how
   `_system/pulse-platform.sqlite` already works (server-side Go code reads
   the global file, exposes a properly workflow-scoped view through a
   virtual tool). Lower risk, no data migration, preserves the current
   single-file architecture and whatever benefit it has for admin-level
   queries — but doesn't fix the underlying architectural inconsistency
   (this ledger would remain the only cost-relevant system that isn't
   naturally per-workspace), and adds a second access pattern
   (tool-mediated vs. direct-file) that the older cost system doesn't need.

## Non-goals

- Not re-litigating PLAT-088 — confirmed unrelated (a different, already-
  fixed live-write attribution bug, not this ticket's read-access gap).
- Not deciding between the two fix directions above — that's the next step,
  not resolved here.
- Not fixing the workspace-less chat/builder writer's ambiguity
  (`server.go:5745`) — flagged as a real open question either fix direction
  will need to answer, not resolved in this pass.

## Acceptance tests (once a fix is designed)

1. A Pulse `model_cost_fitness` review pass, or a Workflow Builder chat
   turn, can retrieve its own workflow's cost data — including the
   phase/item-level breakdown PLAT-166/167 added — without going outside
   its normal read/tool scope.
2. Cross-workflow cost reporting (if kept) continues to work, or is
   confirmed genuinely unused (per the dead-`CostDashboard` finding above)
   and intentionally dropped.
3. The workspace-less writer path (`server.go:5745`) has an explicit,
   documented landing place under whichever design is chosen — not silently
   dropped or silently misattributed to a workflow that didn't produce it.

## Fix implemented (`5552fbe71`, `main`)

Direction 1 was chosen (per-workspace SQLite ledger), but scoped down at the
user's explicit direction: **dual-write, no backfill.** The existing 28,276
rows in `_system/costs.sqlite` are untouched and that ledger stays
authoritative for the human-facing Cost Analysis UI. Going forward only,
every cost event attributable to a real `Workflow/<name>` workspace is
written to *both* the global ledger (unchanged) *and* a new
`Workflow/<name>/costs/costs.sqlite` (`pkg/costledger/workspace_ledger.go`),
via a new best-effort `appendToWorkspaceLedger` hook in
`costobserver.Observer.append` (`pkg/costobserver/observer.go`) that never
blocks or fails the primary write.

A new read-only tool, `query_workflow_costs`
(`cmd/server/virtual-tools/workflow_costs_tools.go`), mirrors
`query_workflow_db`'s shape (raw read-only SQL against one table, `action`
optional, same schema-hint-on-failure behavior) but resolves to this
per-workspace file instead of `db/db.sqlite`. It is registered as
capability-derived — the same "cannot be removed by a custom tool allowlist"
treatment `query_workflow_db` already gets — at every path a workflow
step, a Pulse background reviewer, or the interactive Workflow Builder gets
its tools from
(`pkg/orchestrator/agents/workflow/step_based_workflow/controller_agent_factory.go`'s
two `prepareCustomTools` branches, `interactive_workshop_manager.go`'s
`prepareReadOnlyBackgroundAgentTools` and `GetToolsForWorkshopMode` system
list, and `cmd/server/tool_setup.go`'s workflow-mode tool merge).

Guidance updated to point at the new tool instead of describing the ledger
as unreachable: `pulse-gate.md`'s `model_cost_fitness` focus key,
`file-layout.md`'s file table, and the Workflow Builder's own "answer user
questions" instruction list in `interactive_workshop_manager.go`.

**Acceptance tests, against the three listed above:**
1. **Met**, and proven through the real tool-call path, not just storage:
   `cmd/server/workflow_costs_tool_e2e_test.go`'s
   `TestQueryWorkflowCostsToolReadsOwnPhaseBreakdownAndIsIsolatedFromAnotherWorkflow`
   runs the actual MCP-bridge-shaped executor against a real workspace-API
   HTTP server and two real per-workspace SQLite files, and asserts a
   session scoped to `Workflow/social-media` gets its own
   `phase="item:draft-message"` row and never sees Upwork's `9.99`/
   `item:apply-to-job` row (including when a `db_path` argument naming
   Upwork's file is smuggled into the call — the tool has no path parameter
   at all, so it's silently ignored). `pkg/costledger/workspace_ledger_test.go`
   covers the same isolation property one layer down, at the ledger/storage
   level.
2. **Met.** The global ledger and `cost_routes.go`'s cross-workflow reader
   are unchanged; this fix only adds a second write, it removes nothing.
3. **Still open, deliberately.** The workspace-less chat/builder writer path
   (`server.go:5745`, formerly cited at that line number, may have moved)
   still has no per-workspace ledger to land in — `WorkspaceLedgerPath`
   returns `""` for any non-`Workflow/<name>` path, so those events continue
   to reach only the global ledger, exactly as before this fix. This was
   explicitly out of scope for this pass (the user's instruction was "new is
   fine" for workflow-scoped events specifically, not a mandate to also
   resolve pre-existing chat/builder attribution ambiguity) — left as a real
   open question for a future ticket if it turns out to matter.

## Verification

`GOWORK=off go build ./...` clean. `go test` across
`cmd/server/...`, `cmd/server/virtual-tools/...`, `pkg/costledger/...`,
`pkg/costobserver/...`, and
`pkg/orchestrator/agents/workflow/step_based_workflow/...` shows zero new
failures against this session's already-established pre-existing baseline
(`TestWorkshopResolveLLMConfigExpandsCodingAgentMode`,
`TestStandalonePulseReviewCommandsUsePersistedReviewerPipeline`,
`TestArtifactDriftAuditsTheSchedule`,
`TestRunInBackgroundPassesBuilderSkillSnapshotToBothAgentKinds`,
`TestWorkshopPromptShellExamplesUseAbsolutePaths`). The workshop tool-set
invariant tests (`TestToolSetInvariants`,
`TestEveryRegisteredWorkshopToolIsAllowedInSomeMode`,
`TestWorkshopSurfaceCoversWhatTheBuilderPromptsPromise`, and siblings) pass,
confirming `query_workflow_costs` is both registered and reachable in every
mode that advertises it.
