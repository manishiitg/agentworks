[← platform / frontend-chat](index.md)

# PLAT-292 — Shared, scoped, acknowledged agent UI control across workspace views and products

| Field | Value |
|---|---|
| State | deployed |
| Priority | P1 |
| Product | app |
| Area | ui |
| Summary | proposes a scoped, acknowledged UI-control framework building on PLAT-278: capability discovery, UI-state inspection, semantic actions, browser receipts and generated contracts. |

| Coordination | Value |
|---|---|
| State | Partially implemented locally: acknowledged AgentWorks baseline + Notify expansion; remaining adapters and rollout gates open |
| Date | 2026-09-05 |
| Priority | Proposed P1 platform capability |
| Type | Platform enhancement and UI-action reliability |
| Related | PLAT-278 (workspace open/refresh/target foundation) |
| Owners | Platform tools/events, shared frontend, individual view/product maintainers (unassigned) |
| Request | Give the workflow builder deeper, discoverable control of UI views; cover every current view and supported product integration through one framework |

## Outcome

The agent can discover available views and their supported actions, inspect the
current UI, navigate/focus useful content, and receive a browser-confirmed result.
It must distinguish “request emitted” from “the user can now see it.”

One platform framework serves all products. Each view contributes a typed adapter;
new views must not require hand-editing independent tool enums, prompts, event
handlers, and routing switches. Product styling may differ; scope, permissions,
action lifecycle, and receipts remain shared.

This ticket defines the full integration inventory and delivery contract. It
does not claim every proposed view action already exists or that arbitrary DOM
control is needed.

## Existing implementation and evidence

- Backend registry and tools: `agent_go/cmd/server/workflow_view_tool.go`.
  There are **22** current view IDs. `open_workspace_view` and
  `refresh_workspace_view` accept `view` and optional string `target`.
- An action emits `presentation_updated` with kind `workflow.view`, workspace,
  action and target. The tool immediately returns success; it does not await
  browser acknowledgement.
- Frontend registry: `frontend/src/components/workflow/workspaceViews.ts`.
  The Go list is mirrored with a test, not generated from a single contract.
- Consumer: `useWorkflowViewPresentations.ts`. Only the displayed chat session
  is consumed; existing presentations are treated as history and not replayed.
  Tracking currently uses presentation count, not a per-action request lifecycle.
- Shared store: `frontend/src/stores/useWorkflowStore.ts` provides open, target,
  pane visibility and refresh tokens.
- `canvas/WorkspaceViewHost.tsx` focuses plan steps and file paths;
  `ReportViewer.tsx` and `reportWidgets/HtmlWidgetFrame.tsx` provide report
  focus integration. Backend target descriptions also name database tables,
  schedule IDs/names and log step IDs. Audit each consumer before advertising
  those actions as implemented; a declared target alone is not proof of support.
- Video Studio already declares presentation bindings in
  `agent_go/internal/videoproduct/product.yaml`, with presentation tools such
  as show_video, show_character, show_reference and show_document.
- Dominion uses shared ChatArea but has its own portfolio/detail surface.
  Its current read-only chat contract must be preserved.

### Gaps

1. No bounded, authoritative UI-state inspection or action discovery.
2. Success reports emission, not visible rendering or target resolution.
3. Targets are free-form strings and unsupported targets may be silently ignored.
4. No shared select/expand/filter/highlight contract across view implementations.
5. No explicit stale-state, browser-disconnected, multi-tab, or duplicate-action receipt.
6. Tool/UI capability lists can drift; product-specific integrations can diverge.
7. An active pane must never be controlled on behalf of another account, workflow,
   product or chat merely because its store is global.

## Proposed tool surface (names subject to API review)

Keep existing open/refresh tools as compatibility wrappers over the new engine.

| Tool | Contract |
|---|---|
| list_ui_capabilities | Permission-filtered views, actions, target kinds, parameter schemas, schema version and availability; supports view/product filters and pagination |
| get_ui_state | Bounded snapshot of the bound UI instance: active view, selected stable IDs, open panels, loading/error states, filters, state revision and observation time |
| perform_ui_action | One validated semantic action against a view/target, with idempotency key and optional expected state revision; await a bounded acknowledgement |
| get_ui_action_result | Retrieve final receipt after an accepted/pending action; no re-execution |

