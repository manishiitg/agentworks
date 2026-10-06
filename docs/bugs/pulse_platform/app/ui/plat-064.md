[← platform / frontend-chat](index.md)

# PLAT-064 — an entire `workflow_*` event family is dead; one completion check had no live fallback

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P3 |
| Product | app |
| Area | ui |
| Summary | P3 (no confirmed user-visible symptom; see scope note) |

| Coordination | Value |
|---|---|
| Assigned agent | `Claude Code` |
| Ticket state | `implemented` |
| Last synchronized | `2026-08-09` |

- **Priority:** P3 (no confirmed user-visible symptom; see scope note)
- **Owner:** frontend event-type consumers
- **Follow-up from:** PLAT-063, which flagged `workflow_end`/`batch_execution_end`
  as dead entries in `EVENT_TYPES.COMPLETION` and deferred the fix here.

## Scope is bigger than PLAT-063 recorded

PLAT-063 found two dead entries in one list. Tracing every consumer of
`workflow_end` found a whole parallel event family, all dead on the Go side:

| type | Go emitter | frontend surface |
|---|---|---|
| `workflow_start` | **none** | full type, dispatcher case, `WorkflowStartEvent` component |
| `workflow_progress` | **none** | full type, dispatcher case, `WorkflowProgressEvent` component |
| `workflow_end` | **none** | full type, dispatcher case, **76-line** `WorkflowEndEvent` component |
| `batch_execution_end` | **none** | full type, dispatcher case, `BatchExecutionEndEventDisplay` component |

Live in parallel: `orchestrator_start` / `orchestrator_end`
(`BaseOrchestrator.EmitOrchestratorEnd`, **exactly one caller** —
`workflow_orchestrator.go:678`, `execution_mode:"workflow_execution"`, i.e. this
already *is* the workflow-completion signal). `workflow_error` is separately
confirmed live (emitted directly in `server.go`) and untouched by this ticket.

Best explanation: emission was refactored from `workflow_*` to `orchestrator_*`
at some point and the consumer side was never cleaned up — the components are
mature and fully styled, not stubs, so this reads as drift, not
work-in-progress.

## The one place this was a live gap, not just dead weight

`useChatStore.ts` `checkTabCompletion`, workflow-mode branch:

```js
const completionEventTypes = mode === 'workflow'
  ? ['workflow_end', 'request_human_feedback']
  : [...]
```

No `orchestrator_end` fallback. **A workflow that completed without ever
requesting human feedback could not satisfy this check at all.**
`useWorkflowStore.ts` has a near-identical `checkTabCompletion` with the same
gap, one degree safer because its list also included `unified_completion` /
`agent_end`.

**Currently low real risk**: neither `checkTabCompletion` has any call site in
the codebase today — both are dead code themselves. This is being fixed anyway
because it is a landmine: whoever wires either one up next would silently
inherit a function that can hang on the exact condition it exists to detect.

## Fix (2026-08-09)

- `constants/runningWorkflows.ts` — `EVENT_TYPES.COMPLETION` trimmed to
  `['orchestrator_end']`, the only real signal. `workflow_end` / `workflow_start`
  also dropped from `EVENT_TYPES.IMPORTANT` (checked: `workflow_error` there is
  live and untouched).
- `useChatStore.ts` `checkTabCompletion` — added `orchestrator_end` ahead of
  `workflow_end` in the workflow-mode list.
- `useWorkflowStore.ts` `checkTabCompletion` — added `orchestrator_end`.

`npx tsc --noEmit` clean.

## Deliberately left alone

The display components (`WorkflowStartEvent`, `WorkflowProgressEvent`,
`WorkflowEndEvent`, `BatchExecutionEndEventDisplay`), their `EventDispatcher.tsx`
cases, the generated types (`event-types.ts`, `events.ts`, `events-bridge.ts`),
and the Go-side dead references (`session_execution_tree.go`,
`session_activity_tree.go`, `polling.go` switch cases; `event_store.go`'s
`STRUCTURAL_EVENTS`/`NEVER_SHOW_EVENTS` map entries) are unchanged. All are
inert — they reference an event type that cannot occur, so they cost nothing at
runtime. Removing them is a larger, purely cosmetic change (codegen, generated
types, several Go files) with no functional payoff; flagged here rather than
done, in case someone later decides to actually wire up real
`workflow_start`/`workflow_progress` emission instead of deleting the
consumers — the components already exist and are ready to receive it.

## Acceptance

`EVENT_TYPES.COMPLETION` and both `checkTabCompletion` implementations no longer
depend on an event that cannot fire. No runtime reverify needed — this is a
type-level/dead-code correction with `tsc` as the check, not a behavioral change
to any currently-exercised path.
