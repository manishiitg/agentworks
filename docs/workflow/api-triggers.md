# Workflow API triggers

A workflow route can run from either a **time trigger** (cron/calendar) or an **API trigger** (an inbound webhook). Several triggers can target the same route. API triggers use the normal workflow execution, ownership, cancellation, run history, and run history. Webhooks skip the entire post-run Pulse lifecycle, including backup and publish.

## Setup

Ask the workflow builder chat to create a webhook with a name, optional saved routing/branch selections (omit them to run the full workflow), variable groups, and authentication mode. Create and edit bindings through builder chat; **Views → Webhooks**, in the view toolbar, displays endpoints and existing-trigger controls. Each trigger gets its own endpoint and secret. The selected route runs through the full plan, including its prerequisites; it does not jump directly to an arbitrary step.

Use generic bearer authentication for services capable of sending an Authorization header. Use GitHub authentication for GitHub's signed webhooks. Copy the generated secret immediately; it is returned only at creation/rotation and stored encrypted with the existing server secrets key. Editing authentication mode rotates the secret. Disable revokes new deliveries and access to run results/downloads; remove deletes the attachment. Neither interrupts a run already accepted; use Stop in run history for that.

The displayed URL uses the currently connected server. Localhost can receive requests from the same computer. Public services require a reachable HTTPS deployment or a separately configured tunnel; this feature does not publish the local server automatically.

## Generic caller

```sh
curl -X POST 'https://YOUR_SERVER/api/hooks/workflow/TRIGGER_ID' \
  -H "Authorization: Bearer $TRIGGER_SECRET" \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: unique-delivery-123' \
  -H 'X-Webhook-Event: issue.created' \
  -d '{"issue":{"number":123,"title":"Example"}}'
```

The body may be any valid JSON up to 1 MiB. Authentication and arbitrary request headers are not copied into the run. Fields such as `model`, `route_selections`, `selected_folder`, or `execution_options` inside the JSON remain payload data and cannot override the saved execution configuration.