Do not invent one tool per toolbar button. Do not expose arbitrary JavaScript,
CSS selectors, shell execution or synthetic mouse coordinates as this API.

Example proposed action:

```json
{
  "view": "flow",
  "action": "select",
  "target": {"kind": "step", "id": "verify-report"},
  "params": {"reveal_details": true, "center": true},
  "expected_state_revision": 42,
  "idempotency_key": "request-scoped-unique-key"
}
```

Server-derived scope includes user, product, workflow/workspace, chat session,
UI instance and connection generation. These are not unrestricted caller-selected
identities. If multiple clients qualify and none is explicitly bound, return
`ambiguous_client`; never broadcast an action into every open browser.

Example final receipt:

```json
{
  "request_id": "ui-action-id",
  "status": "applied",
  "view": "flow",
  "action": "select",
  "resolved_target": {"kind": "step", "id": "verify-report"},
  "state_revision": 43,
  "visible": true,
  "detail": "Step selected; details panel visible"
}
```

Lifecycle: accepted → applying → applied / failed / rejected / expired / cancelled.
Use structured reason codes: unsupported_action, target_not_found,
ambiguous_target, forbidden, stale_state, inactive_scope, browser_disconnected,
ambiguous_client, render_failed, timeout, autoplay_blocked, user_interrupted.
An accepted or timed-out action is not proof of success. Unknown outcomes must
not trigger blind retries or duplicate actions.

## Shared registry and adapter architecture

1. Add a neutral versioned JSON/YAML contract under the shared platform, or a
   build-generated artifact from one canonical source. Do not import React
   components or icons into Go. Generate Go/tool enums, TS types, validation
   schemas and documentation from it.
2. Keep frontend rendering metadata (icons, components, toolbar grouping) attached
   by stable view ID. Product manifests declare supported adapters/extensions.
3. Each mounted view registers supported semantic actions, typed target discovery,
   minimal state projection, loading readiness and completion verification.
4. Intersect manifest declarations, actually registered runtime adapters and
   effective permissions before reporting a capability as available.
5. Use the existing presentation/event transport and shared view store where
   appropriate. Add correlated command/receipt handling rather than a second
   competing layout store. Persistence/event history must not imply re-execution.
6. Browser ACK endpoint authenticates the client binding and validates request,
   scope, nonce, generation and state revision. Store bounded pending actions
   with expiry and bounded retention of terminal receipts.
7. Action handlers await mount, target resolution and the necessary render/data
   completion. “Refresh applied” requires the new data version or an explicit
   refresh failure, not only incrementing a token.
8. Unsupported actions or targets are rejected explicitly. Labels can assist
   discovery, but exact stable IDs are required when labels are ambiguous.

## Complete workflow view integration matrix

All 22 current views receive capability discovery, open/refresh and bounded state
inspection where meaningful. The deeper actions below are **proposed coverage**,
not promises that existing controls already expose these handlers.

