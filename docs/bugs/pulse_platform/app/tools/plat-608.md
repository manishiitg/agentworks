[← app / tools](index.md)

# PLAT-608: product.yaml is the only tool registry; Brain tools renamed to brain_*

| Field | Value |
|---|---|
| State | in progress |
| Priority | P1 |
| Product | app |
| Area | tools |
| Summary | Tools are registered and authorized through several paths besides product.yaml; Brain tools keep their knowledgebase names |

## What happened

## Fix

## Left

## Why

Owner, 2026-10-06: "we use product.yml to register tools right ... and there should be no other path to register", and rename the Brain tools to brain. Two bugs today came from the other paths: PLAT-600 (the Builder's Brain project tools were swapped in by server.go when the request was workflow_phase, but their authority check ran after handleQuery rewrote the mode to multi-agent, so they always refused) and the need to add a new Brain tool by hand to four copied name lists (external builder, feature catalog, test mode, step execution policy).

## Plan

1. Decide product, phase and role once at admission and store it on the request; registration and every permission check read that value instead of re-deriving it from request fields that change during the request.
2. product.yaml is the only registry: phase tool variants (such as the Builder's project-scoped Brain tools) are declared there, and copied name lists are derived from the tool owner's definitions.
3. Rename the six Brain tools to `brain_*` (browse, read, update, backup, skills, access), keeping the `*_knowledgebase` names as aliases for a transition; update prompts, docs and the AgentWorks skill. Product ID, URLs and token scopes stay `knowledgebase` for now.

## Progress

- Survey done (2026-10-06): runtime-enforced product.yaml lists are Crew, Code, Brain, Vault, SparkQuill, Video, the Relay builder and every external MCP list. Goals workflow chats (Builder and Run) run the gate in observe mode and register about 20 tool groups by Go conditions; their yaml lists are compared only in tests. The Builder/workflow-phase decision is re-derived in about 30 places, about half after handleQuery rewrites AgentMode. Brain names are copied into about 10 code lists, and read/update/manage_knowledgebase_access are also internal operation names.
- Step 1 done: `QueryRequest.admittedWorkflowPhase` (server-set, not JSON) is recorded once before the rewrite; `knowledgeProjectBuilderQuery` reads it. This also closes a gap in the PLAT-600 fix, which accepted any multi-agent request that named phase_id=workflow-builder. Tests now build the request in the shape the server actually binds tools with.
- Step 2 done: the public Brain tool names live in `pkg/knowledgebase/names.go` (`ToolBrowse` ... `ToolAccess`, `ToolNames()`); every public-name use outside the package and in the MCP surface uses them, and the copied lists (external builder, feature catalog, test mode, step execution policy, step agent allowlists) derive from them. Internal operation names stay literal. Step agents that may read Brain now also get `knowledgebase_skills` (they were missing it).
- Step 3 done: renamed to `brain_browse`, `brain_read`, `brain_update`, `brain_backup`, `brain_skills`, `brain_access`. `CanonicalToolName` converts the old names at every entry: external REST and MCP (`call_tool`, `get_api_spec`), `CallTool`, `ValidateToolArguments`, `IsMCPTool`, `ToolActionMutates`, `ReserveIntegrationRequest` (so a retried request_id under either name is one request), the server dispatch, `knowledgebaseConnectionAllowsAction`, `externalKnowledgebaseCall`, `externalBuilderToolDenied` and the Builder project path. Internal operation names are unchanged. product.yaml lists, prompts, guidance, the AgentWorks skill, the operations doc and two frontend Ask-AI messages use the new names. A CLI session that cached the old names gets the new catalog when its chat definition refreshes. Pinned by `TestLegacyBrainToolNamesAreAliasesNotSecondTools`; tests that asserted names now assert the new ones.
- Step 4a done: Goals workflow chats (no profile) get a shadow gate measured against `chat.builder.tools` / `chat.run.tools`. It filters nothing; each session logs `[PRODUCT_TOOL_GATE] profile=goals-builder|goals-run ... registered=N` and `would_filter=N: <names>`. Gate logging used to skip profile-less chats entirely. The existing surface test covers only the phase/workshop, human and Brain tools, so it cannot show the full list.
- Step 4b (left): after a deploy, read `would_filter` from a real Builder and a real Run session (including a coding CLI and an API model), add the missing names to product.yaml (or drop tools that should not be there, as an owner decision), then switch Goals chats to `newProductToolGateForAllowlist` and keep the shadow log until it shows zero.
