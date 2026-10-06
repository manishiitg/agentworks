[← ops / cost](index.md)

# PLAT-032 — child-agent LLM calls are missing from parent step telemetry

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | ops |
| Area | cost |
| Summary | child-agent dispatch telemetry and per-step usage aggregation |

| Coordination | Value |
|---|---|
| Assigned agent | `Claude Code` |
| Ticket state | `implemented` (root cause; richer trace breakdown not built) |
| Last synchronized | `2026-08-05` |

> Claim this ticket in this file before implementation. During active work,
> update this fragment rather than the shared index; synchronize the index
> once at handoff, review, or completion.

- **Priority:** P1
- **Owner:** child-agent dispatch telemetry and per-step usage aggregation
- **Source finding:** `HARNESS-SUBAGENT-DISPATCH-STATUS`
- **Source workflow:** `Workflow/social-media`
- **Source fingerprint:** `0b7d9d4a1b498e71`
- **Problem:** a parent step's timing trace counts the parent agent call but
  omits the LLM calls made by child agents it dispatches. The authoritative
  cost ledger therefore records far more calls than the step timing trace.
- **Impact:** Pulse cannot use the timing traces for trustworthy step-level
  cost, latency, or capacity analysis. Expensive orchestrator steps appear
  almost free.
- **Current evidence:**
  - all 17 retained timing traces total 40 LLM calls, while the cost ledger
    records 490 calls for the same run;
  - `step-execution-pipeline` reports 1 call in its trace and 167 calls in the
    ledger;
  - `execute-actions` reports 1 call in its trace and 231 calls in the ledger.
- **Required fix:** propagate a child's execution identity and usage into the
  parent step's telemetry aggregation, while retaining separate parent and
  child breakdowns and preventing double counting. A completed parent trace
  must reconcile with the authoritative ledger for that execution and step.
- **Acceptance:** for a step that dispatches multiple child agents, the trace
  exposes parent calls, child calls, and their total; the total equals the
  ledger total exactly once; successful and failed children are both included;
  and a step with no children is unchanged.
- **Required tests:** a nested-agent telemetry unit test, a failed-child case,
  a no-double-counting case, and one real workflow/message-sequence bridge E2E.

## Scope correction: the source finding contains an unproven second claim

The finding's title and concern text also claim that a child returning
`success:false` can be reported by its dispatch envelope as `completed`.
However, its structured reproduction explicitly says that the current run had
no failed route and therefore did **not** reproduce that behavior. Do not fold
that historical claim into this implementation. Keep it in
`needs_reproduction`; create a separate ticket only after a fresh failed-child
run proves the envelope-status mismatch.

## Implementation (2026-08-05, Claude Code, `mcp-agent-builder-go` `cdc3d1a76`)

**Root cause found in code, not just from the harness evidence.**
`ContextAwareEventBridge`'s timing capture is context-scoped:
`StartTimingCaptureFor(ctx)` stamps a capture ID into `ctx`, and
`captureToolCallEvent` writes into whichever collector matches the `ctx` an
event actually arrived with (`context_aware_bridge.go`, `timingCaptureIDFromContext`).
Async sub-agent dispatch (`sub_agent_async.go`, `registerAsyncCall`) built the
child's execution context from `execCtx.ParentContext` — the step's long-lived
base context — not from `toolCtx`, the in-flight tool-call context that
actually carries the parent's active capture ID. `copyAsyncSubAgentContextValues`
only copied four explicitly whitelisted keys (tier, share-browser,
message-sequence-restart, generic-agent-message-sequence); the timing-capture
key wasn't one of them. So a child's LLM/tool events landed in a different (or
the default) collector than the one the dispatching step later drains, and
never appeared in its trace.

The **sync** dispatch path (`!execCtx.AsyncEnabled`) passes `toolCtx` straight
through and was not affected — the gap is specifically the async fan-out path,
which is what a todo_task with parallel routes (Social Media) exercises
heavily. That maps directly onto the evidence: `step-execution-pipeline` and
`execute-actions` are todo_task step names dispatching many async children.

**Fix:** `ContextAwareEventBridge.CopyTimingCaptureContext(dst, src)` (new,
exported) propagates the active capture ID from one context to another.
Wired into `copyAsyncSubAgentContextValues` in `sub_agent_async.go`, so a
child's detached context still carries the parent's capture ID.

**What this closes vs. what remains open against the ticket's acceptance
criteria:**
- "the total equals the ledger total exactly once" — closed for the async
  dispatch path: a child's calls now land in the parent's own collector
  (mixed in with the parent's calls), so the drained total now includes them.
- "the trace exposes parent calls, child calls, **and their total**" —
  **not built**. This fix makes the totals correct; it does not add a
  breakdown that separately labels which entries came from the parent vs.
  which child. The `LLMCallEntry`/`ToolCallEntry` types have no
  parent/child/execution-ID field to group by. Building that breakdown is a
  distinct, larger change (tagging every captured entry with its owning
  execution) that was out of scope for closing the undercount itself.
- "successful and failed children are both included" — not discriminated
  against by this fix (capture is driven by generation/tool events, not by
  the child's final outcome), but **not explicitly tested** with a failed
  child.
- "a step with no children is unchanged" — true by construction (the change
  only touches `registerAsyncCall`, which only runs for dispatched children),
  but not explicitly tested.
- Generic-agent dispatch (`createExecuteGenericAgentFunc`) shares the same
  `registerAsyncCall`/`copyAsyncSubAgentContextValues` helpers, so it's fixed
  by the same change — not verified with a separate test.

**Tests:** `TestAsyncSubAgentChildLLMCallsReportIntoParentStepTiming` (proves
a child's `LLMGenerationStart` event now lands in the parent's drained
capture; confirmed to fail against the pre-fix code via `git stash`) and
`TestAsyncSubAgentWithNoActiveParentCaptureLeavesChildContextUnchanged`
(no active capture → no panic/spurious behavior). **Not built:** a
failed-child case, a no-double-counting case (two children + parent all
captured without inflating either side), or a real workflow/message-sequence
bridge E2E — all named in the ticket's "Required tests" and still open.

**Remaining/runtime reverify:** confirm on a real `social-media` run that
`step-execution-pipeline`/`execute-actions`-style steps now show LLM-call
counts that reconcile with the cost ledger, not just 1.
