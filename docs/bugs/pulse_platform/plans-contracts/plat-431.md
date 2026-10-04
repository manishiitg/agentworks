[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-431 — Relays reuse the workflow migrations, minus the goal-driven ones

| Coordination | Value |
|---|---|
| State | fixed on `main`; not deployed |
| Date | 2026-10-04 |
| Owner | plans-contracts |
| Related | PLAT-428 (contract 1.0.45), PLAT-423 (Relay Python tools) |

## Problem

A Relay is a workflow folder with `kind: "relay"` and the same contract
`version`. Every Relay run path (API run, draft test, Relay schedules) goes
through `directWebhookPreflight`, which demanded exactly the current Goals
contract. So the PLAT-428 bump to 1.0.45 blocked every existing Relay, and the
Relay Builder had none of the migration tools, so it could not be unblocked.
Most of the ladder (schedules, Pulse, reports, notifications) means nothing for
a Relay.

## Decision (owner)

Relays reuse the workflow runtime and its migrations as much as possible; they
skip only what is goal-driven.

## Done

- `goalsOnlyWorkflowUpgrades` tags 16 goal-driven migrations. A Relay's plan
  (`workflowVersionUpgradePlan`) keeps every other one: step types, scripts,
  code layout, nested artifacts, managed DB scripts. It still ends at the current
  contract.
- `manifestContractIsExecutionCompatible`: a Goals workflow must be on the current
  version; a Relay must be on a known version with no shared migration pending.
  Used by the webhook/Relay preflight, the Builder run guard, the manifest
  banner and the upgrade status.
- The Relay Builder gets the tools the shared migrations call
  (`get_contract_upgrades`, `set_workflow_contract_version`,
  `set_code_layout_version`, the `migrate_*` tools, `scan_workflow_script_db_usage`,
  `apply_workflow_db_migration`, `query_workflow_db`, `mutate_workflow_db`). A test
  fails if a shared migration names a tool the Relay Builder lacks.
- Fixed `TestRetiredMarkersStillRequireCurrentNestedArtifactMigration`, missed in
  PLAT-428.

## Left

- A Relay on 1.0.44 still owes the managed-DB-scripts migration before it runs
  (shared, by design). Published Relay releases keep their frozen version; a
  release made before 1.0.45 must be republished after migrating.
- No live Relay migration has been run end to end.
