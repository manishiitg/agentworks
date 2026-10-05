[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-525 — Make chat history simple: one source of truth instead of merging the CLI's own transcript into the saved conversation

| Coordination | Value |
|---|---|
| State | open: design proposal, no decision yet (owner asked on 2026-10-05 whether the "LCS stuff" had been removed and made simple; it had not) |
| Priority | P2 |
| Date | 2026-10-05 |
| Owner | chat-reliability |
| Related | PLAT-518, PLAT-341, PLAT-178, PLAT-140, PLAT-324, PLAT-354 |

## What exists today (verified 2026-10-05)

- The saved conversation file (`.../conversation/.../session-<id>-conversation.json`) is rebuilt from two sources: what the platform saved while the chat ran, and the coding CLI's own transcript (Codex rollout,
  Claude JSONL ...). `claude_native_transcript_sync.go` (`refreshLatestBuilderConversationFromNativeTranscript`, `mergeBuilderConversationHistory`, `builderConversationMergeRefs`) aligns them with a longest-common-subsequence
  step and appends what looks missing (log line `[CHAT_HISTORY] Native transcript catch-up: merged N missing message(s)`). It runs when a chat is loaded (`sync_native_transcript=1`) and after retained turns.
- It exists because a message sent into a running turn (live input) is saved at once but its reply cannot be, and because reconnects after a provider restart need the history back (commit `dcac66a6e`, 2026-09-17; native terminal mode 2026-09-29/30).
- The structured-events migration (PLAT-354) made COMPLETION structured; it did not remove this catch-up. The chat itself shows structured events from `structured-chat-events-v2.sqlite`.
- No decision, commit or open PR removes or replaces it.

## Why it keeps hurting (same code, five tickets)

- PLAT-341: the alignment's cost saturated the RTS agent CPU (fixed by materialising keys once).
- PLAT-178 / PLAT-140 / PLAT-324: chat UI behind the terminal, a resume rebuilt from partial state, tabs losing their conversation.
- PLAT-518 (2026-10-05): two quick messages into one Codex turn gave `you, you, reply` saved versus `you, reply, you, reply` in Codex's file; a tie in the alignment appended the second message again, shown as a stuck, unanswered duplicate.

## Proposal (not decided)

1. The event store is the one source of conversation history: user messages, replies, tool calls are saved from the structured events the chat already shows; the live-input path saves its reply itself.
2. Rebuild a chat after a restart or provider change from the event store, not from the CLI's transcript.
3. Delete the native transcript merge and the alignment; keep a read-only recovery tool for old conversations if needed.

## Risks and what to check first

- The merge carries rules for old edge cases (replayed tails after provider restarts, stale tails, interleaved replies, structured tool entries); each needs an end-to-end check on every CLI (Claude, Codex, Cursor, Muse, Agy, Pi) with a restart and a provider switch.
- Conversations already saved may contain duplicates (PLAT-518 added none retroactively).
- It changes how history is restored; needs the owner's decision and a staged rollout (local first).

## Decision needed

Owner: replace the sync with the event store (this ticket), or keep the sync and only patch bugs.
