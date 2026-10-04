[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-468 — Muse chats on a Mac start with no MCP bridge (Seatbelt refused Muse's read of the folders above its working folder)

| Coordination | Value |
|---|---|
| State | fixed on `main` (provider `4dba54e`, pinned `e6cc149`); needs a rebuild and backend restart |
| Date | 2026-10-04 |
| Owner | coding-agent-bridge |
| Related | PLAT-394 (Mac Seatbelt for all CLIs), PLAT-463 |

## Source

The owner's Muse Builder chats for jobsearch and upwork could not call any platform
tool (`get_contract_upgrades`, `set_workflow_contract_version`, decisions). The server
built the bridge config (6 tools) and wrote `mcpServers.api-bridge` into Muse's
settings, but no `mcpbridge` process ever started. Muse printed `MCP configuration
error in <runtime>/.mcp.json; MCP is disabled for this runtime`. The jobsearch agent
then hand-edited `workflow.json` to stamp contract 1.0.45.

## Cause

Muse's own bootstrap log (`~/.local/share/muse/sessions/<date>/<id>/cli-*.log`)
says `mcp.config.resolve stage="startup" outcome="failed" reason="source_reservation"
server_count=0`. Muse walks up from its working folder looking for project sources
and must read every folder on the way. Under the Mac Seatbelt the runtime folder lives
inside the closed AgentWorks state area, whose parent folders were allowed
metadata-only, so the read was refused, MCP startup failed and the chat had no
bridge. It was reproduced outside the app by running the Muse TUI under the same kind
of profile (fails), then adding a literal `file-read-data` grant on the four folders
above the runtime folder (works; any three of the four still fail). Project rules
(AGENTS.md), the path's space, the environment and an empty `.mcp.json` were each ruled
out by experiment. Codex and Claude do not walk up this way and were unaffected.

## Done

- multi-llm-provider-go `internal/clisandbox/seatbelt.go`: for `muse-cli` the ancestor
  folders get `(allow file-read-data (literal ...))` next to the metadata grant. Names
  of siblings become listable; nothing inside the closed folders opens. Other CLIs'
  profile is unchanged.
- `seatbelt_darwin_test.go`: under the real sandbox, Muse can list the three folders
  above its runtime folder, Codex cannot, and Muse still cannot list or read another
  runtime.

- `agent_go/go.mod` pins multi-llm-provider-go at `e6cc149` (includes `4dba54e`). The
  first push of the provider fix did not bump this pin, so the server built after the
  owner's 19:57 restart still had the old sandbox and the relaunched Muse chats
  (`muse resume`) failed the same way. A provider change reaches the app only through
  this pin.

## Left

- Takes effect on the next backend restart (the running server holds the old code).
- Chats started before the restart stay without a bridge; start a new chat or switch
  provider and back.
- Workflows migrated by hand-editing `workflow.json` while the bridge was missing
  (jobsearch) should be re-checked with `get_contract_upgrades` once a chat has the
  bridge. A Muse run with project rules and the real bridge was not re-run end to end
  after the fix; the reproduction used the same profile shape.
