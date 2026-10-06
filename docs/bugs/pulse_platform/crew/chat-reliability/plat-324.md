[← crew / chat-reliability](index.md)

# PLAT-324 — open Work/Crew chat tabs must retain their conversation across reloads and deployments

| Field | Value |
|---|---|
| State | deployed |
| Priority | P0 |
| Product | crew |
| Area | chat-reliability |
| Summary | The broad reliability change `1979a25ef` was too large for one rollout: it |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `structured-only formatted Chat implemented on main; deployment and live acceptance pending` |
| Last synchronized | `2026-09-21` |
| Latest regression fix | `15ec6141a` on main — remove frontend provider-native transcript merging; tmux unchanged |
| Previous deployed regression fix | `1e87e0186` — retain live CLI finals across stale hydration |

## Post-incident assessment — 2026-09-17

The broad reliability change `1979a25ef` was too large for one rollout: it
changed 58 files and added about 3,400 lines across persistence, submission
receipts, queue ownership, native recovery, multi-user isolation and background
notifications. It introduced two confirmed regressions:

- workflow-scoped sessions could not recover their project after a backend
  restart because the new durable fallback began with an empty workspace path;
- conversation snapshot reconciliation used the shorter incoming runtime
  snapshot as the merge base, allowing prior history blocks to be appended
  again.

Two other incidents were pre-existing defects exposed during this rollout:

- the ChatInput live-delivery effect had bypassed the shared queue worker since
  August. The new durable idempotency journal made that race visible as a 409
  instead of allowing a possible second delivery. For the observed Strategic
  Review action, the first request was durably accepted and the conflicting
  second attempt was rejected;
- the background completion retry sweep predated this change and did not honor
  `suppress_auto_notification`, so it could revive a deliberately suppressed
  child completion.

The architectural failure is split message ownership. Browser storage, React
effects, the queue worker, `/api/query`, `/live-input`, in-memory session maps
and durable history were each able to make independent routing or persistence
decisions. Durability was added around that structure before every entry point
shared one immutable submission lifecycle.

The targeted corrections have fail-before/pass-after coverage and are deployed,
but service health is not proof of end-to-end chat correctness. This ticket
remains open until live acceptance covers two users, multiple tabs, idle and
running turns, browser reload, backend restart and deployment. Known remaining
limits are process-local conversation locks, no general exactly-once guarantee
after an uncertain provider response, and already-corrupted duplicate history
that has deliberately not been rewritten.

- **Priority:** P0 — a follow-up sent from an already-open Work chat reached a
  fresh agent conversation. The visible transcript remained in the tab, but the
  agent had no knowledge of it and asked the user to restate prior context.
- **Category:** Chat Reliability.
- **Owner:** Work tab hydration, product-conversation registry binding, and the
  profile chat submission boundary.

## Regression

Work restores its tabs from browser storage after a page refresh. Those tabs
retain their application session id and visible conversation, but project
preparation only hydrated messages and runtime selection. It did not rebind the
tab's saved session to the server-owned product conversation registry before
enabling the composer.

The send route treats the registry as authoritative. After a deployment or
server restart, a stale/shared registry key could therefore resolve to a newer
or empty session even though the open tab displayed an older conversation. The
message was delivered, but to a fresh provider conversation with no prior
context.

Legacy Work tabs make this worse because they used the project id itself as the
conversation key, the same key as the permanent Builder launch surface.

The production transcript supplied direct evidence: after 191 messages in the
same visible chat, the 2026-09-16 follow-up was answered with “I don't have
prior context … this looks like the start of a new session.”

## Fix

Released in `b4beba011` (`Preserve Work chat resume across deployments`). Work
project preparation now blocks readiness while it rebinds every retained tab's
saved session, and upgrades legacy project keys to tab-specific keys. The
profile query boundary also compares each authenticated `X-Session-ID` with the
tab-specific registry record and switches back to the verified project session
when they drift. This backend guard covers tabs that remain open while a new
release is deployed.

Custom chat titles also became visually detached from resumed conversations.
The title remained in the transcript and the older dated index row, but a
next-day resume created a newer index row with an empty title. Recent chats
then preferred that newer row and fell back to the latest user message. Chat
titles now follow the durable session id across dated files; listing repairs
already-stale rows, saving inherits the session title, and renaming updates
every indexed copy of the session.

## Required contract

1. An open Work chat tab can survive any number of frontend refreshes and
   backend deployments without being closed or reopened.
2. Before its composer becomes ready, the tab's durable application session is
   rebound to its server-owned product conversation key.
   The server repeats this verified comparison at every send so a tab that
   remained open during a deployment is protected without loading new
   frontend code first.
3. Each retained chat has a tab-specific conversation key. Legacy project-key
   tabs are upgraded without changing their saved session.
4. If the saved conversation cannot be verified, sending must fail visibly;
   the platform must never silently start a fresh agent conversation.
5. The next message resumes the same provider-native conversation when the
   provider supports native resume, and otherwise receives the saved transcript
   through the established cross-provider fallback.

## Acceptance

- Open multiple Work chat tabs, complete at least one turn in each, deploy a new
  server/frontend release, refresh the page, and send a context-dependent
  follow-up from every tab. Each agent must answer from its own prior context.
- Repeat after a backend-only restart and after closing/reopening the browser.
- Verify legacy project-key tabs are upgraded to distinct keys and do not merge
  with Builder or with one another.
- Verify an unverifiable saved session blocks readiness with an explicit error
  instead of accepting a context-free turn.

## Verification

- Focused frontend tests: 33 passed, covering Work key migration, tab-store
  hydration and profile submission payloads.
- Focused server tests passed, including the verified Work-only rebind guard.
- Chat-history regression tests cover title inheritance across dated resume
  paths and self-repair of existing untitled index rows.
- Production release `b4beba0-20260916081750` deployed successfully; public
  health and frontend returned 200.
- Browser verification reopened the affected 191-message chat and then reloaded
  the page. The same named tab and full transcript remained active after the
  reload. No message was sent into the user's production chat during this
  verification, so the multi-tab context-dependent follow-up matrix remains.

## 2026-09-16 multiline paste follow-up

Three concurrently open Work tabs were reported as cross-wiring a pasted
message. Production request and provider logs disproved cross-tab routing: the
paste was delivered to the selected `faceexpression` application session, while
`prodissue` was independently executing in its own session. The actual failure
was below the Work tab boundary. Claude Code converts bracketed multiline and
large pastes into an attachment asynchronously, and the retained-input adapter
pressed Enter before that conversion settled. Claude therefore received an
empty turn while the paste remained in its editor.

The provider fix is `multi-llm-provider-go` commit `8e97d8c` (`Wait for Claude
multiline paste before submit`). Multiline or large retained input now waits for
the Claude prompt/attachment to settle before Enter. A settlement failure is
returned to the caller instead of being reported as successful delivery; short
single-line steering keeps the immediate path. Focused and full Claude adapter
tests pass. Cursor and Pi already wait for a visible draft before submit, Muse
waits for and expands its pasted-content attachment, and Codex verifies the
rollout/prompt transition with resubmission; their relevant adapter suites pass.
Production release `8623c20-20260916120839` deployed successfully and public
health returned healthy. A non-destructive live multiline send remains to be
verified.

The same incident also exposed a frontend isolation weakness even though the
captured request was routed correctly: Crew reused one mounted `ChatArea` and
`ChatInput` while changing its `tabId`. Drafts lived durably per tab, but local
paste, picker, upload and submit closures could briefly retain the preceding
tab during a rapid switch. Crew now remounts the chat/composer at each tab
boundary. Before unmount, a pending debounced draft is flushed to its owning
tab, so switching cannot lose the draft or carry it into the next composer.
AgentWorks' workflow chat host uses the same tab-keyed boundary and the same
shared draft-flush safeguard.

