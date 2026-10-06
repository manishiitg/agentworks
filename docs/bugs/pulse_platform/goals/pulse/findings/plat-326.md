[← goals / pulse / findings](index.md)

# PLAT-326 — Collapse Pulse persistence to reviews, issues and human decisions

| Field | Value |
|---|---|
| State | deployed |
| Priority | P2 |
| Product | goals |
| Area | pulse/findings |
| Summary | completed its corrected schema-v2 rollout across the local workspace after the `Workflow/testing` canary. |

| Coordination | Value |
|---|---|
| Assigned agent | Unassigned |
| Ticket state | `schema-v2 + reviewer prompt/tool cutover complete locally; deployment rollout pending` |
| Last synchronized | `2026-09-18` |
| Priority | `P2 simplification / migration` |

## Problem

Pulse currently spreads one product lifecycle across roughly twenty-five
workflow-local SQLite tables: module state and audits, review notes and focus
history, recovery, finding details and events, fix attempts and verifications,
impact records, command state, telemetry and Activity projections. Much of this
is bookkeeping for intermediate states rather than user-facing product truth.
It increases tool count, prompt complexity, synchronization risk and the chance
that Pulse spends more effort maintaining its own records than improving the
workflow.

The intended product lifecycle is deliberately smaller:

```text
review concludes -> issue reported/open -> action taken -> issue closed
```

There is no separate verification lifecycle. If Pulse cannot complete the fix,
the issue remains open. A later review may add current evidence, but should not
create a second semantic issue.

## Recommended target model

### `pulse_reviews`

```text
id
pulse_run_id
reviewer_type
status
summary
evidence
created_at
```

One row records the user-readable result of Plan Drift, Technical,
Architecture or Strategic Review. Activity is derived directly from these rows;
Pulse must not maintain a second notification-authored copy of the result.

### `pulse_issues`

```text
id
review_id
description
evidence
status          -- open | closed
action_taken    -- empty while open; actual completed action when closed
created_at
updated_at
```

Do not add `recommended_action`, `outcome`, verification status, monitoring
status, fix attempts or a general issue-event ledger. When the same root cause
is encountered again, update the canonical issue's evidence and timestamp.

### `pulse_decisions`

```text
id
issue_id
question
options
status          -- pending | answered
answer
created_at
answered_at
```

Every Pulse decision must belong to an open issue. Answering a decision does
not itself claim that work was performed:

- approval leaves the issue open until the approved action is applied; then
  `action_taken` is recorded and the issue closes;
- rejection records the choice as `action_taken` and closes the issue;
- an unanswered decision remains pending and its issue remains open.

This replaces Pulse-specific use of the much broader
`report_human_inputs`/event/apply-contract lifecycle. General workflow
`human_input` steps remain outside this migration.

## Migration plan

Implement only in a dedicated migration change, not opportunistically during
other Pulse work.

1. Back up every workflow database and introduce a new workflow contract/schema
   version.
2. Create the three target tables alongside the existing tables.
3. Backfill reviews from `pulse_module_audit`:
   `module -> reviewer_type`, `result -> status`, `reason -> summary`, retained
   evidence -> `evidence`, and `recorded_at -> created_at`.
4. Backfill one canonical issue by merging `run_concerns`,
   `pulse_finding_details` and the latest useful lifecycle record. Collapse all
   unresolved intermediate states to `open`; collapse resolved, rejected and
   successfully changed states to `closed`. Copy the best truthful completed
   repair/resolution description to `action_taken`; discard verification-only
   history.
5. Backfill Pulse-owned pending and answered rows from `report_human_inputs`
   into `pulse_decisions`, preserving their link to the canonical issue.
6. Validate before cutover: every latest reviewer result is present, every
   active public `PUL-*` identity exists exactly once, every pending decision is
   visible, and every closed issue has a non-empty `action_taken`.
7. Switch `record_pulse_result`, finding writes, decision writes, Gate reads,
   Pulse UI and Activity to the new model. Prefer one terminal review write that
   atomically stores its review and issue changes.
8. Stop legacy writes, retain old tables read-only for one release/rollback
   window, compare old and new UI results, then remove legacy tables and tools.

## Tables expected to retire from Pulse ownership

The migration should explicitly assess and normally remove or move out of the
workflow Pulse model:

- `pulse_module_state`, `pulse_module_audit`, `pulse_review_notes`,
  `pulse_review_focus_state`, `pulse_review_focus_history`,
  `pulse_module_result_history`, `pulse_review_recovery`, `pulse_review_log`;
- `run_concerns`, `pulse_finding_details`, `pulse_finding_events`,
  `pulse_fix_attempts`, `pulse_fix_attempt_findings`,
  `pulse_fix_verifications`;
- Pulse-specific copies of human-decision event, claim and apply-contract state;
- Pulse command, context, shadow-signal and agent telemetry tables when their
  data belongs to the scheduler or platform telemetry instead;
- intervention/impact tables unless a separately approved product requirement
  demonstrates information that cannot be represented by the review, issue or
  action taken.

`org_dashboard_notifications` may remain for ordinary run notifications, but
Pulse Activity must read/project from `pulse_reviews` rather than storing a
second independently authored Pulse summary.

## Acceptance criteria

- A complete Pulse pass can be explained entirely from the three target tables.
- Reviewer results, open issues, closed actions and pending/answered decisions
  match the pre-migration UI on representative workflow databases.
