[← goals / step-execution](index.md)

# PLAT-170 — product presentation tools passed canonical `_users/<id>/...` paths to the user-scoped workspace database API

| Field | Value |
|---|---|
| State | deployed |
| Priority | P1 |
| Product | goals |
| Area | step-execution |
| Summary | P1 — every file-backed presentation could validate its artifact |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented and deployed; direct tool retry pending` — shared presentation tests pass and the corrected database path was verified against the production workspace API |
| Last synchronized | `2026-08-21` |

- **Priority:** P1 — every file-backed presentation could validate its artifact
  successfully and then fail at the final persistence boundary, leaving the
  user with no playable video, character card, or document panel.
- **Owner:** the shared product-presentation persistence boundary in
  `agent_go/pkg/presentations`, not Video Studio's media generation or the
  workspace database authorization policy.
- **Affected tools:** `show_video`, `show_character`, `show_document`, and any
  future product tool that persists through `presentations.Upsert`.

## Live incident

On 2026-08-21, Video Studio generated a valid, non-empty MP4 at:

```text
work/productions/chatgpt-for-beginners-cinematic/shots/shot-01-hook.mp4
```

The agent then called `show_video`. The shell bridge returned only the promoted
canonical error:

```text
ERROR: tool execution failed: tool execution failure envelope
```

The MP4 could be downloaded successfully through the workspace file API. No
row appeared in `ui_presentations`. Replaying the database path shape against
the live workspace query boundary isolated the failure:

```text
_users/default/Chats/Video Studio/projects/rr1-e10ed291/db/db.sqlite
→ Invalid db_path: access to _users/ is not allowed
```

The equivalent user-relative path succeeded:

```text
Chats/Video Studio/projects/rr1-e10ed291/db/db.sqlite
```

## Root cause

Agent profile runtimes intentionally canonicalize project roots to the physical
server namespace `_users/<id>/...` so native Folder Guard policies and trusted
server-side file reads have an unambiguous tenant root.

The workspace database API has a different trust contract. It receives the
tenant identity through `X-User-ID` and accepts only a path relative to that
user's workspace. It rejects a caller-supplied `_users/<id>/...` prefix so a
caller cannot select a tenant by path.

`presentations.Upsert` crossed those two contracts without converting between
them. Artifact validation used the canonical runtime path and succeeded. The
subsequent `db/db.sqlite` mutation reused the same canonical path and was
rejected. Relaxing the database API would weaken the tenant boundary and is
not the repair.

## Implementation

The shared presentation layer now converts a canonical runtime workspace path
to the user-relative database path at the workspace API boundary:

```text
_users/default/Chats/...  →  Chats/...
```

Paths already in user-relative form remain unchanged. The conversion lives in
`presentations`, once, so every current and future presentation tool inherits
the same behavior rather than each product stripping the prefix independently.

Implementation commit: `13df0681f` (`Fix product presentation database paths`).

## Security decision

Keep the database API's `_users/` rejection. `X-User-ID` remains the authority
for tenant selection; user input must not choose a tenant namespace through a
path. The file API currently accepts the canonical path used by trusted
server-side product tools. That is a distinct transport behavior, not a reason
to relax the database boundary. Its cross-user denial behavior should remain
covered independently.

## Tests and verification

- `go test ./agent_go/pkg/presentations ./agent_go/internal/videoproduct -count=1`
- Regression coverage proves canonical `_users/default/Chats/...` becomes
  `Chats/.../db/db.sqlite` and an already-relative `Chats/...` path is stable.
- The production workspace API accepted the corrected path and returned the
  `ui_presentations` query successfully.
- Rootless backend release
  `show-video-path-20260821172125` was deployed; agent, workspace, and gateway
  services were all active and the public HTTPS gateway remained healthy.

## Acceptance

- `show_video`, `show_character`, and `show_document` persist using a
  user-relative database path even when their runtime workspace is canonical.
- The workspace database API continues rejecting caller-supplied `_users/`
  paths.
- Repeating a presentation for the same identity updates its existing row
  rather than creating a duplicate.
- A live retry of the original `show_video` call creates the presentation row
  and renders the playable video in the product UI. Until that retry is
  observed, runtime acceptance remains pending.

## Decision history

- **2026-08-21:** Do not broaden workspace database path authorization.
  Convert the server-internal canonical path to the API's user-relative path at
  the shared presentation boundary. This preserves tenant isolation and fixes
  all presentation kinds together.
