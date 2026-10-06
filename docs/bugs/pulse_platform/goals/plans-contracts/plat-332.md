[← goals / plans-contracts](index.md)

# PLAT-332 — manual workflow execution skipped contract migration preflight

| Field | Value |
|---|---|
| State | closed |
| Priority | - |
| Product | goals |
| Area | plans-contracts |
| Summary | closes the manual-execution migration gap: every interactive `run_full_workflow` and `execute_step` call rechecks `workflow.json` before starting. |

| Coordination | Value |
|---|---|
| State | Deployed; schedule-policy follow-up implemented on `main` |
| Date | 2026-09-18; updated 2026-09-22 |
| Owner | workflow execution / contract migrations |

## Problem

Contract migrations ran as schedule preflight turns. A workflow used only from
Run Workflow or Run Step could therefore remain on an old contract forever,
and manual execution could start without applying required platform repairs.
Webhook execution already failed closed on an old contract, but its error did
not give the owner an interactive migration path.

## Contract

Every manual `run_full_workflow` and `execute_step` call re-reads
`workflow.json` immediately before execution. When its contract differs from
the platform contract, the call starts no execution and tells the agent to ask
the owner whether to migrate. After approval, the agent uses Workshop
`get_contract_upgrades`, completes and verifies each migration in order, then
retries the original run.

The guard lives on the shared execution-tool registrar, so toolbar actions,
chat requests, restored sessions, and agent-profile callers receive the same
behavior. Direct webhooks retain their own fail-closed preflight.

As of the 2026-09-22 follow-up, cron/calendar schedules and `trigger_schedule`
runs no longer execute contract migrations and are not blocked by a pending
migration. They keep running the workflow's saved contract. Contract upgrades
are operator-started work in the interactive Builder chat, and scheduled
sessions cannot authorize or stamp one. The pending-upgrade banner and
**Setup → Identity → Upgrades** provide the manual entry points and upgrade
history. This avoids turning a platform-maintenance operation into an
unattended schedule turn while preserving the interactive execution guard.

Release `bb24ab5-20260918164118` (`bb24ab544`) was deployed on 2026-09-18.
Post-deployment verification confirmed the exact source revision, all four
services active, a healthy planner endpoint, and the migration guard present in
the production binary.

The schedule-policy follow-up is implemented on `main`; deployment verification
for that follow-up is recorded separately from the original 2026-09-18 release.

## Register notes

[PLAT-332](plat-332.md) closes the manual-execution migration gap: every interactive `run_full_workflow` and `execute_step` call rechecks `workflow.json` before starting. An old or unknown contract starts no execution and directs the owner to the manual Workshop upgrade path. The 2026-09-22 follow-up removes contract-upgrade turns from cron/calendar and `trigger_schedule` runs: schedules continue on their saved contract and cannot stamp migrations. Direct webhooks remain fail-closed. The original guard release `bb24ab5-20260918164118` is deployed and verified; the schedule-policy follow-up is implemented on `main` with deployment verification pending.
