[← platform / chat-reliability](index.md)

# PLAT-518 — A message sent while Codex was still answering showed up twice, the second with no reply ("stuck")

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | chat |
| Area | reliability |
| Summary | fixed on main: the native transcript merge no longer re-adds a human message saved before an earlier reply. |

| Coordination | Value |
|---|---|
| State | fixed on main (the merge); needs a restart. The flicker the owner also reported is NOT fixed (see Left) |
| Date | 2026-10-05 |
| Owner | chat-reliability |

## Source

Owner, Upwork chat (Codex, 17:48 and 17:55 IST): "if mcp is giving error" appeared twice; the second copy showed a single tick and a spinner and never got an answer ("stuck"). Codex had answered the first copy at 5:48 PM
(visible in its terminal).

## Cause

Two messages were sent quickly (17:48:24 "change to browser", 17:48:45 "if mcp is giving error"); the second was steered into the running Codex turn. The platform saved them as `you, you, reply-1`; Codex's own transcript
orders them `you, reply-1, you, reply-2`. The native transcript catch-up (`claude_native_transcript_sync.go`, `builderConversationMergeRefs`, a longest-common-subsequence alignment) saw a tie between keeping `you,reply-1` and
`you,you`, kept one copy of the second message unmatched on each side, and appended the native copy as "missing" at 17:55:35 (log: `Native transcript catch-up: merged 1 missing message(s)`, event id `native-transcript-sync-user-...`).
The duplicate had no reply after it, so the UI showed a pending message.

## Done

- After the alignment, a native HUMAN message that matches an unmatched persisted human message with the same text within four positions is dropped as a duplicate (`dropReorderedHumanDuplicates`). Real repeats of a message
  (same text sent again after a reply) stay. Test `TestMergeBuilderConversationHistoryDoesNotDuplicateALiveInputSavedBeforeTheEarlierReply` fails with two copies on the old code.
- The already-saved duplicate in the Upwork chat's conversation file is not removed.

## Left

- Reported at the same time: "the chat flickers a lot". Not investigated yet in this ticket.
- Not seen live after the fix; needs a restart and two quick messages into a running Codex turn.

## Register notes

[PLAT-518](plat-518.md), fixed on main: the native transcript merge no longer re-adds a human message saved before an earlier reply. Left: flicker.
