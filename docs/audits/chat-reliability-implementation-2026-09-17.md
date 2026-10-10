# Chat reliability implementation — 2026-09-17

Implemented in commit `1979a25ef` and deployed to server A as `1979a25-20260917063441`; service health and frontend HTTP checks passed. User live chat acceptance remains pending. This supersedes the failure status in the two earlier architecture/isolation reviews; it does not claim production verification.

## Underlying problem

Selected UI state, provider delivery, canonical history, and native recovery had independent lifetimes. Delayed operations could follow the selected tab, stale snapshots could overwrite newer history, and a delivery response could precede durable acceptance. The fixes enforce identity and persistence boundaries without requiring a UI rewrite.

## Changes

- **Tabs and ownership:** submissions capture account generation, tab, session and submission identity. Builder submissions bind to their exact created conversation. Workflow options and selected servers come from the source tab/preset, including background queue delivery. Established Work continuation fails visibly when its requested session cannot be verified instead of substituting another conversation.
- **Account transitions:** persisted tabs, drafts and queues are namespaced by workspace and owner; login, OAuth, refresh, logout and cross-window account changes invalidate stale callbacks. Legacy migration requires verified identity. Requests capture their authorization synchronously and late old-token failures cannot clear the current account.
- **Input:** store-owned composer revisions protect drafts across remounts and delayed success/failure. Attachment upload and acceptance cannot overwrite newer drafts. Submitted file context is consumed after acceptance while preserving newly added files.
- **Queues:** an account-scoped controller processes every eligible tab independently of selection. Manual steering and background execution share a delivery lock and stable receipt. Rejection retains messages and exposes retry; accepted delivery consumes only the exact captured queue occurrence/prefix. Runtime processing locks do not survive reload as permanently busy state.
- **History:** account/session generation and monotonic snapshot revisions reject stale hydration. Backend history writers share per-path locks, reread canonical state and merge existing content. Canonical snapshots receive incrementing revisions; opaque metadata and structured entries are retained.
- **Acceptance:** interactive query/live-input dispatch first persists an owner/project/session-scoped submission journal. Idempotent retry reuses the receipt; uncertain delivery returns an explicit conflict and is never automatically resent through another endpoint. An owner-scoped receipt endpoint supports status recovery. Acceptance does not claim provider completion.
- **CLI recovery:** new demand is retained while reconciliation runs; missing history can retry. Durable owner-scoped markers survive process restart. A cancellable scanner retries unresolved native flushes every 30 seconds using four workers. Unknown ownership fails closed. Recovery does not resend messages or guess that text matching proves delivery.
- **Observers:** unmounting one ChatArea no longer disconnects every session. Delayed polling, stop/new-chat and response callbacks check their original account/session before changing state.
- **Schedule/webhook tabs:** automatic run discovery no longer adds these read-only runs as chat tabs. Explicit run access remains available. The separate agent's notification feature changes and other unrelated working-tree changes were preserved.

## Validation

- TypeScript project build check: `npx tsc -b` passed.
- Latest combined frontend run: **147 tests across 15 files passed**, covering authentication transitions, hydration/session isolation, restoration, composer ownership, queues/manual steering, Builder targeting, promise-lane aliases, workflow discovery and source settings helpers.
- Combined Go server tests passed for product conversation identity, profiles, history, native recovery, concurrent snapshot writers, submission journaling, live input, session ownership and existing auto-notification ownership. A broader backend access/resume/ownership suite also passed.
- `git diff --check` passed.
- Historical queue extraction reproducer now points to the replacement controller regression suite when the removed vulnerable effect is absent.

Local logs: `/tmp/chat-reliability-final-tests.log`, `/tmp/chat-reliability-tsc.log`, `/tmp/chat-backend-combined-tests.log`, `/tmp/chat-backend-review-tests.log`.

## Release and architecture limits

The transcript mutex protects one active backend process. Concurrent writing replicas still require storage-level compare-and-swap or a transactional conversation store. The configured AgentWorks state root must persist across replacement/deployment for recovery markers; acceptance journals are stored in the durable workspace under each user’s chat_history/submissions directory.

Native adapters still lack application turn IDs. Ordered text-occurrence import remains a repair heuristic; ambiguous provider execution cannot be called exactly-once. Unresolved markers are retained and periodically retried, without automatic garbage collection. These changes are incremental safeguards, not a replacement transactional conversation database.

Deployment completed on 2026-09-17. No live provider/browser restart acceptance matrix was performed. Before production sign-off, validate two users with multiple tabs, queued input while another tab is selected, delayed native flush, reload/restart during acceptance and completion, and a context-dependent follow-up through each supported CLI. Verify both visible history and actual provider context; unit checks alone cannot establish that result.
