[← chat / restore](index.md)

# PLAT-700: Restored chats lose their memory until the agent reads the archive

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | chat |
| Area | restore |
| Summary | After a provider session restart the agent only sees the continuity note, not the saved history, so it answers as if the chat just began |

## What happened

When a coding CLI's native session cannot be resumed (server restart, idle reap, definition or mode change, provider switch), `prependCodingAgentContinuityNotice` (`agent_go/cmd/server/workflow_chat_reconnect.go`) typed only the archive path and a jq command into the fresh CLI (path-only since eca917fa6, which removed a ~24 KB pasted transcript). The CLI often answered without running it, so it behaved as if the chat had just begun.

## Fix

The notice now carries a bounded excerpt of the newest dialogue before the archive pointer: the last 10 user/assistant text turns, oldest first, one `role: text` line each, every turn shortened to 700 bytes (start and end kept), the whole excerpt capped at 7 KB. Only text parts cross; tool calls/results, earlier handoff notices and auto-notifications are left out, and a user turn that carried an earlier notice keeps only the user's own message. The notice says the excerpt is only the tail and to read the archive for anything older or cut (counts, dates, exact wording). With no history it falls back to the old "run this jq command before answering" wording. All three call sites in `server.go` (cross-provider resume, native-resume-unavailable fallback, policy/mode refresh, which uses the pre-refresh conversation) pass the history, so it applies to every coding CLI provider. Test: `TestCodingAgentContinuityNoticeCarriesBoundedRecentTurns`.

## Left

Not verified live on a restored chat; check on the next deploy that a restored Crew/Code chat answers a recent-history question from the excerpt.

## Report

#agent_works, 2026-10-07 18:51 (Ashutosh, Excellence, Crew yami / Code): after a server restart (today's deploys) or a long idle, the next message shows "Conversation restored · Previous conversation loaded"; asked "how many messages did I send today", the agent said 2 (he had sent 10+). The relaunched CLI gets `[AGENTWORKS CONVERSATION CONTINUITY] … the complete conversation is saved at builder/conversation/…` but does not read it unless the question needs it.

## To do

Seed the relaunched CLI with a compact summary of the recent conversation (last N turns, bounded), not only the file path, and tell it to read the archive for anything older; check cost impact (restores re-send the system prompt anyway).
