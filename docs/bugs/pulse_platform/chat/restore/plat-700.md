[← chat / restore](index.md)

# PLAT-700: Restored chats lose their memory until the agent reads the archive

| Field | Value |
|---|---|
| State | open |
| Priority | P2 |
| Product | chat |
| Area | restore |
| Summary | After a provider session restart the agent only sees the continuity note, not the saved history, so it answers as if the chat just began |

## What happened

## Fix

## Left

## Report

#agent_works, 2026-10-07 18:51 (Ashutosh, Excellence, Crew yami / Code): after a server restart (today's deploys) or a long idle, the next message shows "Conversation restored · Previous conversation loaded"; asked "how many messages did I send today", the agent said 2 (he had sent 10+). The relaunched CLI gets `[AGENTWORKS CONVERSATION CONTINUITY] … the complete conversation is saved at builder/conversation/…` but does not read it unless the question needs it.

## To do

Seed the relaunched CLI with a compact summary of the recent conversation (last N turns, bounded), not only the file path, and tell it to read the archive for anything older; check cost impact (restores re-send the system prompt anyway).
