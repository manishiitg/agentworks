[← ops / cost](index.md)

# PLAT-088 — every scheduled workflow and Pulse turn was billed to `chat`, so Pulse cost could not be measured

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | ops |
| Area | cost |
| Summary | P1 — not a crash, but it makes the "what does Pulse cost us |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `implemented` — attribution fixed and tested; runtime reverify pending |
| Last synchronized | `2026-08-11` |

- **Priority:** P1 — not a crash, but it makes the "what does Pulse cost us
  versus the goal work?" question unanswerable, and silently inflates the
  `chat` bucket with automation spend nobody typed.
- **Owner:** cost scope attribution (`handleQuery` observer construction,
  `pkg/costobserver`)
- **Found on:** 2026-08-11, while answering "how do we measure cost we spend
  on Pulse vs the goal process".

## Evidence

Every cost row for the instagram scheduled run
(`schedule-manual--bae435e5_1786432476119732000`, 2026-08-11) landed in
`scope='chat'` — including the Pulse Gate, Review+Fix parent, and Finalize
turns, which the logs show running at 08:56, 08:58, 09:48 and 09:53 UTC:

```
08:56:19  chat  schedule-manual--bae435e5_…  $1.56   ← Pulse Gate turn
08:58:36  chat  schedule-manual--bae435e5_…  $0.82   ← Pulse Review+Fix turn
09:48:05  chat  schedule-manual--bae435e5_…  $1.06   ← Pulse Finalize turn
09:53:59  chat  schedule-manual--bae435e5_…  $3.18   ← Pulse post-finalize turn
```

Only Pulse's *delegated background children* were tagged correctly, because
their execution ids literally contain the string "pulse"
(`bg-pulse-review+fix:-engineering…`), which `matchPhaseScope` matches:

```
09:19:38  pulse  bg-pulse-review+fix:-engineeri  $11.14
09:34:58  pulse  bg-pulse-review+fix:-engineeri  $12.87
09:40:28  pulse  bg-pulse-review+fix:-engineeri  $4.90
09:47:20  pulse  bg-pulse-review+fix:-engineeri  $4.56
```

So Pulse's measured cost for that run was $33.47 when the real figure was
roughly $40 — and the missing ~$6.62 was charged to `chat`. Corroborating the
scale of the problem: the ledger contains **zero** rows with `scope='builder'`
in its entire history, because the code path that should produce them cannot.

## Root cause

`handleQuery` captures `isWorkflowPhase := req.AgentMode == "workflow_phase"`
(`server.go:3215`), then **destructively rewrites the field** a hundred lines
later, purely to route the request down the standard agent path:

```go
// Convert to multi-agent mode so it falls through to the standard agent path
req.AgentMode = "multi-agent"        // server.go:3321
```

The cost observer is constructed ~2200 lines further down (`server.go:5561`)
and inferred its scope from that rewritten value:

```go
inferCostScope(req.AgentMode, workflowPhaseID)   // ("multi-agent", "workflow-builder")
```

`InferScope` matches `"multi-agent"` against neither `chief` nor `workflow`,
so it fell through to its default — `ScopeChat`. The routing rewrite and the
cost attribution are ~2200 lines apart with no stated relationship, so nothing
flagged that one silently destroyed the other's only input.

Separately, **no signal on that path could have identified a Pulse turn even
without the rewrite**: a scheduled Pulse turn runs in the same session, with
the same agent mode and the same `workflow-builder` phase id, as the workflow
orchestration turns before it. Mode and phase genuinely cannot tell them
apart.

## Fix

Two parts, matching the two causes:

1. **Honor the scheduler's explicit marker.** New
   `costobserver.ScopeForScheduledLLMRole(llmConfigSource)` maps the stamp the
   scheduler *already* sets when it swaps in the Pulse/maintenance LLM
   (`applyPulseLLMToReqMap` → `llm_config_source = "scheduled_pulse"`) onto a
   cost scope. `scheduled_auto_improve` maps to Pulse deliberately: Goal
   Advisor and Strategy Auditor are Pulse modules that merely run on the
   maintenance LLM. Returns `""` for anything else so callers keep their own
   default rather than inheriting a wrong one.
2. **Stop reading the rewritten mode.** Scope resolution now uses
   `isWorkflowPhase` — captured before the rewrite — to pass the real
   `workflow_phase` mode, so scheduled orchestration turns resolve to
   `builder` rather than `chat`.

Priority order is marker first, then mode+phase, because only the marker can
separate Pulse from workflow inside one scheduled session.

## Consequence for existing data

Historical rows are **not** back-filled and must not be read as if they were.
Every scheduled run before this fix has its Pulse-stage and
workflow-orchestration spend sitting in `chat`. Any "Pulse vs goal" comparison
over pre-2026-08-11 data understates Pulse and overstates chat. (Distinct
from, and additional to, the 15,235 legacy `unknown`-scope rows, which stopped
being produced on 2026-07-12 and are a separate pre-attribution artifact.)

## Verification

- `go build ./pkg/costobserver/` clean; `go vet` clean; `gofmt` clean.
- New tests (`plat088_scheduled_role_scope_test.go`): every scheduled-role
  marker maps to the right scope and unknown markers return `""`;
  `InferScope("workflow_phase", …)` yields `builder` while the rewritten
  `"multi-agent"` yields `chat` — pinning the exact regression rather than
  implying it.
- The full `./cmd/server/...` suite could not be run at commit time: another
  session's in-flight `CreateSchedule`/`UpdateSchedule` signature refactor
  leaves `interactive_workshop_manager.go` uncompilable. Confirmed by stashing
  that work that HEAD builds clean and that **no** build error is attributable
  to this change. Re-run the suite once that refactor lands.
- **Not yet reverified live** — needs a restart, then one scheduled run;
  expect its Pulse turns to appear under `scope='pulse'` and its orchestration
  turns under `scope='builder'`, with neither in `chat`.

## Acceptance

- A scheduled run's Pulse Gate / Review+Fix / Finalize turns are recorded as
  `scope='pulse'`, not `chat`.
- A scheduled run's workflow-orchestration turns are recorded as
  `scope='builder'`, not `chat`.
- `chat` contains only genuine interactive chat.