- The runtime has no verification/monitoring/fix-attempt state machine for
  Pulse issues.
- Reporting an issue creates/updates one `open` row. Completing the fix writes
  `action_taken` and closes it. Failure to fix leaves it open.
- Activity displays the stored review summary without a publish/update tool or
  duplicate prose record.
- Migration is reversible during the rollback window and never silently drops
  an active issue or pending human decision.

## Implementation status — 2026-09-18

The reversible schema and data-migration phase is implemented locally:

- every server startup scans available workflow `db/db.sqlite` files before
  schedules start, while each Pulse database open also performs the same
  idempotent version check for laptops or deployments that were offline;
- schema version 1 creates `pulse_reviews`, `pulse_issues` and
  `pulse_decisions`, backfills their product truth, validates invariants and
  records the version only after the transaction commits;
- databases with legacy Pulse data receive a consistent, integrity-checked,
  read-only backup under `db/migrations/.backups/` before mutation;
- partial/old schemas do not abort the fleet-wide scan, and a failure is
  isolated to that database and retried on its next open;
- temporary compatibility projections keep the compact records current while
  legacy tables remain available for one rollback window.

This is phase 1, not permission to drop legacy data. Gate/UI/Activity cutover,
legacy tool removal, and final table deletion remain the next rollout phase
after representative databases have been compared successfully.

## Canary and broader local rollout

The first single-workflow canary completed on 2026-09-18. No other workflow
database was migrated.

- schema version 1 is recorded and a second targeted run is a no-op;
- both the current database and its read-only pre-migration backup pass
  `PRAGMA integrity_check`;
- the schema-v1 canary exposed a filtering defect: eight retired
  health/reviewer module identities were initially copied into
  `pulse_reviews` alongside `technical_review` and `strategic_review`;
- schema v2 now admits only the four current review identities and maps the
  supported Technical and Strategic legacy aliases to their current names;
- the corrected testing canary contains only `technical_review` and
  `strategic_review`; its currently available legacy data also produced one
  canonical issue and one linked decision.

The corrected migration was then applied to the broader local workspace:

- 14 real database paths were scanned;
- 11 additional workflow databases migrated, `Workflow/testing` was already
  current, and two unrelated databases were skipped;
- all 12 migrated workflow databases are at schema version 2, pass
  `PRAGMA integrity_check`, contain zero retired reviewer identities, and pass
  a second idempotency run with no additional migrations;
- 29 temporary test databases under `workspace-docs/var` and three duplicated
  absolute-path artifacts under `workspace-docs/Users` are now excluded from
  deployment scans.

This completes the local data rollout. RTS, other laptops and deployments such
as Confida will migrate through the same startup and lazy-open guards only
after this code is deployed there; they have not been changed by the local run.

## Reviewer skill and tool cutover

The reviewer-facing contract is also updated locally:

- Plan Drift, Technical, Architecture and Strategic skills now persist one
  concise terminal review result and canonical issues; they do not ask for
  separate focus, recommendation, verification, impact, assessment or
  publication records;
- Strategic Review keeps optional strategic focus areas as reasoning lenses,
  not a focus-history ledger. Technical and Architecture select scope from the
  evidence in front of them;
- Gate writes only its small platform scheduling receipt. Workflow/evaluation
  steps and collectors own metric observations;
- `record_pulse_result` is the user-visible Activity summary, removing the need
  for a second publish/update action;
- `record_pulse_impact` is no longer registered in the reviewer or Workshop
  tool surface. Its implementation remains temporarily as rollback-window code
  while legacy tables still exist;
- the old `focuses` result field and `focus_agenda` read remain accepted only as
  compatibility data for this rollback window and are explicitly excluded from
  new reviewer instructions.

The repair executor still translates changed files, immediate checks and the
canonical issue outcome through legacy lifecycle fields behind the compact
contract. Removing that compatibility translation, switching every UI/read
path to the three compact tables, and dropping the retired tables remain the
post-deployment cutover.

## Expected product effect

The compact schema is an enabler, not the complete reviewer improvement. It
reduces bookkeeping only after Gate, reviewer prompts, tools, Activity and the
Pulse UI stop asking reviewers to maintain the legacy focus, disposition,
verification, impact and publication records.

After that cutover, a reviewer should have only three persistence actions:

1. record one concise review result;
2. open or update a canonical issue when action is needed;
3. record a human decision only when work genuinely requires one.

This should leave more model context and tool turns for inspecting run evidence,
reasoning about goals and making or validating changes. Strategic Review should
benefit most, because it can spend its run comparing outcomes with goals rather
than maintaining lifecycle receipts. The skill/tool cutover should reduce that
bookkeeping immediately, but its effect on review quality must still be measured
after deployment. Compatibility writes remain behind the compact contract until
the rollback window closes.

## Register notes

[PLAT-326](plat-326.md) completed its corrected
schema-v2 rollout across the local workspace after the `Workflow/testing`
canary. Twelve workflow databases are current, integrity-clean and contain only
Plan Drift, Technical, Architecture and Strategic review identities; a second
pass was a no-op. RTS, other laptops and deployments such as Confida remain
pending until this code is deployed. Reviewer skills now use one result,
canonical issues and genuine human decisions; Gate owns only scheduling,
Activity reuses the result, and the separate impact tool is no longer exposed.
UI/read cutover and removal of rollback-window compatibility tables remain.
