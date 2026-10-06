[← platform / cost-telemetry](index.md)

# PLAT-167 — break out cost and time per message_sequence item, not just execution vs reflection

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P3 |
| Product | platform |
| Area | cost-telemetry |
| Summary | P3 — a UI/observability enhancement, not a correctness bug. |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `implemented` — build/test verified, live reverify pending |
| Last synchronized | `2026-08-21` |

- **Priority:** P3 — a UI/observability enhancement, not a correctness bug.
  Direct follow-up to [PLAT-166](plat-166.md), same day: after shipping the
  execution/reflection cost split, the user asked whether the same mechanism
  generalizes to per-message granularity within a `message_sequence` step.
- **Owner:** cost ledger phase attribution (`pkg/costobserver`,
  `pkg/orchestrator/agents/workflow/step_based_workflow`), Cost Analysis UI
  (`CostsPopup.tsx`).
- **Related:** [PLAT-166](plat-166.md) (built the `Phase`/`ByPhase`
  mechanism this ticket reuses verbatim — no new ledger/schema work).

## Evidence

PLAT-166 made `costobserver.Observer.SetPhase(phase string)` accept any
string, and `costledger`'s aggregation (`ExecutionAggregate.ByPhase`) already
groups by whatever phase value it sees — neither is hardcoded to
`"execution_only"`/`"reflection"`. What's missing is a second caller: today
only `reflection_turn_run.go` ever calls `SetPhase`.

Every LLM-turn-producing `message_sequence` item — `user_message`, a
`prevalidation` repair turn, and each synthesized row of a `foreach` — funnels
through exactly one call site:
`controller_message_sequence.go:876-877`, inside
`executeMessageSequenceUserMessage`:

```go
result, history, err := hcpo.withWorkshopMessageTarget(turnCtx, step.GetID(), "message-sequence:"+item.ID, runtime.Agent, func() (string, []llmtypes.MessageContent, error) {
    return runtime.Agent.Execute(turnCtx, templateVars, session.ConversationHistory)
})
```

Confirmed by tracing every item type: `prevalidation`'s own gate check
(`RunPreValidation`) is deterministic and produces no LLM cost; its repair
turn calls `executeMessageSequenceUserMessage` directly
(`controller_message_sequence.go:718`). `foreach` synthesizes one
`user_message`-typed item per row and also calls
`executeMessageSequenceUserMessage` per row
(`controller_message_sequence.go:996-998`). So one bracket, at one call site,
covers every item kind — the same shape PLAT-166 found for the reflection
turn.