For GitHub, configure Payload URL, `application/json`, and Secret in its webhook settings, then select the events to deliver. AgentWorks verifies HMAC-SHA256 over the raw body using `X-Hub-Signature-256`, records `X-GitHub-Event`/`X-GitHub-Delivery`, and acknowledges setup `ping` without starting a run. See [GitHub's signature specification](https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries).

Other services can call the generic JSON endpoint directly if they support its authentication. Provider-specific challenge/signature schemes (for example Stripe or Slack) require an adapter; they are not interpreted as GitHub signatures.

## Run input

The server writes `webhooks/deliveries/<run_id>.json` inside the workflow before dispatch. An envelope contains:

```json
{
  "run_id": "generated-run-id",
  "delivery_id": "unique-delivery-123",
  "event": "issue.created",
  "received_at": "2026-09-12T10:00:00Z",
  "payload": {"issue": {"number": 123, "title": "Example"}}
}
```

Steps receive the file reference as run context. Script and agent shells also get its absolute path in `WORKFLOW_TRIGGER_INPUT_FILE`:

```python
import json
import os

with open(os.environ["WORKFLOW_TRIGGER_INPUT_FILE"], encoding="utf-8") as source:
    delivery = json.load(source)
issue_number = delivery["payload"]["issue"]["number"]
```

The execution folder guard grants this delivery file for reading. A payload reference never fills a required human_input response. Delivery files are retained in the workflow independently of rotated execution folders; there is no automatic payload expiry in this version.

## Responses and retries

- **202:** accepted, with `run_id`, `delivery_id`, and a relative `status_url`. Execution continues asynchronously; poll `status_url` for results.
- **200, duplicate=true:** the delivery ID already has a durable run record. Returns its run ID and current status, including failed/interrupted status. No new run is launched.
- **200, status=pong:** authenticated GitHub setup ping.
- **401:** missing/invalid credentials or signature.
- **404 / 410:** unknown / disabled trigger.
- **400 / 413 / 415:** malformed payload or metadata / payload too large / unsupported content type.
- **503, Retry-After:** workflow could not start (for example busy or saved route unavailable). Storage unavailability also returns 503.

Reuse an Idempotency-Key for retries of the same event; GitHub uses its delivery ID. Without an ID, each request is a separate delivery. Duplicate detection survives server restarts through the scheduler run store and its unique run identity. Use a new delivery ID to intentionally run an event again after a failed or interrupted run. This is not an automatic durable delivery queue: the sender must retry rejected requests, and interrupted accepted runs remain visible in run history rather than being replayed automatically. GitHub deliveries may need manual redelivery or an external retry adapter.

## Configuration and management API

API triggers are stored beside time schedules in `workflow.json.schedules`, with `schedule_type="webhook"`, `route_selections`, `group_names`, constrained run mode, and a `webhook` configuration containing auth mode and encrypted secret. The scheduler does not register clock ticks or missed occurrences for them. A webhook can overlap schedules and other triggers. Each trigger accepts up to four concurrent deliveries; every delivery owns a separate runtime state, durable lease, session, input file, and immutable hook folder.

Authenticated management endpoints are `GET/POST /api/workflow-webhooks` and `PUT/DELETE /api/workflow-webhooks/{id}`. Supply `workspace_path` in mutation JSON or GET/DELETE query parameters. Writes require workflow ownership/write access; readers may inspect bindings but receive no secrets. POST/PUT accept `name`, `enabled`, `auth_mode` (`bearer`/`github`), `route_selections`, `group_names`, and optional `rotate_secret`. Responses include the relative endpoint `path`; a plaintext `secret` appears only when newly issued. The inbound endpoint and its run-result/artifact endpoints bypass user JWT authentication and perform their own trigger-specific authentication.

Incoming payloads never use the legacy `trigger_payload` session override mechanism. API triggers reject that field, schedule-local messages, session resume, dependency queues, and clock settings. Workflow duplication excludes API triggers; configure a new attachment and credentials in the copy.

## Observing webhook runs

The builder conversation panel has a **Webhooks** filter beside Recent, Schedules,
and Bots. Webhook executions appear here; the Schedules filter shows time jobs.
The feed refreshes every 10 seconds while either run filter is visible. Each
webhook run links to its execution transcript and shows its status and run folder.
New run records retain the trigger name, event type, delivery ID, and received
time. When a delivery contains deploy fields, run history also retains
`commit_sha`, `component`, `env`, and `deployed_at`. The full payload and
authentication secret are excluded from history responses. The original
delivery body is stored with the run artifacts and can be opened in the UI.
External `get_schedule_runs` includes the deploy fields under each run's
`webhook` object; `get_run` includes the same object for a webhook run folder.
Workflow run history can be paged with `limit` and `offset` and retains terminal
records for at least 90 days, independently of run artifact retention.

These fields describe the delivery that **started that run**. A rollout may
send one ping per component. If several pings share a SHA and collision policy
skips overlapping deliveries, the recorded run is associated with the accepted
delivery. If two rollouts overlap, its SHA is the deploy that started the run,
not necessarily the last deploy that finished while it was running. Skipped or
busy deliveries do not create run history entries.

The activity monitor and current-workflow header distinguish Webhook, Scheduled,
and Manual origins. Older sessions with a `schedule-webhook--` identity are still
shown as Webhook even if their legacy session metadata says `cron`.

This is execution history, not a durable inbox: busy deliveries still receive 503
and have no pending entry. Pending-delivery visibility requires extending the
existing scheduler's pending-event storage.

The workflow Schedules panel also shows the POST endpoint and a copy button on
each webhook row, including disabled triggers. Credentials remain in API trigger setup.

## Triggers in Plan

Plan displays saved schedules and webhooks above Start, with timing, state,
route selections, groups, and webhook URLs. Settings buttons open the existing
Schedules or API triggers panels. The cards refresh from the scheduler every
15 seconds while Plan is open; they are projections, not editable plan steps.

Highlight path starts at workflow entry, retaining prerequisite steps and
following saved choices at routing/branch nodes. Unspecified decisions show
possible paths. Missing route bindings are shown as unavailable. Pulse-only and
optimizer jobs appear as maintenance triggers and do not link into plan execution.


## Isolated outputs and CI polling

Each new delivery gets `runs/iteration-<n>-hook/<group>/`. These folders are not
rotated into or overwritten by `iteration-0`; duplicate delivery IDs retain their
original run. The latest 10 terminal hook folders are retained, plus every active hook. Hook
retention is independent of ordinary run rotation; delivery payloads remain
retained separately. A webhook does not drain answered Pulse
decisions or start post-run Pulse, backup, publish or reviewer turns. A direct
webhook never performs a workflow contract upgrade: it fails closed when the
saved contract is incompatible and tells the owner to update the workflow
manually in Builder before retrying the delivery. Explicit steps in the selected
workflow remain part of the run.

Poll `GET /api/hooks/workflow/{trigger_id}/runs/{run_id}` using
`Authorization: Bearer <trigger-secret>`. This read authentication also applies to
GitHub-mode triggers; inbound GitHub POSTs still require HMAC signatures. Only
runs owned by that trigger/workflow are returned. Disabling/deleting the trigger
revokes reads, including signed download links.

```json
{
  "run_id": "example-run-id",
  "status": "completed",
  "terminal": true,
  "run_folder": "iteration-1-hook",
  "finished_at": "2026-09-12T12:00:00Z",
  "steps": [{
    "step_id": "smoke", "group": "dev",
    "outputs": {"result.json": {"passed": 2, "failed": 0}},
    "artifacts": [{
      "name": "video.webm",
      "path": "dev/execution/smoke/video.webm",
      "size_bytes": 1234,
      "download_url": "/api/hooks/workflow/TRIGGER/runs/RUN/artifact?path=...&token=...",
      "expires_at": "2026-09-12T12:30:00Z"
    }]
  }]
}
```

The schema inside each step output belongs to the workflow author. `completed`
means execution completed; CI must also enforce its semantic assertion (for
example `result.json.failed == 0`). Failed/stopped/interrupted runs are terminal
too and retain available outputs. A newly accepted run can have an empty folder
and steps until dispatch allocates them. Poll until `terminal` is true, with a
caller timeout and a modest interval (e.g. five seconds).

JSON and common text outputs under each group's `execution/<step>/` are inlined
up to 128 KiB per file and 2 MiB per response. Binary/larger outputs remain available
as artifacts. Up to 10,000 output files are listed (`truncated=true` beyond that).
Hidden files, code, logs and symlinks are excluded. Evaluation outputs outside the
run's execution tree are not included. The first terminal poll persists the
result document, so later polls return the same inline outputs; files remain
in the unique run folder. Do not edit historical run files.

Download URLs are relative to the server and carry a signed, file/run/trigger
scoped token lasting 30 minutes. They support GET, HEAD and byte ranges. Poll
again to refresh expired links. Treat links as credentials. No login cookie is
needed; a token for one file cannot download another file or poll other runs.
CI can download each artifact and upload it through its own artifact facility.

Example after saving the accepted response as `accepted.json`:

```sh
STATUS_PATH=$(jq -r .status_url accepted.json)
for attempt in $(seq 1 360); do
  curl --fail --silent --show-error "$SERVER$STATUS_PATH" \
    -H "Authorization: Bearer $TRIGGER_SECRET" > result.json
  if jq -e '.terminal == true' result.json >/dev/null; then break; fi
  sleep 5
done
jq -e '.terminal == true and .status == "completed"' result.json
# Replace this assertion with the workflow's actual output contract:
jq -e '[.steps[] | select(.step_id == "smoke") | .outputs["result.json"]] |
  length > 0 and all(.[]; .failed == 0)' result.json
```

The shared Playwright helper detects builder versus schedule/webhook/bot context.
Unattended runs skip live-view registration. Builder live view/recording is
unchanged; Playwright test video/trace output follows the test configuration and
can still be saved as downloadable step artifacts.


## Runtime group and variable selection

Keep `input_mode="raw"` for native provider webhooks; all body fields remain event
data. For CI callers, Builder can set `input_mode="envelope"` and
`allowed_variables=["base_url", "test_suite"]` using manage_workflow_webhook.
Names must be declared non-secret workflow variables. Settings are returned by
list and preserved by updates that omit them; an empty array clears permission.

```json
{
  "group": "dev",
  "variables": {"base_url": "https://staging.example.com", "test_suite": "smoke"},
  "payload": {"pr_number": 123}
}
```

`group` selects one of the trigger's `group_names`. Omit it to execute all saved
groups. Overrides are string values (maximum 16 KiB each) scoped to the delivery;
they do not edit variables.json or secrets. Unknown groups, variables, protected
names or invalid envelopes return 400. Steps read the inner payload in the normal
delivery file, alongside the resolved group and variables. Native GitHub payloads
should normally stay raw; CI envelope POSTs can use bearer authentication.

Polling also returns `progress`: observed step IDs, group, path, title, status and
last-update timestamps. Outputs appear as steps write files. There is no guessed
percentage or denominator that counts unused branches. Builder can read this with
`manage_workflow_webhook(action="status", id=..., run_id=...)`.

Cleanup retains the latest 10 terminal webhook runs independently of schedules,
plus all active runs. After expiration, history/status remains accessible with
`artifacts_expired=true`; artifact requests return 410 Gone. CI should archive
assets promptly. A fifth concurrent delivery receives 503 with `Retry-After: 30`;
retry with the same delivery ID for idempotent recovery. Hooks and schedules
otherwise share provider capacity but have separate run locks. Shared
KB/scripts, database semantics and external effects are not isolated by folders:
use transactional DB tools, existing resource locks and idempotent route actions.

### Direct route execution

Webhook deliveries dispatch directly to the workflow plan executor with the saved route selections and validated group/variable inputs. They do not start a Builder chat or ask an agent to call `run_full_workflow`. Plan prerequisites and agent steps still run normally. An empty route selection runs the full plan. Scheduled jobs retain their existing execution path.

Prepare and upgrade the workflow in Builder before testing the trigger; a webhook never edits or upgrades the plan. Poll the returned status URL (or use `manage_workflow_webhook` action `status`) for progress, step outputs, final success/failure and artifact links. The isolated `iteration-<n>-hook` folders, last-10 retention, cancellation, authentication, idempotency and no-Pulse policy still apply.

### Single-step targets

Use `manage_workflow_webhook` action `list` to discover `steps`. To create or update a single-step trigger, set `step_id` to that saved ID and `route_selections={}`. Empty `step_id` selects the existing route/full-workflow behavior. The Webhooks and Plan views show the target; creation stays in Builder chat.

Supported targets are top-level executable plan steps (agent/script, agent, or an orchestrator with its own child work). Human-input and routing/branch nodes are not standalone targets; select a route instead. Nested steps must be exposed as a top-level step to bind directly. Single-step webhooks skip prior and subsequent plan steps, automatic evaluation, and Pulse. They run the chosen step once for each configured or envelope-selected group. Supply all required inputs through that group, allowed variables, or payload; never rely on outputs from a previous invocation. IDs are checked again against the loaded plan, so deletion fails clearly and reordering does not run another step.

Test using action `test`, then poll action `status`. Verify only the chosen step (and its internal work, if any) produces progress and outputs, and that failures produce a failed terminal result. Authentication, folder isolation, retention, and artifact download behavior are unchanged.
