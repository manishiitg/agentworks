[← platform / frontend-chat](index.md)

# PLAT-349 — Chat delivery has no browser-side observability

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | app |
| Area | ui |
| Summary | closes the browser-observability gap in chat delivery. |

| Field | Value |
|---|---|
| Status | `implemented locally; deployment and live acceptance pending` |
| Priority | P2 observability |
| Owner | frontend chat delivery / server telemetry |
| Reported | 2026-09-22 |
| Related | [PLAT-292](plat-292.md) |

## Gap

When chat delivery misbehaves — a stuck stream, a late paint, SSE-vs-poll
disagreement — only the server side is measurable. The browser milestones
(when an event arrived, over which transport, when it was processed and
painted) never leave the client, so delivery investigations guess from
server logs and the Network tab.

## Fix

`frontend/src/utils/chatDeliveryTelemetry.ts` records content-free
milestones for visible chat events (`user_message`,
`conversation_thinking`, `llm_generation_end`, `unified_completion`,
`conversation_error`, `agent_error`) at four phases: `sse_received`,
`poll_received`, `catchup_received`, `processed`. Call sites in
`ChatArea.tsx` (SSE, poll, foreground catchup, event processor) and
`TerminalEventTranscript.tsx` enqueue into a 100ms-batched, 100-event
queue; the batch POSTs to `POST /api/client-telemetry/chat-delivery`
fire-and-forget with no retry, so diagnostics can never delay delivery.

The server (`agent_go/cmd/server/client_chat_telemetry.go`) accepts a
fixed schema with `DisallowUnknownFields`, allowlisted phases, and
bounded fields. Message text, tool arguments, and arbitrary metadata are
never accepted or logged; each event lands as one
`[CLIENT_CHAT_TIMELINE]` JSON log line. The endpoint is excluded from
request tracing to avoid noise.

The `painted` phase is reserved in the schema but not recorded yet; the
first skepticism to resolve live is whether the endpoint needs
authentication or rate limiting, since an unauthenticated log sink is a
volume vector (payloads are JSON-escaped, so no log forging).

## Verification

- `TestClientChatTelemetryAcceptsContentFreeTimeline`,
  `TestClientChatTelemetryRejectsUnknownFieldsOfMeaning`,
  `TestClientChatTelemetryRejectsMessageContent` pass.
- `chatDeliveryTelemetry.test.ts` passes.
- Live acceptance pending: confirm timeline lines correlate with a real
  slow-delivery report.

## Register notes

[PLAT-349](plat-349.md) closes the
browser-observability gap in chat delivery. The client records content-free
milestones (`sse_received`, `poll_received`, `catchup_received`,
`processed`) for visible chat events into a 100ms-batched queue that POSTs
fire-and-forget to `/api/client-telemetry/chat-delivery`; the server
validates a fixed schema and logs `[CLIENT_CHAT_TIMELINE]` lines. Message
text and tool arguments are never accepted. The `painted` phase is reserved
but not recorded yet. Implemented locally; live acceptance pending.
