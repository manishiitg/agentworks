[← app / activity](index.md)

# PLAT-111 — Cost Analysis blocks first paint on an unbounded all-history scan and per-run log fan-out

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | app |
| Area | activity |
| Summary | Opening **Cost Analysis** can leave the whole pane on `Loading cost data...` |

| Field | Value |
|---|---|
| Status | `implemented_pending_live_reverify` — bounded ledger summary, cursor pagination, and removal of the initial log fan-out are implemented; restart and live UI timing remain |
| Priority | P1 |
| Owner | Cost Analysis query boundary, cost-ledger rollups, and `CostsPopup` lazy detail loading |
| Reported | 2026-08-16 |
| Evidence | `frontend/src/components/workflow/CostsPopup.tsx`, `frontend/src/services/api.ts`, `cmd/server/workflow.go`, `cmd/server/workflow_review_data.go`, `cmd/server/cost_storage.go`, `pkg/costledger/sqlite.go` |
| Related | [PLAT-081](../../ops/cost/plat-081.md)), [PLAT-090](../../ops/cost/plat-090.md)) |

## Problem

Opening **Cost Analysis** can leave the whole pane on `Loading cost data...`
for a long time. The initial view should need only totals and a small recent
daily window, but its current request loads and expands the workflow's complete
retained cost and timing history before the loading state clears.

This is an unbounded scaling defect rather than a slow machine or a rendering
problem. Every additional day, run, evaluation, and timing file makes the next
open slower. The screenshot that triggered this ticket shows an account with
2.7 billion displayed tokens; this is precisely the history size at which an
all-history implementation becomes unusable.

## Verified current path

### Frontend

`CostsPopup.loadAllCosts` makes one unbounded
`GET /api/workflow/costs?workspace_path=...` request and then, before it clears
`loading`, loops over every returned run **serially**. For each run it calls
`GET /api/workflow/logs` only to obtain step titles for a breakdown that the
user has not opened yet.

Consequences:

- initial request count is `1 + historical run count`;
- the log calls are sequential, so their latencies add together;
- collapsed per-run detail is fetched eagerly;
- the loading effect depends on `runFolders`, so unrelated run-list changes can
  repeat the full load even when the cost store did not change;
- `/api/workflow/costs` is not deduplicated, abortable, or revision-cached.

### Backend

`handleGetCosts` accepts only `workspace_path`. It has no date range, page,
cursor, summary/detail mode, or response limit. `loadWorkflowCosts` then does
all of the following synchronously for every open or refresh:

1. `SummarizeWorkflow` selects every matching `cost_events` row and aggregates
   it in Go. The SQLite workflow/scope/date index can locate the rows, but the
   all-time query still reads and decodes the full workflow history.
2. It recursively reads every phase daily cost JSON.
3. It recursively reads every execution daily cost JSON to build run totals.
4. It recursively reads every evaluation daily cost JSON to build evaluation
   totals.
5. `readWorkflowRunDailyCosts` traverses and parses the execution and
   evaluation daily trees again to build the daily projection.
6. `loadWorkflowActivityTiming` loads all Pulse metrics and walks every retained
   workflow and evaluation `*-timing.json` file.

Thus one response combines an all-time ledger query, repeated cost-file tree
walks, and an all-time timing-file walk. The browser then adds the per-run logs
fan-out on top.

### Retained-data evidence

The problem is already material in normal workspaces:

| Workflow | Cost JSON files read by the unbounded path | Cost JSON bytes | Workflow timing JSON files |
|---|---:|---:|---:|
| Upwork | 435 | 2,819,253 | 52 |
| Social Media | 311 | 2,861,619 | 120 |
| LinkedIn | 280 | 1,632,731 | 55 |
| Instagram | 194 | 1,413,545 | 235 |
| Build in Public | 153 | 485,860 | 20 |
| RTS Latency | 146 | 702,766 | 77 |

These counts exclude the additional evaluation timing walk and the frontend's
one-log-request-per-run work. Latency and payload grow with retained history
even when the user only wants today's summary.

## Required repair

### 1. Make initial loading bounded

Replace the monolithic response contract with a bounded query, for example:

- a compact summary containing authoritative all-time totals, recent daily
  totals (default 30 days), available-date bounds, and a next cursor; and
- an explicit detail query for one date or execution when the user expands it.

The UI should render the summary immediately and offer **Load older days**.
The initial response size and work must not grow with total retained history.

All-time headline totals must come from a maintained aggregate or a SQL
aggregate/rollup query, not by returning every historical event to Go and
reconstructing the total on every open.

