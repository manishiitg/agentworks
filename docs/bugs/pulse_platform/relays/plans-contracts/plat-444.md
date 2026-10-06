# PLAT-444 — A Relay can advertise a script tool that registration drops

| Field | Value |
|---|---|
| State | closed |
| Priority | P2 |
| Product | relays |
| Area | plans-contracts |
| Summary | open. |

Status: fixed on `main`; not deployed. Reproduced in an isolated local worktree on main f38701175.
Priority: P2.
Related: PLAT-432, PLAT-441.

## Finding

`ValidateRelayPlanStructure` admits script routes whose normalized tool name
collides with a platform tool or another route. `scriptedRouteDirectTools`
silently skips a reserved name; its warning says `call_scripted_sub_agent`
still reaches the route. PLAT-441 removes that fallback from authored agents.
`authoredRoutesPromptBlock` independently advertises every scripted route using
its normalized name, including a skipped one. The agent can then call the
platform tool under the name instead of its script, or cannot call its intended
script at all. A successfully published graph does not guarantee all its tool
routes are reachable.

## Evidence

A temporary focused Go reproduction used the existing valid Relay graph:

- Attach an authored agent's saved script route with matching route/step id
  `execute-shell-command`, regular type and `script_only: true`.
- `ValidateRelayPlanStructure` succeeds.
- Registration with the actual platform name `execute_shell_command` reserved
  returns no script tool.
- The authored prompt block still advertises `execute_shell_command`, and the
  delegation conversion preserves AuthoredPrompt (so no generic fallback is
  installed).

The same normalization can collapse two distinct route IDs (such as `a-b` and
`a_b`) into a single tool name. No model invocation is needed to reproduce it.

## Needed

Validate normalized names and collisions, or assign stable unique names. Fail
registration explicitly for an authored agent if one of its tools cannot be
registered. Render its prompt from the actual admitted tool names. Verify both
platform-name and route-to-route collisions while retaining the original
workflow compatibility behavior where a generic fallback exists.

## Fix (2026-10-04)

- Registration records the tool name actually registered for each route
  (`SubAgentExecutionContext.ScriptToolNames`). For an authored agent
  (`AuthoredPrompt`) a route that cannot be registered (name taken by a platform
  tool or another route, or an invalid contract) is now an explicit error that
  fails the agent's creation; an ordinary workflow keeps the old skip, still
  reachable through `call_scripted_sub_agent`.
- `authoredRoutesPromptBlock` renders only registered tools, by their registered
  names; nothing registered means no block.
- `ValidateRelayPlanStructure` rejects two routes of one agent that normalize to
  the same tool name (`a-b` and `a_b`).
- Tests: platform-name and route-to-route collisions fail for an authored agent,
  an ordinary workflow keeps the skip, the prompt lists only registered tools,
  and Relay validation refuses colliding route ids.

Not done: a collision with a platform tool name is detected at registration (the
run/test fails clearly), not at publish, because the platform tool list is not
available to Relay plan validation.

## Register notes

[PLAT-444](plat-444.md), P2, open. Reproduced:
a valid authored Relay advertises a normalized script name that collides with a
platform tool; its generic route fallback was removed by PLAT-441.
