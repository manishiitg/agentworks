[← ops / cost](index.md)

# PLAT-226 — orchestrator/Pulse/Builder overhead cost is provably merged, and the disambiguating signal doesn't exist yet

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | ops |
| Area | cost |
| Summary | P1 in the queue, low severity per the finding itself. |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `root cause confirmed in code — fix deferred, needs live instrumentation design` |
| Last synchronized | `2026-08-29` |

- **Priority:** P1 in the queue, low severity per the finding itself.
- **Owner:** `agent_go/pkg/orchestrator/context_aware_bridge.go` — the shared
  event bridge every `BaseOrchestrator`-based session (workflow execution,
  Pulse review, Pulse fixer, the interactive Builder) constructs through the
  same single call site (`base_orchestrator.go:167`,
  `NewContextAwareEventBridge`).
- **Findings:** Upwork `PUL-A8AB0913` (cost-ledger overhead attribution) and
  `PUL-E717A5E1` (aggregated call/context telemetry) — same underlying
  mechanism, grouped here per the audit's own framing.

## What the findings established

`PUL-A8AB0913`'s own evidence (re-checked this pass, not re-derived): the
per-execution cost ledger (`costs/execution/__ungrouped__/<date>.json`) is
correctly ID-keyed per execution now — that part of an earlier diagnosis is
resolved. But every execution record still carries exactly one attribution
row, `workflow_orchestrator:workflow_orchestrator`, even when the actual
spend is a mix of the real run-level orchestrator, a same-day Pulse pass,
and Builder/interactive work. The finding's own reproduction on
`2026-08-12.json` shows this directly: a `17:25:31Z` record that post-dates
a `16:57:51Z` run completion — i.e. this is a Pulse pass's own cost — booked
under the identical `workflow_orchestrator` label as genuine orchestrator
spend. `PUL-E717A5E1` is the same symptom from a different angle: aggregated
telemetry prevents item-level diagnosis of a workload's actual cache-hit
composition.

## Root cause, confirmed in code

`context_aware_bridge.go`'s step-based token-attribution path
(`persistTokenUsage`, around line 961) falls back to a single hardcoded
bucket whenever neither `currentPhase` nor `currentStepID` is set:

```go
attributedPhase := currentPhase
attributedStepID := currentStepID
if strings.TrimSpace(attributedPhase) == "" {
    if strings.TrimSpace(attributedStepID) != "" {
        attributedPhase = "execution_only"
    } else {
        attributedPhase = "workflow_orchestrator"
        attributedStepID = "workflow_orchestrator"
    }
}
```

This branch is reached by *any* token event that never had a plan-step
phase/ID pushed onto the bridge — which includes the real run-level
orchestrator, but also Pulse review turns, Pulse fixer turns, and the
Builder/interactive workshop session, none of which are workflow-plan
steps and so never call `PushContext` with a phase/step at all. All of them
collapse onto the identical `"workflow_orchestrator"` label.

**Checked for an existing disambiguating signal before concluding a new one
is needed.** `currentAgentName` (`c.currentAgentName`, already read into
scope at the point this fallback runs, already surfaced elsewhere as
`orchestrator_agent_name` event metadata) looked like a promising existing
field to key off instead of hardcoding a shared bucket. It is not: searching
every `PushContext`/`PushContextRich` call site in this codebase found
exactly one caller that sets an agent name at all (`background-task`, in
`interactive_workshop_manager.go`). Pulse review, Pulse fixer, and the main
Builder session never call `PushContext` — so `currentAgentName` is empty
for them too, for the identical reason `currentPhase`/`currentStepID` are
empty. There is no existing signal anywhere in this bridge's current inputs
that can distinguish these three sources from each other or from the real
orchestrator.

## Why this ticket stops at diagnosis rather than shipping a fix

A real fix is not a one-line change to the fallback branch — there is
nothing to branch on yet. It requires deliberately calling
`PushContext`/`PushContextRich` (or an equivalent new "session kind" field
threaded into `ContextAwareEventBridge`) at the start of Pulse review turns,
Pulse fixer turns, and the Builder/interactive workshop session — each a
separate call site, not a shared one, since they're set up independently
(the Builder session config is in `interactive_workshop_manager.go`; Pulse
review/fixer dispatch is in `pulse_worklist.go` and related files). Missing
one of the three, or mislabeling which existing bucket becomes "the real
orchestrator" once the others are split out, produces a cost ledger that is
*confidently wrong* rather than *honestly merged* — worse than the current
state, and not something this investigation could verify without a live
Pulse pass, a live Builder session, and a live scheduled run to check the
resulting buckets against. That live verification loop is exactly the kind
of check this session cannot perform from a static repo checkout, which is
why PLAT-224 reached the same kind of stopping point for a different
reason (unknown external-tool schema) and why this ticket does the same for
this one.

## Concrete next steps, for whoever picks this up next

1. Add a `sessionKind` (or reuse/extend `currentAgentName`, now that its
   real gap is confirmed) that Pulse review, Pulse fixer, and the Builder
   session each set once at session start via a small, explicit call —
   not inferred from the absence of phase/step, which is exactly what
   silently merges them today.
2. Change the fallback branch to attribute by that signal when phase/step
   are both empty, keeping `"workflow_orchestrator"` only for the case
   where none of the three explicit signals are set (the genuine run-level
   orchestrator, which is the one caller that legitimately never has a
   plan-step phase).
3. Verify against a real same-day run that includes a scheduled full run,
   a Pulse pass, and Builder activity — the exact reproduction shape both
   findings already used — and confirm the resulting
   `costs/execution/__ungrouped__/<date>.json` carries three separable
   attribution rows instead of one merged one.

## Verification

N/A — no files changed. This is a diagnosis record with a concrete
implementation plan for the next pass, not a shipped fix.
