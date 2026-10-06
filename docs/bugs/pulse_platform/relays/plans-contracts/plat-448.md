[← relays / plans-contracts](index.md)

# PLAT-448 — Relay migration history claims inapplicable upgrades were applied

| Field | Value |
|---|---|
| State | deployed |
| Priority | - |
| Product | relays |
| Area | plans-contracts |
| Summary | fixed on `main`, not deployed: applied history now uses the same product-filtered shared ladder as pending upgrades; audit checks preserve all applicable Relay migrations and gates. |

| Coordination | Value |
|---|---|
| State | fixed on `main`; not deployed |
| Date | 2026-10-04 |
| Owner | plans-contracts |
| Related | PLAT-431, PLAT-441, PLAT-447 |

## Found during the owner-requested migration review

Relays use one shared workflow contract ladder, not a separate migration engine
or version sequence. `workflowVersionUpgradePlan` excludes 16 Goals feature
migrations and two platform-store migrations. All new ladder entries are shared
by default. The six currently applicable entries cover message-sequence code,
step type/execution-mode conversion and nested artifacts/code layout.

The run guards and next-stamp lookup respect this filtering. However,
`workflowContractUpgradeLists` built its applied history from the Goals ladder.
Advancing a Relay's marker therefore made skipped Pulse/report/store migrations
appear applied. PLAT-431's old documentation also still required a managed-DB
migration, contrary to the later approved no-stores policy.

## Done

- History uses the same product-filtered ladder as pending upgrades. No separate
  migration implementation or new version namespace.
- Regression checks retain every applicable migration, in order, at ladder
  boundaries; skipped upgrades never appear in Relay pending/applied lists.
- Workspace-backed checks exercise the Builder run gate and next stamp alongside
  API preflight: shared artifact migration blocks; DB-only migration does not;
  missing code layout and unknown versions remain blocked. Goals still owe DB.
- Existing tool-contract check verifies Relay Builder has the tools required by
  applicable migration instructions.
- Corrected PLAT-431 and its historical decisions entry for the superseding
  PLAT-441 policy.

## Verification

Focused server migration/status/run-guard tests and the required commit checks
run in owned worktrees. No running backend or production Relay was changed.

## Left

- Deployment and a live Builder-led migration remain unverified. This review
  exercises eligibility, status and gates with safe fixtures, not actual model
  edits of an old Relay. The live verification remains recorded in PLAT-431.

## Register notes

[PLAT-448](plat-448.md), fixed on `main`, not
deployed: applied history now uses the same product-filtered shared ladder as
pending upgrades; audit checks preserve all applicable Relay migrations and gates.
