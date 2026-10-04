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

The skill gets a section "Reaching the tools (they are bridge tools)": a missing direct entry does not mean unavailable; find them with `search_tools(query="ui")`, read the schema with `get_api_spec(tool_name="perform_ui_action")`, call them over the bridge; say "unavailable" only after `search_tools` returns none.
The Workflow guidance (`workspace-views.md`, also used by Relays) gets the same paragraph.
The Code and Crew system prompts replace the ambiguous sentence with an explicit one (platform tools are bridge tools, not in the direct tool list; find them with `search_tools`, schema and route with `get_api_spec`; never conclude a tool is missing because it is not listed directly). Tests: `TestCodePromptNamesTheBridgeToolDiscovery`, `TestCrewPromptNamesTheBridgeToolDiscovery`. Test: `internal/workproduct/product_config_test.go` requires the wording in the skill.

## Left

- Deploy; then ask Codex, Claude and Muse in a Code chat to open the Costs panel and check each finds and calls `perform_ui_action` through the bridge.
- Relays has no contract of its own for these tools: it reuses the Workflow views, and its right panel (workflow panel on, files panel off) may not match them; not checked in the UI.
- Other platform tools have the same shape (a CLI that only checks its direct tool list will think they are missing); the general bridge guidance already says to use `search_tools`.