`runtime.Agent` (`agents.OrchestratorAgent`) is created once per
`message_sequence` step and reused across every item
(`getMessageSequenceRuntime`, confirmed by its own comment: "created ONCE and
reused for every item") — the same "shared agent/observer across sequential
sub-turns" shape PLAT-166 already solved for execution vs reflection.

**Cost and time both ride along for free.** `Aggregate` (embedded in every
`ByPhase` bucket) already carries `TotalCostUSD` and
`LLMGenerationDurationMS` together — there is no separate mechanism for time.
Tagging an item's turn with a phase gives its cost AND its LLM time in the
same `ByPhase[<item>]` entry, with zero additional plumbing.

## Decision

Reuse PLAT-166's mechanism verbatim; the only new work is a second call site
and a generalized (not reflection-hardcoded) frontend render.

1. **Backend** (`controller_message_sequence.go`): bracket
   `executeMessageSequenceUserMessage`'s turn call with
   `SetPhase("item:" + item.ID)` / deferred `SetPhase("")`, found via
   `runtime.Agent.GetBaseAgent().Observers()` — same lookup pattern
   `reflection_turn_run.go` already uses. `item.ID` already carries the
   per-row identity for `foreach` (`"<item.id>-<idx>"`) and per-repair
   identity for `prevalidation` (`"<item.id>-repair-<n>"`), so no new
   identifier needs to be minted. No `costledger`/`costobserver` schema
   change — `Entry.Phase` and `ExecutionAggregate.ByPhase` already accept any
   string.
2. **Frontend** (`CostsPopup.tsx`, `costActivityBreakdown.ts`): the current
   render is hardcoded to look for `by_phase.reflection` specifically and
   show exactly one sub-line. Generalize: when `by_phase` has more than one
   key, render one indented sub-line per key (sorted by cost descending),
   each labeled by a new `phaseLabel(phase)` helper —
   `"execution_only"` → `"Execution"`, `"reflection"` → `"Reflection"`,
   `"item:<id>"` → the same dash/underscore-to-space cleanup
   `executionLabel` already does, with the `"item:"` prefix stripped. When
   `by_phase` has exactly one key (every non-`message_sequence` step today),
   render nothing — unchanged from PLAT-166's behavior, since a single bucket
   is definitionally the same as the row's own total.

## Non-goals

- Do not change `costledger`/`costobserver`'s schema or aggregation logic —
  PLAT-166 already built a generic phase mechanism; this ticket is purely
  "add a second caller and generalize the one hardcoded render."
- Do not tag anything inside a `todo_task` sub-agent's own turns, or inside a
  plain `regular`/scripted step — those are out of this ticket's evidence
  (only `message_sequence` items were traced) and each would need its own
  call-site investigation if wanted later.
- Do not attempt to break down cost below one message_sequence item (e.g. per
  individual tool call within one item's turn) — `Aggregate` has no
  sub-item-turn dimension, and the evidence for wanting that goes further
  than this ticket's evidence.

## Acceptance tests

1. A `message_sequence` step with 3 `user_message` items produces 3
   `ByPhase` entries (`item:<id1>`, `item:<id2>`, `item:<id3>`) whose costs
   and LLM times sum back to the step's combined execution total.
2. A `foreach` item with N rows produces N `ByPhase` entries
   (`item:<foreach-id>-0` .. `item:<foreach-id>-(N-1)`).
3. A `prevalidation` item with a failed gate and 2 repair turns produces
   `ByPhase` entries for each repair (`item:<id>-repair-1`,
   `item:<id>-repair-2`); the prevalidation check itself (no LLM call)
   contributes nothing.
4. A plain `regular`/scripted step, or a step with only one message_sequence
   item, still renders no sub-lines in `CostsPopup.tsx` — `by_phase` has
   exactly one key, same as before this ticket.
5. `CostsPopup.tsx` renders one sub-line per phase key when there is more
   than one, each showing that item's own tokens/LLM-time/cost, and the
   row's own combined total is unchanged.

## Verification

`go build ./...` and `npx tsc --noEmit` clean. `go test ./pkg/costledger/...
./pkg/costobserver/... ./pkg/orchestrator/... ./cmd/server/...` and `npx
vitest run` — zero new failures versus this session's already-confirmed
pre-existing baseline (PLAT-164/PLAT-166's verification passes).

Frontend: new tests in `costActivityBreakdown.test.ts` — a `by_phase` with 3
`item:<id>` keys reaches the execution row intact, plus `phaseLabel` unit
tests for the `execution_only`/`reflection`/`item:<id>`/unknown-fallback
shapes.

**No new Go test for the `controller_message_sequence.go` wiring itself.**
The mechanism it exercises (loop `ba.Observers()`, type-assert
`*costobserver.Observer`, `SetPhase`) is textually identical to what
`reflection_turn_run.go` already does and which PLAT-166 tested at the
`Observer` level (`SetPhase`/`Phase`/`baseEntry` — the actual logic), not at
the wiring-call-site level (reflection's own wiring has no dedicated test
either). Exercising `executeMessageSequenceUserMessage` end-to-end would need
a full agent/session harness this codebase doesn't have a lightweight
fixture for; the ticket's original plan to add one was reconsidered given
the wiring is a direct, mechanical repeat of an already-tested pattern with
no new logic of its own. Flagged as a real gap, not silently downgraded: if
`ba.Observers()` or the type assertion ever silently stopped matching (e.g.
a refactor of how `attachCostObserver` registers), nothing today would catch
it before a live run did.

Live reverify (a real `message_sequence` step with a `foreach` and a failed
prevalidation repair, confirming the UI actually renders the per-item
breakdown) not done in this pass — flagged, not claimed.

## Implementation (2026-08-21)

Built exactly as designed. `controller_message_sequence.go`'s
`executeMessageSequenceUserMessage` now brackets its turn call:

```go
if ba := runtime.Agent.GetBaseAgent(); ba != nil {
    for _, observer := range ba.Observers() {
        if costObserver, ok := observer.(*costobserver.Observer); ok {
            costObserver.SetPhase("item:" + item.ID)
            defer costObserver.SetPhase("")
        }
    }
}
```

Because this function is called synchronously and sequentially — once per
`user_message` item, once per `foreach` row, once per `prevalidation` repair
turn, always returning before the next call begins — the `defer` correctly
scopes each item's phase to exactly its own turn, with no risk of one item's
tag leaking into the next. Resets to `""`, not `costobserver.PhaseExecutionOnly`
— see PLAT-166's same-day scope-fix section for why: an untagged phase writes
no `by_phase` entry at all, instead of a named-but-redundant duplicate of the
row's own total.

**This ticket landed together with PLAT-166's scope-fix**, not as a separate
follow-up commit: while implementing this, review caught that PLAT-166's
`Observer` had defaulted every instance's phase to `PhaseExecutionOnly`
(not just ones that ever call `SetPhase`), which meant every non-reflection
execution across the whole platform — chat, builder, Pulse, evaluation,
every plain step — was growing a redundant `by_phase.execution_only` entry
in every Cost Analysis response. Since this ticket's own `CostsPopup.tsx`
render generalization was already touching the exact same code, both fixes
shipped in the same pass rather than being artificially split. Full details
in [PLAT-166](plat-166.md)'s own "Scope-fix" section.

`CostsPopup.tsx`'s render generalized from a single hardcoded
`by_phase.reflection` check to iterating every key in `by_phase` (sorted by
cost descending) whenever there is more than one — covering both PLAT-166's
execution/reflection case and this ticket's per-item case with the same
code path. A new `phaseLabel` export in `costActivityBreakdown.ts` prettifies
whatever phase string it's given (`execution_only` → "Execution",
`reflection` → "Reflection", `item:<id>` → the id with its prefix stripped
and separators cleaned up, matching `executionLabel`'s existing style).
Imported under an alias (`costPhaseLabel`) in `CostsPopup.tsx` — that file
already had an unrelated local variable named `phaseLabel` in a different
render block (the older token-usage-file system's per-step-and-model view),
and reusing the bare name would have shadowed it confusingly.
