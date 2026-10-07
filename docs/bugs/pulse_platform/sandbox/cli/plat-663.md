[← sandbox / cli](index.md)

# PLAT-663: tmux server environment keeps service credentials

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | sandbox |
| Area | cli |
| Summary | The tmux server that hosts coding CLIs keeps the service's credential variables in its global environment (the CLIs themselves are clean) |

## What happened

tmux's server keeps the environment of the client that started it as its global environment. The adapters
(multi-llm-provider-go) ran `tmux new-session` with the service's full environment, so whichever launch
started the platform's tmux server left the service's tokens in it; on slot hosts slottmux passes the same
environment through to the real tmux. Slot tmux servers were already clean (slottmux `slotEnv`).

## Fix

- multi-llm-provider-go ecfc814: `llmtypes.IsServiceOnlyEnvKey` names the service-only variables (server-owned
  secrets, `SUPABASE_*`, `VAULT_*`, `LANGFUSE_*`, keyring password, database, sign-in lists,
  `AGENTWORKS_CLI_ENV_DENY` extras). `tmuxlaunch.WithHistoryLimit` (Claude, Codex, Cursor, Pi) prefixes
  `set-environment -g -u NAME ;` for each one present, so both a new server and an already-running dirty one
  are clean before the pane starts. Muse and agy run tmux with `CleanClientEnv()` (their command must stay a
  bare new-session for slottmux routing). Real-tmux test in `tmuxlaunch`.
- agent_go: `cmd/server/tmux_server_env.go` checks tmux's global environment at startup and every 15 minutes,
  removes any service-only names and logs names only (`[TMUX_ENV]`, ERROR if any remain). Real-tmux test
  `TestTmuxServerEnvCheckRemovesServiceTokens`.

## Left

- Deploy and confirm on Excellence (Confida, Dominion): after the restart, `[TMUX_ENV]` should log the removed
  names once, then nothing.
- The tmux server PROCESS's own environment (`/proc/<pid>/environ`, readable by the service account only) still
  holds the variables when a Claude/Codex/Cursor/Pi launch started the server: those adapters' command helpers
  take no environment, so only tmux's global environment (what panes and `show-environment` get) is cleaned.
  A server started by Muse/agy, or after a full restart of a server, is clean in both. Passing
  `CleanClientEnv()` through those helpers would close it.

## What was checked (Excellence host, 2026-10-07, names only)

- Running `claude` and `muse` CLI processes carry none of the service's credential variables: the launch step strips them.
- The `tmux` server processes for Confida and Dominion do: they inherit the service environment, and `tmux show-environment -g` would print it to anything that can reach that tmux socket.
- Slot accounts cannot reach the socket (PLAT-480), so exposure is limited to code running as the service account.
