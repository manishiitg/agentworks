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

This original decision was narrowed by [PLAT-441](plat-441.md) and enforced at
runtime by [PLAT-447](plat-447.md): Relays also have no platform DB, KB or
learnings, so migrations for those stores are inapplicable. There is still one
workflow migration ladder and contract version namespace. New migrations apply
to Relays by default unless explicitly excluded for an absent feature.

## Done

- `goalsOnlyWorkflowUpgrades` tags 16 goal-driven migrations. A Relay's plan
  (`workflowVersionUpgradePlan`) keeps every other one: step types, scripts,
  code layout and nested artifacts. PLAT-441 subsequently excluded KB and managed
  DB changes; a Relay can therefore finish its applicable upgrades at an older
  known marker rather than stamping work it never performed.
- `manifestContractIsExecutionCompatible`: a Goals workflow must be on the current
  version; a Relay must be on a known version with no shared migration pending.
  Used by the webhook/Relay preflight, the Builder run guard, the manifest
  banner and the upgrade status.
- The Relay Builder gets the tools the shared migrations call
  (`get_contract_upgrades`, `set_workflow_contract_version`,
  `set_code_layout_version`, the `migrate_*` tools). The DB tools initially added
  here were removed in PLAT-441. A test fails if an applicable migration names a
  tool the Relay Builder lacks.
- Fixed `TestRetiredMarkersStillRequireCurrentNestedArtifactMigration`, missed in
  PLAT-428.

## Left

- A Relay on 1.0.44 is now compatible, provided `code_layout_version` is 1.
  Older releases still owing an applicable shared migration must be migrated in
  the draft and republished; published versions remain immutable.
- No live Relay migration has been run end to end.