## 2026-09-16 resumed-tab transcript regression

Opening the saved `gptlive1` chat exposed a second regression in another open
Crew tab. A later message in `faceexpression` reached the correct application
session, Claude completed it, and the server emitted both the transcript chunk
and structured completion. The raw terminal showed the full turn, while the
formatted conversation stopped at the preceding answer.

Two frontend lifecycle choices combined to cause the loss:

1. The tab-isolation follow-up above keyed the entire `ChatArea`. Resuming or
   switching a chat therefore unmounted the observer that owns every open
   session's SSE connections and foreground catch-up loops. Composer isolation
   only requires a hard boundary around `ChatInput`; the session observer must
   remain mounted across tab switches.
2. `hydrateTabEvents` painted durable history as soon as that request resolved,
   then projected the same history a second time when the live event request
   completed. The first projection marked the optimistic user row as restored;
   the second projection intentionally discards previously restored trace. If
   the live window was empty or delayed, the second paint erased the new user
   row and answer from Chat even though the provider terminal retained them.

The correction keys only the shared `ChatInput`, preserving per-tab draft,
paste, upload and submit state without restarting `ChatArea`. Durable history
and the live window are now reconciled once, so a stale history response cannot
temporarily mark and then discard the current retained turn. A regression test
resolves history before an empty live window and verifies that the optimistic
retained message remains present through the final commit. The focused suite
passes 30 tests and TypeScript compilation passes.

Production read-only verification after that frontend release showed the
already completed turn still disappeared after a server restart. The retained
Claude conversation was intact, but its Work transcript had never been updated.
Production logs showed both `LIVE INPUT` and `RETAINED_TURN` for the correct
session with no intervening `CHAT_HISTORY` write.

The retained persistence code had two project-scope errors. The live-input
writer searched global history because it omitted the Work workspace path, and
the completion-time native transcript reconciler hardcoded the legacy
`default` owner even when the active session belonged to another user. Both
lookups therefore missed the project-owned file without reporting a delivery
failure. Live-input persistence now uses the session's resolved workspace, and
native reconciliation carries the authenticated session owner through its
read, path resolution and index update. The resume HTTP path passes its
authenticated owner through the same function. Regression coverage uses a
non-default user and `_users/<id>/Chats/Work/projects/<project>` transcript,
verifying both the resumed human append and native assistant reconciliation.

## 2026-09-16 consecutive-message completion regression

Production reproduction in the `ci/cd` Work chat isolated the remaining
multiple-message failure. The user submitted a second message nine seconds
after the first, while Claude was still answering. Both messages reached the
same Claude session, and the native transcript contains complete assistant
answers to both. The application event stream stopped after the first answer,
however, leaving the second answer visible only in Terminal. Chat rendered an
empty Agent row followed by a misplaced `Previous conversation` divider.

The retained-turn lifecycle stored later execution ids as aliases of the
currently running turn. The first completion therefore marked every accepted
message complete, canceled the terminal observer, and removed the session's
tracking state. This assumption is invalid for coding CLIs: input submitted
while busy is queued as a distinct provider turn with its own completion.
Additionally, the warm MCP session's event bridge can close after the first
answer even though the retained CLI immediately continues with queued input.

Retained executions are now kept in submission order. One structured
completion settles only the active execution; if another accepted message is
pending, it becomes the active execution, the session remains busy, and a new
retained-terminal observer follows that response through its own completion.
Provider failure still fails the active and pending executions together.
Regression coverage submits two retained messages before the first completion
and verifies that the first completion promotes the second, reattaches an
observer, and does not settle the session until the second completion arrives.
The formatted chat also suppresses the `conversation_resumed` lifecycle marker;
older pages remain available through the transcript's existing top pagination
control, without a misleading divider below the newest restored answer.

## 2026-09-21 — formatted Chat no longer merges provider-native transcripts

The retained-turn bridge now owns stable `turn_id` assignment, exactly-one
canonical completion, ordered pending executions, structured progress/tool
events, and publication of any backend-recovered assistant reply into the live
EventStore. The frontend completion-time transcript hydration therefore became
a second, competing presentation source. It could replace current structured
rows with a differently timed history snapshot and made every new CLI provider
an implicit frontend behavior change.

Formatted Chat now consumes only structured live events and persisted AgentWorks
conversation history. Provider-native transcript readers remain inside the
backend adapter/recovery boundary, where they normalize recovered output into
structured events; tmux remains the persistent CLI process host and raw Terminal
view. The frontend provider list and completion-triggered native-history merge
were removed. A source-boundary regression prevents `ChatArea` from restoring
that coupling. The existing retained-turn P0 continues to require stable turn
identity, exactly one canonical completion, persisted final response/history,
and reuse of the same tmux session.

Implementation commit: `15ec6141a` (`fix(chat): keep formatted view on
structured events`). Verification completed before push:

- 73 focused frontend chat, queue, restore and session-isolation tests passed;
- frontend TypeScript compilation and the production frontend/Electron builds
  passed;
- the `mcpagent` retained-turn tests passed;
- the application coding-agent canonical turn/identity contract tests passed;
- `git diff --check` and pre-commit lint/build gates passed.

Deployment and live verification remain open. Acceptance must confirm that a
normal turn, two consecutive retained turns, Stop then continue, and an app
reload all render from structured history while the raw Terminal remains
independently backed by the same retained tmux process.

## 2026-09-16 Cursor live-publication regression

The `rts-pr-reviweer` Cursor session produced the complete “Here are the
links” answer in Cursor's native SQLite transcript and Terminal. Native
transcript catch-up subsequently merged it into the durable Work conversation,
so a refresh restored the answer, but the already-open formatted Chat remained
blank. Production logs showed several Cursor completions with
`final_response_missing` followed by successful native transcript merges.

The catch-up path previously updated only the conversation file and its index.
It now also publishes each newly recovered assistant message to the session's
live EventStore using the existing whole-message transcript event contract.
Recovered messages therefore appear immediately in an open Chat and remain
available through SSE backfill, while the event is deliberately not a second
completion signal that could settle the next queued turn. Exact assistant-text
occurrence counts across durable history and existing main-agent events prevent
duplicate rows, including repeated sync attempts and replies that already
arrived through the normal live stream. This provider-neutral backstop applies
to Claude Code, Codex, Cursor, Pi, and Muse native transcript recovery.

Production verification of the first release exposed the restart variant: the
durable conversation was already current, so reconciliation returned early
while the restored UI-event trace still lacked several final replies. An
unchanged reconciliation now compares the recent canonical conversation tail
with the live EventStore and republishes only missing assistant rows. This makes
refresh and deployment recovery repair answers that were persisted before the
new server process started, as well as answers recovered during the current
process.

Live verification then exposed a final timing race in the same Cursor path.
The reconciliation scheduler stopped as soon as any transcript change was
observed, even when that change contained only progress, and its three retries
ended about two seconds after completion. Cursor can flush the final assistant
message later than that. The scheduler now continues a bounded series of
reconciliations for roughly fifteen seconds after completion, even when an
earlier pass recovered progress. The existing occurrence-count deduplication
makes those follow-up reads idempotent while ensuring the delayed final is
published to Chat and the session reaches its ready state.

## 2026-09-16 final-message flash regression

