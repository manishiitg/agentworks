[← platform / performance](index.md)

# PLAT-344 — Global workspace write lock turns one slow upload into a server-wide write stall

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | platform |
| Area | performance |
| Summary | records the confida 2026-09-21 incident where one slow `POST /api/upload` held the workspace service's single global write mutex for 3m10s, queueing every document PUT behind it — including chat journal saves, which tim… |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented locally` on `remove-workflow-files-api` (`dc649a5ab`); not merged or deployed |
| Last synchronized | `2026-09-21` |
| Priority | `P1 performance / cross-user outage vector` |

## Report

Selected workspace document routes (uploads, document writes, folder operations,
restores, imports) took one process-wide `workflowFilesMu` mutex
(`WorkflowDocumentLock`). The lock was acquired before the upload body was
read. A single slow `POST /api/upload` therefore blocked those document writes
from every user — including the agent's chat submission journal PUTs — for as
long as the upload streamed. This did not lock unrelated workspace routes.

On confida (2026-09-21 10:12–10:15 CEST) one upload from an external client
held the lock for **3m10s** (ending 400). When it released, the queued writes
completed in a burst: conversation PUTs at 3m10s/2m40s and two chat journal
saves at 40.9s/21.2s. The agent's 15s journal-save context had long fired, so
both saves returned `503 "Cannot durably accept submission; nothing was sent"`
— and the writes landed late anyway, orphaning a `delivery_uncertain` journal
record that replays `409` while its delivery cannot be reconciled (a live tmux
blocks the existing native-transcript absence proof). The reported user's
message remained wedged when the incident was recorded; this implementation
does not clear that record.

Ordinary document GETs bypassed the mutex. The internal `/api/workflow-files`
endpoint used POST even for reads and acquired it, so those reads could also
queue behind the upload. The incident presented mainly as scattered send
failures rather than a full outage.

## Cause

The relevant design choices in `workspace/` were:

1. **One lock for otherwise independent document routes.**
   `WorkflowDocumentLock` (`workflow_files.go`) was a single `sync.Mutex`
   shared by uploads, document PUTs, folder ops, restores, and imports
   (`server.go`). The workspace service principally proxies real filesystem
   operations; it does not need one global transaction over those requests.
2. **Lock held across network I/O.** Middleware acquires the mutex, then the
   handler reads the body: `UploadFile` calls `FormFile` under lock, with its
   10 MB file-size check occurring only after multipart parsing. Network
   slowness became lock-hold time. Multipart parsing may spool to disk; it
   should not be described as necessarily holding the whole body in memory.
3. **A legacy revision endpoint remained after its write surface was retired.**
   `/api/workflow-files` still implemented checked writes and a managed
   multi-file commit, but the current external-tool catalog is read/run-only:
   `write_file`, `patch_file`, and plan mutations are not exposed. Read-only
   external tools still called this endpoint, needlessly sharing the mutex.

## Implemented solution (local branch only)

Commit `dc649a5ab` on `remove-workflow-files-api`:

1. Removed `WorkflowDocumentLock` from upload, document, folder, restore, and
   import routes in `workspace/server.go`, and deleted its process-wide mutex.
   A slow upload can no longer hold that mutex ahead of unrelated journal PUTs.
2. Removed `/api/workflow-files`, its handler, revision-checked write/commit
   branches, and obsolete tests. Kept the generic authenticated document API.
3. Preserved external `read_file`, `list_files`, `search_files`, run-file views,
   knowledge reads, and `get_plan`: the agent server reads its shared workspace
   filesystem with `os.Root` confinement and private-path/symlink checks.
   Where the agent has no shared workspace mount, it uses the existing
   service-token-only **read-only** `/api/shared-assets` endpoint. That endpoint
   remains blocked from the generic public workspace proxy. The existing
   `get_plan` response retains its read-only content fingerprint for client
   compatibility; there is no checked-write consumer.
4. Removed the now-pointless external per-workflow read mutex and updated the
   CLI/MCP setup documentation. No per-path locks, JSONL migration, journal
   rewrite, or upload-stream size/time limit was introduced by this change.

The ordinary document API now has filesystem-style concurrency: independent
paths proceed independently, while overlapping same-file writes are not
transactionally merged. Existing `os.WriteFile` paths may still expose a
partial file to a concurrent reader. Atomic replacement and targeted
read-modify-write coordination, if required, are separate scoped work—not a
reason to restore a process-wide lock.

## Separate chat-reliability follow-up

The stranded `delivery_uncertain` receipt is **not** fixed here. An outcome-less
receipt is not proof that dispatch never ran: it can also remain when the
post-dispatch outcome save fails. A blind TTL or automatic resend could duplicate
a user's message. Recovery needs explicit durable phase/acknowledgement evidence
or a provider/transcript reconciliation path, with a targeted regression test.
`ListUnresolvedChatSubmissions` currently has no callers. Do not clear or
redispatch the live receipt solely because the workspace lock is removed.

## Verification

- `go test ./...` in `workspace/` passes after removing the endpoint and lock.
- Focused `TestExternal*` agent-server tests passed with temporary local compile
  shims for unrelated, in-flight Gmail changes; the shims were removed and
  are not in the commit. Normal agent-server compilation remains blocked by
  those unrelated Gmail symbol/signature errors until that work is completed.
- Still needed before claiming production resolution: slow-upload/concurrent-PUT
  regression, sustained concurrent-write latency check, merge to `main`,
  deployment to Confida, and live verification. None has been claimed here.
- Separate live chat-reliability case to reconcile: Shubham's orphaned submission
  `39b34b8f-c6b5-4d26-9ab7-c3bda2cef129` (session
  `be5087d3-b2c4-4fe1-a114-007a3fe12dcd`). Its current state has not been
  rechecked after this local-only implementation.

## Register notes

[PLAT-344](plat-344.md) records the confida
2026-09-21 incident where one slow `POST /api/upload` held the workspace
service's single global write mutex for 3m10s, queueing every document PUT
behind it — including chat journal saves, which timed out at the agent's
15s budget, landed late, and orphaned a `delivery_uncertain` submission
that replays 409 forever. Long-term fix only (proposed, not implemented):
ingest-before-locking, per-path locks, lock-free append shapes, bounded
upload streaming. One live wedged submission remains on confida.
