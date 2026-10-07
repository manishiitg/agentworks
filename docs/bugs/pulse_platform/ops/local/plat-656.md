[← ops / local](index.md)

# PLAT-656: Start script overwrote the shared mcpbridge

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | ops |
| Area | local |
| Summary | Each checkout installs its own mcpbridge and points its server at it, so a test instance cannot replace the running app's bridge |

## What happened

2026-10-07: an isolated test instance started from a worktree (PLAT-649 reproduction) ran
`run_server_with_logging.sh`, which `go install`ed `mcpbridge` into the shared `~/go/bin`. The owner's running app
launches that same file for new coding-agent sessions, so a test instance silently replaced a binary the running app
depends on.

## Fix

The script installs `mcpbridge` into `agent_go/.bin` of the checkout it runs from (git-ignored) and exports
`MCP_BRIDGE_BINARY` to that path; mcpagent already prefers `MCP_BRIDGE_BINARY` over `PATH` and `~/go/bin`. The source
it builds from is unchanged. Each app instance now uses its own bridge. The old `~/go/bin/mcpbridge` stays as a
fallback for anything started outside the script.

## Verification

`bash -n` clean. Takes effect on the next local start; the startup log line now names `agent_go/.bin/mcpbridge`.