A later Cursor completion exposed a frontend-only race: the final answer was
visible from the live transcript chunk, disappeared when the stream settled,
and returned only after durable history caught up or the page was refreshed.
The Cursor terminal and durable provider transcript both contained the answer.

The first completion hydration correctly merged the raw live completion into
the formatted timeline and marked it as persisted trace. A second hydration
that had started against older durable history treated every marked trace event
as a generated restore row and discarded it. That rule was too broad: synthetic
`restored-*` conversation carriers have generated timestamps, while marked
transport events retain stable backend IDs and real timestamps.

Repeated hydration now drops only synthetic restored carriers. Raw traced
events remain in the merge and deduplicate by their stable IDs, so an older
history response cannot remove a live final while provider persistence catches
up. The regression test performs two hydrations against stale history, with the
second live window empty, and verifies that the Cursor completion remains.

### Production verification

- Commit `2f387fa37` (`Keep reconciling delayed CLI final replies`) was pushed
  to `main` and deployed in release `2f387fa-20260916174454`.
- In session `c7d58080-0058-4f6f-a394-feea651f405b`, Terminal completed with
  “Done. Local output is now just one line…” while formatted Chat initially
  stopped at the preceding progress row. Transcript reconciliation then
  published the omitted final, Chat displayed the complete answer, and the
  session changed from running to ready.
- Production `/api/health` reported `healthy`, an idle drain, and all agent,
  workspace, and gateway services remained active after the release swap.
- Focused native-transcript recovery and live-publication tests pass. The
  full server suite retains its unrelated existing Claude model-discovery
  expectation failure.
- Commit `1e87e0186` (`Keep live CLI finals across stale hydration`) was pushed
  to `main` and deployed in RTS release `1e87e01-20260916183335`.
- The frontend regression suite performs two stale-history hydrations, with an
  empty live window on the second pass, and verifies that the stable-ID Cursor
  final remains visible. All 19 focused session-restore/product-fallback tests
  and TypeScript compilation passed.
- The release activated after draining the active turn without interruption.
  The public health endpoint then reported `healthy`, zero active sessions and
  an idle drain. The later `bdcbb00-20260916184549` release also contains this
  correction.


## 2026-09-17 architecture and multi-user/multi-tab hardening

The repeated incidents exposed shared lifecycle gaps: selected-tab state could
influence delayed submissions, stale saved-history snapshots could replace newer
answers, CLI delivery could precede durable acceptance, and recovery demand could
be lost while another reconciliation was running. The earlier production
verification above applies to earlier releases, not to this new fix set.

Implemented locally:

- Capture account, source tab, session and submission identity through delayed
  input, queue, Builder handoff and response callbacks. Resolve workflow settings
  from the owning tab; never use selection as a delivery destination.
- Namespace persisted tabs, drafts and queues by workspace and user. Invalidate
  old-account callbacks across login, OAuth, refresh, logout and browser windows.
- Protect composer drafts with store-owned revisions across remounts. Consume
  submitted attachments only after acceptance, preserving newer edits.
- Process queues independently of the selected chat; retain rejected messages,
  share manual/background delivery locks and reuse stable submission receipts.
- Reject stale hydration using session/account guards and canonical revisions.
  Serialize transcript read/merge/write within the backend process and preserve
  newer persisted answers and structured metadata.
- Persist interactive acceptance journals before dispatch. Return explicit
  uncertainty for ambiguous delivery instead of automatically resending through
  another endpoint. Fail visibly when established Work continuation cannot
  verify the requested conversation.
- Persist owner-scoped native recovery demand, retain demand arriving during
  reconciliation, and retry unresolved native flushes on restart and every
  30 seconds through a cancellable four-worker scanner.
- Remove view-owned global SSE teardown and automatic schedule/webhook chat-tab
  discovery. Explicit access to those runs remains available. The separate
  agent's auto-notification work was preserved.

### Local verification and release status

- 147 frontend tests across 15 files passed; TypeScript `npx tsc -b` passed.
- Combined Go suites passed for conversation/profile identity, history writers,
  native recovery, acceptance journals, live input and session ownership,
  including existing auto-notification ownership tests.
- `git diff --check` passed.
- No commit, deployment or live provider/browser restart matrix was performed
  for these changes. Keep live acceptance pending: two users with multiple
  tabs, reload/deployment during delivery, and context-dependent follow-ups
  through each supported CLI must verify both visible history and provider context.
- Transcript locking is process-local; concurrent writing replicas still need
  storage-level coordination. Deployment must preserve the AgentWorks state root
  containing acceptance/recovery journals. Native adapters lack application
  turn IDs, so unresolved delivery remains explicit rather than claiming
  exactly-once provider execution.

See the [implementation report](../../../../audits/chat-reliability-implementation-2026-09-17.md)
for the complete change mapping, validation logs and remaining architecture limits.


### RTS deployment — 2026-09-17

- Pushed implementation commit `1979a25ef1176d406f887785b355581374449ffb`
  to main and deployed release `1979a25-20260917063441` through the standard
  RTS server-side build and idle-drain activation procedure.
- Recorded sibling revisions: mcpagent `0220d4294b1fab3168611ed1c2c4263d005c281b`;
  multi-llm-provider-go `5e625eb6219be2fc5a7622989af1e18d3d0c28ed`.
- Native Linux backend and production frontend builds passed. Release asset
  checks and bundle limits passed (JS gzip 975.43 kB, below the 1030 kB limit).
  The deploy's Landlock overlap smoke test skipped because its config directory
  was not writable; it is not recorded as verified by this release.
- Confirmed current release symlink and source revisions, all three user
  services active, public health healthy with zero active sessions and idle
  drain, and frontend HTTP 200 at 06:40 UTC.
- The service's persistent config root is writable and lives outside releases;
  native recovery markers survive release replacement. Acceptance journals live
  in the persistent user-scoped workspace chat_history/submissions directory.
- User will perform live chat testing. Multi-tab/provider continuity acceptance
  remains pending; successful deployment and health do not close this ticket.


### 2026-09-17 post-deployment live-input regression — hotfix

The user reported an RTS `404 Session not found` at 12:54 IST and a separate
local workflow live-input failure. RTS logs show a successful normal Work query
and retained follow-up in `product-6a648cdb-fc00-43cc-b1db-2e200c9b9c7a`, followed
at 07:24:50 UTC by live-input to unrecognized browser session
`67007965-e580-4093-8268-0b0b67d86135`. The latter was rejected before dispatch
or journal acceptance. The exact browser transition remains unverified.

Code review also confirmed the new cold journal project resolver only requested
history with an empty workspace path, which misses workflow-scoped transcripts.
This is a regression/gap in commit `1979a25ef`; earlier passing injected-resolver
tests did not cover real workflow discovery after cache loss.

Follow-up work requires retained input to target a session known to the server,
and real owner-checked workflow history discovery after restart. Live acceptance
remains open; the deployed hardening did not yet satisfy the complete contract.


Hotfix implementation: retained live-input now requires the exact session to
appear in the server session cache; provisional/cold sessions use the ordinary
request carrying workspace and continuation context. Backend project recovery
now discovers workflow-scoped and private Work transcripts from disk or the
workspace API, verifies owner/session/path, and rehydrates the project binding.
Missing optional directories do not block recovery; ambiguous project matches
fail closed. Terminal authorization is unchanged.

Validation: 69 frontend tests across six files and TypeScript passed. New backend
regressions invoke the live-input handler with empty project caches and an actual
saved workflow transcript, verify restored journal attribution, reject another
owner, and cover workspace-API-only discovery. Focused journal/live-input tests
passed. These checks cover the previously missed cold-workflow lookup, not the
unobserved browser transition that produced the RTS provisional UUID.


