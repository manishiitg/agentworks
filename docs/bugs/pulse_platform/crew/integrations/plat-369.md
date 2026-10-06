[← crew / integrations](index.md)

# PLAT-369 — External crew/function polling has no complete pending-question and reply route

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | crew |
| Area | integrations |
| Summary | `ask_crew`, `call_crew_function`, and `call_workflow_function` produce a |

| Coordination | Value |
|---|---|
| State | Open — code-reviewed implementation gap; not implemented |
| Date | 2026-09-28 |
| Priority | P2; prerequisite for bidirectional function/Ask support |
| Owner | integrations; implementation unassigned |
| Related subsystems | crews, workflow functions, human-decisions |
| Reviewed revision | `ebc37cf87` on main, including the local changes present during review |

## Missing behavior

`ask_crew`, `call_crew_function`, and `call_workflow_function` produce a
pollable call ID. Their external pollers return status, progress, run ID, and
results, but do not expose structured pending human questions. There is no
external call-scoped answer operation. A caller can see that work is still
running without being told that a human answer is needed or how to provide it.

The existing workflow-session `run_status`/`run_reply_input` pair covers a
different scope. Crew trigger-run IDs are not generally chat-session IDs;
workflow Ask also has the independently reproduced ownership mismatch in
[PLAT-367](../../goals/human-decisions/plat-367.md).

This ticket records a missing integration capability, not a claim that
ordinary Ask replies fail. A final free-text clarification followed by a new
Ask call already supports a conversational exchange. The missing route is
for questions that suspend active execution.

## Code evidence

- [crew_functions.go](../../../../agent_go/cmd/server/crew_functions.go): `snapshot` at 312 has no pending-input projection; `crewTargetRunSessionID` at 820 demonstrates the crew run/session distinction.
- [external_crews.go](../../../../agent_go/cmd/server/external_crews.go): `externalCrewCallResponse` at 48 returns the snapshot and a generic polling instruction.
- [external_workflow_functions.go](../../../../agent_go/cmd/server/external_workflow_functions.go): workflow polling uses the same response machinery.
- [external_tools.go](../../../../agent_go/cmd/server/external_tools.go): exposed external tool catalog.

Verification was static path/catalog inspection. No end-to-end crew Ask
suspension was exercised against a live model or third-party client.

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

- [ ] A crew Ask, typed crew function, workflow Ask, and typed workflow
  function can each expose and answer a question when their execution asks one.
- [ ] A question arriving after the initial 25-second wait is discoverable
  through polling without restarting or duplicating the call.
- [ ] Concurrent child questions remain distinct and answers reach only the
  intended execution.
- [ ] Invalid, duplicate, expired, cancelled, and foreign replies are handled
  consistently by the common backend.
- [ ] Current user, token scope, target kind, and target access are checked;
  [PLAT-366](../security-sandbox/plat-366.md) is fixed before reusing the crew poll authorization.
- [ ] Tests cover actual external REST and MCP dispatch with suspended
  execution, not only manually constructed snapshots.

The polling backend is the first deliverable. Native MCP elicitation and
Claude Code Channels can later adapt this same API; neither is necessary to
validate the common question/reply flow. Unsolicited idle-chat delivery and
restart recovery are not acceptance requirements here; see
[PLAT-370](../../goals/human-decisions/plat-370.md) for recovery.
