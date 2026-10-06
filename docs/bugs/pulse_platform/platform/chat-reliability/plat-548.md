[← platform / chat-reliability](index.md)

# PLAT-548 — An auto-notification restarted the coding CLI's session, so the chat kept showing "Conversation restored"

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | platform |
| Area | chat-reliability |
| Summary | fixed on main, not deployed: a notification shares the interactive role. |

| Coordination | Value |
|---|---|
| State | fixed on main (2026-10-05, `75bc9e981`); not deployed; owner to check locally |
| Date | 2026-10-05 |
| Owner | chat-reliability |

## Source

Owner, Upwork chat (Codex): the "Conversation restored" card shown again and again, here after a server restart.

## Cause (verified in the log)

`resolveWorkflowChatPolicy` gives an auto-notification the origin `notification` and a typed message `interactive`. The origin is part of the role key (`workflowChatPolicy.sessionKey`), and a role change replaces the native coding-CLI session (`chatPolicyRoleRequiresReconnect`). So every switch between a notification turn and a typed turn discarded the Codex thread and started a new one with the "[AGENTWORKS CONVERSATION CONTINUITY]" summary.

2026-10-05 23:48: the "Your browser extension has disconnected" notification led to `[CHAT_POLICY] Policy refresh ... (mode "workshop" -> "workshop"); starting a fresh native coding-agent session` at 23:48:41. The owner's message led to the same again at 23:48:53, cancelling the first. The notification origin changes no capability anywhere; it was only a label in the key.

## Done

- `sessionKey`: `notification` counts as `interactive` for the role, as `pulse` already counts as `scheduled`. The origin stays `notification` for provenance. The capability set is still in the key, so a real difference still starts a fresh session.
- Test `TestAutoNotificationKeepsTheInteractiveNativeConversation`. One pre-existing one-line `if` in the same function was formatted.
- The code commit `75bc9e981` landed without this ticket (a script error); this file and the register and decision entries were added right after.

## Left

- After a real server restart one continuity notice can still appear when the saved role key differs; check it appears at most once, not once per notification.
- Owner check: in a Codex chat, trigger a notification (e.g. a step finishing), then type; no new "Conversation restored" card.

## Register notes

[PLAT-548](plat-548.md), fixed on main, not deployed: a notification shares the interactive role.