Hotfix deployed to RTS as `4e2d78b-20260917073436` on 2026-09-17. Verified
current release, all three services active, public health healthy/idle and
frontend HTTP 200 at 07:39 UTC. The Linux backend and production frontend builds,
asset checks and bundle limits passed. The existing Landlock overlap smoke test
again skipped due to its config-directory permission issue. Live user acceptance
remains pending; this deployment did not replay or resend the user's failed input.


### 2026-09-17 duplicate exchange after live-input — confirmed, unresolved

User screenshot shows `did you trigger webhooks for it` twice. RTS logs show
one live-input delivery and one HTTP 200 at 07:49:35–07:49:37 UTC for
`c7d58080-0058-4f6f-a394-feea651f405b` (Workflow/rtsprreviweer).
Read-only inspection of that saved transcript found the exact human row and
its same adjacent assistant exchange repeated at different history offsets;
the copy count grew between reads while background processing continued.
This is persisted duplication, not merely an optimistic UI bubble. No duplicate
provider dispatch of that exact input was found in the inspected log.

History reconciliation is the suspected writer boundary: cumulative/native
snapshot merging can preserve earlier repeated blocks, and the continuation
merge appends an entire incoming segment when strict prefix/suffix matching
fails. Exact responsible save sequence still needs a deterministic reproducer.
No history deletion, production transcript repair or new fix is claimed here.


### Duplicate snapshot merge and suppressed-child retry — reproduced

A private replay of the affected production history established the snapshot
writer failure: with the saved prefix containing the reported message once,
merging a 114-message source snapshot using the old reversed ordering produced
two copies; merging the subsequent 126-message source produced three. Keeping
canonical saved history as the base retained one copy through the 114/126/132
snapshot sequence. The runtime source snapshots themselves each contained the
message once. A sanitized regression covers the same long-history/short-snapshot
shape without committing user conversation data.

A separate real-notification defect was identified: retry sweeping requeued
completed children marked suppress_auto_notification, even after producer-side
suppression. Production trace contains full-run, suppressed verify-and-repair
child, then Basic PR review completion prompts. The child caused an extra
actual model turn; it is distinct from persisted duplicate blocks.

The correction is deployed and its focused regression tests pass. Existing
corrupt history has not been deleted or automatically deduplicated, because
text-only deletion would also remove legitimate repeated turns.


Duplicate follow-up locally verified: both snapshot writers now preserve canonical
order and full tool content identity. Regression tests cover repeated saves,
retained tails, opaque metadata and distinct numeric tool payloads. Suppression
is enforced by retry, delay, batch, steer and atomic claim paths. Focused combined
snapshot/native/journal/notification Go tests pass. Existing duplicated rows are
preserved pending a separate verified repair; distinct enclosing run and step
notifications remain separate under current policy.

The Recent/Schedules/Bots/Webhooks filter header now uses panel-width container
queries: labels disappear at 560px and counts at 320px, with icon tooltips and
accessible button names retained. Chrome computed-style checks at 700/530/300px,
five existing filter tests and TypeScript passed.


2026-09-17 report/human-decision 409 follow-up: production recorded a successful
/api/query delivery at 08:05:04 UTC, immediately followed by a conflicting
/live-input attempt for the same session. The durable receipt confirms the
Strategic Review question was delivered; its original project was empty while
the live-input path resolved Workflow/rtslatency. The server correctly rejected
the second use of the same submission ID with different project metadata.

The ChatInput live queue effect bypassed the shared queue controller: when the
idle worker started streaming before its response settled, the effect could
remove and resubmit the same still-pending message. It now uses
sendQueuedChatMessage, sharing the worker's lock and durable receipt, retaining
entries until acceptance, respecting queue errors, and delivering separate human
actions separately. Both human-decision questions and generated report buttons
use sendWorkflowMessageToChat; neither entry point is removed or disabled.

Validation: 43 tests passed across queue ownership, human-decision context,
shared report dispatch, dashboard request IDs and live routing; TypeScript
passed. Added race cases for both entry points and preservation of later actions.
Live authenticated acceptance still needs the user's test.

The preceding duplicate-history/responsive release is now live on RTS as
4ad83b2-20260917080551 (includes b90b02639). Agent, workspace and gateway services
are active and local API health is healthy. Existing duplicate history remains
preserved; the fixes prevent the identified new duplicates.


Report queue follow-up deployed to RTS as eb93a59-20260917081444 at
2026-09-17 08:19 UTC. The current release symlink was verified, all three user
services (agent/workspace/gateway) are active, and the public /api/health is
healthy. The additional report-frame/bootstrap/decision-refresh checks passed
(13 tests), for 56 focused frontend tests overall; production frontend build,
release assets and bundle budget passed. Existing non-blocking Go lint warnings
remain; the unrelated Landlock deployment smoke skipped because its configured
GOG_HOME could not be created. No authenticated production actions were replayed.
User acceptance remains pending after a browser refresh: one decision question
and one generated report chat action during an active turn should each deliver
once to the intended interactive conversation with context intact.

### Work chat rename before first transcript

RTS reproduced a timing gap for a newly created Work tab. The frontend had a
valid product session ID and allowed rename immediately, while the backend
rename endpoint required the first conversation file to already be discoverable.
The initial request therefore returned `Session not found`; after the running
turn saved its transcript, the same rename succeeded. Production timestamps
show the first transcript at 08:34:55 UTC and the successful `latency` title at
08:37:06 UTC.

The backend now accepts an early rename only for the authenticated user's
private `Chats/Work/projects/...` scope. It records the pending title in that
project's durable chat index without fabricating a transcript. The first
conversation save adopts the indexed title into the transcript, then replaces
the pending index row with the completed metadata. Conversation and index locks
keep a simultaneous first-save/rename race ordered. Workflow-scoped chats retain
their existing author verification and do not use this fallback.

Regression coverage exercises rename before the first save, adoption by the
first transcript, completed index metadata, and a subsequent normal rename.
Focused chat-history, snapshot and submission tests pass. The correction was
deployed to RTS as `624d4a8-20260917085131` at 08:56 UTC. The current release
symlink, agent/workspace/gateway services, local health and public health were
verified. Live acceptance remains pending: create a Work chat, send its first
message and rename the tab immediately while the turn is still running; the
rename should succeed without waiting for the first transcript save.

### Native transcript replies replayed after server restart

