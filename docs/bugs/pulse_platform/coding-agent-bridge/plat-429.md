[← Platform issue index](../../pulse_platform_issue_register.md)

# PLAT-429 — A Codex chat in Code said it could not open a panel because "the UI control tools aren't available"

| Coordination | Value |
|---|---|
| State | fixed on `main` (skill and guidance wording); deploy pending |
| Severity | P3 (the agent refused a supported action until the owner asked "did you check the API spec") |
| Date | 2026-10-04 |
| Owner | coding-agent-bridge |
| Related | PLAT-396 (full mode), the `workspace-ui` feature |

## Problem

In a Code chat on Excellence with Codex (full native tools), asked to open the Costs panel, the agent replied: "I can't open it from this chat because the UI control tools aren't available here".
The tools were available: the server registered `list_ui_capabilities`, `get_ui_state` and `perform_ui_action` for the session (the tool gate lists them), and after the owner asked "did you check the API spec" Codex found them through the API discovery tools.

## Cause

A coding-CLI chat has only a few direct tools (here `agent_browser`, `execute_shell_command`, `get_api_spec`, `search_tools`); every other platform tool is reached through the API bridge (`search_tools`, then `get_api_spec`, then HTTP).
The shared `work-ui-control` skill (rendered for Crew as `work-ui-control` and for Code as `code-ui-control`) said "use these only in a chat that actually exposes them" and never said how to reach them. Codex checked its direct tool list (`ALL_TOOLS`), found no match and concluded they were missing.

## What a Code chat with Codex gets as its prompt

Codex receives the platform's prompt through the project's `AGENTS.md` (about 10 KB, delivered as the first user message and in `world_state`) next to Codex's own developer messages (about 7 KB plus a skills list).
`AGENTS.md` does contain the bridge facts: an "IMPORTANT — bridge tool routing" block (direct runtime tools: `search_tools`, `get_api_spec`, `execute_shell_command`, `agent_browser`, `read_image`, `read_skill`; other tools are found with `search_tools`).
The ambiguous line was in the product section "Platform actions": "Discover tools through the current runtime's tool search" (Crew: "Use the current runtime's tool discovery"). For Codex that reads as its own tool search (`ALL_TOOLS`), which is what it used.

## Fix

The 2026-10-01 prompt reduction (DECISIONS "Keep prompt contracts upfront and load procedures through skills"; Code -21%, Crew Builder -40%, workflow chats about -40%) gave discovery ONE owner: mcpagent's runtime block "bridge tool routing"
(`coding_agent_bridge_routing_prompt.go`: direct runtime tools, "find other tools with search_tools", schema and route via `get_api_spec`), with a short pointer in each product prompt. The failing text was a pointer that contradicted the owner.
So the fix keeps one owner and only corrects the pointers, in as few words as possible:
- Owner (mcpagent, all CLIs): the sentence above, once.
- Code and Crew system prompts: "Find platform tools with `search_tools` (see bridge tool routing); a tool missing from your own tool list is not missing." (Code prompt 2076 -> 1826 bytes, Crew 4178 -> 3965, smaller than before the incident.)
- The shared `work-ui-control` skill (Crew and Code): two lines saying these are bridge tools found with `search_tools(query="ui")`.
- Tests: `TestCodePromptNamesTheBridgeToolDiscovery`, `TestCrewPromptNamesTheBridgeToolDiscovery` and the skill expectations in `product_config_test.go`.
A first version repeated the full explanation in four places (both prompts, the skill, the Workflow guidance); it was cut back to the pointers above because it went against "one owner for transport instructions".

## What a Code chat with Codex gets as its prompt

Codex receives the platform's prompt through the project's `AGENTS.md` (about 10 KB, first user message and `world_state`) next to Codex's own developer messages. `AGENTS.md` holds the runtime block. The ambiguous pointer was in the product section "Platform actions"
("Discover tools through the current runtime's tool search"; Crew: "Use the current runtime's tool discovery"). For Codex that reads as its own tool search (`ALL_TOOLS`), which is what it used.

## Review of the other prompts (2026-10-04)

Only the Code and Crew prompts used the ambiguous pointer. Video Studio and Dominion prompts refer to `get_api_spec` with exact tool names (the catalog of names is still provided there, as the reduction decision requires); the Workflow guidance templates and Relays rely on the runtime block;
the only other "tool search" wording is about step tool selection in `optimize-playbook.md`, unrelated.

## Left

- Deploy; then ask Codex, Claude and Muse in a Code chat to open the Costs panel and check each finds and calls `perform_ui_action` through the bridge.
- Relays has no contract of its own for these tools: it reuses the Workflow views, and its right panel (workflow panel on, files panel off) may not match them; not checked in the UI.
- Other platform tools have the same shape (a CLI that only checks its direct tool list will think they are missing); the general bridge guidance already says to use `search_tools`.
