[← goals / steps / database](index.md)

# PLAT-372 — Scripted steps could not write the workflow database after per-session bridge tokens

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | goals |
| Area | steps/database |
| Summary | **fixed** on `main` (`7ce796701`), RTS deploy pending: a scripted step's script writes as the group session, which never had the DB grant, so salesoutreach lead discovery saved nothing from 2026-09-27. |

| Coordination | Value |
|---|---|
| State | fixed on `main` (`7ce796701`, `b5840ec68`); local verified 2026-09-30; **RTS deploy pending** |
| Date | 2026-09-30 |
| Owner | step-execution |
| Related | H1 per-session bridge tokens (`f48d158a4`, `b1a08dbee`, `c53802f5a`) |

## Problem

salesoutreach `step-apollo-company-search` saved nothing from 2026-09-27 (last
`provider_credit_usage` row 2026-09-26 17:39 UTC). Every run failed after the
Apollo search, at the first `mutate_workflow_db`:

`workflow database mutation denied for session "session-group-<group>-<ns>": explicit db_access=read-write is required (effective value "")`

Reads through `query_workflow_db` worked. The step then produced no
`candidate_handoff.json`, its repair checks could not help, the run failed, and
each failure started a Pulse fix run that could not fix a platform grant.

## Cause

A scripted step runs its script as the group's MCP session (`MCP_SESSION_ID`),
not the step's exec session where `configureWorkflowDBSession` sets
`WORKFLOW_DB_ACCESS`. The group session copies only folder capabilities from its
parent (`common.CopySessionFolderGuard`), never the DB grant. Before H1 a shared
token let the script act as any session, which hid the gap.

## Fix

- `grantScriptBridgeSessionDB` (`controller_agent_factory.go`), called in
  `execScriptedScript` before the script runs: the script's session gets the
  step's DB access. Only the grant is set; raw `db.sqlite` stays blocked.
- A refused write now says it is a platform grant problem to report, not
  something the caller can fix (`errWorkflowDBWriteGrantMissing`).
- salesoutreach's script checks a write before any Apollo call (local
  workflow file, not in git).

## Done / left

- Done: unit tests (`script_bridge_db_grant_test.go`, `workflow_db_context_test.go`);
  local runs after the 2026-09-30 15:29 restart saved rows for the dubai groups.
- Left: see a usa or india group run save; deploy to RTS; check the other
  scripted steps that write through the bridge (listed in the 2026-09-30 scan).

## Register notes

[PLAT-372](plat-372.md), P1, **fixed** on `main`
(`7ce796701`), RTS deploy pending: a scripted step's script writes as the
group session, which never had the DB grant, so salesoutreach lead discovery
saved nothing from 2026-09-27.
