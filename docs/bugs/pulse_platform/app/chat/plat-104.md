# PLAT-104 — live chat has two competing user-message creation paths

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | app |
| Area | chat |
| Summary | Live chat uses HTTP to deliver a command and SSE to receive the durable event, |

| Field | Value |
|---|---|
| Status | `open` — design recorded; implementation intentionally deferred |
| Priority | P1 |
| Owner | frontend chat transport and event-store contract |
| Reported | 2026-08-14 |
| Related | [PLAT-102](../../coding-agents/sessions/plat-102.md)), [PLAT-103](../../coding-agents/sessions/plat-103.md)) |

## Problem

Live chat uses HTTP to deliver a command and SSE to receive the durable event,
but both paths can independently create the visible user message:

1. the backend records `user_message` and SSE publishes it;
2. the frontend appends an optimistic `user_message` after HTTP acknowledges;
3. arrival-order-specific filtering tries to collapse the two copies.

This produced a real duplicate when SSE arrived during the 79 ms HTTP request.
The current narrow repair checks the acknowledgement's backend-generated
`message_id` before appending the optimistic copy. It handles both arrival
orders, but message reconciliation still lives inside `ChatArea` instead of
being a single event-store invariant.

HTTP plus SSE is not itself the defect. The defect is that they are treated as
two message producers instead of two observations of one command.

## Proposed design

- The frontend creates a stable `message_id` before sending.
- It immediately upserts one optimistic message under that ID.
- HTTP carries the same ID and returns only delivery acknowledgement/status.
- The backend persists and emits the same ID through SSE.
- The frontend event store upserts the canonical SSE event by ID, transitioning
  the existing optimistic record to confirmed.
- Components never deduplicate messages by text or arrival order.
- A later intentional repeat of identical text gets a new ID and remains
  visible as a distinct message.

## Boundaries

- Keep HTTP as the command/acknowledgement transport.
- Keep SSE as the authoritative event/state transport.
- Do not delay optimistic rendering until SSE arrives.
- Do not use message content as identity.
- Preserve queued, failed, retried, and accepted delivery states on the same
  record rather than creating replacement bubbles.

## Acceptance

1. Every submission creates exactly one frontend record before HTTP completes.
2. HTTP and SSE reconcile through the same client-generated `message_id` in
   either arrival order.
3. The event store, not `ChatArea`, owns reconciliation.
4. Delivery failure updates the optimistic record to failed while retaining the
   draft/retry affordance.
5. Repeating identical text intentionally creates a second message.
6. Tests cover SSE-before-HTTP, HTTP-before-SSE, retry, failure, reload, and two
   identical intentional submissions.

## Current decision

Do not implement this refactor yet. Keep the narrow message-ID race repair from
PLAT-103 and revisit when simplifying the frontend chat/event architecture.