| View ID / label | Targets and proposed presentation actions | Existing mutation boundary |
|---|---|---|
| report / Report | Report tab, widget, semantic section, metric; select tab, scroll, highlight, inspect preview load/error state | Editing/generating report remains a backend/file tool |
| flow / Plan | Step, route, group, edge, validation finding; select, center, fit, expand details, reveal dependencies, highlight errors | Add/edit/delete/run steps stays in workflow APIs with permissions |
| costs / Costs | Run, step, model, time range; select, filter, sort, expand breakdown; expose missing/unmeasured state | No pricing/ledger mutation through navigation |
| execution-logs / Execution logs | Run, step, tool call, event; filter level/status, search, jump to event, expand result | Cancel/retry/delete are separate authorized operations |
| learnings / Learnings | File, section, entry; browse, search, expand, highlight | Persist edits via guarded workspace tools |
| knowledgebase / Knowledgebase | Document, section, source; select, search, expand, reveal provenance | Index/import/edit/delete remain backend operations |
| database / Database | Table, stable row key, column; select table, read-only filter/sort/page, inspect schema, highlight row | SQL/data/schema writes remain managed DB tools; bound query size |
| evaluation / Evaluation | Run, check, step, finding; select, filter failures, expand evidence | Execute/change evaluations stays in evaluation tools |
| schedules / Schedules | Schedule ID, run ID; select, filter, expand history, reveal next-run state | Enable/disable/edit/run/delete remain scheduler APIs |
| files / Files | Authorized workspace-relative path, line/anchor, preview tab; open, reveal, scroll, highlight, inspect load/error | Write/rename/delete/download obey file APIs and access policy |
| pulse / Pulse | Pulse run, review module, finding/public ID; select, filter, expand evidence and history | Run review, approve fix, resolve issue remain Pulse tools |
| backup / Backup | Snapshot ID, status section; select, expand metadata/verification | Create/restore/delete backups require separate authority |
| publish / Publish | Publication/version, URL/status; select preview, reveal delivery state | Publish/unpublish/domain changes remain backend actions |
| notify / Notify | Channel, Gmail connection ID, Run/Pulse section, delivery receipt; expand full instructions, focus sender/recipient controls, show errors | Save policy/connect/disconnect/test-send use notification APIs; navigation never sends |
| access / Access | Workflow member, role, grant section; select, expand permission explanation | Change grants/create users remains authorized access management |
| skills / Workflow skills | Skill ID, version, reference path; search, select, expand instructions | Install/update/remove/attach uses skill APIs |
| secrets / Workflow secrets | Secret name and presence/status only; search, select metadata | Never expose values through UI state, target discovery, logs or highlights |
| mcp / Workflow MCP servers | Server ID, tool ID, connection state; select, expand cached tools/errors | Inspect does not connect/discover/test; those require explicit connector actions |
| browser / Browser automation | Setting section, configured session metadata; focus and inspect safe status | Starting sessions/changing access remains existing controls, not arbitrary browser automation |
| llm / Workflow LLM configuration | Provider/model selection, locked policy section; inspect/focus/explain disabled controls | Save changes through model policy APIs; admin locks enforced |
| bots / Workflow bots | Bot/connection ID, status section; select and inspect redacted metadata | Link/unlink/reconnect and message send remain explicit operations |
| folders / Attached folders | Attachment ID/path, read/write scope; select, expand effective access | Attach/detach or broaden access remains guarded backend mutation |

Capability details must state whether a view supports opening only, deep targeting,
or a specific filter. “All views integrated” must not hide placeholder no-op handlers.

## Cross-product and surrounding UI integrations

| Surface | Proposed integration |
|---|---|
| AgentWorks builder chat | Primary scoped host; visible activity receipts and optional undo for navigation, no duplicate assistant messages |
| Execution agents, scripted/orchestrator steps | Same scope inherited through existing session bindings; presentation requests require a bound UI or return unavailable, not false success |
| Scheduled/Pulse/background runs | No unsolicited foreground pane stealing. Persist an “Open in workspace” link; queue only if explicitly requested and scoped with expiry |
| Activity/global monitor | Select authorized workflow/run, expand Run/Pulse receipts; cross-workflow navigation requires explicit intent and access revalidation |
| Video Studio | Adapt existing show_video/character/reference/document presentations to the same lifecycle; select asset, open preview, seek/play/pause where supported; report autoplay blocking; never generate/delete media as a navigation side effect |
| Dominion | Advertise only actual portfolio, stock-detail, watchlist and shared-chat presentation actions. Preserve read-only chat; no trade/watchlist mutation via UI tools and no fabricated file-workspace view |
| Future products / product.yaml | Declare namespaced view capabilities and register adapters; shared validation/ACK/privacy tests required before advertising support |
| Report iframe/widgets | Versioned message bridge with source/origin verification, sandbox-compatible handshake, allowlisted commands and correlated ACKs. External/uninstrumented HTML reports unsupported rather than accepting arbitrary script |
| File/media previews | Reuse shared file preview and media player contracts; authorize paths and asset IDs, avoid exposing signed URLs, acknowledge rendered/error state |
| Workflow toolbar / right pane | Manual buttons and agent actions use the same controllers; closing, sizing and selection behavior must stay consistent |
| Chat tabs / account switching | Binding follows identity and explicit active context; logout clears pending actions and cached state; inactive product events cannot restore or hijack another chat |

## Safety, interaction and privacy requirements

- Initial release is presentation-only: inspect, open, refresh, select, expand,
  filter, sort, scroll and highlight. UI actions cannot bypass backend mutation
  tools by clicking save, send, run, delete, authenticate or permission controls.
