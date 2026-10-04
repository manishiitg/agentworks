[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-473 — A coding CLI's own shell has no platform credentials: say so, and redirect the call (Muse hook)

| Coordination | Value |
|---|---|
| State | fixed on `main` for the prompt (all CLIs) and the hooks of Muse, Claude Code, Cursor and Agy (shell, Monitor and fetch tools; Muse cron_create refused); Codex has no hook; needs a rebuild and restart |
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

## Second round: fetch tools, Monitor, cron, subagents (provider pinned `c270cb4`)

Owner asked whether the hooks cover all tools and whether the CLIs' own subagents run
them. Results, each against the real CLI unless noted:

- **Muse** (`75bde61`): the hook also checks `monitor` commands and `web_fetch` URLs
  (platform route, or the host:port of `MCP_API_URL` when the hook process has it; Muse's
  own env does not, so only the route match fires in practice) and REFUSES
  `cron_create` by name, because a Muse cron runs unattended outside the platform's
  schedule controls (owner decision). `cron_list` and `cron_delete` stay allowed.
  Subagents run the hook (child `bash` platform curl blocked; tested with delegation
  "auto", the setting the adapter writes when `subagent_spawn` is allowed).
- **Claude Code** (`800619c`): matcher is now `Bash|PowerShell|Monitor|WebFetch`.
  WebFetch is refused for a platform route or an `MCP_API_URL` host:port match. A
  subagent's native Bash is blocked too. Monitor and PowerShell: unit tests only.
- **Cursor** (`c270cb4`): the shell script also reads a `url` field and a second
  `preToolUse` entry covers `Shell|WebFetch|WebSearch`. Cursor fires NO hook for its
  web tools (fetch runs on Cursor's servers, which cannot reach localhost), so that part
  is inert today. Subagents run the project `hooks.json` for their shell calls.
- **Agy and Codex**: unchanged (Agy not checked live; Codex has no hook).
- Findings not acted on: only the shell and URL tools are hooked; a script file that
  calls the platform, or another native tool, is not matched. Nothing here protects the
  token (it never enters those shells); the hooks only turn a call that cannot succeed
  into a clear instruction.

## Muse Full CLI: refuse every native tool not on the list (provider `7ee3932`)

Audit: Claude Code has an explicit tool list and a strict MCP config; Codex disables
everything except the shell and its own subagents; Pi is bridge-only; Muse in Full CLI
mode had NO list, so every tool it ships, and every tool a later update adds, was allowed.
Owner decisions (2026-10-04): Muse's own goals out; memory and peer-session tools out;
unlisted and future tools refused by default.

- `musecli_mcpsettings.go`: with the bridge mounted and no bridge-only allowlist (Full
  mode), the settings step installs the existing PreToolUse allowlist hook with
  `museFullNativeTools` and its own deny reason; the launch flags are unchanged (still
  `--yolo`; the bridge-only `--disable-shell/--disable-write` are not applied). MCP tools
  (`mcp__*`) are never refused.
- Allowed: `read_file`, `search`, `write_file`, `edit_file`, `bash`, `bash_input`,
  `monitor`, `web_fetch`, `web_search`, `read_skill`, `write_todos`, the six `subagent_*`
  tools, `work_status`, `work_list`, `work_stop`, `request_user_input` (plus
  `tool_search` and `submit_reminder_decision`, always allowed by the hook).
- Refused: goals (`get_goal`, `create_goal`, `update_goal`, `report_progress`), memory
  (`read_memory`, `add_memory`, `edit_memory`), peer sessions (`list_peer_sessions`,
  `send_session_message`), `cron_*`, `snooze_reminder`, `workflow`, and anything not
  listed. Also turns workflow triggers off and subagent delegation on.
- Real Muse (one Full-mode turn with the generated settings): file read/write, bash,
  todos, `web_fetch` and a subagent worked; `get_goal`, `read_memory` and `cron_create`
  were refused with the message. `list_peer_sessions` does not exist in headless `exec`
  mode (the hook refuses it in the TUI; unit-tested).
- Adding a tool is one line in `museFullNativeTools`; a refused tool shows up in the
  chat transcript as "tool blocked by hook".

## Left (policy)

- Cursor and Agy were not audited tool by tool; Cursor uses `WithCursorFullNativeTools`
  plus its hooks, Agy a mode hook.

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
