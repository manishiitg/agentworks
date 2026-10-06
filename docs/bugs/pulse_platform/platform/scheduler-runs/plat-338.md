[← platform / scheduler-runs](index.md)

# PLAT-338 — Scheduled scripted steps could not call owner-scoped tools

| Field | Value |
|---|---|
| State | open |
| Priority | P0 |
| Product | platform |
| Area | scheduler-runs |
| Summary | records why a scheduled Twitter workflow's direct `agent_browser` probe succeeded while its scripted CDP preflight repeatedly received `caller does not own this tool session`. |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented locally; focused tests green; live schedule acceptance pending` |
| Last synchronized | `2026-09-21` |
| Priority | `P0 execution` |

## Symptom

After PLAT-337 restored the workflow owner's identity, direct tools in a
scheduled `twitter-automation` chat worked again. The workflow's scripted CDP
preflight still failed, however, even while an immediate conversational
`agent_browser status` call reported CDP port 9222 as reachable.

The step retried six times and then reported “Authenticated CDP browser mode is
not available.” The underlying response on every attempt was actually:

```text
agent_browser caller does not own this tool session
```

## Cause

Commit `74c66ab301` correctly bound every action tool to the authenticated
query session, but treated every different caller session as hostile. Workflow
scripts use a registered `session-group-*` child for their authenticated HTTP
bridge. The direct chat call carried the parent schedule session and passed;
the scripted bridge call carried the legitimate child session and was rejected.

Restarting the server could not change this deterministic authorization check.
The same defect applied to cron, manual schedule, API/internal trigger, and
interactive workflow executions whenever a scripted child called a protected
tool through `$MCP_CUSTOM`.

## Fix

`bindToolExecutionContext` now accepts either the exact authenticated parent
session or a live child whose parent relationship was explicitly registered by
`RegisterHTTPSession`. It does not trust names or prefixes. Unregistered,
stopped, ambiguous, and lookalike child IDs remain rejected. The tool executes
with the parent run's bound owner identity, so this restores delegation without
widening workflow access.

## Regression coverage

`TestToolExecutionContextAdmitsRegisteredWorkflowChildSession` proves both
sides of the boundary:

- a registered scheduled-run child can invoke `agent_browser` and receives the
  parent's owner identity;
- an unregistered lookalike child is rejected.

Focused tool-context, scheduler, and browser tests pass. The full
`cmd/server` suite reaches the unrelated in-progress internet-share surface
failure (`manage_internet_share` is implemented but not yet declared in
`product.yaml`). Live Twitter execution is intentionally left for an owner-run
acceptance check because it can perform external actions.

## Register notes

[PLAT-338](plat-338.md) records why a scheduled
Twitter workflow's direct `agent_browser` probe succeeded while its scripted
CDP preflight repeatedly received `caller does not own this tool session`.
The authenticated-tool boundary admitted only the parent schedule session and
mistook the workflow's registered `session-group-*` bridge child for an
unrelated caller. The boundary now accepts only live, registry-proven children
of that exact parent; forged and unrelated sessions remain blocked. Focused
tool-context, scheduler, and browser tests are green; live external-action
acceptance remains owner-triggered.