### 2. Remove the frontend N+1 log load

Do not call `/api/workflow/logs` while constructing the collapsed list. Fetch
one run's step detail only when that run is expanded, cache it by immutable
execution identity, and cancel or ignore it if the workflow changes.

If step labels are needed in the compact response, return one small batched
`step_id → title` map from the plan rather than fetching every run's complete
logs. Bounded parallelism is not a substitute for lazy loading; it would still
make initial work proportional to history.

### 3. Stop reparsing the same historical files

Use the SQLite cost ledger as the authoritative read model for this UI and
maintain/query rollups keyed by workflow, UTC date, scope, execution, and model.
Cost JSON files can remain durable compatibility artifacts, but the initial UI
must not recursively scan them.

If a transitional file reader remains, it must parse each scope once per
revision and derive run-total and daily projections from the same in-memory
result. It must not independently traverse the execution and evaluation trees
twice.

Timing must follow the same boundary: recent aggregate timing in the summary,
one execution's timing on expansion. Do not walk every retained run directory
when the popup opens.

### 4. Cache by a real cost-store revision

Return an ETag or monotonic ledger revision and cache the compact response per
workflow. Reopening an unchanged view should render cached data immediately
and revalidate cheaply. Invalidate on a cost/timing append, not because the
React `runFolders` array changed identity. Deduplicate concurrent requests and
use an abort/generation guard when switching workflows.

## P0 performance and correctness coverage

Build a realistic fixture with at least 365 daily shards, 10,000 ledger events,
1,000 executions, and retained timing files. The test must exercise the real
HTTP handler and frontend loading contract, not only helper functions.

1. Opening Cost Analysis makes no `/api/workflow/logs` request before a run is
   expanded.
2. The initial API response contains only the bounded recent window and a
   cursor/date bound; its row count and payload do not grow when history grows
   from 30 to 365 to 1,000 days.
3. Warm initial load is below 500 ms and cold initial load below 1 second on the
   fixture, with the threshold measured in CI on a controlled local server.
4. All-time headline cost/tokens and recent daily totals exactly match an
   unbounded oracle over the same ledger.
5. Expanding one run performs at most one detail request, returns that run's
   step/model breakdown, and re-expanding it uses the cache.
6. Switching workflows aborts or ignores the old response; it cannot replace
   the new workflow's costs.
7. Reopening an unchanged workflow paints cached summary data immediately and
   a revision/ETag revalidation returns without rebuilding history.

## Acceptance

1. Cost Analysis shows totals and recent days quickly regardless of how many
   historical days are retained.
2. Older days and per-run details remain available on demand without changing
   accounting semantics.
3. Initial loading performs bounded database work, no recursive all-history
   timing/cost-file walk, and no per-run log fan-out.
4. All-time totals remain exact and reconcile with the canonical ledger.
5. The P0 scale test prevents an apparently harmless history expansion from
   restoring the current linear slowdown.

## Implementation — 2026-08-16

The initial Cost Analysis path now requests
`GET /api/workflow/costs?view=summary&days=30`.

- SQLite calculates exact all-time headline totals with one grouped aggregate
  query instead of decoding every historical event into Go.
- Only the requested recent date window retains per-day and per-execution
  detail. The response returns `history.next_before` when older events exist.
- The frontend offers **Load older days**, merges that bounded page into the
  daily table, and ignores a response from a workflow that is no longer
  selected.
- The authoritative ledger path does not walk cost/timing artifact trees and
  does not call `/api/workflow/logs` for historical runs.
- Time is displayed from the ledger's canonical
  `llm_generation_duration_ms`, labelled **LLM time** rather than presenting it
  as wall-clock agent time.
- Workspaces without canonical scoped ledger data keep the previous artifact
  reader as a compatibility fallback; it is not used by Social Media.

### Verification evidence

- Social Media's old page remained loading beyond 27.8 seconds and hit the
  frontend's 15-second timeout.
- Its canonical ledger currently contains 2,570 events across five product
  scopes. The new all-time grouped SQL aggregate completes in approximately
  0.01 seconds on the retained database.
- Cost-ledger overview tests verify exact all-time totals, bounded daily detail,
  cursor detection, scope/execution attribution, duration, and billing-basis
  totals.
- Server tests exercise both the summary loader and real HTTP handler contract.
- `go build ./...`, the focused Go tests, TypeScript compilation, and the Cost
  popup unit tests pass.

The existing ETag/revision cache and large controlled P0 fixture remain useful
hardening work, but they no longer block first paint: the live request is now
bounded and avoids both recursive artifact walks and the N+1 log calls.
