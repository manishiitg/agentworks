[← platform / security-sandbox](index.md)

# PLAT-355 — Scripted-step tool calls borrowed another run's executor

| Field | Value |
|---|---|
| State | open |
| Priority | P0 |
| Product | platform |
| Area | security-sandbox |
| Summary | fixes the intermittent `caller does not own this tool session` left open by PLAT-338. |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `fixed; live-verified 2026-09-23` |
| Last synchronized | `2026-09-23` |
| Priority | `P0 execution` |
| Category | security-sandbox (runner-up: step-execution) |

## Symptom

A scripted step or a delegated call occasionally fails with
`<tool> caller does not own this tool session`, and then passes on retry.
Two cases were seen on 2026-09-23:

- `websiteaeo`, step `collect-goal-observations`, 11:08:26:
  `query_workflow_db caller does not own this tool session`. The self-repair
  wrapped the script's tool helper in six-attempt retries, which hides the
  bug but does not fix it.
- `substack`, 11:01:58: `get_human_input_request` failed 16 times in a row.

Both failures came minutes after the scheduled runs of `salesoutreach`,
`substack` and `social-media` started at 11:01:43–59.

## Cause

This is the variant of [PLAT-338](../scheduler-runs/plat-338.md) that PLAT-338
left open. PLAT-338 taught the ownership check to accept a registered
`session-group-*` child of the parent run. But the child never reached the
parent's executor.

1. A workflow script calls platform tools through its `session-group-<group>-<ns>`
   bridge session. No agent runs under that ID, so no per-session tool registry
   is ever created for it.
2. `codeexec.CallCustomToolWithSession` finds no registry for the session,
   so it falls back to the **global** `customTools` map. The log shows
   `Session registry not found, falling back to global`, then
   `Using global custom tool (no session scope)`.
3. `InitRegistryWithVirtualTools` replaces every global executor whenever any
   agent starts, so the last agent to start wins. Each executor is a closure
   from `bindToolExecutionContextForSession` and is bound to the session
   that registered it.
4. When a concurrent run registered last, the executor's bound session is
   not the child's parent, so the ownership check rejects the call. That is
   correct behaviour, but it makes the feature fail at random. A retry
   passes once the owning run happens to register again.

## Fix

`mcpagent/agent/codeexec/registry.go`: a new function,
`registryScopeForSession`. When a session has no registry of its own and
`mcpclient` knows it as the live child of a parent run (`RegisterHTTPSession`),
the call uses the parent's registry and allow list. That parent executor is
bound to the parent session, so PLAT-338's check admits the call with the
parent's identity.

The existing safeguards still hold:

- Unregistered, stopped and ambiguous children resolve to themselves, never
  by name, so they keep the legacy global path.
- A session that has its own registry is unchanged.
- The no-borrow rule for existing registries still holds.

### Refinement (mcpagent `af11626`)

A self-review found that the first version could turn a working call into a
failure, in two ways:

- It also applied the parent chat's per-turn allow list to the step script.
- It returned "not registered" for a tool the parent never registered, where
  the legacy lookup had resolved it (for example `record_goal_observations`
  or `get_human_input_request`).

The parent is now used only after the calling session's own allow-list check,
and only when the parent actually registered that tool. Otherwise the call
behaves exactly as before. `TestCallCustomToolWithSessionParentResolutionIsConservative`
fails on `445e64f` and passes on `af11626`.

## Regression coverage

`TestCallCustomToolWithSessionUsesRegisteredParentRegistry` (mcpagent
`agent/codeexec`) sets a different run's global executor and checks two cases:

- A registered child reaches its parent's executor.
- An unregistered child with a similar-looking name keeps the legacy path.

The test fails without the fix (`child call = "other-run"`) and passes with
it. The full `agent/codeexec` suite and the `virtual-tools` package are green.

`TestToolExecutionContextTopologyMatrix` (cmd/server) fails when run together
with other `ToolExecutionContext|WorkflowDB` tests, with or without this fix,
and passes on its own. It is an existing shared-state/ordering problem, not
caused by this change.

## Live acceptance

Verified on 2026-09-23 on the local server (restarted 11:51:44 with mcpagent
`445e64f`; the refinement is not live-run yet), in `websiteaeo` run `workflow-full-mudpu2g001`, launched from the
builder chat:

- Step `collect-goal-observations` is the scripted step that failed at 11:08.
  It ran at 12:05:19. Every tool call on its bridge session
  `session-group-default-1790144533809580000` logged `Resolved child session
  to its parent run's tool registry` (parent `6eaa17e1-…`).
- There were zero `falling back to global` lines and zero ownership errors.
- The script exited 0 on its first attempt in about 2s and passed
  validation. Its retry helper never retried.

## Follow-up

The six-attempt retry wrapper that the self-repair added to
`Workflow/websiteaeo/code/collect-goal-observations/main.py` is no longer
needed. `code-authoring.md` now tells scripts to fail fast on tool-boundary
errors instead of retrying them.

## Register notes

[PLAT-355](plat-355.md) fixes the
intermittent `caller does not own this tool session` left open by PLAT-338. A
scripted step's `session-group-*` bridge session has no tool registry of its
own, so `CallCustomToolWithSession` fell back to the global executor map. The
last agent to start overwrites that map, so a concurrently starting schedule
could leave it bound to another run. mcpagent now resolves a live, registered
child to its parent run's registry and allow list. Unregistered children keep
the legacy path. Live-verified 2026-09-23.
