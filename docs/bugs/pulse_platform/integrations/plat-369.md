[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-369 — External crew/function polling has no complete pending-question and reply route

| Coordination | Value |
|---|---|
| State | Implemented on `codex/plat-369-call-replies`; pending review and merge |
| Date | 2026-09-28 |
| Priority | P2; prerequisite for bidirectional function/Ask support |
| Owner | integrations; implementation unassigned |
| Related subsystems | crews, workflow functions, human-decisions |
| Reviewed revision | `ebc37cf87` on main, including the local changes present during review |

## Original missing behavior

`ask_crew`, `call_crew_function`, and `call_workflow_function` produce a
pollable call ID. Their external pollers return status, progress, run ID, and
results, but do not expose structured pending human questions. There is no
external call-scoped answer operation. A caller can see that work is still
running without being told that a human answer is needed or how to provide it.

The existing workflow-session `run_status`/`run_reply_input` pair covers a
different scope. Crew trigger-run IDs are not generally chat-session IDs;
workflow Ask also has the independently reproduced ownership mismatch in
[PLAT-367](../human-decisions/plat-367.md).

This ticket records a missing integration capability, not a claim that
ordinary Ask replies fail. A final free-text clarification followed by a new
Ask call already supports a conversational exchange. The missing route is
for questions that suspend active execution.

## Original code evidence

- [crew_functions.go](../../../../agent_go/cmd/server/crew_functions.go): `snapshot` at 312 has no pending-input projection; `crewTargetRunSessionID` at 820 demonstrates the crew run/session distinction.
- [external_crews.go](../../../../agent_go/cmd/server/external_crews.go): `externalCrewCallResponse` at 48 returns the snapshot and a generic polling instruction.
- [external_workflow_functions.go](../../../../agent_go/cmd/server/external_workflow_functions.go): workflow polling uses the same response machinery.
- [external_tools.go](../../../../agent_go/cmd/server/external_tools.go): exposed external tool catalog.

The original report used static path/catalog inspection. The branch tests
below exercise the suspended Ask paths with a controlled turn and the real
REST/MCP dispatch, without a live model or third-party client.

## Proposed implementation and acceptance

Introduce a common internal interaction resolver:

```text
authenticated call_id → authorized target → actual execution session(s)
                     → pending request_id → validated answer → existing waiter
```

Expose pending questions on the applicable pollers and add a call-scoped
reply tool. Each question should have a stable request ID, prompt, choices,
free-text policy, expiry, and an explicit reply operation. Determine child
execution ownership from trusted runtime records, not caller-supplied session IDs.

- [x] A crew Ask, typed crew function, workflow Ask, and typed workflow
  function can each expose and answer a question when their execution asks one.
- [x] A question arriving after the initial 25-second wait is discoverable
  through polling without restarting or duplicating the call.
- [x] Concurrent child questions remain distinct and answers reach only the
  intended execution.
- [x] Invalid, duplicate, expired, cancelled, and foreign replies are handled
  consistently by the common backend.
- [x] Current user, token scope, target kind, and target access are checked;
  [PLAT-366](../security-sandbox/plat-366.md) is fixed before reusing the crew poll authorization.
- [x] Tests cover actual external REST and MCP dispatch with suspended
  execution, not only manually constructed snapshots.

The polling backend is the first deliverable. Native MCP elicitation and
Claude Code Channels can later adapt this same API; neither is necessary to
validate the common question/reply flow. Unsolicited idle-chat delivery and
restart recovery are not acceptance requirements here; see
[PLAT-370](../human-decisions/plat-370.md) for recovery.

## Implementation under review

The call pollers now return `pending_inputs` and `needs_user_input`. The
`reply_function_call_input` tool accepts `call_id`, `request_id`, and `response`
over both REST and MCP. It resolves typed runs through trusted run records,
checks current target access and run scope, and submits to the existing
feedback waiter. Built-in Ask calls to the same continuing session are
serialized; if overlapping calls could own one question, neither call may
claim it. Pending feedback remains process-local; restart recovery is PLAT-370.

Focused external API, feedback-store, and orchestrator tests pass, including
actual suspended Crew and workflow Ask turns, typed run-session mapping,
multiple pending questions, foreign and stale replies, and MCP dispatch. A
full server-package run has unrelated failures in catalog/template and prompt
size assertions; this change does not alter those fixtures.
