[← platform / frontend-chat](index.md)

# PLAT-251 — A sent live-input message is echoed twice: once as a real chat bubble, once as a composer "Sent to X" banner

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | app |
| Area | ui |
| Summary | harness_issue, severity low/cosmetic — no data loss or |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `implemented; runtime reverify` |
| Last synchronized | `2026-08-31` |

- **Priority:** harness_issue, severity low/cosmetic — no data loss or
  functional break, but visibly confusing (the same message text appears
  twice in a row in the Workflow Builder chat).
- **Findings:** No workflow finding is linked. Reported live by the user
  with a screenshot showing "no nothing more.. lets build it" as a normal
  right-aligned chat bubble, immediately followed by a green "Sent to
  Claude Code  no nothing more.. lets build it" banner underneath it.

## Root cause

Two independent, un-deduplicated rendering paths for the same submitted
message:

1. `ChatArea.tsx`'s `submitQueryImmediately` (~line 2564) calls
   `agentApi.sendLiveInput`, and when the response's `delivery_status` is
   `'sent_to_cli'` or `'next_turn_started'`, optimistically appends a real
   chat-history event via `chatStore.addTabEvents(...,
   [createUserMessageEvent(trimmedQuery, ...)])`. This renders as the
   normal right-aligned user bubble, on every surface (product chat and
   Workflow Builder alike).
2. `ChatInput.tsx`'s `routeSubmit` (~line 2481) independently tracks
   `liveMessageDelivery` state for the same submission, and once `onSubmit`
   resolves to `'sent_to_cli'`, renders a composer-area banner
   (`showLiveDelivery`, ~line 3283) with the text
   `` `Sent to ${liveDeliveryProviderLabel}` `` followed by a truncated copy
   of the same message (`liveDeliveryPreview(liveMessageDelivery.message)`),
   auto-clearing after 6 seconds.

The suppression comment directly above `showLiveDelivery` already stated
the intent — "the project chat already echoes an accepted message in the
conversation; do not leave an extra success banner" — but the condition
only suppressed the banner `if (isProductSurface)`. The Workflow Builder
surface (`isProductSurface === false`) gets the exact same optimistic
bubble from path 1, so the stated rationale applies there too; the
suppression just wasn't extended to that surface, leaving the banner
visible for a full 6 seconds directly under the real bubble it duplicates.

## Fix

Narrowed `showLiveDelivery` in `ChatInput.tsx` to suppress the banner for
`'sent_to_cli'` and `'next_turn_started'` specifically — the two statuses
that path 1 turns into a real bubble — on every surface, not just product
chat. `'queued_for_injection'` and `'queued_locally'` never get a bubble
(no other UI indicates them), so those, along with the always-transient
`'sending'`/`'failed'` states, still show the banner on both surfaces,
unchanged from before.

## Verification

`npx tsc --noEmit` and `npx eslint src/components/ChatInput.tsx` both
clean (one pre-existing, unrelated `react-hooks/exhaustive-deps` warning
on a different hook in the same file). No new automated test added —
this is a pure UI-visibility condition on already-existing delivery-status
plumbing; reproducing it live requires an actual retained live-input
session mid-turn, which risks disrupting other concurrent sessions'
in-progress chats on the shared dev server.

## Follow-up hardening — 2026-08-31

The earlier change removed the duplicate *successful* delivery banner, but
the message itself still waited for the `/api/live-input` acknowledgement
before appearing in the transcript and before the composer cleared. Claude
Code can take several seconds to acknowledge a retained turn even though the
message has already been accepted for delivery, which made the UI look
stalled and left the exact text sitting in the input.

`ChatArea.tsx` now appends one optimistic user event before awaiting delivery,
and `ChatInput.tsx` clears the captured draft immediately. If delivery fails,
the draft is restored only when the user has not typed something newer or
switched tabs. The transient `sending` banner now shows status only, rather
than a second copy of the message.

Focused live-input submission tests and the TypeScript build pass. Live
reverification remains: the user bubble and cleared composer should happen
immediately, while Claude Code continues starting the turn in the background.

## Reverify

Confirm live: in the Workflow Builder, send a message into an
already-running chat (steering/live-input path). The message should
appear once as a chat bubble; the transient "Sent to &lt;provider&gt;" banner
in the composer should no longer also echo the same text underneath it.
