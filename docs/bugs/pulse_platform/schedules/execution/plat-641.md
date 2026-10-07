[← schedules / execution](index.md)

# PLAT-641: Scheduled runs use an active owner, not a former creator

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | schedules |
| Area | execution |
| Summary | Scheduled runs executed as the workflow's creator even after ownership moved to someone else, and would run as a disabled account |

## What happened

## Fix

## Left

## What happened

Dominion, 2026-10-07: the owner moved `tectonicusadaytrading` to a new account (`access.owners` = new) and disabled the old one. Scheduled and triggered runs take their identity from `workflowExecutionOwnerUserID`, which returned `created_by` (the old account) first, so the trading runs kept executing as a non-owner and, once disabled, as a disabled account. The old account was re-enabled until this ships.

## Fix

The creator runs it while still an owner with an active account; otherwise the first active owner; with no active owner, the first owner (fail closed). Test `TestScheduledRunsUseAnActiveOwner`. Not deployed. After it reaches Dominion, the old account can be disabled or deleted.
