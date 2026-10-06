[← app / chat](index.md)

# PLAT-140 — a restart-and-resume rebuilt a chat from partial state and wrote it over the full record

| Field | Value |
|---|---|
| State | in progress |
| Priority | P1 |
| Product | app |
| Area | chat |
| Summary | P1 — irreversible loss of a user's conversation record. |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `implemented` — overwrite guard shipped; recovery of already-flattened records not attempted |
| Last synchronized | `2026-08-18` |

- **Priority:** P1 — irreversible loss of a user's conversation record. Nothing
  reports an error; the chat simply comes back as its first exchange.
- **Owner:** `cmd/server/chat_history_persistence.go`, `cmd/server/server.go`
  (the two conversation write sites).

## How it surfaced

Reported live: resuming a long salesoutreach chat showed the first user message
and the first assistant response, and nothing after it.

## Measured

| record | user turns |
|---|---|
| Claude Code's native transcript (`87de1833-…`) | **242** (1,262 rows, 396 assistant) |
| the app's saved conversation | **2** (16 history entries, 23 `ui_events`) |

The app's file was written at **20:29:53**, after the 20:05 server restart. Only
one copy exists on disk and there is no backup of the prior contents.

## Root cause

Two independent records. Claude Code keeps its own transcript and is resumed
with `--resume <native-session-id>`; the app keeps its own conversation JSON for
display. The restart cleared the in-memory event state the app's copy is built
from, the resume rebuilt what it could, and persistence wrote that rebuild
straight over the full record.

Nothing about the write was conditional. A conversation record was replaced by a
strictly smaller one with no comparison and no warning.

## Fix

Both write sites now refuse a write whose **user-turn count** is lower than the
record already on disk.

User turns are the invariant worth guarding. Assistant and tool entries are
legitimately rewritten, summarised and trimmed by normal persistence —
`cleanChatHistoryForPersistence` exists to do exactly that — so a drop in their
count proves nothing. A session cannot un-ask a question, so a drop in user
turns can only mean the incoming history is partial.

Refusing is the right failure. A stale-but-complete record is strictly better
than a fresh-but-empty one: the next complete turn rewrites it correctly,
whereas a lost conversation does not come back.

## What this does not do

- **It does not recover records already flattened.** salesoutreach's app-side
  copy is 2 turns and will stay that way. The conversation itself is not lost —
  Claude Code's transcript holds all 242 turns and `--resume` still restores the
  model's context — but the app's rendering of it cannot be rebuilt from what
  remains on disk.
- **It does not repair the rebuild.** The resume still produces a thin history
  after a restart; the guard only stops that thin history from destroying the
  good one. The display will therefore look stale until a full turn runs.
  Reconstructing the app's copy from the native transcript is the real repair —
  the parser for that shape already exists in `multi-llm-provider-go`'s
  `claudecode` package but is unexported.

## Acceptance

- Restart the server mid-conversation, resume the chat, and confirm the file on
  disk still holds its full user-turn count.
- The refusal is logged with both counts and the path.
- A conversation that gains turns, or re-persists the same turn with more tool
  detail, still writes normally.
