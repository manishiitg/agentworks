[← chat / general](index.md)

# PLAT-639: Queued turn broke the Codex chat: security policy changed

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | chat |
| Area | general |
| Summary | A queued chat message ran as 'default' instead of the signed-in local user, and Codex then refused the chat |

## What happened

Local Upwork Builder chat, 2026-10-07. The owner sent "check again now .. browser" (10:11:48) and "run the steps" two
seconds later. The second message waited in the chat turn queue. When the first turn finished (10:13:33), the queue
delivered it, and the turn failed with:

`parent_transport_unavailable: could not restore the coding-agent terminal for this session: codex-cli/gpt-6.1-sol
[unknown]: Codex session security policy changed; close the existing session before continuing`

The live turn ran as user ID `default` with username `user` (the single-user login token). The queue rebuilds the
sender with `internalBotRequestContext`, which had no directory record for `default` and named it `default`. The
turn's settings were otherwise identical. The Codex adapter fingerprints each retained process's sandbox policy and
credential scope, which are resolved per person and per request; the queued turn's scope did not match the idle
process from the first turn, so the adapter refused it. The queue also re-resolves secrets and keys rather than
storing them, so a rotated secret or revoked account can change the scope the same way.

## Fix

1. Single-user mode names the local user `singleUserUsername` ("user") in both the login token and every
   server-rebuilt request context (`internalBotRequestContext`), so a queued turn runs as the same person. The
   queue does not store and trust a username: its file is in the chat-history folder, which the agent can write.
2. multi-llm-provider-go a7f6b50 (pinned here): when a retained Codex process's scope changed and it is idle (the
   acquire holds its lock, so no turn is running), the adapter closes it and launches a fresh one under the new
   scope, resuming the same Codex thread, instead of refusing. One attempt per call. The old process and its
   credentials still never serve the new turn.

## Verification

Builds; the Codex adapter tests and the server queue, bot-session and login tests pass. One adapter test
(`TestConcurrentRolloutResolutionDoesNotCrossLockTurnSessions`) failed once in a full run and passes on its own
both with and without the change: timing-sensitive, not related. Live check: in a local Codex chat, send two
messages back to back; the second runs after the first without the error.