- Read-only users retain view access only where existing authorization permits.
  Creator rights differ per workflow; admin status is not inferred from a UI flag.
- State is explicitly allowlisted and bounded. Return stable IDs/labels and safe
  status; do not dump DOM, hidden chats, credentials, tokens, cookies or raw secrets.
  Rich content readback follows existing data-read permissions and opt-in scope.
- Treat view/report text as untrusted data, never instructions or authority.
- Do not auto-connect MCPs while listing views or inspecting connector metadata.
- A user interacting with the pane can interrupt pending navigation. Do not
  continually reset scroll, overwrite filters, steal typing focus, close unsaved
  editors, or change product/workflow silently.
- Check state revision before stale actions. Preserve previous navigation state
  for reversible back/undo; do not promise undo of external operations.
- Do not replay old actions during hydration/reconnect. Deduplicate by request ID,
  not list length. Auth/account changes invalidate pending requests.
- On browser absence, use explicit unavailable/pending semantics with expiry.
  Never poll forever or claim a view opened on a disconnected browser.
- Keyboard access, focus-visible styling, screen-reader announcements and reduced
  motion apply equally to manual and agent-driven changes.

## Observability and agent guidance

Audit safe metadata: request/trace IDs, bound scope identifiers, view/action,
redacted target, timestamps, queue/render/ACK latency, result code, retry count
and registry version. Never record secret values or complete sensitive UI snapshots.

Track ACK timeout, not-found, stale-state, permission-rejection, duplicate-drop
and user-interruption rates by product/view. Browser telemetry is evidence of
rendering, not evidence a human read the content.

Tool guidance should say: discover unsupported/unknown actions first; inspect
when state matters; act once; wait for receipt; report the actual result.
Prefer a small highlight or focused panel over unnecessary full-view changes.
After changing data with a backend tool, request refresh and verify its receipt.

## Implementation phases

1. **Contract and baseline audit:** map all 22 views to real handlers; choose
   canonical manifest; add generated schemas/types and registration completeness
   checks; document supported/unsupported targets.
2. **Shared lifecycle:** scoped browser binding, action IDs, capability/state
   tools, ACK endpoint, timeout/idempotency/cancellation, safe UI state and
   compatibility wrappers. Open/refresh coverage for every current view.
3. **First deep adapters:** Plan, Report, Notify and Files. Deliver selection,
   expansion, semantic focus/highlight and reliable loading/error receipts.
4. **Remaining workflow adapters:** logs, schedules, costs, database, evaluation,
   Pulse and knowledge; then setup/access/backup/publish views. Permission parity
   is required before each adapter is enabled.
5. **Product adapters:** Video Studio, Dominion, Activity and report-widget bridge.
   Reuse product presentation bindings; no product-specific protocol forks.
6. **Rollout/hardening:** feature flag per product/view, trace monitoring, browser
   compatibility, accessibility and multi-tab/multi-user race testing. Preserve
   old tools until callers migrate; rollback disables new actions safely.

## Acceptance criteria and proof

1. Registry coverage test accounts for every current view ID; adding a view
   without an adapter or explicit unsupported declaration fails CI.
2. Generated Go/TS/tool contracts match. Unknown actions/targets fail with codes,
   not silent no-ops. Version mismatch is recoverable and visible.
3. Real browser integration tests call backend tools, receive stream events,
   render/focus actual views, ACK, and verify the returned receipt and UI state.
   Unit tests that only assert emitted JSON are insufficient.
4. Plan step selection, Report tab/section focus, Notify instruction expansion,
   and file line focus work both with an already-open and initially-hidden pane.
5. Refresh waits for refreshed content or reports its load error; target deletion
   between inspection and action yields not_found/stale_state.
6. Two users, two products, two workflows, multiple tabs and multiple browsers
   cannot cross-control or leak UI state. Inactive/background behavior is tested.
7. Duplicate SSE events/reconnects apply an action once; historical hydration
   never reopens a view; stale ACKs cannot complete a different request.
8. Slow mount, virtualized targets, iframe not ready, offline browser, failed
   fetch, autoplay blocked and user interruption all return truthful receipts.
9. Read-only/creator/admin permission matrix passes; no control action sends a
   notification, runs a workflow, reveals a secret or mutates data.
