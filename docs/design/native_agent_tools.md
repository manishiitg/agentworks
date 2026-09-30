# Native agent tools (coding-agent tool modes)

Status: shipped 2026-09-24. All code is on main in mcpagent, multi-llm-provider-go
and this repo. Transport history is in
[PLAT-354](../bugs/pulse_platform/coding-agent-bridge/plat-354.html).

## Tool modes

A coding CLI runs in one of four tool modes, set by the agent profile's
`runtime.agent_tools.mode`:

| Mode | UI name | What the CLI gets |
|---|---|---|
| `mcp_only` | off | Native web search only. Everything else goes through the MCP bridge. |
| `hybrid` | **Native agent tools** | The bridge, plus the CLI's allowed native reads, searches and support tools. The allowlist varies by provider. |
| `full` | **Full CLI** | MCP plus native reads, edits, shell and delegation under an enforced Linux Landlock policy. |
| `full_unconfined` | Local Full CLI | MCP plus the full native toolset on an explicitly opted-in single-user host. |

Native writes remain denied in `mcp_only` and `hybrid`. Full CLI permits native
writes, commands and delegation. Local Full CLI uses
`AGENTWORKS_CLI_FULL_UNCONFINED=on`, requires single-user mode and upgrades only
chats with Native agent tools already enabled. AGY's local integration and
certification scope are in [AGY Full CLI](agy_full_native_tools.md).

Why hybrid exists: models did worse with their own tools off. A Muse log audit
found 28 `read_file`, 16 `read_skill` and 12 `search` denials. The CLIs also
handle long tasks better with their own todo lists and background subagents.

## What hybrid enables, per CLI

| CLI | Enabled in hybrid | Denied in hybrid |
|---|---|---|
| Claude Code | `Read`, `Grep`, `Glob`, `Skill`, `Agent`, `TaskCreate/Get/Update/List`, `TodoWrite`, `WebFetch`, `WebSearch` | `Bash`, `Write`, `Edit`, `MultiEdit`, `NotebookEdit` |
| Muse | `read_skill`, `read_file`, `search`, `subagent_*` (delegation is on when `subagent_spawn` is allowed); `write_todos` works in both modes | shell, file writes |
| Codex | shell and `multi_agent` only. Every other native feature is disabled (browser, computer use, image generation…). Todos via `update_plan`. | Writes: Codex's sandbox is `read-only` in hybrid, so `apply_patch` and shell writes fail |
| Cursor | `Read`, `List`, `ListDir`, `Glob`, `Grep`, `Search` | `Write`, `Edit`, `Delete`, shell, `task`/subagents, `ComputerUse`, `RecordScreen`, `generateImage` (hooks deny them) |
| Pi | nothing; Pi stays bridge-only in hybrid | everything native |
| AGY | `view_file`, `list_dir`, `find_by_name`, `grep_search`, `search_web`, `read_url_content` | native writes, edits, shell and subagents |

Where the lists live:

- Claude: `claudeHybridNativeTools` in mcpagent `agent/coding_agent_integrations.go`
- Codex: `WithCodexReadOnlyHybridTools` (same file) and `CodexReadOnlyHybridDisabledFeatures` in multi-llm-provider-go `pkg/adapters/codexcli/options.go`
- Muse: `museHybridNativeTools` in mcpagent `agent/coding_agent_options.go`
- Cursor: `cursorReadOnlyHybridAllowedTools` in multi-llm-provider-go `pkg/adapters/cursorcli/cursorcli_interactive_adapter.go`

`api_transport: native_shell` is still rejected by `validate.go`.

## On by default (since 2026-09-25)

**Native agent tools is on for every workflow and crew** unless its owner turns
it off (user decision, 2026-09-25). `capabilities.native_agent_tools` is a
tri-state: unset or `true` means on, only an explicit `false` means off. The
server reads it through `nativeAgentToolsEnabled` (`workflow_manifest.go`), the
frontend through `utils/nativeAgentTools.ts`. Before this, unset meant off and
turning the switch off deleted the key, so an earlier "off" was not recorded and
those workflows and crews are now on.

