# PLAT-549 — Browser connection changes send unwanted automatic chat messages

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P3 |
| Product | platform |
| Area | browser |
| Summary | fixed on main, not deployed. |

State: fixed on main, not deployed. Date: 2026-10-05. Priority: P3. Reported by owner.

## Change

Remove the browser notification observer from ChatArea and delete its polling,
queue-delivery and receipt state. Code, Crew and workflows no longer send chat
messages on connection, first share, disconnection or reconnection. Discard the
three legacy browser notice formats during automatic queue preparation so
persisted pending notices do not start a turn after updating.

Browser toolbar/pane health and backend routing remain independent of chat:
status indicators still update and tools resolve the selected connection on
each call. MCP connection notices and other queued messages are unaffected.

## Verification

Queue preparation checked with legacy notices alone and mixed with human/MCP
messages. Existing queue ownership, queued delivery, MCP notification and browser
settings checks pass; frontend release build passes.

## Remaining

Deploy/rebuild the frontend and reload open app tabs to unload the old observer.
Already submitted messages remain in the transcript.

## Register notes

[PLAT-549](plat-549.md), P3, fixed on main, not deployed. Remove browser lifecycle chat polling/messages in Code, Crew and workflows; discard legacy pending browser notices while keeping browser status and tool routing.
