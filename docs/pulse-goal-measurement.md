# Outcome goals and measurable progress

Pulse starts with primary and optional secondary outcome goals, then one or more
primary metrics per goal and linked supporting measurements. Goal priority and
metric role are independent.
Acceptance conditions and explicit user boundaries remain available as expandable
sections. The current soul document stays canonical for intent; SQLite owns metric
definitions and observations. There is no extra reviewer-authored report.

## Setup

- Pulse can be enabled directly without configured metrics. Missing measurement
  setup remains visible in Goal Progress; its "Set up goals & metrics" action
  opens the shared setup conversation independently of the review toggle.
- Existing workflow: `/setup-goals`, its `/define-success` alias, or the Pulse
  "Set up goals & metrics" button runs the same flow. Existing runs are not stopped.
- Setup is idempotent. Existing targets/constraints are preserved unless the user
  agrees to change them. Historical goal prose is not automatically rewritten.

## Data contract

`configure_goal_metrics` saves the complete active list to
`workflow_goal_metrics` in the workflow database. At least one metric is primary. Primary metrics may be grouped with `goal_id`/`goal_name`; supporting measurements link to primary IDs through `supports` and `support_kind`. Multiple primaries require explicit supporting links. See [PLAT-311](bugs/pulse_platform/goals/pulse/general/plat-311.md) for the shipped workflow-boundary contract.
Definitions include stable ID, criterion, name, unit, direction, calculation,
source, window, route/environment, collection cadence, freshness tolerance, and
optional target/date. Omitting a metric retires its definition without deleting
its observations. Reusing an ID after changing its calculation, source, unit,
window, scope, or direction is rejected; use a new ID for the new series.

`get_goal_metrics` returns definitions, recent history and deterministic progress
snapshots suitable for review and notifications. Active streams retain up to 120
recent observations each in the response, independently of unrelated Pulse rows;
the underlying database retains the full history.

`record_goal_observations` is available to producing runs and collectors. It uses
`pulse_goal_observations` and requires configured IDs, matching scope/unit, source
run identity, observation time and evidence. Missing values are represented by a
status, never zero. Repeated identical submissions are idempotent; conflicting
values for the same run/metric/scope are rejected. Use the actual collection run
ID for delayed refreshes, retaining source/post identity in evidence.

The builder wires collection using managed workflow tools and verifies a value
against its source. A definition is not an executable query or proof of a working
collector. Deterministic collection should be scripted; delayed outcomes need a
suitable later collection run. Merely saving definitions never launches external
requests or changes existing schedules. Backfill uses original observation times
and only evidence that matches the new definition; incompatible historical rows
are retained but excluded from the displayed series.

## Presentation

The primary card shows current value, window, configured target, change since the
previous measurement and a dated trend. Supporting cards show the same facts at
smaller scale. Expand details for definition, source, cadence and exact history.
States distinguish missing setup, unavailable collection, stale data, initial
baseline collection, tracking and a currently observed target met. Missing latest
collection never falls back silently to an earlier successful value. There is no
invented universal progress percentage. Target met is a metric state, not a claim
that every goal and boundary is satisfied.

Run/Pulse summaries receive platform-computed goal progress grouped by primary metric with linked supporting measurements
sections ahead of review details, including when agents provide rich email HTML.
The provider reads the trusted notification workspace; recipients and routing are
unchanged. If readings cannot be loaded, the summary says progress is unavailable.
Reviewers interpret the same definitions/history; they do not collect measurements
or maintain parallel Markdown/chart reports.

## Rollout

No live workflow is bulk-migrated. Begin with `/setup-goals` in social-media to map
existing follower history, then LinkedIn/Substack. Target choices and semantic
changes remain user decisions. Workflows with missing outcome instrumentation
can keep running while setup identifies and connects the required collector.

## Using measurements in workflow reports

The report host exposes the same scoped metric history and progress calculations
used by Pulse. Add an optional prebuilt section without designing a chart:

```html
<section id="goal-progress"></section>
<script>
window.report.ready(async () => {
  await window.report.renderGoalProgress('#goal-progress');
});
</script>
```

For a custom visualization, use `await window.report.getGoalMetrics()` inside
`ready`. The result is `{ metrics, observations, progress }`; progress entries
include the metric definition, current value, delta, state, freshness, target
status, and recent comparable history. Up to 120 observations per active metric
are returned. The existing read-only `window.report.query` can query the full
`workflow_goal_metrics` / `pulse_goal_observations` history when needed.