## Plain chats and Code (since 2026-09-29)

- A plain AgentWorks chat (no workflow, no product profile) runs with native
  agent tools for every turn type, except read-only principals
  (`plainChatNativeAgentTools`, `workflow_chat_policy.go`).
- Code has the same switch as a Crew (**Models → Agent tools → Native agent
  tools**), on unless its owner turns it off; everyone who may chat with the
  Code gets it.
- Known exposure until the coding CLIs run under Landlock (PLAT-364 part 2):
  native reads can reach files outside the workflow, Crew or Code, and a run
  on someone else's shared provider account can read that account's login
  files. Confining the CLIs is the follow-up that closes both.
  The plan (lock every CLI, CLI-login accounts, login proxy, then **Full CLI**
  on Code) is in [PLAT-364](../bugs/pulse_platform/security-sandbox/plat-364.md#plan-2026-09-29-lock-every-cli-then-full-cli-on-code).
  The leak and the lock were both shown live on RTS on 2026-09-29.

## The crew switch

Crew page → **Models** tab → **Native agent tools** toggle. This writes
`capabilities.native_agent_tools: true|false` to the crew's `workflow.json`.
`resolveAgentProfileForQuery` (`agent_go/cmd/server/agent_profile_runtime.go`)
then sets `hybrid` on a copy of the resolved profile, and only when the owner
runs the crew. Live-checked on and off through `/api/agent-profiles/work/query`.

## The workflow switch

A workflow has the same switch: **Identity → Models → Agent tools → Native
agent tools**, stored as `capabilities.native_agent_tools` in `workflow.json`.
Since 2026-09-29 (owner decision: "only off for workflow steps") it applies
to **every turn type** of the workflow's conversation: interactive Builder and
Run chats, schedules, webhooks and triggers, auto-notifications, Pulse turns,
and Slack and WhatsApp turns (DMs and routes). Only these keep AgentWorks-only
tools:

- the plan's step agents (child sessions of a run, and the agents the
  orchestrator builds with the structured transport);
- read-only principals (viewers, read-grant bot routes).

Step agents are limited to their own folders, and the CLI's native file
reading is not bound by those limits. Flipping the switch starts a fresh CLI
session on the next message, carrying the recent dialogue, as for a Crew.
Code: `workflowChatNativeAgentTools` (`workflow_chat_policy.go`).

## Background subagents

With subagents on, both Claude and Muse used to end a turn early with an interim
"waiting for the subagent" reply. Completion now waits on structured records:

- Claude: the transcript's `turn_duration` row must report
  `pendingBackgroundAgentCount` 0.
- Muse: the adapter follows `spawn_accepted` → `inbox_item_queued` → the run that
  drains it → that run's terminal, and reads the answer from the last run. If the
  log stops growing for 5 minutes, it stops waiting.

AGY local Full CLI waits for each launched native child's completed conversation
record and its notification in the parent conversation before accepting the
parent's final answer. An idle terminal alone does not prove completion.

## Tests

Live tests (real CLIs):

- Claude: `HybridNativeToolsP0`, `HybridBackgroundAgentP0`
- Muse: `ReadOnlyToolsAllowed`, `ProjectedSkillReadNatively`, `SubagentContainment`, `BackgroundSubagentNoEarlyAnswer`
- Codex: `TestCodexCLIRealReadOnlyHybridP0`, plus its subagent P0
- Cursor: `TestCursorCLIRealReadOnlyHybridP0`, `BlocksDelete`

Each hybrid test also checks that no native write ran. AGY adds
`TestAgyCLIRealFullNativeToolsExec` and
`TestAgyCLIRealFullNativeToolsInteractive` for actual native edits, commands,
subagents and MCP, including retained-session continuation.

Stress tests are opt-in: `-coding-cli-stress`, with `CODING_CLI_STRESS_ITERATIONS`
setting the count. Each iteration runs parallel subagents, todos, a slow MCP tool
and a mid-turn steer. Results on 2026-09-24: Claude 3/3, Codex 3/3, Muse 3/3,
Cursor 5/5.
