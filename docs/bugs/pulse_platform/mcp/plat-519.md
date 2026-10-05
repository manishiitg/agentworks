# PLAT-519: an MCP tool named like a platform tool stops the chat from starting

**State:** stopgap fixed in mcpagent b619356 and now in the builder build (mcpagent pin bumped to 78db549, 2026-10-06), not deployed, not verified live; the real fix (prefix every MCP tool) is open. P1.

**Found:** 2026-10-05, Excellence, Code project `linkedscrapper`: "Failed to finalize agent definition: finalize immutable agent definition: register direct tool "delete_function": tool name "delete_function" is already registered by MCP server "ue03d5aec…__neon_46fb2cad6010"". A Neon MCP connection on that project exposes a tool named `delete_function`; the platform has its own `delete_function` (Crew/Code functions, `crew_functions.go`). mcpagent's `registerDirectTool` (`agent/agent.go`) rejected any direct tool whose name an MCP server already used, and that failed the whole agent, so the chat could not start at all. Any connected server with a tool named like one of ours does the same.

**Stopgap (owner decision, option 3):** the platform tool is used and the MCP tool of that name is hidden, with a `[TOOL_SHADOW]` warning in the log (`canonicalToolRegistry.removeMCP`). The chat starts; that one MCP tool is not callable. One test pins it (`agent/tool_registry_uniqueness_test.go`).

**Real fix (open, owner decision 2026-10-06: prefix EVERY MCP tool, not only on a clash):** in the platform's own agent loop (direct/native tools) every MCP tool is exposed as `<alias>__<tool>`; platform tools keep their names. The alias is short and stable (the connection's name, e.g. `neon`, with a short id suffix only when one user has two connections of the same server), never the internal key (`ue03d5aec…__neon_46fb2cad6010` is ~50 characters; tool names are capped at 64). The visible name translates back to the real server and tool everywhere a tool is called: the agent loop (`conversation.go`), the parallel runner (`parallel_tool_execution.go`), the CLI bridge (`executor/handlers.go`), code execution (`codeexec/registry.go`) and retries. Unchanged: saved `server:tool` selections, and the bridge URL `$MCP_MCP/<server>/<tool>` used by CLI and code-execution agents (already namespaced by server). Claude Code's own `mcp__server__tool` names for its plugins are not ours and stay as they are.

**Left:** deploy the stopgap to Excellence (owner's go) and confirm `linkedscrapper` starts; then build the prefix as above, with tests at the five routing points and a live check with a clashing tool.

**Impact scan (2026-10-06, read-only, all servers):** nothing saved depends on bare MCP tool names.
- Local: only legacy `gmail` / `google_sheets` (dormant workflows) and the built-in `workspace_*` categories; one plan step text `gmail.send_email` (dormant instagram workflow).
- Confida: Linear, Notion, Resend; filters only `server:*`; no plan, skill or note names a tool. The bridge URL `$MCP_MCP/Linear/list_issues` appears only in CLI session transcripts.
- Excellence: personal connections Neon (x3, two for one user: identical tool names), Context7, DeepWiki; no saved text names their tools.
- RTS: Notion, Jam, an official Notion connection; one narrow filter `Notion:notion-fetch` / `Notion:notion-search` on `rtsprreviweer`, stored as `server:tool`, unaffected.

**Tests at the pin bump:** the mcpagent registry tests pass; 8 builder tests in `cmd/server` and `pkg/orchestrator` fail with both the old and the new pin (pre-existing, e.g. PLAT-537); none fails only with the new pin.
