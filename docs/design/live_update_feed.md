# Live update feed: one SSE per tab instead of timers

Status: Phase 1 built (2026-09-24); shared Plan/Graph refresh restored (2026-09-28).
- **Server:** `agent_go/internal/livefeed`, `cmd/server/live_feed.go`.
- **Client:** `frontend/src/services/liveFeed.ts`,
  `frontend/src/hooks/useLiveRefetch.ts`.

## Problem

With one user idle on Dominion, the browser makes 150–220 API calls every
5 minutes. Almost all of them are timers that ask "did anything change?" and
get "no":

| Endpoint | Caller | Interval (after the 2026-09-24 cuts) |
|---|---|---|
| `GET /api/header-summary` | `GlobalActivityMonitor.tsx:219`, `useChatStore` safety poll | 10s, plus a 60s safety poll |
| `GET /api/workflow/pulse-module-state` (252 KB) | `WorkspaceViewHost.tsx` | 30s while the Pulse view is open |
| `GET /api/org-dashboard/notifications` | `GlobalActivityButton`, `WorkflowActivityButton` | 30s each |
| `GET /api/report-human-inputs[/aggregate]` | `usePendingDecisionCount`, the activity buttons | 30s each |
| `GET /api/scheduler/config` | `useGlobalSchedulerPaused.ts` | 30s |
| ~~`GET /api/browser/sessions`, `/api/wp/api/processes`, `/api/wp/api/browser/processes`~~ | ~~`RuntimeHealthControl.tsx`~~ | Removed 2026-10-07 with the Runtime Health panel (PLAT-696) |

This design has two costs:

- **Slow networks.** On a lossy link, polling fills the connection and delays
  the requests the user is waiting on. On 2026-09-24 page reloads took 5
  minutes.
- **Slow updates.** A change shows up only on the next timer tick, 10–30s
  later.

## Proposal

The server keeps **one** Server-Sent Events connection per browser tab. Over
it the server pushes small **change notices**. A notice says what changed and
where. It carries no data. Each panel keeps its existing fetch and calls it
only when a relevant notice arrives.

```
browser tab ──GET /api/live (SSE)──> agent server
                                      ▲
   writers ── livefeed.Publish(kind, workflow) ──┘
```

### Scope: the header and the right pane

The feed exists for two surfaces:

- **Header (top bar):**
  - the activity monitor (`GlobalActivityMonitor`, from `header-summary`)
  - the global and workflow activity buttons (notifications and pending
    "needs your input" counts)
  - the scheduler-paused indicator
- **Right pane (workspace views in `WorkspaceViewHost`):** Pulse, Report,
  Backup, Publish, notifications and decisions. Each view refetches
  only while it is the open view.

**Chat conversations are out of scope.** They keep their own per-session
streams (`/api/sessions/{id}/events/stream`) unchanged. This feed never
carries chat events, token deltas or session transcripts. The `sessions` kind
below only tells the header's activity monitor that the list of running
sessions changed. It carries nothing from inside a conversation.

**Left (chat) and right (workspace pane) state are separate.** Chat events
never refresh, mark stale, or otherwise touch the right pane. The right pane
gets its state only from the server (its own fetches plus this feed) and from
the user's clicks. The one allowed link is explicit agent navigation
(ui-control `open_workspace_view`), delivered over the chat's SSE. Its lease
renews every 5 minutes; this used to be a 10s poll. Because the chat's SSE can
be down when the agent queues an action, the broker also publishes a
`ui_control` notice naming the session to its owner (PLAT-703). The tab bound
to that session syncs at once; the action itself is still claimed and acked
through the ui-control endpoint, and the notice carries nothing from it. While
a tab is bound and this feed is down, its renewal runs every 60s instead.

**Sending a message does not directly refresh the right pane.** A concrete
workspace write publishes its own notice, and a workflow or Crew chat turn
publishes one `plan` catch-up notice when it finishes because a coding CLI may
have written plan files directly. Finished workflow runs and scheduled sessions
also publish notices for their affected views. ChatArea does not fire
`WORKFLOW_LOG_REFRESH_EVENT`; the decisions panel listens for `human_inputs`.