RTS reproduced a second, separate duplicate-display failure immediately after
the `624d4a8` deployment restarted the backend. The user's message (`ok when
will it get pick up do you know`) was accepted once for session
`7864f623-67fd-4929-88bd-02d8427bfe91`. When the missing native Cursor process
was relaunched, transcript recovery published dozens of older assistant rows as
new live events. Logs record recovered history indexes across the retained
window at 08:57:14 UTC, followed by another recovered batch at 08:58:15 UTC.
The same replay signature occurred after the earlier 08:26 restart, so the
rename change did not introduce it; deployment exposed an existing recovery
defect.

The restart repair gate checked the durable conversation against only the
in-memory EventStore. That store is empty after process restart, even though
the conversation file already contains the UI events the browser restores.
Consequently every recent saved assistant reply appeared absent and was
republished. Recovery now combines live and durable UI visibility counts,
taking the larger count so the same event present in both sources is not
double-counted. A native reply absent from both sources is still published.

Regression coverage clears the live store to simulate a restart and verifies
that a reply in the durable UI trace is not replayed. A companion case verifies
that one genuinely missing newer native reply is still recovered while an
older durable reply is skipped. Existing restart repair behavior for records
without any durable UI trace remains covered.

The correction shipped in `e03b1baba` and was deployed to RTS as
`e03b1ba-20260917090545` at 09:12 UTC. The release symlink, agent/workspace/
gateway services and public health endpoint were verified. The deployment
restart restored the affected session's SSE subscription without any
`Published recovered native assistant reply` entries afterward. For comparison,
the prior release logged another ten-row replay for a separate Work session at
09:08:05 UTC, confirming this was a shared recovery-path defect rather than a
single corrupted tab.

### Stale retained session routed a new message to live input after deployment

RTS reproduced a related send failure after the 09:50 UTC deployment. The
browser retained session `product-a159a050-a710-49f6-837d-ba149cb1a2e1` in its
active-session cache after the server process had stopped it. UI-control polling
then returned `409 session_not_active` every three seconds, but the composer
still classified the next message as retained live input. At 09:51:35 UTC it
posted to `/live-input`, whose pre-delivery ownership check returned the exact
`404 Session not found` response. The message never reached the durable
submission journal or a provider.

The client now treats only that exact pre-delivery 404 as proof that retry is
safe. It removes the stale session from the active cache, removes the temporary
optimistic row, and submits the same message and submission receipt through the
normal turn endpoint. Other 404 and 409 responses remain non-retriable because
they may follow durable acceptance or uncertain provider delivery.

The same release moved project-memory behavior into the shared AgentWorks prompt
assembly. Every project product and workflow now uses one visible project-root
`MEMORY.md` across chats, schedules, bots, webhook-triggered tasks, background
work, and delegated project agents. Claude's provider-private auto-memory is
disabled at the shared root/delegated launch boundary. The affected Work project
now has a visible root `MEMORY.md`; the previous hidden provider files were left
untouched as backup.

Focused live-input routing tests (22), TypeScript, shared prompt tests, Crew
profile tests, and the relevant server tests passed. The changes shipped in
`be77ed4d3` and were deployed to RTS as `be77ed4-20260917095842` at 10:04 UTC.
The release symlink, agent/workspace/gateway services, visible memory-file
ownership, and public health endpoint were verified.

The provider-specific half of the one-memory rule shipped next in
`75afbf985`. The shared prompt now explicitly forbids Claude auto-memory,
Cursor Memories/rules and Codex `AGENTS.md` as alternate persistence stores;
all three coding providers are directed to the same project-root `MEMORY.md`.
RTS release `75afbf9-20260917100931` completed at 10:15 UTC with release assets,
services and public health verified.

### Profile refresh truncated resumed context and replayed old replies

The memory rollout exposed a more fundamental provider-reconnect defect in Work
session `product-a159a050-a710-49f6-837d-ba149cb1a2e1`. Its durable transcript
was intact. At 10:08:20 UTC the Work profile fingerprint changed, so the server
correctly rejected the old Claude session and seeded a replacement with the
bounded recent tail (40 of 119 UI-history messages). The replacement was then
saved under the current fingerprint. On the following turn at 10:09:18 UTC,
the resume gate trusted that replacement as complete and skipped all durable
history, so Claude claimed the chat only concerned the recent memory exchange.

The bounded tail was also confused with new output at two persistence layers.
The normal save appended the 40 replayed messages back to the canonical record,
growing it from 119 to 170 messages. Native transcript recovery then aligned on
the first matching repeated text, treated many replayed assistant rows as new,
and published dozens of old replies into the open chat at 10:09:08 UTC. This is
why the earlier durable-UI visibility fix did not prevent this batch: these were
new duplicate rows manufactured during the reconnect, not existing rows merely
missing from the process-local EventStore.

The correction treats a coding-provider replacement as an explicit continuity
boundary across Claude, Codex, Cursor, Pi and Muse. A bounded tail now begins
with a synthetic continuity notice that records how many earlier messages were
omitted and points to the complete project-relative conversation archive. The
provider can read that archive before answering whole-chat or older-context
questions. The notice and replayed tail are model context only: the save path
removes their exact injected segment and persists only the new exchange. Native
transcript reconciliation filters synthetic handoffs and uses longest-common-
subsequence alignment, so repeated text and replayed tails cannot be appended
or live-published as new replies.

Focused fallback persistence, resume, native transcript, live-input, submission
journal and queue-ownership tests pass. The full server suite has one unrelated
environment-dependent failure in `TestBotFollowUpReachesRetainedMuseThroughQueryP0`
because its durable submission-journal workspace is unavailable in the local
test environment; the relevant focused suite passes.

The fix shipped in `dcac66a6e` and was deployed to RTS as
`dcac66a-20260917102738` at 10:33 UTC. Agent, workspace and gateway services,
local health and public health were verified. The affected production
conversation was backed up, then repaired from 230 to 140 canonical messages:
90 rows belonging to the two verified replay blocks and the 36 recovery events
published together at 10:09:08 UTC were removed. Its project chat index was
updated, and the partial native handle was invalidated so the next user message
performs one clean provider reconnect with the new continuity notice.

Follow-up decision: a replacement coding-provider session now receives only the
archive pointer and must read the complete conversation JSON before answering
its first message. The 48-message/48-KiB tail remains solely as an emergency
fallback when the durable archive cannot be resolved. This makes the durable
conversation the single context source and avoids copying historical messages
into a new provider transcript at all.

The same protected archive handoff now covers an explicit coding-CLI provider
change. Claude-to-Codex/Cursor (and other supported coding-provider switches)
cannot reuse the old provider's native session, so the new provider receives
the identical mandatory full-archive read instruction. The older query suffix
that merely suggested scanning recent JSON entries remains only for non-coding
fallbacks.

The archive-read instruction is sent in the same provider user turn as the
current user message, ahead of a visible `[USER MESSAGE]` delimiter. It is not a
separate synthetic history row. The combined turn remains visible in durable
history so operators and users can see when a provider reconnect required an
archive read; the server does not silently add or later strip that instruction.
This behavior shipped in `f68164b7a` and was deployed to RTS as
`f68164b-20260917110335`. The release symlink, all three services, local/public
health, and the repaired production conversation counts were verified.

### Project-relative recovery paths and restart replay

The first live continuity test exposed two remaining path and event-boundary
mistakes. Product resume targets store their workspace as
`Chats/Work/projects/<project>` while the canonical archive path includes the
`_users/<id>/` owner prefix. The continuity prompt therefore failed to strip
the project prefix and told a CLI already running at the project root to open
`_users/<id>/...`; its first archive read failed before it searched for the
correct `builder/conversation/...` path. The same split affected project
memory: shell reads of `MEMORY.md` were project-relative, while
`diff_patch_workspace_file("MEMORY.md")` validated that bare name against a
docs-root-qualified folder guard and rejected the first write.

Conversation archive paths now normalize both owner-prefixed and owner-relative
workspace identities before producing a provider path. The diff tool now maps
a relative filename into the sole guarded writable project root; it refuses to
guess when multiple roots make the target ambiguous and never rewrites an
already workspace-qualified path.

The test also exposed a separate duplicate-display source. When native
transcript synchronization found no canonical history change after a backend
restart, it treated the empty in-memory EventStore as evidence that up to 50
historical replies needed live publication. Those replies were already loaded
from durable conversation history, so the open tab received them again. A
no-change synchronization now publishes nothing; only messages actually added
to canonical history during that synchronization may be emitted as recovered
live replies.

These corrections shipped in `39d5cabbb` and were deployed to RTS as
`39d5cab-20260917141618`. The release symlink, three services, local/public
health, persisted memory, canonical conversation counts, and zero post-restart
historical-reply publications were verified.

### Crew workspace panels disappeared after the memory profile bump

The governed-memory rollout intentionally advanced Crew's backend profile from
version 1 to version 2, but `frontend/src/products/work/workData.ts` still
pinned version 1. RTS consequently returned 404 for
`GET /api/agent-profiles/work?version=1`. The frontend capability helper
translated that request failure into an empty panel set, producing “No
workspace view is enabled for this product” even though Crew still declares
and implements all workspace panels.

The frontend now requests Crew profile version 2. Crew also treats an empty or
temporarily unavailable remote capability set as unresolved and retains its
local workspace-panel surface instead of disabling the entire workspace. A
cross-tree contract test compares the frontend constant with the backend
manifest version so a future profile bump cannot silently repeat this drift.

This correction shipped in `bb0d6182d` and was force-activated on RTS as
`bb0d618-20260917142749`. The normal drain remained occupied by a scheduled
workflow rather than an interactive chat, so the operator explicitly chose an
immediate swap. The active release symlink, agent/workspace/gateway services,
release source revision, runtime configuration, local agent/workspace health,
and public agent health were verified after activation.

### Crew project schedules were saved but absent from the project view

The first Crew schedule created after this release was persisted correctly in
the project's durable `workflow.json`, but the embedded Schedules view showed
no rows. Crew supplied the public project path (`Chats/Work/projects/...`) to
the shared AgentWorks schedule panel while the scheduler response correctly
used the user-scoped runtime path (`_users/<user>/Chats/Work/projects/...`).
The shared path equality filter therefore rejected the project's own schedule.

Crew now supplies the stable project ID as the schedule scope, and the shared
schedule matcher checks that identity before falling back to preset or path
matching. This follows the AgentWorks identity-based model and keeps schedules
isolated correctly when multiple users or projects have similar paths.

Crew already shared AgentWorks' acknowledged workspace-view tool family and
implemented project-scoped webhook triggers, including authenticated delivery,
one-time secrets, idempotency, and the combined Schedules/Webhooks panel. One
presentation gap remained: Crew's UI-control contract advertised the Schedules
shell without its `schedules` and `webhooks` section targets, and the Crew
adapter discarded a supplied target. The contract and adapter now preserve the
section target, so Crew can open the Webhooks section after creating or
discussing a project trigger and wait for the browser acknowledgment just as
AgentWorks does.

The schedule identity correction shipped in `33d7507a8`; the complete schedule
and webhook-view parity change shipped in `00d63a3ef` and was deployed to RTS
as `00d63a3-20260917145523`. The active source revision and release symlink,
agent/workspace/gateway services, local agent/workspace health, and public
agent health were verified after the final swap.

### Remaining Crew public/physical workspace comparisons

A follow-up boundary audit found two remaining backend comparisons that treated
the browser's public Crew path (`Chats/Work/projects/...`) and the runtime's
physical per-user path (`_users/<user>/Chats/Work/projects/...`) as different
projects. Agent-profile turn authorization could consequently reject a valid
restored Crew conversation, while Bot Connector resume/status filtering could
omit the matching active conversation. Dashboard share-link generation also
passed through a physical path when one reached the restored tab, producing a
URL the public Dashboard route intentionally rejects.

All three paths now use the same authenticated-user-aware identity rule already
used by chat persistence. Only `_users/<authenticated-user>/...` is reduced to
its public form; another user's physical path remains distinct. Dashboard share
links likewise convert the current user's physical path to the public API form
and retain the owner UID. Focused Go regressions cover Work turn/bot path
identity and cross-user rejection; frontend regressions cover public conversion
and foreign-path preservation.

The cross-component workspace-identity correction shipped in `c0588418f` and
was deployed to RTS as `c058841-20260917162036`. The production build, release
asset checks, idle drain, all three services, active release symlink, and public
agent health passed after activation. User acceptance of a restored Crew turn,
bot resume, and Dashboard share link remains pending.

### Shared global activity and Ctrl+K across AgentWorks and Crew

AgentWorks and Crew previously mounted separate top-bar shells even though they
share the same authenticated chat store and header-summary feed. `Ctrl+K` was
explicitly disabled outside AgentWorks, its switcher removed every product tab,
and Crew's reduced top bar hid the global activity monitor. Clicking an
AgentWorks activity while another product was visible also activated a hidden
tab without changing product surfaces.

The application now mounts one Quick Switcher above the AgentWorks/Crew surface
boundary and keeps the same Global Activity Monitor visible in both top bars.
The switcher groups retained Crew chats alongside AgentWorks chats, automations,
and active work, with an `@crew` filter. A shared navigation router changes the
product surface before activating a tab, persists the selected Crew project,
and routes tabless Crew schedules or bots into the corresponding project view
without creating automatic chat tabs. Other dedicated product surfaces remain
outside this shared shell. Backend session ownership remains the authenticated
user boundary; frontend navigation uses stable project and conversation
identity.

The shared AgentWorks/Crew navigation shipped in `30235d0aa` and was deployed
to RTS as `30235d0-20260917180009`. The production build, focused frontend
regressions, release asset checks, idle drain, active release symlink, all three
services, and public health passed. Browser acceptance of cross-surface
`Ctrl+K` and activity-pill navigation remains pending.

### Accepted live-input prompt disappeared while its reply remained

On 2026-09-18 the retained Work session accepted `which repo is pr 79` and
persisted it immediately before the answer, but the open chat rendered only the
answer. Completion reconciliation is allowed to replace the complete tab event
array. It could receive a durable snapshot whose text history or UI trace was
still behind the accepted live-input receipt and thereby remove the optimistic
user carrier. The display-event reference cache compounded this by assuming
events were append-only and treating equal length plus equal boundary IDs as an
unchanged array even when hydration replaced a middle row.

The browser now attaches the backend `message_id` to the accepted optimistic
carrier and treats that carrier as a mutation receipt. History reconciliation
cannot remove it until the durable UI trace confirms the same server identity.
The display cache now reuses its prior array only when every event object is
unchanged, so middle-row replacements reach the transcript renderer. Focused
coverage reproduces the accepted-message/completion/stale-history ordering and
checks the client/server identity handoff.

The reliability correction shipped in `d4000d378`; compact activity-type icons
shipped in `7f445d682`. Both are live on RTS in release
`7f445d6-20260918045339`. The production build, release assets, idle drain,
active release symlink, all three services, and public health check passed.

### One queued Pulse action was delivered twice through duplicate chat projections

On 2026-09-18 a manual Architecture Review command appeared twice in the same
RTS conversation. Durable evidence showed that this was not a renderer-only
duplicate. Session `877d4d7c-1032-4f21-a69a-9e9bb3ce3eaf` accepted the queued
command at `05:34:27Z` under submission `a410023c-...`, then accepted the same
command again at `05:34:51Z` under submission `977f3b7b-...`. The first copy
was injected as a numbered queue item and the second as a plain live query, so
the transcript rendered them as equivalent user cards. A separate session had
already claimed the Pulse review, which is why the duplicate pass reported a
collision.

The browser queue lock was scoped to `tabId` even though backend delivery and
idempotency are scoped to the conversation `sessionId`. Two UI projections of
one conversation could therefore serialize rather than collide: after the
first tab released its lock, the delayed second tab minted a new idempotency key
and the backend correctly accepted it as a distinct submission.

Queue delivery now uses one in-process lane per authenticated identity and
conversation session. It also retains a bounded accepted-message receipt for
two minutes, so a delayed projection consumes its stale queue copy without
calling the backend again. Acceptance is recorded before checking whether the
originating tab still exists, covering close/replace races. Because separate
browser windows do not share JavaScript memory, queued UI submissions also
carry an explicit transport marker. The server journals one semantic receipt
per authenticated owner, conversation session, and normalized queued message
for the same two-minute race window. A numbered single-item batch (`#1:`) and
its plain-message fallback therefore replay one durable outcome even when the
windows generated different client idempotency keys; ordinary typed input keeps
its existing per-submission semantics. Focused frontend regressions cover both
concurrent and delayed duplicate projections, and backend regressions cover
cross-client durable replay without suppressing intentional typed repeats. This
correction is locally verified and has not been deployed pending explicit
operator approval.

