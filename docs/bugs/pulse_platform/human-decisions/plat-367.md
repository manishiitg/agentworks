[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-367 — Direct workflow Ask session replies fail the external ownership check

| Coordination | Value |
|---|---|
| State | Mitigated by call-scoped reply in [PR #246](https://github.com/manishiitg/agentworks/pull/246); direct session route still rejects `wfask-` |
| Date | 2026-09-28; updated 2026-09-29 |
| Priority | P1 for external interactive Ask |
| Owner | human-decisions; implementation unassigned |
| Related subsystems | workflow functions, external MCP/CLI |
| Reviewed revision | `03e6844cf` merge commit on main |

## Problem and cause

A user calls a workflow's built-in `ask` function through an external
connection. The workflow assistant runs in a continuing `wfask-…` session.
If it requests blocking human input, its original caller cannot answer
through `run_reply_input`: token-authenticated requests must name a session
starting with `pat-<token-id>-`. `run_status` has the same prefix check.

The supported function-call route now uses `get_workflow_function_call` and
`reply_workflow_function_call` with the server-issued `call_id`, which maps to
the question's operation ID. It does not pass a `wfask-` session ID through
the PAT run-session endpoint. This removes the original caller-facing route
gap for workflow Ask in code, but a real suspended Ask through external REST
and MCP still needs verification. The direct session API remains restricted;
its 404 for `wfask-` is no longer evidence that the call-scoped route fails.

This is distinct from [PLAT-365](plat-365.md). Correctly registering the
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

The authorized call handle is implemented by [PLAT-369](../integrations/plat-369.md).
Keep the existing PAT session guard. Decide whether separate connections
should share a user-level Ask thread or require separate conversations; the
current caller identity shares it per user.

- [x] The call-scoped workflow poll/reply route exists and checks the caller's
  user, target and current workflow write access before accepting an answer.
- [ ] A real suspended workflow Ask resumes with an answer submitted through
  that external REST or MCP route.
- [ ] Another user, unauthorized workflow grant, or unrelated call cannot
  use a supplied `wfask-…` ID to inspect or answer a question.
- [ ] Existing `pat-…` run-session isolation remains covered by tests.
- [ ] Two successive Ask calls retain the intended conversation continuity.
- [ ] A regression covers a real pending question and the external MCP or
  REST route, in addition to the focused prefix-rejection probe.

The call-scoped path is merged on main. Deployment and live Ask verification
are not claimed here. The original direct-session rejection remains recorded
so future clients do not mistake `run_reply_input` for the workflow Ask reply
tool.
