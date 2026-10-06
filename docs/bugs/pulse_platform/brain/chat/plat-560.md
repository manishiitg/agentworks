[← brain / chat](index.md)

# PLAT-560: Brain chat could not change providers: only Codex and Pi were offered

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | brain |
| Area | chat |
| Summary | The Brain chat offered only Codex and Pi, so its provider picker was empty on RTS, where neither is signed in. |

## What happened

Owner, 2026-10-06 (screenshot): the Brain chat's model settings said "No providers are ready to use" and the provider could not be changed, while the chat itself ran on the installation default. Brain's product file offered only Codex and Pi. On RTS the server Codex is not logged in and Pi has no account, so neither was ready; Claude Code and Cursor, which RTS has, were never offered.

## Fix

Brain offers every coding CLI, the same list as Code (Claude Code default, Codex, Cursor, Pi, Muse, Antigravity), each in mcp_only mode, so no native tools or personal MCP config (`agent_go/internal/knowledgebaseproduct/product.yaml`). Product tests pass.

## Left

Deploy RTS and check the picker lists Claude Code and Cursor.