10. Selecting an existing workflow connector/view does not trigger broad MCP
    discovery. UI inspection and registry listing cause zero external sends.
11. Media tests verify playback state without paid generation. Notification UI
    tests send no Slack/email; seeded fixtures cover sender/recipient focus.
12. Audit logs contain no secrets or raw sensitive content. State size, target
    enumeration and receipt retention are bounded; no unbounded polling.
13. The existing toolbar and old open/refresh callers remain functional, with
    accurate receipt semantics documented for old and new clients.

## Open design decisions

- Binding policy when several user browsers are open: explicit per-chat UI
  attachment is recommended; never choose by unauthenticated “last active.”
- Default ACK timeout and retention: propose 10 seconds for ordinary navigation,
  up to 30 seconds for explicit refresh, with pending/status lookup; measure before
  committing these values as SLA.
- Which report documents support semantic target instrumentation? Nonparticipating
  documents retain preview-only support.
- Whether later phases allow unsaved form drafts: if added, drafts must be
  explicitly labeled and never auto-submit or override user edits.

## Implementation record — 2026-09-05

Local baseline added after pulling main through `f56feeda9`:

- Builder-only registration: all six UI tools (including legacy open/refresh)
  are excluded at definition construction for cron/manual schedules, persisted
  scheduled origins, typed/Pulse children, bots, product profiles and automatic
  notification turns. Read-only human Builders retain presentation access.
  Explicit interactive promotion is distinguished from merely observing a run
  or retaining its native CLI. Role changes invalidate existing UI leases.
  The synthetic request now preserves the origin fields needed for this decision;
  shared workflow skills and execution tools are unchanged.

- Canonical embedded `ui_control_contract.json` drives Go capability/tool IDs and
  generated `frontend/src/platform/ui-control/contract.generated.ts`. A test
  checks all 22 entries against the actual toolbar registry. Generate with
  `node frontend/scripts/generate-ui-control.mjs`; `--check` verifies freshness.
- Four tools: `list_ui_capabilities`, `get_ui_state`, `perform_ui_action`,
  `get_ui_action_result`. State is bounded to view, visibility, revision and
  observation time; no DOM/content/secrets or connection discovery.
- Authenticated `/sessions/{session_id}/ui-control` checks live-session ownership
  and current workflow access. Opaque per-mount leases remain memory-only;
  mismatched scopes/tokens, ownerless chats and ambiguous browsers fail closed.
- SSE is a wake-up only. The broker atomically claims commands for a single
  binding, waits up to 10 seconds, expires disconnected leases, deduplicates
  request keys, bounds storage and retains receipts for ten minutes. Scope
  changes/unmount cancel pending work; an uncertain outcome is never replayed.
- Shared frontend host acknowledges actual shell mounting and visibility.
  **Opening does not certify all data loads.** Notify `expand` targets
  `run_summary` / `pulse_review` additionally verify the real instructions
  disclosure is open and visible. No sends or settings changes occur.
- Legacy open/refresh tools remain operational; their results and activity
  labels now explicitly describe unverified requests, not completed rendering.
- Logs record request/view/action/status/reason only, not targets, tokens or
  sensitive snapshots. Guidance explains capability discovery and receipts.

### Verification and remaining work

Focused Go tests cover registry/mutation rejection, disconnected and ambiguous
browsers, duplicate keys, scope and receipt isolation, stale revisions, expiry,
unbinding, redacted state and HTTP rejection of foreign/ownerless sessions.
Frontend contract/disclosure tests and TypeScript compile pass. A Chromium smoke
test (`node scripts/test-ui-control-browser.mjs` from frontend, with Playwright
available) exercises the actual Notify component plus semantic adapter: hidden
pane + lazy mount, stale revision, abort, expired request, missing target and
user interruption. This is **not** the complete application/backend integration
test required by acceptance criterion 3.

Still open: runtime adapter intersection beyond this fixed AgentWorks host;
verified refresh/data completion; deep Plan/Report/Files and remaining view
adapters; full product/iframe integration; legacy wrappers on the new engine;
permission-matrix, end-to-end transport, reconnect/multi-user and rollout tests.
Unsupported deep/refresh actions are rejected, never advertised as no-ops.
Baseline deployed to RTS on 2026-09-05 as release
`e2e851bd9-20260905133957` (main plus these local changes). All three services
were active, `/api/health` reported healthy/idle, and the public site returned
HTTP 200. This records service health, not full UI acceptance coverage.
No external sends or ticket-completion claim.

