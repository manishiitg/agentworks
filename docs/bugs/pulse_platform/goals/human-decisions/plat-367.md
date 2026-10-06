[← goals / human-decisions](index.md)

# PLAT-367 — Workflow Ask sessions fail the external status and answer ownership check

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | goals |
| Area | human-decisions |
| Summary | A user calls a workflow's built-in `ask` function through an external |

| Coordination | Value |
|---|---|
| State | Open — reply rejection reproduced locally; not fixed |
| Date | 2026-09-28 |
| Priority | P1 for external interactive Ask |
| Owner | human-decisions; implementation unassigned |
| Related subsystems | workflow functions, external MCP/CLI |
| Reviewed revision | `ebc37cf87` on main, including the local changes present during review |

## Problem and cause

A user calls a workflow's built-in `ask` function through an external
connection. The workflow assistant runs in a continuing `wfask-…` session.
If it requests blocking human input, its original caller cannot answer
through `run_reply_input`: token-authenticated requests must name a session
starting with `pat-<token-id>-`. `run_status` has the same prefix check.

This is distinct from [PLAT-365](../../crew/human-decisions/plat-365.md). Correctly registering the
question's session ID does not make that session acceptable to these APIs.
It is also distinct from an ordinary clarification in a final assistant
reply, which can be answered with another Ask call.

Evidence:

- [workflow_ask.go](../../../../agent_go/cmd/server/workflow_ask.go): `workflowAskSessionID` at 46 and `runWorkflowAsk` at 65.
- [external_run.go](../../../../agent_go/cmd/server/external_run.go): status prefix check at 343; reply prefix check at 413.
- [external_crews.go](../../../../agent_go/cmd/server/external_crews.go): `externalCrewCaller` identifies the user, not a particular connection, at 31.

## Reproduction

1. Create a token-authenticated caller with user `owner` and `runs:execute`.
2. Derive its workflow Ask session with
   `workflowAskSessionID("invoices", externalCrewCaller(claims).Stamp)`.
3. Register that session as an active workflow assistant session owned by
   `owner` and bound to `Workflow/invoices`.
4. Call `externalRunReplyInput` as the same caller with that session ID.
5. Expected: the owner's Ask session reaches question validation. Actual:
   HTTP 404 at the session-prefix check, before question lookup.

`TestReviewWorkflowAskCanUseRunReplyInput` reproduced:

```text
same caller cannot use existing reply endpoint for its workflow ask session
wfask-9747c3234b8d250cb1b95160:
{"error":{"code":"session_not_found",
 "message":"This access token does not own that run session."}}
```

This is a focused authorization-path reproduction, not a model-driven live
Ask run. The corresponding status rejection follows from the same code check.

## Proposed fix and acceptance

Resolve external Ask interactions through an authorized call handle that
maps to the actual assistant session, or persist an explicit external
session ownership record. Do not remove the existing token/session guards.
Decide whether separate connections share a user-level Ask thread or require
separate conversations; the current caller identity shares it per user.

- [ ] The authorized caller can inspect and answer a pending input from its
  workflow Ask execution, and the waiting execution receives the answer.
- [ ] Another user, unauthorized workflow grant, or unrelated call cannot
  use a supplied `wfask-…` ID to inspect or answer a question.
- [ ] Existing `pat-…` run-session isolation remains covered by tests.
- [ ] Two successive Ask calls retain the intended conversation continuity.
- [ ] A regression covers a real pending question and the external MCP or
  REST route, in addition to the focused prefix-rejection probe.

No fix or deployment has started. Coordinate with
[PLAT-369](../../crew/integrations/plat-369.md) for the common call-scoped reply path;
keep this reproduced ownership mismatch independently tracked.
