[← goals / human-decisions](index.md)

# PLAT-368 — Cancelling a workflow does not release its human-feedback wait

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | goals |
| Area | human-decisions |
| Summary | `HumanFeedbackStore.WaitForResponse` accepts a request ID and timeout but no |

| Coordination | Value |
|---|---|
| State | Fixed on `main` in `b616789cd`; not yet deployed to RTS |
| Date | 2026-09-28 |
| Priority | P2 |
| Owner | human-decisions; implementation unassigned |
| Related subsystems | workflow execution lifecycle, agent human_feedback |
| Reviewed revision | `ebc37cf87` on main, including the local changes present during review |

## Problem and cause

`HumanFeedbackStore.WaitForResponse` accepts a request ID and timeout but no
caller context. It creates its timeout using `context.Background()`. A
workflow cancelled while waiting for human input therefore continues waiting
until an answer or that independent timeout arrives. Its question can remain
pending after cancellation.

Both BaseOrchestrator feedback helpers and the agent `human_feedback` tool
use this waiter. They have caller contexts, but do not pass cancellation
through to it. The probe established that the helper remains blocked and can
be released by an answer; it did not establish that downstream workflow
actions execute after cancellation.

Evidence:

- [human_feedback_store.go](../../../../agent_go/cmd/server/virtual-tools/human_feedback_store.go): `WaitForResponse` at 271; background timeout at 280.
- [base_orchestrator_feedback.go](../../../../agent_go/pkg/orchestrator/base_orchestrator_feedback.go): first waiter call at 70.
- [human_tools.go](../../../../agent_go/cmd/server/virtual-tools/human_tools.go): `handleHumanFeedback` calls the same waiter at 1098.

## Reproduction

1. Start `RequestHumanFeedback` with a cancellable context in a goroutine.
2. Wait for its request to appear in the feedback store.
3. Cancel the supplied context.
4. Wait 100 ms for the helper to return. It remains blocked.
5. Submit a cleanup answer and join the goroutine; it then returns.

Review-only regression `TestReviewCanceledWorkflowReleasesFeedbackWait`
failed with:

```text
cancelled workflow still blocked awaiting human input; wait ignores caller context
```

No live run was stopped for this test. No downstream side effect is claimed.

## Proposed fix and acceptance

Make the waiter context-aware, remove the request when its execution is
cancelled, and emit a distinct cancellation outcome instead of reporting it
as an answer or generic expiry. Preserve the distinction between cancelling
a status-poll HTTP request and cancelling the actual background execution.

- [ ] Cancelling the execution promptly releases its waiter and removes the
  pending question without requiring an answer.
- [ ] A late answer is rejected and does not restart or advance cancelled work.
- [ ] Cancelling/disconnecting an MCP polling request does not cancel an
  otherwise healthy independent workflow.
- [ ] Answer/cancellation/timeout races settle once, with no leaked waiter,
  duplicate completion, or deletion of a newer request reusing an ID.
- [ ] Resolution events distinguish answered, expired, and cancelled requests.
- [ ] Tests exercise both an orchestrator helper and the `human_feedback`
  agent-tool path with caller cancellation.

The expiry mismatch in
[PLAT-365](../../crew/human-decisions/plat-365.md) is related but does not solve caller cancellation.

## Fix (2026-09-28)

Fixed in `b616789cd`. `WaitForResponseCtx` ends the wait on an explicit
cancellation, removes the question (it is no longer listed, and a late
answer is refused) and returns `ErrFeedbackCancelled`; the resolution marker
records outcome `cancelled`. A context deadline does not shorten the
requested wait. Cleanup removes only this wait's own entry. All four waiters
(the three orchestrator helpers and the `human_feedback` tool) pass their
context.

Tests: `human_feedback_cancel_test.go`, `base_orchestrator_feedback_test.go`.
Not yet deployed to RTS.
