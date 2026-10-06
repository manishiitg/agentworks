[← platform / cost-telemetry](index.md)

# PLAT-166 — the cost ledger cannot separate a step's execution cost from its reflection cost

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | platform |
| Area | cost-telemetry |
| Summary | P2 — not a correctness bug (nothing is billed wrong), but the |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `implemented` — build/test verified, live reverify pending |
| Last synchronized | `2026-08-21` |

- **Priority:** P2 — not a correctness bug (nothing is billed wrong), but the
  Cost Analysis UI's per-step figure silently mixes two different kinds of
  spend, and there is no way today to answer "what does reflection cost this
  step" from the surface the user actually looks at.
- **Owner:** cost ledger schema/aggregation (`pkg/costledger`), cost observer
  attribution (`pkg/costobserver`), Cost Analysis UI (`CostsPopup.tsx`).
- **Related:** [PLAT-068](../learnings-knowledge/plat-068.md)) (shipped the execution/reflection split
  for the *older* token-usage-file system, not this one), [PLAT-111](../frontend-chat/plat-111.md))
  (Cost Analysis performance, unrelated to attribution), [PLAT-090](plat-090.md)
  (Pulse-specific cost measurement, unrelated).

## Evidence and root cause

A user looking at the Cost Analysis panel's expanded per-date view asked
whether a step's row (e.g. "review measure" — 8.28M tokens, LLM time 14m 50s,
$1.5762) could show reflection time/cost separately. Tracing the actual data
path (not the docs) found two different systems with two different answers:

**System 1 — the older per-step token-usage JSON files
(`pkg/orchestrator/base_orchestrator_tokens.go`,
`pkg/orchestrator/context_aware_bridge.go`).** This one already splits
correctly: `stepAggregationKey` produces `"<phase>:<stepID>"` keys, so
`ByStepAndModel["reflection:<step-id>"]` and
`ByStepAndModel["execution_only:<step-id>"]` are separate entries. PLAT-068
(shipped) confirmed and dated this. It is what `ops-review.md` means when it
says "reflection is attributed separately... in the cost ledger" — but that
phrase is a naming collision. This system feeds `llm_ops_review`'s text
analysis and `frontend/src/utils/dailyCostBreakdown.ts`'s `classifyPhase`
(which already has a distinct `'reflection'` `StageBucket`, comment: *"so
'what does reflection cost' is answerable"*) — **not** the Cost Analysis
panel the user was looking at.

**System 2 — the SQLite cost ledger (`pkg/costledger`), read by
`CostsPopup.tsx` via `GET /api/workflow/costs`.** This is what actually backs
the per-step row in the screenshot, and it cannot split reflection from
execution at all:

- `costledger.Entry` (`pkg/costledger/ledger.go:30-76`) has no phase field.
- `agentCostExecutionID` (`pkg/orchestrator/base_orchestrator_cost.go:89-116`)
  mints `ExecutionID` as `sessionID:stepID` — carries no phase either.
- The reflection turn reuses the *same* agent and the *same* cost observer
  instance as the step's execution turn (`reflection_turn_run.go:178`,
  `hcpo.withWorkshopMessageTarget(..., executionAgent, ...)`) — no new
  observer is attached for it. The reflection turn's existing
  `cab.PushContext(reflectionCostPhase, ...)` call
  (`reflection_turn_run.go:178`) only updates the `ContextAwareEventBridge`
  (`cab`) — a completely separate `AgentEventListener` from the cost observer
  (`pkg/costobserver/observer.go`), registered independently
  (`base_orchestrator_agent_factory.go:238` vs `:264`/`base_orchestrator_cost.go:79`).
  The cost observer never reads `cab`'s phase. Pushing `reflectionCostPhase`
  today has **zero effect on cost-ledger rows** — it only affects System 1.
- `addEntryToSummary` (`ledger.go:549-622`, shared by both the legacy JSONL
  path and the SQLite `summarizeWindow` path at `sqlite.go:291-367`)
  aggregates `ByExecution map[string]*Aggregate` keyed only by `ExecutionID`.
  With no phase to key on and no phase on the entry, execution and reflection
  cost for one step land in the exact same bucket, summed, indistinguishable.

**A sibling system has the identical disease.** The per-step *timing* files
(`-timing.json`, separate from both systems above) already write a
`reflection-timing.json` with `"phase":"reflection"`
(`reflection_turn_run.go:257-340`), but `workflow_review_data.go`'s
`persistedWorkflowTiming` struct never unmarshals that field —
`loadWorkflowTimingFiles` (`workflow_review_data.go:276-307`) keys purely on
`executionPrefix+timing.StepID`, so execution- and reflection-timing files for
the same step already silently merge into one `by_execution` bucket. Flagged
here for visibility; **not fixed by this ticket** (see Non-goals).

