[← brain / agents](index.md)

# PLAT-651: Workflow steps could not use Brain (no caller identity)

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | brain |
| Area | agents |
| Summary | Workflow steps on a coding CLI got 'Brain tool requires its authenticated caller' for every Brain call; their bridge calls carried no user |

## What happened

## Fix

## Left

## What happened

RTS, 2026-10-07 (and in yesterday's log): `step-weekly-knowledge-refresh` in rtslatency, started from the Workshop chat, failed: every brain_browse/brain_access call returned "Brain tool requires its authenticated caller". Steps on a coding CLI call their tools through their token-authenticated bridge session (`/s/<session>/tools/custom/...`) with no user in the request. The main chat and delegated sub-agents get a server-owned identity binder (`bindToolExecutionContextForSession`); workflow step tools did not, and the Brain executor (rightly) refuses a call with no identity.

## Fix

`bindKnowledgebaseStepIdentity`: where a workflow session's tools are set up (handleQuery workflow mode and `buildWorkshopConfig`), the Brain tools get the authenticated request's identity for calls from that session or a step session the bridge registry ties to it, when the call brings none. The executor's own check is unchanged (a bare tool table still refuses). Test: added to `TestKnowledgebaseToolExecutionRejectsForeignIdentity`. Not deployed.
