[← app / chat](index.md)

# PLAT-413 — A coding agent's background tasks filled the chat with boxed cards of raw tool payload

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P3 |
| Product | app |
| Area | chat |
| Summary | fixed on `main`; deploy pending. |

| Coordination | Value |
|---|---|
| State | fixed on `main`; deploy pending |
| Severity | P3 (noise; the chat was hard to read) |
| Date | 2026-10-04 |
| Owner | frontend-chat |
| Related | PLAT-402 (native tools always on, so Relays now run Muse's own shell tool) |

## Problem

In a Relay chat on Excellence a Muse run showed a column of cyan boxes: "Background task 01a1032e · started / status / output", each `output` box holding the
whole tool payload as JSON (the full polling loop `for i in $(seq 1 24); do ... sleep 10`, `chunk_id`, `work_id`, ...). Muse runs its own shell tool in the background since native
tools became always on; the lifecycle events themselves (`coding_agent_background_task`) were added on 2026-09-23.

## Fix

`summarizeBackgroundTaskMessage` (frontend/src/utils/cleanConversation.ts) turns the message into one readable line: the task's own `description` when the payload is JSON that has one, else the
first line, cut to 140 characters. The detailed event view (`EventDispatcher`) shows that line and keeps the full text behind "Show details" (capped at 6000 characters); the clean
conversation view uses the same line. Short messages and bare lifecycle events look as before.
Tests: `EventDispatcher.backgroundTask.test.tsx`, `CleanConversationSurface.test.ts`.

## Left

- Deploy.
- Each poll still produces its own started/status/output rows; folding one task's rows into a single item was not done.

## Register notes

[PLAT-413](plat-413.md), P3, fixed on `main`; deploy pending. Full text behind "Show details".
