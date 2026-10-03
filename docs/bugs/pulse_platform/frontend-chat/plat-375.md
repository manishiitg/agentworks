[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-375 — Muse's multiple-choice question showed as "Unknown Event Type" JSON

| Coordination | Value |
|---|---|
| State | fixed on `main`; deploy pending |
| Severity | P2 (the question still worked; the detailed view showed raw JSON) |
| Date | 2026-10-03 |
| Owner | frontend-chat |
| Related | [PLAT-354](../coding-agent-bridge/plat-354.html) (Muse native questions in chat) |

## Problem

On Excellence, a Muse turn that asked three questions (invoice fields, PDF type,
input format) showed two cards titled "Unknown Event Type: coding_agent_question"
with the full event JSON, one for the question and one for the answer.

PLAT-354 added the `coding_agent_question` event and an answerable card
(`CodingAgentQuestionCard`) in the clean conversation view
(`cleanConversation.ts`, `CleanConversationSurface.tsx`). The detailed event view
(`EventDispatcher.tsx`) never got a handler, so it fell through to the
unknown-event fallback.

## Fix

`EventDispatcher` renders `coding_agent_question` as a short read-only summary:
"Muse asked" with each question and its options, and, once settled,
"Muse's question answered" with the chosen labels. Answering still happens in
the existing card.

Test: `EventDispatcher.codingAgentQuestion.test.tsx`, built from the two
reported events.