### Crew now owns one persistent conversation per user and project

The retained-tab design kept trying to reconcile three identities: the
server-owned product conversation, multiple browser chat tabs, and a permanent
blank Workshop/Builder tab. That complexity was the common source of reload,
provider-switch, resume, duplicate-projection, and close/reopen failures. It
also made the UI imply that a user could safely create several live chats while
the backend product registry still had one authoritative project slot.

Crew now renders the authenticated user's server-owned conversation for the
selected project as one permanent **Chat**. The browser resolves that durable
conversation before enabling the composer, reuses a matching legacy local tab
when possible, and removes other local projections without stopping or deleting
their durable history. There is no blank Workshop tab, chat tab close/rename
control, chat-pane collapse control, previous-chat resume action, or Crew “New
Chat” shortcut/help entry. Changing from Codex to Cursor, Claude, Pi, or another
declared runtime keeps the platform conversation identity and relaunches its
native provider on the next message with the durable transcript.

Earlier conversations remain available from a **Conversation history** icon in
the right workspace toolbar. They expand in place for reading and do not expose
Open, Resume, Rename, Delete, or bulk-cleanup actions. Schedules, webhooks, bots,
project creation, workspace tools, reports/dashboard files, and the Workshop
execution contract remain unchanged.

Right-pane actions use the shared `sendWorkspacePaneMessageToChat()` queue, but
Crew no longer gives that dispatcher a transient browser `tabId`. It supplies
the durable `(profile_id=work, conversation_key=projectId)` identity, and the
dispatcher resolves the current interactive project Chat immediately before it
appends the message. It excludes legacy per-chat keys, Builder tabs, scheduled
runs, bot runs, and read-only projections. If the canonical Chat is not ready,
the action fails visibly instead of reviving an older conversation or creating
a second one.

