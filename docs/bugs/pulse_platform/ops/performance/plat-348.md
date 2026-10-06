[← ops / performance](index.md)

# PLAT-348 — Retained completed sessions fan out workflow-open restoration

| Field | Value |
|---|---|
| State | open |
| Priority | P0 |
| Product | ops |
| Area | performance |
| Summary | records the RTS regression where `/api/sessions/active` correctly retained 45 completed conversations for continuity, but workflow navigation treated 43 terminal workflow rows as live reconnect candidates. |

| Field | Value |
|---|---|
| Status | `implemented locally; deployment and live acceptance pending` |
| Priority | P0 production navigation latency |
| Owner | frontend workflow reconnect / active-session projection |
| Reported | 2026-09-21 |
| Related | [PLAT-109](../../app/chat/plat-109.md), [PLAT-343](../../app/chat/plat-343.md), [PLAT-341](plat-341.md) |

## Incident

RTS workflow switches displayed `Loading conversation…` and `Loading
dashboard…` for minutes. During the wait, Chat could briefly render the new-chat
guide even though the destination workflow already had durable history. Both
panes eventually loaded without a server restart.

Live endpoint timing ruled out backend storage latency. For
`Workflow/rtssprinttracking` and `Workflow/rtsprreviweer`, chat history,
dashboard HTML, Pulse, playbooks and active-session reads completed in 1–55 ms.
The active-session response was the outlier in semantics rather than duration:
it returned 45 retained sessions, all with terminal `completed` status. Forty-
three were workflow sessions and 42 belonged to `rtsprreviweer`.

## Root cause

The backend intentionally retains terminal coding sessions for 24 hours so a
follow-up message can reuse the provider terminal and conversation identity.
The workflow-open frontend still treated every row returned by
`/api/sessions/active` as live work. It therefore:

1. performed per-session running-workflow resolution across retained completed
   turns;
2. allowed previously persisted tabs to enter transcript rehydration; and
3. marked a hydrated tab streaming merely because its retained session ID was
   present in the response.

On a mature workflow this multiplied one navigation into dozens of lookups and
restores. While that asynchronous chain ran, selection was cleared before the
durable chat was resolved, so the UI could show a false empty-chat state. The
dashboard request itself was fast but competed with the unnecessary navigation
fan-out in the same client.

## Fix

- Filter the retained runtime index through the canonical live-activity rule
  before any workflow reconnect lookup or hydration.
- Apply the same live rule before setting a hydrated tab back to streaming.
- Key conversation-resolution state by workflow identity. During a switch,
  mask the old/empty chat surface with an explicit loading state until the
  current workflow's durable chat selection settles.
- Keep 24-hour backend retention unchanged; continuity handles and live work
  remain separate concepts.

## Regression coverage

- A 42-session retained-completed fixture produces zero reconnect candidates.
- An authoritative running workflow remains eligible even when its legacy row
  says `completed`.
- Non-workflow sessions cannot enter workflow reconnect.
- Focused Vitest suites pass (15 tests), TypeScript project build passes, and
  `git diff --check` is clean.

## Acceptance

1. Switching among mature workflows does not issue per-completed-turn runtime
   lookups or transcript restores.
2. Existing durable Chat appears without a false new-chat guide.
3. Dashboard/Pulse loading is independent of retained terminal count.
4. A genuinely live workflow still reconnects and streams normally.
5. RTS interactive switching is verified after deployment.

## Register notes

[PLAT-348](plat-348.md) records the RTS regression
where `/api/sessions/active` correctly retained 45 completed conversations for
continuity, but workflow navigation treated 43 terminal workflow rows as live
reconnect candidates. `rtsprreviweer` alone contributed 42 rows, multiplying a
single switch into per-session resolution/hydration while chat briefly showed a
false empty state. Live chat/dashboard/Pulse endpoint timings were only 1–55
ms. The local fix filters reconnect and streaming projection through the
canonical live-activity rule and masks stale/empty chat until workflow-keyed
durable selection settles. Focused tests and TypeScript build pass; deployment
and RTS acceptance remain pending.