### Follow-up: live Plan-opening failure (2026-09-05, local changes)

Live browser reproduction: legacy open switched Costs to Plan, but the console
reported no node for `livekit-quality`. The node was present as
`message_sequence`; the old focus helper searched only `step` and intentionally
did not open details. New acknowledged tools separately returned
`browser_disconnected` despite a visible workspace. The exact live registration
failure is still unconfirmed; do not mark that issue resolved.

Local follow-up routes `open_workspace_view` through the acknowledged broker,
adds Plan step targets, selects across node types, waits for node mount and the
matching details panel, and requires that selected target in the browser ACK.
Legacy refresh remains explicitly unverified. Registration failures now have
safe frontend/server diagnostics rather than a silent catch. No permissions
were weakened and no deployment was performed for this follow-up.

Verification: HTTP handler bind/sync/ACK round-trip, matching-target rejection,
legacy-wrapper wait-for-receipt test, focused Go race tests, TypeScript checks,
and 10 Chromium adapter-fixture cases. These are not the deployed-app acceptance
test. Deployment and a live retry are required to capture/fix the remaining
registration failure and validate the complete user interaction.

Live diagnostic deployment identified the registration cause: the API wrapper
posted to `/sessions/{id}/ui-control`, missing `/api`. On RTS that path returns
the SPA HTML with HTTP 200, never reaching the UI handler. The corrected wrapper
uses `/api/sessions/{id}/ui-control`, consistent with the other session APIs.
A regression test checks the actual wrapper's route, rather than a mocked URL.
Diagnostics also validate binding-response shape and print safe codes as text.
The corrected frontend was deployed as RTS release
`f28500831-20260905144739` (main plus local follow-up changes). Live acceptance
on the authenticated in-app browser passed:

- Costs → legacy-named `open_workspace_view` → LiveKit details: applied,
  visible=true, target=livekit-quality, matching screenshot and DOM.
- Collapsed workspace → `perform_ui_action` → LiveKit: applied.
- Repeated open with a fresh request: applied.
- Nonexistent target: failed/target_not_found (twice, because the testing
  agent repeated the failed call to obtain the full error); no false success.
- Restore LiveKit after failure: applied.
- Browser reload → Costs → message_sequence node
  `step-ingest-notion-feedback`: applied, matching selected-target state and
  visible details panel. No workflow steps executed or notifications sent.

The original connection and Plan-selection failures are resolved in these live
tests. This does not close the wider PLAT-292 scope (remaining view/product
adapters, legacy refresh, full permissions matrix, etc.). The generic activity
card still uses the misleading "Production update" fallback for UI-action
wake-up events; the actual tool receipt and selected state are correct.

## 2026-09-22 SSE-first sync

`useWorkspaceUIControl` no longer polls every 3s. SSE presentation events,
local view-state changes, and adapter swaps are the primary wake-up paths;
overlapping syncs coalesce into one trailing run. The 10s interval
(`UI_CONTROL_BACKUP_POLL_MS`) only renews the 15s lease and recovers lost
events during stream reconnects. Behavior is unchanged; sync traffic drops
with idle views. Call sites must pass a memoized adapter or the adapter
effect re-fires every render.

## Register notes

[PLAT-292](plat-292.md) proposes a scoped, acknowledged UI-control
framework building on PLAT-278: capability discovery, UI-state inspection,
semantic actions, browser receipts and generated contracts. Covers all 22
current workspace views plus AgentWorks, Video Studio, Dominion, Activity,
file/media previews and report widgets. Includes permissions, privacy,
multi-tab/reconnect handling, phased rollout and acceptance tests.
**Baseline deployed to RTS; partially implemented.** The acknowledged AgentWorks baseline and
Notify instruction expansion are implemented; deeper adapters, product coverage
and full end-to-end rollout proof remain open. No change to live
issue-resolution counts. See PLAT-292's implementation record. The 2026-09-22
update makes SSE, view-state changes, and adapter swaps the primary sync
wake-ups with coalesced trailing syncs; the 10s poll only renews the 15s
lease and recovers lost events.
