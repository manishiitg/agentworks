[← platform / frontend-chat](index.md)

# PLAT-268 — Workflow chat could run normally in tmux while showing no tools or response

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P0 |
| Product | app |
| Area | ui |
| Summary | P0 reliability. |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented; restarted UI reverify pending` |
| Last synchronized | `2026-09-01` |

- **Priority:** P0 reliability. The user repeatedly saw a submitted Workflow
  Builder message remain alone in an otherwise blank chat while the retained
  tmux showed normal assistant output and successful tool execution.
- **Live evidence:** session `b445627b-deae-407c-a05e-02fcd7343d7c`, Social
  Media / `twitter-automation`, submitted at 21:38 on 2026-09-01.

## Root cause

The request path was healthy. The frontend's retained `live-input` probe
returned 404 and correctly fell back to `POST /api/query`; the server created
the same session, emitted assistant chunks, and executed multiple tools. The
browser nevertheless showed none of them.

`ChatArea.tsx` normally consumes those events over a long-lived EventSource.
Browsers cap concurrent HTTP/1.1 connections per origin, and AgentWorks can
have several restored tabs holding SSE connections. A connection waiting for a
browser socket does not reliably emit `onerror`, so the existing “start polling
only after SSE errors repeatedly” fallback never activates.

The code already contained a 750 ms REST catch-up loop specifically because of
this connection-cap behavior, but `startProductEventCatchUp` immediately
returned unless `fullTurnStreaming` was enabled. Product surfaces were
protected; ordinary Workflow Builder chat was not. This exact turn called the
function after its successful fallback query, but the guard disabled it.

This is distinct from PLAT-250: PLAT-250 resets a stale cursor after a backend
restart; this incident used a fresh session and generated a healthy current
event stream that the browser transport never consumed.

## Fix

- Generalized the product-only loop into `startForegroundEventCatchUp` and run
  it for every submitted foreground turn, including retained live-input,
  ordinary query fallback, and `live_input_delivered` responses.
- Active streaming/restored tabs also start the same loop on mount, so a turn
  already in progress when the frontend reloads or hot-updates self-heals
  without requiring another user message.
- The catch-up reads the same session event store as SSE. Durable event IDs
  already make simultaneous SSE and REST delivery idempotent in the frontend
  store, so this adds no duplicate chat rows.
- Stop polling at `completed`, `error`, `stopped`, or `inactive` once no
  background agents remain; replacing the old completed/error-only condition
  also prevents a stopped retained session from leaking a timer.

## Verification

- Root cause confirmed from the exact live prompt stream and server log: tmux
  response/tool activity continued while the browser remained blank.
- Frontend TypeScript, focused lint (zero errors; one unrelated existing hooks
  warning), production build, and bundle-budget checks pass.

## Reverify

Restart the frontend/backend, open several workflow chats, then submit a new
Workflow Builder message. Its assistant response and tool calls must appear
without refreshing the page even when the EventSource connection is delayed.
