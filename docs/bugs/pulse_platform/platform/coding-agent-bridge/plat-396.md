[← platform / coding-agent-bridge](index.md)

# PLAT-396 — Full mode: no bridge edit tool; the CLI edits natively

| Field | Value |
|---|---|
| State | deployed |
| Priority | - |
| Product | platform |
| Area | coding-agent-bridge |
| Summary | fixed on `main`, not deployed: with native tools on, the CLI edits natively and `diff_patch_workspace_file` is not offered; kept for mcp_only, Pi and step agents. |

| Coordination | Value |
|---|---|
| State | fixed on `main` (mcpagent `de58be1`, builder this commit); not deployed |
| Date | 2026-10-03 |
| Owner | coding-agent-bridge |
| Related | PLAT-390 (two modes), PLAT-394 (Seatbelt) |

## Decision (owner, 2026-10-03)

With native tools on, `diff_patch_workspace_file` is not offered. The CLI's own
Edit/Write run inside the sandbox, which already refuses protected files, so a
second edit tool only splits the model's choice.

## Done

- mcpagent: one admission rule drops it for full mode; the bridge config,
  Claude's allowlist and hook, and the routing prompt all follow it. Kept for
  `mcp_only` chats, Pi (bridge-only) and workflow step agents (knowledge base,
  learnings, reflection), which never run full and whose prompts require it.
- The bridge `execute_shell_command` stays in every mode: HTTP tool routes and
  their credentials run through it.
- Chat guidance (workflow-tools, code-authoring, optimize-playbook, the shell
  quoting hint) names "your own Edit tool, or `diff_patch_workspace_file`
  without native tools".

## Left

- Open question from the owner: whether `run_in_background` is still needed
  now that CLIs have their own subagents (Pulse's background reviewer uses it).

## Register notes

[PLAT-396](plat-396.md), fixed on `main`, not
deployed: with native tools on, the CLI edits natively and
`diff_patch_workspace_file` is not offered; kept for mcp_only, Pi and step agents.
