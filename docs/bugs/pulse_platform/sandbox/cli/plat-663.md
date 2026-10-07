[← sandbox / cli](index.md)

# PLAT-663: tmux server environment keeps service credentials

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | sandbox |
| Area | cli |
| Summary | The tmux server that hosts coding CLIs keeps the service's credential variables in its global environment (the CLIs themselves are clean) |

## What happened

## Fix

## Left

## What was checked (Excellence host, 2026-10-07, names only)

- Running `claude` and `muse` CLI processes carry none of the service's credential variables: the launch step strips them.
- The `tmux` server processes for Confida and Dominion do: they inherit the service environment, and `tmux show-environment -g` would print it to anything that can reach that tmux socket.
- Slot accounts cannot reach the socket (PLAT-480), so exposure is limited to code running as the service account.

## Fix

Start the tmux server with a clean environment (the same allowlist as the CLI launch), or unset the credential variables in tmux's global environment after it starts. Add a deploy self-test that checks tmux's global environment has none of them.
