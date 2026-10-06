[← chat / reliability](index.md)

# PLAT-602: Closing a tab mid-turn loses auto-notify waits

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | chat |
| Area | reliability |
| Summary | Closing a tab mid-turn wipes the background registry without cancelling; the trigger keeps running and its notice is lost |

## What happened

Review 2026-10-06 (code): closing a tab while a turn streams calls `stopSession` without `cancelAgents`
(`frontend/src/stores/useChatStore.ts:2598`); `session_lifecycle.go:290-325` then clears the session's background
registry without cancelling anything. A `trigger_and_auto_notify` process keeps running for up to 24h and its
completion is delivered nowhere.

## Fix (not built)

Cancel the session's background work when the registry is cleared, or keep the registry and deliver on reopen.
Live proof: start a trigger, close the tab mid-turn, check `ps` and the chat.

Update 2026-10-06: `trigger_and_auto_notify` is removed ([PLAT-601](../../sandbox/confinement/plat-601.md)), so the
orphaned 24h trigger process no longer exists. Still to check: whether clearing the registry on tab close without
cancelling leaves other background work (run_in_background, sub-agents) running with its notice lost.