## Decision

Per user request, fix this at the schema level in `pkg/costledger` (not a
one-off frontend patch): every cost entry gets a `Phase`, and the aggregation
that already groups by execution also groups by phase within it. This makes
the split available everywhere `costledger.Summary` is read, not just the one
`CostsPopup.tsx` row the user pointed at.

### Backend

1. **`costledger.Entry`** gains `Phase string` (`json:"phase,omitempty"`).
   Two values in use: `"execution_only"` and `"reflection"` — deliberately
   reusing System 1's exact phase-string vocabulary (`context_aware_bridge.go`'s
   `attributedPhase = "execution_only"`, `reflection_turn_run.go`'s
   `reflectionCostPhase = "reflection"`) so an operator cross-referencing both
   systems isn't learning a second vocabulary for the same concept.
2. **SQLite** (`pkg/costledger/sqlite.go`): a `phase` column via the existing
   `ensureCostEventColumn` migration helper (same pattern already used for
   `llm_generation_duration_ms`) — additive, no backfill, old rows read as
   `""`. `append`'s INSERT, `summarizeWindow`'s SELECT, and
   `migrateLegacyJSONL`'s INSERT column lists each gain the one column.
   `summarizeWorkflowTotals` (the raw-SQL all-time-headline path) is
   untouched — it never produces `ByExecution`/phase breakdown today and
   nothing here changes that.
3. **Aggregation** (`ledger.go`): new `ExecutionAggregate` type —
   `Aggregate` embedded (keeps the JSON shape flat/backward-compatible for
   every existing field) plus `ByPhase map[string]*Aggregate` (new, additive,
   `omitempty`). `ScopeAggregate.ByExecution` changes from
   `map[string]*Aggregate` to `map[string]*ExecutionAggregate`.
   `addEntryToSummary`'s two `ByExecution` construction sites (scope-level and
   per-date-scope-level) route each entry into `executionBucket.ByPhase[phase]`
   too, only when `e.Phase != ""` — so unphased entries (every non-workflow
   scope, every entry written before this ships) simply never populate
   `by_phase`, and the JSON key is omitted exactly as it is today. Both the
   legacy JSONL path and the SQLite `summarizeWindow` path already funnel
   through this one function, so this is a single-site aggregation change.
4. **`pkg/costobserver.Observer`**: add a `phase` field defaulted to
   `PhaseExecutionOnly` at construction, plus `SetPhase(phase string)` /
   `Phase() string` (mutex-protected, mirroring the existing `sawPerCall`
   lock). `baseEntry` stamps `Entry.Phase = o.phase`. The observer's
   attribution is otherwise immutable-per-instance (`WithAttribution` at
   construction) — this is the one field that becomes mutable, because it is
   the one thing that legitimately changes mid-lifetime for the same agent
   instance (execution turn, then reflection turn).
5. **Wiring** (`reflection_turn_run.go`): reach the already-attached cost
   observer via a new `BaseAgent.Observers() []mcpagent.AgentEventListener`
   getter (`agents/base_agent.go`), find the `*costobserver.Observer` among
   them, and bracket the reflection turn call with
   `SetPhase(costobserver.PhaseReflection)` / deferred
   `SetPhase(costobserver.PhaseExecutionOnly)` — placed directly alongside the
   existing `cab.PushContext(reflectionCostPhase, ...)` /
   `defer cab.PopContext()` bracket, same shape, same file, same reasoning
   ("deferred so an early return or panic can never leave attribution
   unbalanced for the next turn").

### Frontend

6. `frontend/src/services/api-types.ts`: new `CostExecutionAggregate extends
   CostAggregate { by_phase?: Record<string, CostAggregate> }`;
   `CostScopeAggregate.by_execution` retyped to
   `Record<string, CostExecutionAggregate>`.
7. `frontend/src/utils/costActivityBreakdown.ts`: carry `by_phase` through
   into each `category.executions[]` entry's `cost` object (currently plain
   `CostAggregate`; becomes `CostExecutionAggregate` — additive).
8. `frontend/src/components/workflow/CostsPopup.tsx`: when an execution row's
   `cost.by_phase` has a non-trivial `reflection` entry (non-zero cost, tokens,
   or LLM time), render one additional indented sub-line under that row —
   `↳ Reflection: <LLM time> · <tokens> · <$cost>` — leaving the row's own
   totals (still the combined sum) unchanged, so nothing that reads the
   top-level number needs to change.

## Non-goals

- Do not fix the sibling `-timing.json`/`workflowActivityTimingAggregate`
  merge bug in this pass — flagged above as a related finding, not silently
  ignored, but it is a separate read path (`workflow_review_data.go`'s
  `persistedWorkflowTiming`) with its own fix shape; bundling it in risks
  scope creep on a ticket the user asked for narrowly.
