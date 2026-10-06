[← platform / frontend-chat](index.md)

# PLAT-335 — Dashboard composition widgets (small files instead of one giant HTML)

| Field | Value |
|---|---|
| State | open |
| Priority | - |
| Product | app |
| Area | ui |
| Summary | proposes six optional `window.report` composition widgets (`renderTabs`, `renderKpis`, `renderTable`, `renderActivity`, `renderActions`, `renderCollapsible`, shared card CSS) so dashboards become ~100–200-line compositi… |

| Coordination | Value |
|---|---|
| Assigned agent | Unassigned (proposal) |
| Ticket state | `pilot implemented and browser-verified` |
| Last synchronized | `2026-09-20` |

### Agreed constraints (2026-09-20 review)

- **No nested iframes, ever.** One sandboxed iframe per dashboard (siblings
  only when several reports share a screen). Widgets are bridge methods on
  the existing `window.report` object, not documents — the proposal adds
  zero frames. Verified: current dashboards contain zero `<iframe`.
- **Widgets are optional, never mandated.** Full-custom dashboards remain
  valid; the validator and guidance must accept both styles. Widgets are a
  vocabulary expansion, not a template mandate.
- **Theming contract:** widgets inherit the dashboard theme via the host's
  CSS variables (`hsl(var(--…))` app tokens, `color-mix`/`currentColor`
  patterns per the existing goal/costs widgets) and impose no look of
  their own. No fixed palette, no imposed typeface.
- **Scope split:** pilot is `renderTable` + `renderActivity` only.
  `renderActions` is a separate follow-up (it encodes the PLAT-293
  save-then-send contract plus receipts/dedup — too much for the pilot).
  `renderCollapsible` is a CSS-only candidate (`<details>` + theme), not
  necessarily a JS API. `renderTabs`/`renderKpis` later.
- **Survey correction:** 12 local dashboards, 204–2201 lines
  (salesoutreach largest at 2201, not 1952). Action APIs ARE already used
  locally (`jobsearch`: `sendChatMessage`+`updateField`; `hetznerssh`,
  `websiteaeo`, `salesoutreach`: `updateField(s)`), which strengthens the
  `renderActions` follow-up case.

## 2026-09-20 — Proposal: grow the `render*` family so dashboards compose instead of hand-rolling

### Observation (surveyed 2026-09-20)