**Plan/Graph edits use this feed.** Workspace tool and server writes to
`planning/plan.json`, `planning/step_config.json`, or `workflow.json` publish a
`plan` notice. The visible shared canvas in AgentWorks and Relays refetches
the plan and triggers without clearing the existing graph. A Builder chat turn
also publishes once when it finishes, covering direct coding-CLI file writes
that bypass workspace tools. The manual refresh remains available.
Crew's separate Plan panel subscribes to the same notice. Legacy Crew project
paths publish a path-free `plan` notice; the panel refetches through its normal
authorized read endpoint, so no private project path appears in the stream.

### Wire format

```
event: change
data: {"kind":"pulse_state","workflow":"Workflow/tectonic-usa-day-trading"}

event: resync          # on connect, and after any dropped notices
data: {}

: heartbeat            # every 15s (Cloudflare closes idle streams at 100s)
```

These are the kinds, and the refetch each one triggers:

| kind | Scope | Surface | Client refetches |
|---|---|---|---|
| `sessions` | user | Header: activity monitor | `header-summary` |
| `schedules` | global | Header: activity monitor | `header-summary` (schedule summary) |
| `notifications` | workflow | Header: activity buttons; right pane: notifications | org-dashboard notifications |
| `human_inputs` | workflow | Header: pending count; right pane: decisions | `report-human-inputs` and the aggregate |
| `scheduler_config` | global | Header: scheduler-paused indicator | `scheduler/config` |
| `pulse_state` | workflow | Right pane: Pulse (only while open) | `pulse-module-state` |
| `report` | workflow | Right pane: Report dashboard | re-run the dashboard (`ReportViewer` `refresh()`: document catalog + HTML) |
| `plan` | workflow, shared crew, or path-free legacy project | Right pane: Graph/Plan while open | refetch `plan.json`, `step_config.json`, and graph triggers |
| `ui_control` | session owner only (`"session":"<id>"`) | Right pane: the tab bound to that chat session | ui-control `sync`, which claims any queued view action |

A notice never contains the changed data. The client always refetches through
the existing endpoint, which applies its own access rules. This keeps the
feed small and keeps one source of truth for every shape.

### The Report dashboard refreshes itself (highest value)

Today the dashboard (`db/reports/index.html` in `ReportViewer.tsx`) reloads
only in two cases: the user clicks its refresh button, or an agent calls
`refreshWorkspaceView` (useWorkflowStore.ts:1022). New data written by a
scheduled run or a step never shows up by itself.

**Server publishes `report` for a workflow when:**
1. **A run or session in that workflow finishes** (terminal status on a
   tracked execution or session). This is the main trigger. Most dashboard
   data is written during agent turns, often by coding CLIs straight into
   `db/db.sqlite`, where the server cannot see individual writes.
2. **A write under `<ws>/db/reports/`** goes through the agent server
   (`writeFileToWorkspace`, the `/api/wp` proxy write routes).
3. **A DB write** goes through the workspace service's `/api/mutate` or
   `/api/report-field` via the agent-server proxy.

**Client:**
- While the Report view is open, a `report` notice for its workflow calls
  the existing `refresh()`. Notices are debounced, so there is at most one
  reload every 10s during a busy run.
- A notice that arrives while the view is closed marks it stale, and it
  reloads when opened.
- The reload keeps scroll position and the selected document. If
  `HtmlReportFrame` cannot re-run in place, the dashboard is swapped only
  after the new HTML has loaded, so it never flashes blank.

**Dashboard edits and DB writes go through tools only.** Report HTML
(`db/reports/`) and workflow-DB data are never edited by hand. So
`pkg/workspace` publishes `report` on every successful tool write
(`live_feed_hooks.go`). That covers update, diff/patch, move, delete and
upload under `db/reports/`, and the workflow-DB mutate and migration tools.
Open Report views refresh within about 1s, at most once every 2s during a
burst. No file watching is needed.

