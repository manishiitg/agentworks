[← goals / pulse-governance](index.md)

# PLAT-148 — timeout cleanup closes the continuing Pulse conversation before its next sequence turn

| Field | Value |
|---|---|
| State | closed |
| Priority | P1 |
| Product | goals |
| Area | pulse-governance |
| Summary | P1 — one missed provider completion can strand every later |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `implemented` — timeout cleanup is turn-scoped; focused regression tests pass |
| Last synchronized | `2026-08-19` |

- **Priority:** P1 — one missed provider completion can strand every later
  Review+Fix and Finalize message in the same Pulse sequence.
- **Owner:** conversation turn-stall cleanup and scheduler conversation reuse.
- **Observed on:** Build-in-Public Pulse-only run
  `schedule-cron--af5a941f_1787078711034693000`.
- **Related:** [PLAT-116](../../platform/coding-agent-bridge/plat-116.md)), which owns the upstream missed
  provider-completion signal.

## Evidence and RCA

Gate reached durable worklist completion, but AgentWorks missed the provider's
terminal completion and the inactivity watchdog fired. Its cleanup performed
full conversation teardown (`CloseHTTPSession` and browser cleanup). The Pulse
scheduler then correctly sent Review+Fix as the next message in the same
continuing conversation, but that conversation had already been closed by the
previous turn's timeout handler.

The bug was a lifecycle-scope mismatch: a per-turn watchdog owned only the
stalled foreground response, while its cleanup destroyed conversation-scoped
resources needed by later scheduled messages.

## Fix

Timeout recovery now cancels and settles only the exact foreground turn:

- cancel the registered turn function;
- mark the turn/session projection failed and clear busy/query/tracked-execution
  state;
- preserve the HTTP session, coding-agent session, browser session, and
  continuing conversation.

Full conversation stop remains owned by the explicit conversation-stop path.
The cleanup is idempotent so a late provider completion cannot re-open or
double-settle the timed-out turn.

## Verification and acceptance

Focused tests prove timeout cleanup clears active turn state without closing
the reusable conversation and that `waitForConversationTurnTree` invokes this
turn-scoped cleanup on timeout. The next Pulse sequence message can therefore
reuse the same session. PLAT-116 remains open for preventing the timeout in the
first place.
