[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-473 — A coding CLI's own shell has no platform credentials: say so, and redirect the call (Muse hook)

| Coordination | Value |
|---|---|
| State | fixed on `main` for the prompt (all CLIs) and the Muse hook; Codex, Claude Code, Cursor and Agy hooks not built; needs a rebuild and restart |
| Date | 2026-10-04 |
| Owner | coding-agent-bridge |
| Related | PLAT-468 (Muse had no bridge at all), PLAT-364 (Full CLI) |

## Source

The upwork Builder chat (Muse, Full CLI mode) had its bridge working but could not stamp
the contract: it ran a plain `curl` through Muse's native `bash`, got "missing or
invalid Authorization header", and reported that it had no credentials. The platform
token and `MCP_CUSTOM`/`MCP_AUTH` exist only in the bridge's own shell
(`mcp__api_bridge__execute_shell_command`), by design: a token in the CLI's own
environment would be readable by anything the agent runs. The agent chose the wrong
shell and nothing told it so.

## Done

- mcpagent `runtime_http_skill.go` (inline routing text and the runtime-http skill, so
  every CLI): states that the CLI's own shell has no `MCP_AUTH`/`MCP_CUSTOM`, that a
  platform curl there fails with the Authorization error, and to run the same command
  through the bridge shell tool instead of looking for tokens.
- multi-llm-provider-go Muse adapter: a `PreToolUse` hook, installed whenever the bridge
  is mounted (including Full CLI mode, which has no allowlist hook). It refuses a native
  `bash`/`bash_input` call that uses `$MCP_CUSTOM`, `$MCP_AUTH`, `$MCP_MCP`,
  `$MCP_API_TOKEN` or a `/tools/(custom|virtual|mcp)/` route, and names
  `mcp__api_bridge__execute_shell_command`. Anything else in bash is untouched; no secret
  is revealed and no capability granted. Kept next to the person's own hooks and restored
  byte-exact after the run.
- Verified against real Muse: a platform curl in bash is blocked and the agent receives
  the message; an ordinary command still runs. Unit tests for the script, the settings
  merge (no allowlist, person's hooks kept, no bridge means no hook) and the prompt text.
- Builder `go.mod` pins mcpagent `0a493e1` and provider `7fcad95` (a library change only
  reaches the app through these pins).

## Left

- The same hook for Codex (`.codex/hooks.json`), Claude Code, Cursor and Agy, each with
  its own shell-tool name and payload shape, each needing a live check. Pi has no hooks;
  it relies on the prompt text.
- Chats started before the restart keep the old prompt.