### Server

**Package `agent_go/internal/livefeed`.** It is importable from both
`cmd/server` and `pkg/...`, so there is no import cycle.

- `Publish(kind string, workflow string)`: non-blocking and safe from any
  goroutine. If there is no subscriber it does nothing.
- Each subscriber has a small buffered channel.
  - **Coalescing:** a burst of writes, such as a Pulse run updating 20 rows,
    becomes one notice per `(kind, workflow)`. The connection loop flushes
    pending notices every 250ms.
  - **Overflow:** if the buffer fills, the subscriber is marked dirty and
    gets one `resync` instead of notices being silently dropped.
- Everything runs in one process. Each deployment runs one agent server, so
  no Redis or other broker is needed.

**Route `GET /api/live`.**

- **Auth:** the existing `AuthMiddleware`. `?token=` is already supported for
  EventSource.
- **Filtering:** it drops workflow-scoped notices for workflows the user
  cannot see.
  - The check uses `workflowAccessForWorkspacePath`, cached per connection.
  - The cache is dropped when `manifestMutationGeneration` changes.
  - `sessions` notices go to every connection; `header-summary` already
    filters the sessions on refetch.
  - A notice published with `PublishToUser` (only `ui_control`) goes only to
    that user's connections; the user is never written to the wire.
- **Headers:** the same as `handleSSEStream`: no write deadline and
  `X-Accel-Buffering: no`.

**Where the server publishes.** File:line references come from the
2026-09-24 survey.