- Do not unify System 1 (token-usage-file phase split) and System 2 (this
  ticket) into one system. They serve different consumers
  (`llm_ops_review`'s text analysis vs the Cost Analysis UI) and merging them
  is a bigger architectural change than this ticket's evidence justifies.
- Do not change `ExecutionID` minting or existing scope inference — the phase
  dimension is additive to the existing execution/scope model, not a
  replacement for it.
- Do not backfill historical `cost_events` rows with a phase — additive
  column, old rows read as `""` (no `by_phase` breakdown for pre-ship data),
  matching this codebase's established migration convention.

## Acceptance tests

1. A step with both an execution turn and a reflection turn produces a
   `cost_events` row for each phase; `SummarizeWorkflowScopeWindow`'s
   `by_execution[<step execution id>]` shows both totals combined at the top
   level (unchanged) *and* a `by_phase` breakdown with `execution_only` and
   `reflection` entries that sum back to the top-level total.
2. A step with no reflection turn (or one whose LLM call never landed —
   `turnErr != nil` still records timing today; cost recording happens
   through the normal event path either way) produces `by_phase` with only
   `execution_only`, or no `by_phase` at all if the observer's default phase
   was never toggled.
3. Any entry from before this ships has `Phase == ""` and never appears in
   any `by_phase` map — the aggregation change is proven inert for existing
   data. (Revised during implementation: every observer now defaults to
   `PhaseExecutionOnly` rather than only workflow-step observers, since
   `attachCostObserver` has no cheap way to know in advance whether a given
   agent will ever run a reflection turn — see Implementation below. A
   non-workflow scope's `by_phase` is therefore `{"execution_only": <same as
   total>}`, not absent; harmless, since the UI only renders a sub-line when
   `by_phase.reflection` is non-trivial, but the original wording here was
   inaccurate.)
4. `CostsPopup.tsx`'s expanded per-date view renders the reflection sub-line
   only when `by_phase.reflection` is non-trivial, and never changes the
   row's own combined total.

## Verification

Go: fail-before/pass-after unit tests for `costobserver.Observer.SetPhase`
attribution and `addEntryToSummary`'s `ByPhase` aggregation (in-memory,
covering both the "phase present" and "phase absent" cases), plus a SQLite
round-trip test proving `phase` survives `append` → `summarizeWindow`. Full
`go build ./...` / `go test ./pkg/costledger/... ./pkg/costobserver/...
./pkg/orchestrator/...` against a clean baseline. Frontend: `costActivityBreakdown`
unit test proving the reflection sub-line data reaches
`category.executions[].cost.by_phase`, plus a `CostsPopup` render test.
Live reverify (a real step with reflection enabled, confirming the split
renders correctly in the actual UI) is not done in this pass — flagged
explicitly, matching this register's standard practice, rather than claimed.

## Implementation (2026-08-21)

Built exactly as designed above, with two decisions settled during
implementation rather than upfront:

- **`Observer.phase` defaults to `PhaseExecutionOnly` for every observer**,
  not only ones backing a workflow step. `attachCostObserver` is one shared
  factory call site (`setupStandardAgent`) used for workflow steps, todo_task
  sub-agents, and Pulse reviewers/fixers alike, with no cheap signal at
  construction time for "this specific agent will later run a reflection
  turn" — only `reflection_turn_run.go` knows that, and only once the turn
  actually starts. Defaulting universally means every scope now writes
  `Phase = "execution_only"` (previously all scopes wrote `Phase = ""`); a
  non-workflow execution's `by_phase` is `{"execution_only": <same value as
  the top-level total>}` rather than absent. This is inert for the UI (which
  only renders anything when `by_phase.reflection` is non-trivial) and for
  every other consumer (the flat top-level `Aggregate` fields are byte-for-byte
  unchanged either way) — but it does mean acceptance test 3's original
  wording ("non-workflow scope has `Phase == ''`") was wrong; corrected above
  rather than silently left inconsistent with what shipped.
- **No component-render test for `CostsPopup.tsx`.** `CostsPopup.test.ts`
  only exercises `buildDailyStepCostsByDate` (a different data source, the
  older token-usage-file system) — there is no existing React Testing Library
  render harness for this component to extend. The new conditional rendering
  itself is a direct read of `execution.cost.by_phase.reflection`, and that
  data path is fully covered by new `costActivityBreakdown.test.ts` cases
  (below); adding render-test infrastructure for one component solely for
  this change was judged out of proportion to the risk.

**Files touched:**
`pkg/costledger/ledger.go` (`Entry.Phase`, `ExecutionAggregate`,
`addEntryToExecutionBucket`), `pkg/costledger/sqlite.go` (`phase` column
migration, `append`/`summarizeWindow`/`migrateLegacyJSONL` column lists),
`pkg/costobserver/observer.go` (`PhaseExecutionOnly`/`PhaseReflection`
constants, `phase` field, `SetPhase`/`Phase`, `baseEntry`),
`pkg/orchestrator/agents/base_agent.go` (`Observers()` getter),
`pkg/orchestrator/agents/workflow/step_based_workflow/reflection_turn_run.go`
(bracket the reflection turn with a `SetPhase` toggle, alongside the existing
`cab.PushContext`/`PopContext` bracket; corrected that block's own comment,
which had claimed the `cab` push already reached "the cost UI" and "any
ledger analysis" — it did not, until this ticket), `frontend/src/services/api-types.ts`
(`CostExecutionAggregate`), `frontend/src/utils/costActivityBreakdown.ts`
(`addExecutionCost` phase-aware merge), `frontend/src/components/workflow/CostsPopup.tsx`
(reflection sub-line).

**Verified:** `go build ./...` clean. New tests: `pkg/costledger/plat166_phase_test.go`
(in-memory `ByPhase` aggregation for both the phased and unphased case, a
full SQLite append→summarize round trip, and a pre-PLAT-166-database reopen/
migration case), `pkg/costobserver/plat166_phase_test.go` (default phase,
`SetPhase` toggling and restore), `frontend/src/utils/costActivityBreakdown.test.ts`
(three new cases: `by_phase` carried through, `by_phase` summed correctly
when `executionGroup` merges dispatched/retry ids, `by_phase` absent when
never set). `go test ./pkg/costledger/... ./pkg/costobserver/...
./pkg/orchestrator/... ./cmd/server/...` and `npx vitest run` (frontend) —
zero new failures; every failure present (`cmd/server`, `cmd/server/guidance`,
`pkg/orchestrator/agents/workflow/step_based_workflow`,
`PulseWorkspace.test.tsx`) was independently confirmed pre-existing and
unrelated earlier in this same session (PLAT-164's verification pass).

**Not done:** live reverify against a real step with reflection enabled,
confirming the sub-line actually renders correctly in the running UI —
flagged per the Verification section above, not claimed.

## Scope-fix (2026-08-21, same day)

Caught by review before this shipped further: every `costobserver.Observer`
defaulted its `phase` field to `PhaseExecutionOnly` at construction. That
meant **every** execution — chat, builder, Pulse, evaluation, every plain
workflow step, not only ones that ever run a reflection turn — grew a
`by_phase.execution_only` entry that just duplicated its own top-level total,
in every Cost Analysis API response, forever. Harmless to the UI (which only
ever rendered something when a *second*, non-default phase was also
present), but a real, permanent payload-size regression, and one directly
relevant to [PLAT-111](../frontend-chat/plat-111.md))'s already-tracked Cost Analysis
first-paint/payload problem.

**Fix:** `Observer.phase` now starts empty (`""`). `addEntryToExecutionBucket`
already only writes a `ByPhase` entry when `Entry.Phase != ""`, so an
observer nobody ever calls `SetPhase` on now correctly produces no `by_phase`
at all — not a redundant single-key one. The reflection-turn bracket
(`reflection_turn_run.go`) now resets to `""` after the turn instead of back
to `PhaseExecutionOnly`, so a lingering "execution_only" stamp can't cause
the same duplication for whatever runs after it. `PhaseExecutionOnly` itself
stays defined (still a reasonable explicit value, and
`frontend/costActivityBreakdown.ts`'s `phaseLabel` still recognizes it) — it
is simply never written as a default anymore.

`CostsPopup.tsx`'s render condition updated to match: since `by_phase` can
now legitimately hold exactly one entry that does *not* equal the row's
whole total (e.g. `{"reflection": Y}` with real untagged execution work
alongside it), the show/hide check is no longer "more than one key present"
— it is "more than one key, OR exactly one key whose token count is less
than the row's own total token count" (tokens compared as exact integers,
not float cost, to avoid floating-point-summation false positives).

Also folded into this same pass, ahead of shipping it as a separate defect:
[PLAT-167](plat-167.md) reuses this exact mechanism for
message_sequence-item-level breakdown and needed the corrected default and
render condition from the start — see that ticket for what it adds on top.

Verified: `pkg/costobserver/plat166_phase_test.go`'s default-phase test
rewritten to assert no tag (was asserting `PhaseExecutionOnly`); full
`go build ./...` / relevant `go test` / `npx tsc --noEmit` / `npx vitest run`
rerun clean, zero new failures versus the same pre-existing baseline noted
above.
