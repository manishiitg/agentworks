[← platform / coding-agent-bridge](index.md)

# PLAT-397 — Full CLI uses its own subagents; run_in_background only for read-only reviewers and long loops

| Field | Value |
|---|---|
| State | deployed |
| Priority | - |
| Product | coding-agents |
| Area | bridge |
| Summary | fixed on `main`, not deployed: in-turn parallel work goes to the CLI's own subagents; `run_in_background` only for read-only reviewers and long supervision loops. |

| Coordination | Value |
|---|---|
| State | fixed on `main`; not deployed |
| Date | 2026-10-03 |
| Owner | coding-agent-bridge |
| Related | PLAT-390 (two modes), PLAT-396 (no bridge edit tool in full mode) |

## Decision (owner, 2026-10-03)

With native tools on, parallel work within a turn goes to the CLI's own
subagents. `execute_step` and `run_full_workflow` are already background calls.
`run_in_background` stays only for what native subagents cannot do: an
independent reviewer held read-only by the platform (`access_mode="read_only"`;
a native subagent inherits its parent's write rights), and a long supervision
loop that outlives the turn (steps it starts report back to it). Pulse's
reviewer keeps using it.

## Done

- Prompt section `native-subagents` (Full CLI chats only) says so; the
  `run_in_background` description in workflow-tools names the same scope.

## Found in owner testing (2026-10-04)

- Codex got the section but ran a read-only plan review on its own
  `spawn_agent` ("where it is available" left room; in a CLI the tool is
  reached through search_tools). The rule is now firm: a review that must not
  change anything goes to `run_in_background` with `access_mode="read_only"`,
  never the CLI's own subagents, with how to find the tool.

## Left

- Nothing; removal was considered and rejected for the two uses above.

## Register notes

[PLAT-397](plat-397.md), fixed on `main`, not
deployed: in-turn parallel work goes to the CLI's own subagents;
`run_in_background` only for read-only reviewers and long supervision loops.
