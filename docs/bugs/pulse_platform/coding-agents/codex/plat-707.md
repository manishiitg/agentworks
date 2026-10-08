[← coding-agents / codex](index.md)

# PLAT-707: Codex Crew turn hangs after the first chunk

| Field | Value |
|---|---|
| State | open |
| Priority | P2 |
| Product | coding-agents |
| Area | codex |
| Summary | A Crew ask on Codex (tmux transport) printed one chunk and then hung for minutes on a one-word answer |

## What happened

## Fix

## Left

## Seen

Excellence, 2026-10-07 16:33:21 CEST, Crew smoke-test (work:project:2b4f9cdb…), via MCP ask_crew "Reply with the single word: pong". The log shows "Agent profile definition changed … resuming the same native coding-agent session", Codex CLI resumed native session 01a10a3c-… over the tmux transport, chunk=1 at t+926ms, then nothing for 4+ minutes; a second ask queued behind it. Model gpt-6-luna, global:codex-cli (Codex now open to everyone).

## To check

Whether the resumed Codex tmux session is waiting on a prompt (trust, update notice, or a resume confirmation) or the turn-completion signal was missed (see the earlier "Codex structured turn-completion hang").

## Cause (2026-10-08)

The shared Codex account (global:codex-cli) on Excellence had no sign-in since the 2026-10-06 repair that removed a personal login from the service home (state/repairs/ankita-codex-20261006T125437Z); `codex login status` said "Not logged in", so the turn waited on Codex. After the owner signed the shared account in (2026-10-08 08:14 CEST), the same Crew ask answered "pong" in seconds on gpt-6-luna.

Left: a signed-out shared account should fail the turn with a clear message instead of hanging.