| kind | Publish from |
|---|---|
| `pulse_state` | About 20 writers in `pulse_worklist.go`, `pulse_final_commands.go`, `pulse_goal_work.go`, `pulse_schedule.go`, `pulse_fast_requests.go`, `pulse_fix_run.go` and `pulse_review_notes.go`. Add one `notePulseStateChanged(ws)` helper and call it after each commit. Also publish on `workflow.json` writes (`noteWorkspaceMutation` already sees them), because the response includes autonomy, focus areas and next pulse. |
| `sessions` | The `activeSessions` and `trackedWorkflowExecutions` mutators (server.go `trackActiveSession`, `updateSessionStatus`, `handleDismissSession`, `cleanupInactiveSessionsAt`, `handleQuery` start/finish; `workflow_execution_tracker.go` start/finish/cancel). **Not** `updateSessionActivity`, which runs on every event: publish only when a status changes. |
| `schedules` | A watcher (`watchScheduleSummaryForLiveFeed`) recomputes the cached summary when `scheduleSummaryGeneration` moves, and publishes only when the value actually changes. Summary reads reconcile run state, so publishing straight from `noteScheduleSummaryChange()` could turn a refetch into another notice. It runs only while a stream is open. |
| `notifications` | `SendUserNotification` and `UpsertPulseResultActivity` (services/org_dashboard_connector.go). |
| `human_inputs` | `create`/`answer`/`dismiss`/`consumeReportHumanInput` (report_human_inputs.go), plus `dismissDuplicateHumanInput`. The two `pkg/` outliers (`consumeLinkedPulseDecisionTx`, `activateApprovedAdvisorSpecialization`) run inside agent turns and are covered by the turn-completion publish. |
| `report` | Session/execution terminal status (the same hook as publish-on-session-completion). Writes under `db/reports/` via `writeFileToWorkspace` and the `/api/wp` proxy. Proxied `/api/mutate` and `/api/report-field`. |
| `scheduler_config` | `SaveSchedulerConfig` (scheduler_config_store.go:43). |
| `ui_control` | `performUIActionForContract` when the broker accepts a fresh action (`publishUIControlWake`, to the chat session's owner). |
| `browser_sessions` | The `browser.SessionTracker` mutators (`Touch` on first sight only, `Remove*`, `Close*`, `Clear`). The client computes `age`/`idle` from timestamps instead of polling for them. |

**Writes the feed cannot see.** Coding CLIs and shell `/api/execute` write
straight to disk, and the workspace service's `/api/mutate` writes
`db.sqlite` without going through the agent server. The client covers these
three ways:

1. **Resync** on every (re)connect, and whenever the tab becomes visible
   again.
2. **Session completion.** Most direct file writes happen during an agent
   turn. On terminal workflow or Crew chat status the server publishes `plan`;
   finished tracked runs publish `plan` alongside their other right-pane
   notices. This catches writes that bypass workspace tools.
3. **Safety poll.** A slow poll every 5 minutes, only while the tab is
   visible. It replaces the current 10–30s timers.

The workspace process lists (`/api/processes`, `/api/browser/processes`) and
the Runtime Health panel that polled them were removed on 2026-10-07
(PLAT-696); stale processes and browser helpers are cleaned up automatically.

### Client

**`frontend/src/services/liveFeed.ts`** is a singleton.

- It opens the EventSource once, using the same token handling as
  `shared/session/sse.ts`.
- API: `subscribe(kind, workflow | null, onChange) → unsubscribe` and
  `onResync(cb)`. The hook form is `useLiveRefetch(kind, workflow, refetch,
  {enabled})`.
- **Reconnect:** exponential backoff, and a `resync` on reconnect.
- **Fallback:** if the stream cannot connect, it reports
  `status: 'polling'` and each hook falls back to its old interval. Nothing
  depends on the feed to work.

Each panel keeps its existing fetch and replaces `setInterval(fetch, N)` with
`useLiveRefetch(kind, workflow, fetch, {enabled: visible})`.

### Expected effect

| | Today | With the feed |
|---|---|---|
| Idle requests per tab | ~30/min | ~0.2/min (safety poll) + one open stream |
| Delay before a change shows | 10–30s | <1s |
| Pulse view while a run is working | 252 KB every 30s | 252 KB once per coalesced change |

### Connection budget

This adds one long-lived connection per tab.

- **Behind Cloudflare (HTTP/2):** all streams share one TCP connection, so
  this costs nothing.
- **Plain HTTP/1.1 (local dev, some self-hosted setups):** the browser cap of
  6 connections per origin already squeezes the per-session chat streams
  (`ChatArea.tsx:2397`). This adds one more. The feed opens after the active
  chat's stream and stays a single connection per tab. If it cannot get a
  slot, the client falls back to polling instead of queueing behind chat.

## Phases

1. **Feed, the Report dashboard, and the biggest pollers.** `report`
   (dashboard auto-refresh on run completion) comes first, because it is the
   most visible gain. It needs publish-on-session-completion. Also: the
   `livefeed` package, `/api/live`, the client singleton,
   `sessions`/`schedules` (header-summary),
   `human_inputs`, `notifications`. This removes about 80% of the idle
   traffic.
2. **Pulse.** `pulse_state`. Drop the Pulse timer.
3. **The rest.** `scheduler_config`. (`browser_sessions` was dropped with the
   Runtime Health panel, PLAT-696.)

Each phase keeps the polling fallback. A phase is done only when the gateway
log on Dominion shows that endpoint's idle rate near zero.

## Testing

- **Unit:** `livefeed` coalescing, overflow→resync, and access filtering.
- **Live (e2e):** with a real Builder turn that adds a step, the canvas
  updates within 1s and has no timer running. The same for a Pulse run
  updating the Pulse view.
- **Traffic check:** after deploy, the per-endpoint rates in
  `/srv/dominion/logs/gateway.log` for an idle tab.

## Related finding (separate fix)

These read handlers accept any `workspace_path` and never check workflow
read access:

- `plan-changelog`
- `pulse-module-state`
- `org-dashboard/notifications`
- `report-human-inputs`

`requireWorkspacePath` only validates the path. They should call
`requireWorkflowVisible` (workflow_access.go:220). This should be tracked
separately from the feed.
