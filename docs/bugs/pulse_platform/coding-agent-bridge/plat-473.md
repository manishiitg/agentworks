[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-473 — A coding CLI's own shell has no platform credentials: say so, and redirect the call (Muse hook)

| Coordination | Value |
|---|---|
| State | fixed on `main` for the prompt (all CLIs) and the hooks of Muse, Claude Code, Cursor and Agy; Codex has no hook; needs a rebuild and restart |
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

## Hooks for the other CLIs (built by subagents, provider pinned `32d93ff`)

- **Claude Code** (`345dd4f`): a node `PreToolUse` hook on `Bash` (`tool_input.command`),
  added to the per-launch `--settings` in the tmux and structured launches whenever the
  bridge is mounted; names `mcp__api-bridge__execute_shell_command`. Real Claude Code
  2.1.289: platform curl blocked with the message, `echo`/`ls` ran. The Go-built launches
  and the Landlock path were not run live.
- **Cursor** (`32d93ff`): the adapter's existing `.cursor/hooks.json` shell hook
  (`mlp-allow-shell.sh`, which only allowed) now refuses a platform command in Full CLI
  mode (event `beforeShellExecution`, field `command`); a bash script, no node. Real
  cursor-agent 2026.10.01: platform curl blocked, `echo hi` ran, bridge calls not blocked.
  The adapter replaces a person's own `hooks.json` for the session and restores it; it
  does not merge theirs in (unchanged behaviour).
- **Agy** (`5aaec25`): the existing workspace `PreToolUse` gate also denies a native
  `run_command` platform call in Full mode, naming `call_mcp_tool` with
  `execute_shell_command`. NOT checked against real Agy: the CLI on the machine is not
  logged in; the hook was run as a subprocess against realistic payloads, the real
  payload's argument key is unconfirmed.

## Left

- **Codex has no hook.** On Codex 0.160.0 a config-file hook does not run unless trusted;
  the only automatic route is `--dangerously-bypass-hook-trust` (trust off for every hook
  Codex loads, including a project's), which was not used. Decision for the owner:
  persist trust for our hook (needs Codex's trust hash) or stay with the prompt line.
  Native-tools mode also passes `--disable hooks`.
- Pi has no hook support; it relies on the prompt text.
- Agy's hook needs one live run (logged in, Full CLI, Seatbelt) to confirm the deny text
  reaches the agent.
- Chats started before the restart keep the old prompt.
