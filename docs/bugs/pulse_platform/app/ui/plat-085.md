[← platform / frontend-chat](index.md)

# PLAT-085 — direct HTML report rollout hid primary dashboards that lived outside `db/reports/`

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | app |
| Area | ui |
| Summary | P1 — workflows retain their durable report data and HTML, but |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented` — contract upgrade and broken-DOM validation added; affected RTS report repaired |
| Last synchronized | `2026-08-12` |

- **Priority:** P1 — workflows retain their durable report data and HTML, but
  the Report UI can omit the primary user-facing dashboard after the platform
  changes its discovery contract.
- **Observed on:** Instagram. Its primary live dashboard remains at
  `db/report.html` and is the first enabled document in
  `reports/report_plan.json`. The new viewer discovers only immediate
  `db/reports/*.html`, so it shows seven secondary host/setup pages and omits
  the real Instagram workflow snapshot.

## Root cause

The frontend moved from the legacy `reports/report_plan.json` layout registry
to direct standalone `db/reports/*.html` pages. The rollout changed the shared
viewer and authoring guidance, but no workflow contract version performed the
corresponding per-workflow content migration. This cannot be solved safely by
blindly copying one conventional filename: the old plan contains the ordered,
enabled list, titles, and source paths, and existing workflows use different
locations and report sets.

## Fix

Workflow contract `1.0.23` adds one bounded, agent-run migration:

1. Read the complete old report plan and every enabled HTML source.
2. Preserve immediate `db/reports/*.html` pages that were not in the plan.
3. Compose one complete `db/reports/index.html` without replacing the old
   queries, scripts, styles, media references, or visible content.
4. Let that workflow-owned HTML choose its own tabs, sidebar, anchored sections,
   or scrolling layout; the platform does not manufacture navigation from files.
5. Validate the consolidated document and prove the primary dashboard and all
   intended sections remain reachable before removing the retired plan and
   superseded HTML files.

The migration explicitly rejects wrappers, iframes, generic placeholders, and
empty navigation shells. A missing or ambiguous source blocks the version
stamp rather than silently losing the report.

### Follow-up found on RTS Latency (2026-08-12)

The RTS migration preserved all three dashboards and prefixed their element IDs
(`lat-`, `sec-`, and `cost-`) to avoid collisions, but it left the copied render
scripts targeting the old unprefixed IDs. The document loaded, then threw four
`Cannot set properties of null` errors and left the latency dashboard on
`Loading live data…`.

The report now resolves each render target through its section prefix. The
shared `validate_report_html` tool also rejects immediate
`document.getElementById("...").innerHTML/textContent/...` writes when the
literal target ID does not exist. This catches the concrete consolidation
failure before a workflow version can be stamped instead of accepting a file
merely because it has an HTML root, body, and title.

## Verification boundary

Source-level regression tests cover the `1.0.22 -> 1.0.23` route, the prompt's
preservation requirements, and missing literal DOM write targets. The focused
report-validation tests pass. RTS Latency was also verified in the live Report
tab: its latency data, KPIs, recommendations, environment details, language
table, Sentry proof section, and baselines render rather than remaining on the
loading state.

## Acceptance

- Every enabled HTML document from the old plan remains represented and
  reachable from the single workflow-owned report.
- Primary dashboards outside `db/reports/` are migrated, not omitted.
- The workflow HTML—not the platform—chooses its reporting navigation.
- The workflow version is not stamped if any source is missing, ambiguous, or
  fails HTML validation.