Every dashboard in the fleet is one large self-contained HTML file because the
`sandboxed srcdoc` runtime cannot load relative `<link>`/`<script src>`
(`report_html_tools.go` warns they are "ignored inside the sandboxed report —
drop the tag or inline the asset"). Surveyed:

- Local live dashboards: 8 files, 188–1952 lines
  (`workspace-docs/Workflow/*/db/reports/index.html`,
  `gstdatacollection/db/reports/gst_dashboard.html`, one Crew dashboard).
- Local baked static reports: ~40 × `Comprehensive_Income_Tax_Report.html`
  (400–641 lines each, zero `window.report` calls — per-client generated
  artifacts, a separate genre this ticket does not serve).
- Confida server (`confida@116.202.210.102`,
  `/srv/confida/data/docs/Workflow/confida-login/db/reports/index.html`):
  3827 lines, the largest dashboard in the fleet.

Every live dashboard hand-rolls the same five things with raw
`window.report.query` + custom JS/CSS:

| Pattern | Live dashboards using it |
|---|---|
| Tabs (`nav-tabs`, `role=tab`, `data-tab`) | 7/9 |
| KPI/stat tiles | 6/9 |
| Cards (4–32 per file) | 9/9 |
| Tables (1–15 per file, manual row-html loops) | 9/9 |
| Activity tab (`tab-activity`/`panel-activity`, required by policy) | 6/9 |

Across all local dashboards the only `window.report` calls are `query`,
`getText`, `fileUrl`, `renderMarkdown`, `ready`, `openFile`, `focus`.
Confida additionally shows the heaviest action-button usage in the fleet
(13 `sendChatMessage` calls, 1 `updateField`) plus bespoke collapsible
detail cards (`collapsible-card`, `smoke-detail-card`, `linear-detail-card`).
**Zero uses**
of `renderGoalProgress`, `renderCosts`, `getGoalMetrics`, or `getCosts` —
the two widgets the platform ships cover goal/costs only, while agents need
tables, KPIs, tabs, and activity. `renderEvaluations`/`getEvaluations` are
additionally gone from the runtime after the PLAT-333 eval-subsystem removal,
but `reporting-policy.md`, `report-plan.md`, `improve-report.md`, and
`design-reporting-ui.md` still tell agents to call them — a stale promise
that rejects as unavailable at runtime.

### Why this is a platform gap, not a workflow authoring choice

No workflow can author small dashboard files today: partials via
`getText`+`innerHTML` carry markup/CSS only (`<script>` injected that way
never executes), local JS/CSS files never load in the sandbox, and the only
shipped widgets cover two sections agents rarely need. This is the same shape
as PLAT-293/PLAT-256 — a missing capability tier on `window.report`, not a
per-workflow decision. A full React/TS migration was considered and rejected:
agent-authored TSX needs a build + sandbox anyway (the iframe stays), while
validation, preview, guidance, and every existing dashboard would need a
rewrite.

### Proposed fix (not implemented)

Add six optional composition widgets on `window.report`, following the
existing `renderGoalProgress`/`renderCosts` contract (empty container,
replace contents on refresh, reject on failure, identical behavior in the
app and `preview_report` via the shared host runtime):

- `renderTabs(target, [{id, title, content}])` — accessible tab shell honoring
  `report:focus`; reuse the existing `reportTabSelection.ts` persistence.
- `renderKpis(target, {query | items})` — `{label, value, tone}` tiles.
- `renderTable(target, {query, searchable, sortable})` — themed, responsive
  table with empty state; kills the most repeated code in the fleet.
- `renderActivity(target)` — the policy-required activity tab from
  `org_dashboard_notifications` (`notification_kind='run_summary'`),
  route-grouped via `route_summaries_json`, markdown-rendered. Zero config
  for the one section every dashboard must have.
- `renderActions(target, {actions})` — save-then-send buttons per the
  PLAT-293 contract (`updateField`/`updateFields` first, then
  `sendChatMessage` with stable `requestId`), with built-in
  queued/sending/failed receipts and double-click dedup. Motivated by
  Confida's 13 hand-wired action calls.
- `renderCollapsible(target, {sections})` — themed expandable detail
  sections with summary rows; replaces bespoke collapsible-card JS/CSS
  (Confida's `smoke-detail-card`/`linear-detail-card` pattern).
- Shared card stylesheet (CSS only — not every widget needs JS).

Target: new dashboards drop from ~500–3800 lines to ~100–200 lines of
composition. Adoption is opt-in; existing dashboards keep working untouched.
Suggested pilot: `renderActivity` + `renderTable` first (highest
repetition, clearest contract), with `renderActions` as fast-follow on the
Confida evidence.

### Implementation sketch

- New `frontend/src/components/workflow/reportWidgets/reportComposition.ts`
  (+ tests beside `reportOperationalMetrics.test.ts`).
- Wire into `window.report` in `reportHostRuntime.ts`: real API object,
  bootstrap stub queue list, and pending-call replay map.
- Add names to `reportKnownReportMethods` in
  `pkg/orchestrator/agents/workflow/step_based_workflow/report_html_tools.go`
  so `validate_report_html` accepts them; drop the stale
  `getEvaluations`/`renderEvaluations` entries so calls to the removed API
  are flagged instead of silently accepted.
- Guidance (one rewrite, subsumes the eval-cleanup): document the new
  helpers as optional in `reporting-policy.md` and `design-reporting-ui.md`,
  and remove the `renderEvaluations`/`getEvaluations` promises from
  `reporting-policy.md`, `report-plan.md`, `improve-report.md`, and
  `design-reporting-ui.md`. Note `docs/report-metric-widgets.md` documents
  the eval widget too and needs the same cleanup (outside `guidance/`).
- No `views.json`, multi-document, or `window.report` data-API changes.

### Verification (pilot, 2026-09-20)

- `frontend/src/components/workflow/reportWidgets/reportComposition.test.ts`:
  7 tests (table render/empty state, query validation, refresh race,
  search/sort, activity route-grouping, log fallback, limit validation,
  pre-injection replay through the real `installReportHost`). Green.
- Full `reportWidgets/` vitest suite: 61/61 green (no regressions).
- `tsc -b` clean; `eslint` on changed widget files clean.
- Go validator: new `TestValidateHTMLReportCompositionWidgets`
  (`renderTable`/`renderActivity` accepted, removed eval methods rejected);
  full `step_based_workflow` package green.
- Guidance-render suite (`agent_go/cmd/server/guidance`): green after the
  template edits.
- Browser `preview_report` render: PASS. `TestReportPreviewRealE2E` against
  a scratch composed dashboard (`renderTable` + `renderActivity` over a
  scratch sqlite db) rendered `ready` in headless Chromium with zero
  script/page errors; screenshot confirms the filterable/sortable table
  (3 rows, numeric right-aligned) and the route-grouped activity card with
  markdown summary. Requires the rebuilt `report-preview.js` bundle
  (`npm run build:report-preview`) and a native workspace server
  (`NATIVE_WORKSPACE=true`, else shell-outs get the Docker PATH and
  `agent-browser` is not found).

## Register notes

[PLAT-335](plat-335.md) proposes six optional `window.report` composition widgets (`renderTabs`, `renderKpis`, `renderTable`, `renderActivity`, `renderActions`, `renderCollapsible`, shared card CSS) so dashboards become ~100–200-line composition files instead of 500–3800-line hand-rolled single HTML. Grounded in a 9-dashboard survey (8 local + the 3827-line Confida server dashboard) showing zero usage of the shipped goal/cost widgets, universal hand-rolled tabs/KPIs/cards/tables/activity, and Confida's 13 hand-wired action buttons motivating `renderActions`. The guidance rewrite subsumes removing the stale `renderEvaluations`/`getEvaluations` promises left over from the PLAT-333 eval removal. Proposal only; not implemented.
