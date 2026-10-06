[← goals / step-execution](index.md)

# PLAT-356 — Attached context made query_workflow_db "ambiguous"

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | goals |
| Area | step-execution |
| Summary | fixes `workflow database context is ambiguous`. |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `fixed; live-verified 2026-09-23` |
| Last synchronized | `2026-09-23` |
| Priority | `P1 execution` |
| Category | step-execution (runner-up: security-sandbox) |

## Symptom

Every `query_workflow_db` call in the `websiteaeo` builder session
(`6eaa17e1…`) failed from 10:26 onward with
`workflow database context is ambiguous for session "6eaa17e1-…"`, including a
bare `select 1`. Pulse filed this against the workflow as PUL-BC908119
("Workflow DB tools report ambiguous context after the sales_outreach KB was
attached"). `query_workflow_costs` had the same defect.

## Cause

`resolveWorkflowWorkspaceFolder` (`virtual-tools/workflow_db_tools.go`) works
out which workflow the session belongs to. When `DB_PATH` is not set, it
treats every read path, write path and the working directory as equal
candidates. Attached context adds read grants that belong to **other**
workflows:

- `knowledgebase_sources` grant `Workflow/<other>/knowledgebase`. Here that was
  `Workflow/salesoutreach/knowledgebase`.
- A workflow or Crew project attached as context (`WorkflowContextPaths`,
  `contextReferenceReadRoot`) grants that project's **whole root**. So a
  Crew chat with another Crew or workflow attached hits the same error.

Two distinct candidates meant "ambiguous", so attaching anything broke every
database query in the session.

## Fix

The resolver now decides in tiers, using the first tier that finds a match:

1. `DB_PATH`, unchanged.
2. The session's own writable roots and working directory. Attached context
   is always read-only.
3. Read grants, only when the session's own roots identify nothing.
4. Knowledgebase grants, last.

Two genuine candidates in the same tier are still ambiguous.
`resolveCurrentWorkflowCostsPath` now uses the same resolver, so
`query_workflow_db` and `query_workflow_costs` always pick the same workflow.

## Regression coverage

`virtual-tools/workflow_db_context_test.go` covers four cases:

- A builder session with an attached KB source resolves to its own workflow.
  This test fails without the fix.
- A Crew with another Crew and a workflow attached resolves to its own project.
- A session with only a KB grant still resolves.
- Two genuine workflow grants still report ambiguous.

The `virtual-tools` package is green.

## Live acceptance

Verified on 2026-09-23 on the restarted local server. The `websiteaeo` builder
session `6eaa17e1-…` still had `Workflow/salesoutreach/knowledgebase` in its
read paths. Its `query_workflow_db` call at 11:52:13 succeeded, and no
`context is ambiguous` error appeared for the rest of the run. Before the fix,
the same session failed every call.

## Register notes

[PLAT-356](plat-356.md) fixes
`workflow database context is ambiguous`. When `DB_PATH` was not set, the
session resolver counted every folder grant as a candidate home. An attached
knowledgebase source (`Workflow/<other>/knowledgebase`) or an attached
workflow or Crew project (whole-root read grant) therefore made every
`query_workflow_db` / `query_workflow_costs` call fail, both in builder
sessions and in Crew chats with other Crews attached. The resolver now checks
the session's own writable roots and working directory first, then read
grants, then knowledgebase grants. The costs tool shares the same resolver.
Live-verified 2026-09-23.
