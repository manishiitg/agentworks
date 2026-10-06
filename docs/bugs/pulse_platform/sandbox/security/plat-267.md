[← platform / security-sandbox](index.md)

# PLAT-267 — Scheduled runs resolved secrets against the wrong user identity

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | sandbox |
| Area | security |
| Summary | secrets/authorization, severity high — every scheduled run |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `fixed, live-verified on the Dominion deployment` |
| Last synchronized | `2026-09-01` |

- **Priority:** secrets/authorization, severity high — every scheduled run
  of an affected workflow silently loses access to all of its own
  configured secrets, indefinitely, with no error surfaced to the user
  beyond a downstream `KeyError` in whatever script needed the key.
- **Origin:** on the Dominion Hetzner deployment, a scheduled run's own
  agent reported: "Your trading data credentials (Polygon, Finnhub, and the
  others) aren't reaching today's scheduled runs, even though they're
  correctly set up." Confirmed for two consecutive days —
  `collect_price.py`/`grade.py`/`conviction.py` all failed with
  `KeyError: 'SECRET_POLYGON_API_KEY'` — while the identical workflow
  worked correctly whenever driven interactively from chat.

## Problem

Workflow secrets are stored per-user at
`_users/<userID>/workflow_secrets/<sha256(workflowPath)>.json`
(AES-256-GCM encrypted). Both scheduled-run call sites in
`agent_go/cmd/server/scheduler.go` (the Pulse lifecycle step runner and the
main scheduled workshop-turn loop) invoked `startSessionInternal(ctx,
reqMap, sessionID, "", nil)` — a hardcoded empty user ID. With no user in
the synthetic request's context, `GetUserIDFromContext` falls through to
`GetDefaultUserID()`, the literal placeholder `"default"`. Secret lookup
(`loadSelectedSecrets`) then resolves against `_users/default/`, which
never has anything stored — nobody configures a workflow's API keys while
logged in as `"default"`; they do it from their own real account.
Interactive chat sessions never hit this, because they always carry the
real logged-in user's claims through `AuthMiddleware`.

Confirmed live: `_users/ac513919.../workflow_secrets/` had the real
Polygon/Finnhub keys, correctly stored under the actual creating user's
account; `_users/default/` had no `workflow_secrets` directory at all.

Nothing recorded who created or owns a workflow or a schedule —
`WorkflowManifest`, `WorkflowSchedule`, and `ScheduleContext` had zero
owner/`created_by`/`user_id` fields, so the scheduler had no data to
resolve the correct identity from even if it had tried.

## Resolution

- `WorkflowManifest` gains `CreatedBy`, stamped from the authenticated
  request's user ID in `handleCreateWorkflowManifest` (the sole call site
  of `NewWorkflowManifest`) at creation time.
- `ScheduleContext` gains `OwnerUserID`, threaded from `manifest.CreatedBy`
  in `buildScheduleContext` (the single place a manifest becomes a
  `ScheduleContext` before either `startSessionInternal` call site), which
  now passes `sctx.OwnerUserID` instead of `""`.
- Purely additive: a manifest created before this field existed keeps
  today's already-broken `"default"` fallback until backfilled — not a
  regression for any other workflow.
- The one existing production workflow
  (`Workflow/tectonicusadaytrading`) was manually backfilled with its
  real owner's user ID directly in its `workflow.json` on the server
  (timestamped backup left in place), since the field didn't exist when it
  was originally created.

## Verification

- `TestBuildScheduleContextThreadsOwnerUserID` — confirms `CreatedBy`
  threads into `OwnerUserID`, and that an empty `CreatedBy` (a legacy
  manifest) safely stays empty rather than erroring.
- `TestHandleCreateWorkflowManifestStampsCreatedBy` — confirms the HTTP
  handler actually stamps the authenticated request's real user ID, not a
  hardcoded or inferred value.
- Both fail to compile without the fix (confirmed via `git stash`); full
  `cmd/server` test suite passes.
- Deployed to `trader.tectonicmarkets.com` (`dominion-agent` rebuilt and
  restarted); confirmed healthy post-deploy, and confirmed the live
  workflow's `workflow.json` correctly carries `created_by` after the
  manual backfill.
- Not yet confirmed against an actual live scheduled fire (deliberately —
  manually triggering a real run would place real paper trades and consume
  real market-data API calls; left for the next natural schedule or an
  explicit user-requested manual trigger).
