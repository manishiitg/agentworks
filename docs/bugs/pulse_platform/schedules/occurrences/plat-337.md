[← schedules / occurrences](index.md)

# PLAT-337 — Legacy scheduled workflows lost their action-tool identity

| Field | Value |
|---|---|
| State | open |
| Priority | P0 |
| Product | schedules |
| Area | occurrences |
| Summary | records why every action tool in `twitter-automation` schedule chats returned `requires an authenticated session` while read-only bridge discovery still worked. |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented on main; local identity verified; real scheduled action pending` |
| Last synchronized | `2026-09-21` |
| Priority | `P0 execution` |

## Symptom

Every action-capable tool in fresh `twitter-automation` schedule chats failed
with `<tool> requires an authenticated session`. This included
`execute_shell_command`, `agent_browser`, and `diff_patch_workspace_file`.
Read-only bridge discovery such as `get_api_spec` and `read_skill` still worked,
which made the failure look like several unrelated tool or provider outages.
The same boundary failure was present in other ownerless legacy schedules,
including `salesoutreach` and `websiteaeo`; this was a platform-wide legacy
schedule regression, not a Twitter-specific fault.

## Evidence

The affected workflow is stored at `Workflow/social-media`. Its legacy
`workflow.json` has neither `created_by` nor an `access` ownership block.
Scheduled session tool results consistently recorded the same authorization
failure, including session
`schedule-cron--5227790a_1789963736158053000` on 2026-09-21.

The failure began after commit `74c66ab301`, which correctly made the shared
tool-registration boundary require authenticated claims. The scheduler still
copied only `WorkflowManifest.CreatedBy` into `ScheduleContext.OwnerUserID`.
For a legacy manifest that value was empty, so `startSessionInternal` created an
internal `/api/query` request with no `UserClaims`; every action tool was then
correctly rejected by `bindToolExecutionContext`.

This was not a LinkedIn login failure and retrying tools could not repair it.

## Fix

`buildScheduleContext` now resolves a durable scheduled execution identity in
this order:

1. the manifest's `created_by` value;
2. the first recorded access owner for older partially backfilled manifests;
3. the configured local owner for an ownerless legacy manifest, **only in
   single-user mode**.

An ownerless multi-user workflow remains unresolved and blocked. The fix does
not weaken the shared authentication boundary, fabricate a multi-user owner,
or permit model-supplied identity.

### Trigger owner-scope follow-up

All saved workflow execution sources now use the same owner rule. Cron runs,
manual schedule runs, API webhooks, internal workflow triggers, and configured
Slack workflow triggers execute with the durable workflow owner identity.
External credentials and bot routes still decide whether a delivery is
accepted and constrain what it may invoke; the external caller remains audit
metadata and never replaces the owner for secrets or tool-session identity.
Slack remains forced to Run mode even when it uses the owner's resource scope.
WhatsApp is unchanged: it continues to execute as its explicitly paired user
and receives only that user's actual workflow access.

### Simplified access/mode boundary

Workflow chat no longer accepts a separate requested mode as an authority
input. The effective access decision is the single source of truth:

- owner/write access derives Builder;
- read-only access derives Run;
- an explicit `PinRunMode` is downgrade-only;
- direct headless execution remains Run because it is not workflow chat.

Origin remains provenance, not permission. Consequently cron, manual, API and
internal triggers use their durable workflow-owner identity and receive the
owner/Builder workflow-chat surface needed for migrations and approved repair.
Slack still enters through a read-only principal and therefore derives Run.
WhatsApp derives the paired user's real access. Legacy route/session mode fields
remain parseable for compatibility but cannot override this decision.

## Regression coverage

`TestBuildScheduleContextThreadsOwnerUserID` now covers all four ownership
states: creator, access owner, ownerless single-user legacy workflow, and
ownerless multi-user workflow. The single-user case explicitly verifies that
the scheduler supplies a non-empty authenticated principal rather than relying
on the retired empty-string fallback.

`TestWorkflowTriggersUseOwnerExecutionScope` separately proves that a webhook
trigger binds action tools to the workflow owner. The Slack workflow-trigger
adapter now constructs its context through the same owner-resolving builder
instead of substituting its bot principal as execution owner.

`TestWorkflowAccessModeMatrix` is the matching authority contract. It covers
interactive owner/write/read users, stale client mode input, explicit downgrade,
cron/manual/API/internal owner triggers, restored schedules, Slack read-only,
paired WhatsApp owner, and direct headless execution. CI runs it beside the
execution-principal topology matrix so identity and chat authority cannot drift
independently again.

## Verification

- focused scheduler and tool-execution-context tests pass;
- the focused scheduler/tool-boundary suite passes; the complete `cmd/server`
  run is currently red on the unrelated in-progress internet-share work
  (`manage_internet_share` is exposed but not yet declared in `product.yaml`),
  and one cross-process allocator test was flaky in the full run but passed on
  its immediate focused rerun;
- `git diff --check` passes;
- the local server was restarted on `e21f799a7`; all 29 schedules, including
  every `twitter-automation` schedule, now register with
  `username=default user_id=default` instead of the prior empty identity;
- a real scheduled action remains the final acceptance check. It was not fired
  manually because the available Twitter/LinkedIn schedules can perform real
  external actions.

## Register notes

[PLAT-337](plat-337.md) records why every action
tool in `twitter-automation` schedule chats returned `requires an authenticated
session` while read-only bridge discovery still worked. The workflow predates
ownership metadata, so the scheduler passed no user identity into the stricter
shared tool boundary introduced on 2026-09-17. The scheduler now explicitly
uses the local owner for ownerless legacy workflows in single-user mode and a
recorded access owner when available, while ownerless multi-user workflows
remain safely blocked. Regression coverage is green; restart and live schedule
identity verification are complete on `e21f799a7`. All 29 local schedules now
register with the authenticated `default` principal; the next naturally due
action run is the remaining acceptance check. The follow-up also unifies saved
trigger execution: API/internal triggers and configured Slack workflow triggers
use the workflow owner's resource/secrets scope. Slack remains constrained to
Run mode with its bot principal and external sender retained for authorization
and audit; WhatsApp retains its paired-user identity model.
