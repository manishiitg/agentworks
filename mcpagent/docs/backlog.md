# Backlog

Known gaps and design questions that are understood but not yet acted on. Each
entry states what is true today (with the code that makes it true), why it
matters, and what changing it would involve — so picking one up doesn't start
from re-investigation.

---

## Adding MCP servers beyond the bridge, for coding agents

**Question:** how do we give a coding-agent session more MCP servers than
`api-bridge`?

### What's true today

There are two distinct paths, and they behave very differently:

**1. Non-coding (API) agents — already supports N servers.**
`NewAgent(ctx, llm, configPath, ...)` takes a general MCP config file
(`mcp_servers.json`). It supports any number of servers, merges a base config
with the user's, and connects them lazily on first tool call. Nothing to build
here.

**2. Coding agents (Claude Code / Codex / Cursor / Pi) — exactly ONE server,
hardcoded.**
`BuildBridgeMCPConfig` (`agent/coding_agents_bridge.go`) generates the config
handed to the CLI, and it is a fixed single-entry map:

```go
config := map[string]interface{}{
    "mcpServers": map[string]interface{}{
        "api-bridge": map[string]interface{}{ /* ... */ },
    },
}
```

There is no parameter, option, or merge step that can add a second server. So a
coding-agent session natively sees one MCP server, always.

This is a deliberate design, not an oversight — the bridge exposes a small
native tool set (`execute_shell_command`, `diff_patch_workspace_file`,
`agent_browser`, `get_api_spec`), and **every other MCP tool is reached
indirectly**: the agent calls `get_api_spec` to discover a server's schema, then
invokes it over HTTP through `execute_shell_command`. Keeping the native tool
list tiny is what makes bridge-only containment enforceable and keeps large tool
schemas out of the CLI's context.

### Why it may still need to change

The indirect path costs a round trip and real prompt complexity (the agent must
discover, then hand-construct an HTTP call), and it is noticeably weaker for
servers whose value is a rich typed schema. A caller who genuinely wants a
first-class native MCP server in a coding session currently cannot have one.

### What changing it would involve

- Extend `BuildBridgeMCPConfig` to merge caller-supplied server entries into the
  generated `mcpServers` map, behind an explicit option (e.g.
  `WithAdditionalMCPServers`), rather than widening the default.
- Decide the containment story first. Bridge-only enforcement assumes the
  bridge is the ONLY route to the host — a second MCP server with any
  code-execution capability silently defeats it. This is not hypothetical: it is
  exactly how Codex escaped `--disable shell_tool` (it used an available
  `node_repl` MCP to `child_process` out). Any additional-server option needs to
  state plainly whether the session is still contained.
- Per-provider config plumbing already differs (Codex takes a TOML profile,
  Cursor/Pi take `.cursor/mcp.json`-shaped JSON, Claude takes `--mcp-config`),
  so the merge has to happen before that per-provider shaping, not after.

**Status:** RESOLVED (as "don't build this") 2026-07-25. The question this
entry was chasing — "how do we let a coding agent reach a second MCP server"
— already has an answer, and it isn't a native second connection: the
existing `configPath` → `get_api_spec` → `execute_shell_command`+curl route
(same mechanism the routing prompt already documents for "google_sheets,
github, sub_agent_tools") already does this, with no new mcpagent code. Live-
verified end to end adding Exa's free hosted search MCP
(`https://mcp.exa.ai/mcp`) to SparkQuill this way: `get_api_spec` fetched its
real tool schema, and a full discover-then-curl call round-tripped correctly.
A `WithAdditionalMCPServers` native-connection option was built and then
discarded — it bypasses the bridge entirely, which is the opposite of what
bridge-only containment requires (see the paragraph above this one). This
entry stays for the reasoning trail; treat "add a second server" as a config
change (add it to `configPath`), not a mcpagent code change.

---

## Cursor's native Shell tool occasionally slips through `WithDenyBuiltinTools`

**What's true today:** `WithDenyBuiltinTools` installs a per-session
`.cursor/hooks.json` (`preToolUse` + `beforeShellExecution`, both
`failClosed: true`) that's supposed to deny cursor's native `Shell` tool
unconditionally, forcing every tool call through the bridge. Live-reproduced
across 2026-07-25: cursor's `Shell` tool ran anyway, intermittently — roughly
1 failure in every 4-5 runs of `TestRealBridgeStreamingE2E`/cursor, not
deterministic, and not specific to any one test (the pre-existing, unmodified
sibling test hits it too).

**Root-caused, not just observed.** Temporarily instrumented the hook's own
cleanup to preserve its denial log (`mlp-deny-builtin-denials.jsonl`) across a
failing run instead of deleting it (reverted immediately after, nothing
committed). The log was never created at all. The deny script writes to that
log *before* emitting its deny verdict — so its total absence proves the hook
never fired, not that it fired and was ignored. `hooks.json` is confirmed
written to disk synchronously before `cursor-agent` even launches (no
file-write race on our side), so the likely mechanism is a startup window
internal to cursor-agent's own hook subsystem — analogous to (but distinct
from, and with no readiness signal exposed the way the MCP bridge has
`MCP_READY_FILE`) the already-documented "cursor's first bridge call fails
and falls back to Shell" cold-turn race.

**Decision: not fixing.** Explicitly de-scoped — a native-tool leak here is
accepted, not a target for engineering effort. Reasoning: it's cursor's own
internal timing, outside our control to fix at the root; a mitigation (detect
the leak post-hoc, retry the turn on a warm session) was scoped but not built
once the containment risk was judged low enough not to justify it — cursor's
own default toolset has no bigger blast radius than a normal shell, and for
search-only additional servers (see the entry above) there's nothing extra
for a leak to reach.

**Status:** WON'T FIX, documented 2026-07-25. Revisit only if cursor is ever
used somewhere the containment guarantee is actually load-bearing (e.g. a
genuinely untrusted session where bridge-only routing is the sole thing
preventing host access) — at that point the mitigation above (post-hoc detect
+ retry) is the scoped starting point.
