[← coding-agents / codex](index.md)

# PLAT-418 — Codex on a Mac loaded the person's own MCP servers and could not see its session profile

| Field | Value |
|---|---|
| State | deployed |
| Priority | - |
| Product | coding-agents |
| Area | codex |
| Summary | fixed on `main`, not deployed (Mac only): Codex loaded the person's own MCP servers and could not see its session profile; it now runs with its own `CODEX_HOME` like under Landlock. |

| Coordination | Value |
|---|---|
| State | fixed on `main` (provider `0808d9f`), not deployed (Mac only: Landlock already used a private home) |
| Date | 2026-10-04 |
| Owner | coding-agent-bridge |
| Related | PLAT-394 (Seatbelt for every CLI) |

## Found

The server-level step contract showed a Builder chat on Codex answering from the
person's own AgentWorks MCP connection (their `~/.codex/config.toml`): it listed
five of their real workflows and then said "workflow_not_found" for the test one.

## Cause

Under Seatbelt Codex kept the person's `CODEX_HOME`, so it loaded their personal
MCP servers, plugins and settings next to the platform's. The session profile
(`agentworks-<n>.config.toml`: the platform's bridge tools) was written to the
private home, where Codex never looks, and Codex silently ignores a profile it
cannot find (checked with the real `codex`). The person's own `~/.codex` also held
229 leftover profile files from launches whose cleanup never ran.

## Fix

- Under Seatbelt Codex runs with `CODEX_HOME = <private home>/.codex`, as under
  Landlock: nothing of the person's config or MCP servers loads, the session
  profile is found, the login is linked in (never copied: refresh tokens rotate),
  a resumed chat's native session is adopted from the person's `~/.codex`, and the
  folder trust goes to that home. Other CLIs keep the person's home.
- Session profiles older than a day are swept from a Codex home (once a minute).
- Where a profile is written on a home that still holds personal servers (accounts),
  those are switched off with `enabled = false` (names read from that home's config).

## Verified (macOS, live)

Codex native tools with a real call through the bridge MCP, the subagent test, the
cross-CLI contract, and through a server: the chat uses `mcp__api_bridge__*` (not
`mcp__agentworks__*`), starts the workflow and passes both server contracts.

## Left

- Nothing. (A test server must be started with its own `AGENTWORKS_STATE_ROOT`,
  and stopped by port, not by script name.)

## Register notes

[PLAT-418](plat-418.md), fixed on `main`, not
deployed (Mac only): Codex loaded the person's own MCP servers and could not see
its session profile; it now runs with its own `CODEX_HOME` like under Landlock.
