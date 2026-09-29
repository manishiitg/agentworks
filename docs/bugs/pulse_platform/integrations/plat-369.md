[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-369 — Call-scoped pending questions and replies for external functions

| Coordination | Value |
|---|---|
| State | Implemented on main in [PR #246](https://github.com/manishiitg/agentworks/pull/246); live external-client verification pending |
| Date | 2026-09-28; updated 2026-09-29 |
| Priority | P2; live acceptance remains |
| Owner | integrations |
| Related subsystems | crews, workflow functions, human-decisions |
| Reviewed revision | `03e6844cf` merge commit on main |

## What changed

The original gap was that `ask_crew`, `call_crew_function`, and
`call_workflow_function` returned a pollable call ID but their pollers did not
show questions suspending execution. The merged implementation uses a
server-stamped operation ID on each feedback request. The external
`get_crew_function_call` and `get_workflow_function_call` responses now include
`pending_inputs` with request ID, prompt, choices, free-text policy and expiry.
`reply_crew_function_call` and `reply_workflow_function_call` submit an answer
through the call ID, without asking the caller to supply a target session ID.
The same `get_function_call` / `reply_function_call` path is available to
internal Crew, workflow and private Code callers.

The backend validates the current user, target kind and access, required token
scope or workflow write access, request ownership by operation ID, expiry and
exact-choice answers. Duplicate and cross-call answers are refused. The
call-scoped path also bypasses the `wfask-` versus `pat-` session mismatch
described in [PLAT-367](../human-decisions/plat-367.md); it does not change
the older session-scoped `run_status` / `run_reply_input` rules.

Useful code anchors: `addFunctionCallPending` and `submitFunctionCallInput` in
[external_function_feedback.go](../../../../agent_go/cmd/server/external_function_feedback.go),
the external routes in [external_crews.go](../../../../agent_go/cmd/server/external_crews.go)
and [external_workflow_functions.go](../../../../agent_go/cmd/server/external_workflow_functions.go),
and trusted request lookup in
[external_feedback.go](../../../../agent_go/cmd/server/virtual-tools/external_feedback.go).

## Evidence and remaining acceptance

Focused tests cover Crew REST polling and replies with two concurrent calls,
wrong choices, a read-only token and duplicate answers
(`TestCrewFunctionQuestionIsBoundToCallAndRunScope`). They cover internal
Crew↔Crew, Crew↔workflow and workflow↔workflow reply tools, and access
revocation. These tests seed pending requests; they do not run a real target
turn that calls `ask_user` through an external MCP client.

- [x] The poller projects currently pending questions from the call's
  operation ID; repeated polling does not start another call.
- [x] Request IDs remain distinct across calls and child sessions; invalid,
  duplicate, expired and foreign replies are refused by the common backend.
- [x] Crew polling preserves the [PLAT-366](../security-sandbox/plat-366.md)
  target-kind and current-access boundary.
- [ ] Exercise a real Crew Ask, typed Crew function, workflow Ask and typed
  workflow function that suspend on `ask_user`, then answer each through the
  external REST and MCP routes. Verify the waiter resumes with that answer.
- [ ] Verify the same flow with a warm coding CLI and a third-party MCP client,
  including a question that arrives after the first polling wait.

Native MCP elicitation and Claude Code Channels can adapt the call-scoped API
later; neither is required for this polling/reply route. Restarting while a
question waits is still [PLAT-370](../human-decisions/plat-370.md).
