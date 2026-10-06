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

## Fix (not verified live)

- Builder authority is also attached for a caller the session registry ties to this Builder session. Steps stay refused: they call with their own session in `ChatSessionIDKey`, which `knowledgeProjectBuilderExecute` rejects ("Steps cannot configure knowledge bindings").
- Both refusals now log the sessions (`[KB_PROJECT] ...`), so if the cause is a different condition the next failure shows which.

## Left

- Deploy to RTS, then retry in the rts-aws Builder: browse folders and inspect_project should answer. If they still fail, read the `[KB_PROJECT]` log line.
- rts-aws's own notes (learnings/_global/SKILL.md, soul/soul.md, planning/step_config.json, knowledgebase/notes/knowledge-placement.md, MEMORY.md) still tell it to bind `RTS/Latency` with alias `latency`; it has full Brain access (`brain_access: write`, no bindings), so those notes should be updated by its Builder.
