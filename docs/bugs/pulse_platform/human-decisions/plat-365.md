[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-365 — Workflow human-input questions are stored without their session binding

| Coordination | Value |
|---|---|
| State | Fixed on `main` in `ca65b9886`; not yet deployed to RTS |
| Date | 2026-09-28 |
| Priority | P1 |
| Owner | human-decisions; implementation unassigned |
| Related subsystems | step-execution, external MCP/CLI |
| Source | User-requested review of bidirectional crew/workflow communication |
| Reviewed revision | `ebc37cf87` on `main`, including the working-tree changes present during review |

## Problem and impact

A workflow asks the user a question, but its authoritative feedback-store
record has an empty `SessionID`. The external `run_status` tool therefore
omits it from `pending_inputs`, and `run_reply_input` rejects an answer even
when the caller owns the correct workflow session and knows the request ID.
The workflow remains waiting until another supported response path answers
it or its wait times out.

The question itself is not lost. Its association with the conversation is
missing from the store. The emitted `blocking_human_feedback` event includes
the session ID, so an event-driven UI can still display the question. Event
visibility does not establish that the session-scoped reply API can answer it.

Affected paths:

- `BaseOrchestrator.RequestHumanFeedback` (text / approval-and-feedback).
- `BaseOrchestrator.RequestYesNoFeedback` (yes/no).
- `BaseOrchestrator.RequestMultipleChoiceFeedback` (choices).
- Interactive `human_input` plan steps that use those helpers, including
  existing yes/no and multiple-choice steps.
- Interactive human-routed branches that use `RequestMultipleChoiceFeedback`.

The separate agent tool `human_feedback` already calls `CreatePendingRequest`
with the session ID from its execution context. This ticket concerns the
orchestrator helpers, not every question in the product. Pre-filled or
unattended paths that do not invoke a feedback helper do not reproduce it.

## Root cause

The helpers receive `sessionID` but register through:

```go
feedbackStore.CreateRequestWithoutNotification(requestID, question)
```

That wrapper does not accept session metadata. It forwards:

```go
return s.CreatePendingRequest(
    uniqueID, message, "", "", nil, true, defaultHumanFeedbackTimeout,
)
```

The fourth argument is the empty session ID. Choices and response policy are
also discarded: the store receives no options and `AllowFeedback=true`.
The later UI event carries the session and choices, but does not update the
store record.

`PendingForSession` filters on `request.SessionID == sessionID`.
`SubmitResponseForSession` atomically enforces the same binding before waking
the waiter. Those checks are correct and must remain in place.

There is a related timeout mismatch in the same registration path: the
wrapper uses the five-minute `defaultHumanFeedbackTimeout`, while the
orchestrator helpers wait for ten minutes. Fixing only the session ID would
still cause the external API to reject answers after five minutes while the
orchestrator continues waiting.

Code locations (line numbers at review):

- [base_orchestrator_feedback.go](../../../../agent_go/pkg/orchestrator/base_orchestrator_feedback.go): registrations at 31, 114, 182; first ten-minute wait at 70.
- [human_feedback_store.go](../../../../agent_go/cmd/server/virtual-tools/human_feedback_store.go): wrapper at 69; stored session/options/expiry at 96–105.
- [external_feedback.go](../../../../agent_go/cmd/server/virtual-tools/external_feedback.go): session filtering at 14; atomic submission at 37.
- [external_run.go](../../../../agent_go/cmd/server/external_run.go): pending-input projection at 368; response submission at 427.
- [controller_human_input.go](../../../../agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_human_input.go): helper invocation for interactive input.
- [controller_branch_human.go](../../../../agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_branch_human.go): human branch uses the multiple-choice helper.

## Reproduction and evidence

Reproduced on 2026-09-28 with a temporary Go test overlay; no application
source changes or model calls were required.

1. Construct a `BaseOrchestrator` with a recording event listener.
2. In a goroutine, call `RequestHumanFeedback` with a unique request ID,
   question `Which city?`, session `review-session`, and workflow
   `review-workflow`.
3. Wait until `GetHumanFeedbackStore().ListPending(time.Now())` contains the
   request. Inspect its `SessionID` and expiry.
4. Call `PendingForSession("review-session", time.Now())`.
5. Call `SubmitResponseForSession("review-session", requestID, "Mumbai", time.Now())`.
6. For test cleanup only, submit an unscoped response with `SubmitResponse`
   and join the goroutine. Do not use unscoped submission as the external fix.

Observed failure from `TestReviewWorkflowInputIsExternallyAnswerable`:

```text
actual binding="" expiry_remaining=5m0s
workflow question not exposed/answerable through external session API:
pending=0 reply=feedback request is not pending for this session
```

Control test: a real Streamable HTTP MCP client against the external handler
successfully retrieved and answered a manually registered, session-bound
question. It rejected an invalid choice and a duplicate submission, and the
waiter received `Mumbai`. This isolates the defect to question registration;
the basic MCP transport and scoped response mechanism work.

Existing `TestRequestHumanFeedbackEmitsResolutionMarkerWhenAnswered` passes,
but answers through unscoped `SubmitResponse`, so it does not catch this bug.
Existing external feedback tests manually create correctly bound requests,
which also bypasses the faulty registration helper.

Evidence is local and deterministic. A live ChatGPT/Cowork/Claude Code
workflow reproduction and a production incident are not claimed.

## Proposed fix and acceptance

Use metadata-preserving registration in all three orchestrator helpers:
pass the trusted session ID, actual choices and free-text policy, context,
and the same timeout used by the waiter. Keep the existing UI event and
answer-parsing behavior compatible.

- [ ] Text, yes/no, and multiple-choice helper requests appear under their
  originating session in `run_status.pending_inputs`.
- [ ] The correct authorized session can answer each request through
  `run_reply_input`, and the blocked helper receives the intended response.
- [ ] An interactive human-routed branch uses the selected route after the
  answer; existing pre-filled/unattended behavior is preserved.
- [ ] Other sessions, users, and unauthorized token grants remain unable to
  inspect or answer the request. Do not weaken external ownership checks.
- [ ] Choices, labels, and free-text policy agree between the store and UI
  event; invalid choices are rejected before completing the request.
- [ ] One answer wakes the waiter once; duplicate and expired replies are
  rejected consistently.
- [ ] Store expiry and wait duration match; there is no five-minute period
  where the API considers the question expired but the helper still waits.
- [ ] Regression tests originate questions through the real orchestrator
  helpers, rather than only seeding `CreatePendingRequest` directly.
- [ ] At least one integration test covers the external MCP `call_tool` →
  `run_status` → `run_reply_input` path with a helper-created request.

Separate review findings
(workflow Ask's `wfask-` vs. `pat-` session authorization, crew-call reply
routing, cancellation, durable recovery, elicitation, and push) are outside
this ticket. Fixing this registration bug alone does not complete those
features.

Related: [PLAT-295](plat-295.md) introduced human-routed branches using the
affected helper. [PLAT-001](plat-001.md) concerns propagation of pre-supplied
`human_inputs` into child steps, a different failure mechanism.

## Fix (2026-09-28)

Fixed in `ca65b9886`. The three orchestrator helpers now register with the
trusted session ID, their real choices and free-text policy (yes/no and
multiple choice accept only their labels), and a store expiry equal to the
helper's wait. The session-scoped checks in `PendingForSession` and
`SubmitResponseForSession` are unchanged.

Tests ask through the real helpers and answer through the session API and
through `run_status` / `run_reply_input`:
`base_orchestrator_feedback_test.go`, `external_run_human_input_test.go`.

Not yet deployed to RTS. PLAT-367 (the `wfask-` vs `pat-` session check) is
separate and still open.
