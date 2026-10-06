# PLAT-501: an empty per-user schedule state file makes "Failed to load automation schedules" (500)

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | schedules |
| Area | runs |
| Summary | fixed on main, not deployed. |

**State:** fixed on main (not deployed). P2.

**Found:** 2026-10-05, Excellence, user Ashutosh: the Automations panel showed "Failed to load automation schedules". `GET /api/scheduler/jobs?entity_type=product&limit=10000` returned 500 "failed to extract content from API response" (reproduced with his token, for debugging, at the owner's request). The server log named `_users/<id>/chat_history/product-schedules.json` on every attempt. Not caused by the Crew moves.

**Cause:** `readFileFromWorkspace` (`agent_go/cmd/server/workflow.go`) treated an existing zero-byte file as a failed read. The workspace service leaves out `content` for an empty file and `is_binary` when false, so the reply is a bare `filepath`/`encoding` record, and none of the reader's "empty but exists" checks matched it. `loadState` already handled empty content; it never got the chance. Whatever left the state file empty (a truncating write) is not identified; the file itself was not read.

**Fix:** a reply with a `filepath` and no content is an existing empty file. Any other caller of `readFileFromWorkspace` gets the same correction.

**Left:** deploy (Excellence first, where it was reported; the fix is generic, so RTS and Confida too); then Ashutosh's list loads, which is the check. If the empty file recurs, find what truncates it (the state write is `updateStateByKey`).

## Register notes

[PLAT-501](plat-501.md), P2, fixed on main, not deployed. An existing zero-byte `product-schedules.json` was read as a failed read, so the whole Automations list returned 500 for that user (Excellence, 2026-10-05). The reader now treats it as an empty file.