Migration deliberately trusts the registry's authenticated
`(user, profile=work, project conversation key)` binding. Existing per-chat
browser keys are historical records; they are not allowed to replace the
registry's current project session. This keeps different users and different
Crew projects isolated while giving each user one continuing chat inside each
project.

Local verification covers canonical legacy-tab adoption, cross-project
isolation, provider changes on the same session, scoped runtime invalidation,
and read-only history behavior. The focused Vitest suite passed 47 tests and
the complete production frontend build, report-preview build, release-asset
check, and bundle-budget check passed. The full frontend suite passed 1,402
tests and retained nine unrelated baseline failures in provider-account mocks
and a notification-copy source assertion; none of those failing files changed
in this refactor. The persistent-chat series through `f7e49005e` was deployed
to RTS as `f7e4900-20260918081402`; service and public health checks passed.

### Crew history access and blank scheduled conversations

Live verification on 2026-09-18 showed two remaining mismatches. Crew told the
user it could not search any chat JSON, including another Crew owned by the
same signed-in account. Its product manifest explicitly set
`sandbox.chat_history: none`, so that answer accurately reflected the folder
guard even though account-scoped conversation recall is part of the intended
persistent-memory model. Crew now retains the platform's per-user
`chat_history/` grant and its workspace instructions give the exact authorized
path. The grant remains below `_users/<authenticated-user>/`; another user's
history is never included. `MEMORY.md` remains the curated project memory, while
conversation JSON is read as historical reference.

Separately, opening a run from the global AgentWorks Schedules page hydrated
the read-only tab only from the process-local polling event store. A restart or
deployment emptied that store and therefore rendered a blank chat even when
the durable conversation JSON existed. The opener now uses the shared
`hydrateTabEvents` path, which reconciles the live buffer with durable history
and falls back to the persisted conversation after restart. Product/prompt
tests, focused restore tests, TypeScript, ESLint, and the production build pass.
These two corrections are implemented locally and are not deployed pending
explicit operator approval.

The persistent Crew chat also exposed a global-monitor presentation mismatch:
an idle retained CLI was treated as active work and rendered as an unexplained
clock + identity initial + chat icon pill. Retained process liveness now remains
separate from activity visibility, so an idle conversation leaves the monitor
while running, background, and user-waiting work remains. Visible activity
pills include a short responsive identity label and collapse back to icons on
narrow layouts.

Initial transcript hydration now requests the newest 10 user turns instead of
20. Older turns remain available through pagination, and the coding-provider
resume fallback remains a separate bounded mechanism. This reduces response
transfer and first render work; it does not yet eliminate the server's full
JSON read and workflow transcript-synchronization cost, which remains the next
target if live timing still shows a long restore delay.

Live RTS evidence on 2026-09-18 also showed Claude's background MCP completion
envelope persisted as a synthetic `human` message (`<task-notification>...`).
That internal carrier was rendered as a user chat card and counted as a visible
turn. Resume pagination now excludes complete provider task-notification
envelopes, and the shared frontend restore converter applies the same defensive
filter for legacy or unbounded history responses. The provider still retains
the envelope in its native conversation context, and the assistant response
that follows it remains visible.

### Accepted live-input receipt and durable history echo rendered as two user messages

RTS live evidence on 2026-09-18 showed ordinary prompts such as `check global
secrets` and `check cli` twice while the agent/tool activity remained single.
This was a presentation reconciliation bug, not a second model turn. The client
keeps the frontend-created user row as an accepted mutation receipt until the
durable UI trace confirms the backend `message_id`. During that allowed lag,
the provider conversation history can already contain the same prompt. The
compact product conversation renderer reconciled those two carriers, but the
shared formatted terminal transcript did not, so the newer Crew/AgentWorks
chat surface rendered both event IDs.

The shared transcript now collapses only an adjacent identical pair where one
carrier is frontend-created and the other is durable. It retains two deliberate
repeats when both are real frontend submissions or both are durable, and it
retains an older identical turn when an agent response separates it from the
new prompt. Focused transcript, history reconciliation, clean-conversation, and
live-input suites pass (164 tests); the production frontend build and focused
backend packages also pass. Deployment is authorized and pending completion of
the RTS release procedure.

## 2026-09-18 — Workflow permission reconnect must preserve the same conversation

Implemented and regression-tested locally; deployment/live verification pending. Related permission enforcement: [PLAT-262](../../sandbox/security/plat-262.md).

Both incoming message paths now compare AgentWorks workflow admission before retained delivery. A policy change uses the existing same-session history snapshot/handoff and full transcript merge; it does not invent a new browser chat ID or private namespace. A policy refresh must not seed the previous native session handle, even if a restored conversation target supplies one. Native cleanup is shared with the existing reconnect path and now includes Muse.

The replacement CLI receives the existing visible `AGENTWORKS CONVERSATION CONTINUITY` notice with the current user message, pointing to the complete saved conversation archive. Archived text remains historical context, never current permission authority. The existing bounded handoff remains available as immediate context, including the exceptional case where no archive could be saved.