The widget groups primary metrics by goal and shows their linked supporting measurements, trends, targets,
last observation time, and expandable definitions and evidence. Styling is bundled
and isolated from the report's CSS. It works in both the app and `preview_report`,
refreshes through the standard `ready` lifecycle, and never replaces missing or
failed measurements with invented values. Legacy workflows show a setup message.
Report field edits cannot modify these managed measurement tables.

## Multiple outcomes and workflow boundaries

A workflow groups work planned, executed and evaluated together. Keep shared
investigations/actions and routine tradeoffs together; suggest splitting independent
work. Neither goal/metric count nor schedule differences force a split.

`configure_goal_metrics` accepts one or more primaries (up to 30 total measurements).
Primary metrics may share `goal_id`/`goal_name`; `criterion_id` remains the immutable
observation contract. Supporting metrics use `supports: [primary_id, ...]` and optional
`support_kind: breakdown | diagnostic | guardrail`. Goal names must agree for one ID.
Breakdowns must match their parents' unit, direction and window. A shared supporting
measurement is collected once and shown under each related primary.

Optional `dimensions` identify one fixed slice per metric ID. Collection still uses
that ID and its exact source query, route and environment. Different slices never
share an observation series; changing dimensions requires a new ID. No implicit
aggregation of slices, percentiles or independent outcomes is performed.

Legacy single-primary definitions acquire supporting links on read without rewriting
observations. Reconfiguration with multiple primaries requires explicit supporting
links. Role/group/link edits preserve history; measurement meaning changes do not.
Old workflows and collectors continue to work; no workflow is split automatically.
Existing RTS roles should be reviewed via setup, not automatically promoted or assigned
new targets by a database migration.

Reviews cover each primary with progress and evidence freshness, then prioritize
investigation. An intervention retains its lead `metric` and can add `effects` with
additional metric IDs and expected directions. Assess each using `assessment.metric`.
Legacy assessments without a metric refer to the lead effect. Additional effects are
retained on older-client updates and cannot be redefined in place. Adoption of an
improvement with multiple effects requires positive/unchanged assessments for all
of them; regression or missing evidence is not hidden by another metric's success.

## Outcome priorities

`/setup-goals` groups confirmed outcome bullets in the canonical `soul/soul.md`:

```markdown
## Objective
### Primary goals
- Make learner conversations feel immediate and natural.
### Secondary goals
- Make the learner experience ready to use quickly.
```

Primary goals express the main desired outcomes; secondary goals express additional
outcomes. Multiple primary goals are allowed when equally important to the user.
Secondary goals are optional. These are structured Markdown sections, not another
SQLite copy of the goal. Runtime agents and reviewers receive both groups through
the existing Objective reader. Pulse displays primary goals first and secondary
goals below, with acceptance conditions and constraints still available.

Goal priority is independent of the metric's primary/supporting role. For example,
conversation latency can be the primary metric while Hebrew and English latency
are supporting metrics measuring the same primary goal. Page load time might
measure a secondary goal. A supporting metric is not itself a secondary goal.

Existing ungrouped goals remain visible without an invented priority. Setup
proposes a grouping, preserves commitments and explicit priorities, and asks about
ambiguous tradeoffs. Reviewers must not sacrifice a primary goal or constraint to
improve a secondary goal without the user's agreement. No live workflow is rewritten
until its setup flow runs.

## Strategic reviews and measurement improvement

Both scheduled Strategic Review and `/strategy-auditor` read configured metrics
early and connect recommendations to the primary/secondary outcomes, current
evidence, expected metric movement and a later outcome check. They assess whether
the metrics answer the real goal, rather than optimizing activity or a misleading
proxy. Unknown and stale measurements remain explicit; reviews can still explore
promising strategies while evidence is incomplete.

A material missing or inadequate measurement becomes a concrete proposal: what to
measure, its definition/source, collection needed, one verified observation and
the next useful evidence checkpoint. New or changed metric meaning goes through
the existing decision flow and `/setup-goals`; broken collection under an agreed
definition can go to the fixer. Legitimate outcome lag is an evidence wait.
The reviewer preserves definitions, targets and workflow implementation while
proposing changes. Existing findings and pending decisions are reused; there is
no new report, scorecard, database contract or recording turn to maintain.
