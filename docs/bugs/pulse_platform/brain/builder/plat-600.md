[← brain / builder](index.md)

# PLAT-600: CLI Builder refused its own Brain project tools

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | brain |
| Area | builder |
| Summary | A Builder run by a coding CLI could not use its Brain project tools: 'Only the current workflow Builder can configure knowledge bindings' |

## What happened

## Fix

## Left

## What happened

RTS, 2026-10-06 10:38 UTC, rts-aws Builder (session `d9fdf55d…`, a coding CLI): `browse_knowledgebase` (folders) and `manage_knowledgebase_access` (`inspect_project`) failed with "Only the current workflow Builder can configure knowledge bindings". The agent then tried curl against the session tool route. Builder authority is attached in `bindToolExecutionContextForSession` only when the caller session is empty or equal to the tool session; a CLI Builder calls back through the bridge under an MCP session registered to the Builder session (`callerOwnedBySession`), which the general ownership check accepts but the Brain check did not.

## Real cause (found from the deployed `[KB_PROJECT]` log, 2026-10-06 10:58 UTC)

`knowledgeProjectBuilderQuery` required `req.AgentMode == "workflow_phase"`, but `handleQuery` turns every workflow_phase request into `multi-agent` (server.go, "Convert to multi-agent mode") before the tool context is bound, so the root Builder never received authority for its own Brain project tools; the sessions all matched. The first guess (accepting bridge sessions owned by the Builder) was wrong and is reverted.

## Fix

Authority is granted for `workflow-builder` in either mode; everything else about the check is unchanged (not a bot route, the trigger is the person or an external Builder call, an interactive or Builder token, same session). Pinned by `TestKnowledgeProjectBuilderAuthoritySurvivesTheModeRewrite`. The refusal logs stay.

## Left

## What happened

RTS, 2026-10-06 10:38 UTC, rts-aws Builder (session `d9fdf55d…`, a coding CLI): `browse_knowledgebase` (folders) and `manage_knowledgebase_access` (`inspect_project`) failed with "Only the current workflow Builder can configure knowledge bindings". The agent then tried curl against the session tool route. Builder authority is attached in `bindToolExecutionContextForSession` only when the caller session is empty or equal to the tool session; a CLI Builder calls back through the bridge under an MCP session registered to the Builder session (`callerOwnedBySession`), which the general ownership check accepts but the Brain check did not.

## Fix (not verified live)

- Builder authority is also attached for a caller the session registry ties to this Builder session. Steps stay refused: they call with their own session in `ChatSessionIDKey`, which `knowledgeProjectBuilderExecute` rejects ("Steps cannot configure knowledge bindings").
- Both refusals now log the sessions (`[KB_PROJECT] ...`), so if the cause is a different condition the next failure shows which.

## Left

- Deploy to RTS, then retry in the rts-aws Builder: browse folders and inspect_project should answer.
- Done 2026-10-06: rts-aws's Builder updated its skill, soul, KB placement note and MEMORY.md to whole-Brain access with folder_path (no bindings). `planning/step_config.json` review notes still mention the old rtslatency attachment as something not to use.