Compatibility audit: SDK Session.Send still owns unchanged warm delivery; no second message-delivery implementation or automatic retry is added. Existing submission journal and serialized next-turn path are reused. Explicit new turns and synthetic notifications cannot cancel foreground work merely because their origin gives a different fingerprint. Focused retained/live-input, continuity/history, submission-context and permission regressions pass. After deployment, reverify promotion and demotion in the same active Confida testing chat, one delivery per user message and a visible archive-read notice with no duplicated transcript.

### Follow-up: restored Run mode after owner promotion (2026-09-18)

Confida release `confida-f75a304b-20260918150257` correctly admitted Builder tools, but a restored request still carried `execution_options.workshop_mode=run`. Session metadata, reconnect notices and the private native CLI directory continued using that stale value. Normalize the workflow-builder conversational request from resolved current access before those consumers run, preserving other execution options and leaving Crew profiles and headless execution unchanged. Regression checks cover owner promotion, reader demotion, missing options, private directory mode selection and the exclusive authoring guard.

## Confida provider-switch submission recurrence — 2026-09-18

See [PLAT-099](../../coding-agents/bridge/plat-099.md) for the provider/account
routing repair and the subsequent `/query` versus `/live-input` receipt-project
mismatch. The saved workflow had Gemini while its retained runtime had Claude.
A reconciled failed receipt then hit a project conflict because the query route
had not assigned its preset-resolved workspace before journal acceptance.

The follow-up canonicalizes the workflow project before acceptance, verifies
legacy empty-project bindings against the owner's durable conversation, and
preserves identity conflict and uncertain-delivery checks. The original blocked
receipt was backed up before a narrowly evidenced not-delivered reconciliation;
no message was resent. 350 saved messages remained, but the interrupted Gemini
turn had no completed final reply in its native transcript. Full live acceptance
of retry, refresh and provider switching remains pending; health checks alone
do not close this reliability ticket.

### Provenance of the Confida receipt conflict

The exact project comparison/conflict response originated in `1979a25ef`
(2026-09-17). Cold live-input workflow recovery (`4e2d78b89`) handled one entry
point but left workflow-phase `/query` accepting an empty-project receipt.
Queued deduplication (`e6e77d9c8`, 2026-09-18) preserved that project check.
The confirmed correction is shared canonical workflow resolution before
acceptance and retained-policy checks, not removal of idempotency protection.
See PLAT-099 for the commit-by-commit provenance and remaining live acceptance.

Confida follow-up `58281d117` is now deployed in
`confida-d3624e74-20260918153600`. Service health and targeted regression checks
pass. Fresh user retry remains unverified; this deployment does not close the
broader PLAT-324 acceptance matrix.

### Crew project MCP selection must refresh retained native tools (2026-09-18)

Confida evidence: Shubham's Crew project selected Notion at server time 15:47:35.
The successful tool result promised activation on the next user message, but
messages at 15:48:45 and 15:50:33 used retained Muse live input without rebuilding
its original empty MCP scope. Platform discovery's 45 tools were not the tools
admitted to that native turn.

The existing AgentProfileKey now includes the project's selected MCP servers.
Resolve selection from the trusted runtime manifest during normal product
setup and before explicit live input. A mismatch closes the old native process
and routes the same message through normal setup; native resume rejects the old
key while application conversation history stays intact. Running foreground
turns no longer bypass product fingerprint checks. Both selecting and
deselecting require this refresh; ordering alone does not change the key.

Regression coverage checks MCP selection fingerprints, unchanged selection
ordering and rejection of stale persisted keys while a foreground turn exists.
Platform connection status and tool discovery counts do not imply tools were
loaded into the current project turn. Live acceptance is pending deployment.

### Crew workspace divider must reuse the complete AgentWorks rail (2026-09-18)

The Crew surface imported the shared divider primitive, but assembled only its
workspace-collapse control. AgentWorks separately added device-preview and
chat-collapse controls around the same primitive. That partial reuse produced a
visibly different divider and allowed the two product surfaces to drift even
though Crew's product contract requires the shared AgentWorks shell.

Commit `4a1a5274c` moves the complete rail composition into
`WorkspaceSplitRail` and makes both AgentWorks and Crew render it. Crew now has
the same mobile/tablet/desktop preview controls, chat and workspace collapse
actions, ordering, dimensions and styling. Collapsing chat expands the workspace
and exposes the same restore affordance. A source-contract regression test
requires both products to use the complete shared rail rather than assembling
the lower-level collapse controls locally.

Focused frontend tests (26 tests), TypeScript, targeted ESLint, the production
frontend build, report-preview build, release-asset validation and bundle hard
limit all pass. The commit is on `main`; deployment and live visual acceptance
remain pending explicit operator approval.

### Crew project deletion must remove its durable conversation (2026-09-18)

Crew now exposes a delete action beside each project in the shared top-bar
selector, with the same danger-confirmation pattern used for Automation
deletion. The operation stops retained work before deleting, then removes the
authenticated per-user project folder, its schedules/triggers/bots/files/
dashboard/database, the live and historical product-conversation registry slot,
durable transcript index entries, local chat projections and saved pane
preferences. The next Crew is selected, or the first-use Dashboard is shown
when none remain.

The server owns deletion through the keyed agent-profile project boundary;
calling the generic folder API alone is deliberately insufficient because it
would strand the registry's live conversation binding. The workspace client
forwards the authenticated user id so `Chats/Work/...` resolves beneath the
correct `_users/<id>` root. Active work produces a conflict until stopped.

Focused registry, workspace-client and Crew frontend tests pass, along with
TypeScript, ESLint and the complete production frontend build. The broader
server suite retains its unrelated existing assembled-prompt size failure
(24,153 bytes against a 24,000-byte ceiling). Deployment and live deletion
acceptance remain pending explicit operator approval.
### Foreground Crew admission must not use the preceding turn's snapshot (2026-09-18)

Live Confida regression: a Linear-enabled Crew turn launched at 16:27:19,
then the next user message at 16:28:07 canceled it with another profile mismatch.
The runtime JSON still described the preceding completed turn until the current
turn saved its runtime. Comparing only that JSON falsely treated an unchanged
foreground agent as stale. This is independent of chat length.

Track the fingerprint admitted at actual stream launch separately from the
request's freshly resolved fingerprint. Both query and explicit live-input
paths compare to launched admission when known, falling back to durable runtime
after process restart. Clear launched admission when closing the native agent.
The stable application session ID is unchanged. Genuine integration selection
changes still trigger refresh.

During a product refresh, skip every native-resume seeding path for the old
handle and do not let a leftover live tmux claim ownership of context. The
existing archive-read instruction and bounded emergency fallback then transfer
context while the full application history remains preserved. Regression tests
cover unchanged foreground admission without a completed snapshot, genuinely
changed foreground admission, retained key checks and existing continuity paths.

### Distinct MCP account discovery (2026-09-18)

Confida had two `Linear` OAuth connections with different explicit credential
files. Base/overlay merging hid the base account and project selection could only
see the overlay. The SDK now owns a shared merge function used by runtime and
server discovery. It preserves distinct OAuth credential sources under unique
connection IDs (`Linear-base`, or a collision-safe numbered suffix), retaining
`Linear` overlay precedence for existing selections. Identical credential sources
remain one connection. Base connections with existing explicit OAuth credentials
are selectable; unsigned catalog entries remain unavailable.

Discovery labels exact connection IDs, and Crew guidance requires verifying
workspace identity with read-only service tools and clarifying ambiguous account
requests. Account identity is not inferred from token paths. Credentials remain
backend-only. Regression coverage verifies both accounts survive, alias conflicts
cannot overwrite a configured server, identical accounts are not duplicated,
and an unsigned catalog entry cannot become selectable.
