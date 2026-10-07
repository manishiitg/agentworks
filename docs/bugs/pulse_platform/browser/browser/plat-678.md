[← browser / browser](index.md)

# PLAT-678: RTS browser relay deadlock from the extension version

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P0 |
| Product | browser |
| Area | browser |
| Summary | A nested lock in the extension reader (PLAT-643) deadlocked the browser relay on RTS; every browser and tool call hung for 58 minutes |

## What happened

RTS, 2026-10-07, release 7d48e5e (deployed 08:45 UTC, server started 08:48). From about 08:50 the browser relay
stopped; from about 09:15 no coding-agent tool call got through (all `mcp__api-bridge__*` tools timed out with
`MCP error -32001`), across the owner's Code chat and workflow runs. The UI and the workspace server stayed healthy.
ai-work-42 took a goroutine dump (`/tmp/rts-goroutines.txt`, `logs/agent-sigquit-20261007.log`) and restarted the server
at 09:47.

## Cause

PLAT-643 (7fceb609b) recorded the extension's version in the extension reader's `diagnostic` case with
`b.mu.Lock()`/`Unlock()`. That whole `switch` already runs under `b.mu` (locked before the switch, unlocked after it),
and Go mutexes are not reentrant. The first versioned diagnostic (sent right after pairing) made the reader goroutine
wait on a lock it held (goroutine 1232 at relay.go:722, 58 minutes). The next `Manager.Status` took the manager lock
and waited on that binding lock (goroutine 1225, relay.go:361 → 377), and then everything needing the manager lock
queued behind it: 1,390 `/api/browser/extension` status polls, 128 at relay.go:589, browser tool calls
(relay.go:356 via the extension executor) and session cleanups (conversations.go:156).

The existing relay test sent a diagnostic without a version, so the new path never ran in tests.

## Fix

The `diagnostic` case sets `b.version` without locking again (the lock is already held). `TestRelayPairingIsolationAndStop`
now sends a versioned diagnostic and checks the status reports it; with the bug it hangs.

## Verification

GitHub verify run of the relay tests. Needs a deploy to RTS (and Excellence/Confida, which run the same code since
their deploy) before any extension connects there, or the relay will deadlock again.
