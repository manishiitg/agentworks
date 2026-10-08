# Canonical agent-definition construction

**Status:** Target design retained; canonical construction not implemented. Current
runtime only partially matches it (uniform workflow-step DB writes, separate KB and
learnings gates, consolidated Engineering Review+Fix).

**Temporary runtime policy (2026-08-04):** Until the canonical-definition
refactor replaces the fragmented capability derivation, every ordinary workflow
execution step—including evaluation steps, todo-task children, and every turn of
a message sequence—receives managed workflow-DB read/write access through
`query_workflow_db` and `mutate_workflow_db`. Persisted `db_access: read` values
remain loadable but do not downgrade runtime access. Direct SQLite access stays
blocked for agentic steps. Specialized reviewers and maintenance agents are not
reclassified by this temporary step policy. This temporary policy applies only to
the workflow DB: it does **not** grant every step KB or learnings writes.

**Date:** 2026-08-04

**Last audited against code:** 2026-08-08

**Repositories:** `mcpagent`, `mcp-agent-builder-go`

**Primary motivation:** repeated DB-tool availability failures across workflow
steps, scheduled Pulse, the former standalone Pulse paths, background agents,
and converted chats

**Related:**
[mcpagent_public_api_simplification.md](mcpagent_public_api_simplification.md),
custom_tool_category_as_agent_addressing.md,
pulse_fixer_sqlite_readonly_wal_and_schema_guessing.md,
stage_agents_cannot_read_skills_or_query_db.md,
PLAT-003

## Decision proposed for review

Do **not** add a second public `AgentSpec` abstraction.

`mcpagent` already has the correct immutable contract:

```go
type AgentDefinition struct {
    Instructions string
    Skills       []*llmtypes.Skill
    Tools        ToolSet
}

type ToolDefinition struct {
    Name        string
    Description string
    InputSchema map[string]interface{}
    Execute     ToolExecutor
    Timeout     time.Duration
}
```

It also already has `RuntimeConfig` for model, transport, MCP session,
workspace paths, observability, and other infrastructure. The refactor should
finish adopting these existing types in `mcp-agent-builder-go`, not create a
parallel representation.

The target invariant is:

```text
mcp-agent-builder-go decides the agent's workflow role and authority
                         ↓
builds one complete mcpagent.AgentDefinition + RuntimeConfig
                         ↓
mcpagent validates, clones, materializes, runs, and retires that definition
```

Every agent launch path must cross this boundary once. No launch path may add,
remove, or rediscover identity tools after construction.

For a child explicitly described as inheriting its parent's workshop surface,
the boundary must resolve the child from the same role policy and tool bindings
as the parent. It must not start from a default step tool set and try to append
"workshop-only" tools later. A child may have narrower instructions or a
Reader authority profile, but any difference in its callable tools must be an
explicit role-policy decision made before construction.

## Confirmed decisions

In discussion, "AgentSpec refactor" is shorthand for this canonical builder
path. It does **not** mean adding another public `AgentSpec` beside
`mcpagent.AgentDefinition`.

The authority contract has exactly two values:

- **Reader:** can read the permitted workspace, DB, knowledgebase, and
  learnings surfaces, but receives no mutation tools.
- **Writer:** has the Reader surface plus managed mutation tools for the
  permitted workspace, DB, knowledgebase, and learnings surfaces.

There is no third profile and no independent DB/KB/learnings permission
matrix. Review+Fix executor, specialist reviewer, workflow-step, Goal Advisor,
Builder, and maintenance roles select Reader or Writer as part of their one
construction request.
Runtime authorization, session claims, and folder guards enforce that selected
profile; they do not infer or create another permission model.

Each completed definition also contains exactly one binding for each tool
name. A runtime-specific binding such as `agent_browser` is selected once while
building the definition; nested agents must not append another copy. The
legacy bundle adapter may temporarily collapse repeated names so existing
workflows keep running, but that is a migration guard, not the target design.
Once all launch paths use the canonical constructor, encountering a duplicate
is an assembly defect and strict definition validation must reject it.

## Why this is needed

The product requirement is small:

1. Decide what an agent may do.
2. Give it the corresponding instructions, skills, and callable tools.
3. Enforce and audit those capabilities for its session.

The current implementation represents the same decision independently in
several places:

- tool definition lists;
- executor maps;
- category maps;
- selected-tool strings;
- per-role allow-lists;
- code-execution bridge registries;
- session environment variables;
- folder-guard read/write paths;
- Pulse write-authority lending;
- separate constructors for workflow steps, review agents, Fixers, Goal
  Advisor stages, knowledgebase agents, generic background agents, scheduled
  runs, and converted chats.

This allows internally contradictory states such as:

- registered but not allow-listed;
- allow-listed but absent from the source tool pool;
- present in the parent but absent from its child;
- callable through a direct tool but routed through the wrong shell session;
- visible in a prompt but denied by runtime authority;
- available in scheduled Pulse but missing from standalone or converted-chat
  Pulse.

> **Update 2026-10-04:** the `run_in_background` tool described below was removed
> (PLAT-452); this section is kept as the history of why a child needs its
> parent's complete tool definition.

### Confirmed background-child incident (2026-08-08)

A now-retired standalone `/bug-review` was launched through the Workflow Builder's
`run_in_background` path. The parent Builder assembled plan, schedule, Pulse
recording, human-input, and guidance tools through its workshop registration
path. The child instead called `prepareCustomTools(nil)`, the ordinary
workflow-step default, and therefore initially lacked tools the parent had
already made available. The observed missing surface included Pulse lifecycle
recording, plan/schedule mutation, human-input tools, and
`get_workflow_command_guidance`.

The immediate compatibility repair shares the workshop registration helper and
collects native direct tools before constructing the child. It fixes this
specific path, and the real Bug Review subsequently received and used the
typed Pulse tools. It is **not** the canonical-definition refactor: the main
Builder still registers native tools after construction while the child
prebuilds a parallel list. A future tool added to only one route can recreate
the same fault.

This is direct evidence for the proposal, not merely an analogy to the earlier
DB incident: parent and child represented one intended capability decision in
two assembly paths, then diverged.

Tests have consequently accumulated around individual seams. Passing those
tests proves the pieces, not that a real agent receives a coherent definition.

## Current runtime reality (audited 2026-08-08)

The reader/writer table below is a **target**, not a description of current
workflow-step authorization:

- **DB:** every ordinary workflow execution step resolves to managed
  `read-write` and receives `query_workflow_db` plus `mutate_workflow_db`.
  Persisted `db_access` is compatibility data and does not narrow execution.
- **Knowledgebase:** still opt-in per step. Empty
  `knowledgebase_access` resolves to `none`; reads require `read` or
  `read-write`, and notes writes require write-capable access through the
  configured contribution path. `knowledgebase/context/` remains user-owned
  and is not step-writable.
- **Learnings:** still separately gated. Empty access normally resolves to
  `read`; writes require `read-write` plus a non-empty `learning_objective`.
  Routing/evaluation paths may suppress learning access.
- **Message-sequence turns:** still support item-level narrowing through
  `kind`/`write_access`; one sequence-wide reader/writer claim has not replaced
  that contract.
- **Pulse:** the user-facing standalone `/bug-review` command is retired.
  Engineering and Operations use a consolidated Review+Fix contract in the
  continuing Workflow Builder conversation. Optional specialist children can
  remain Reader analysts, while the parent Review+Fix executor is a Writer.

Consequently, the present code still has the independent DB/KB/learnings and
item-level permission dimensions this refactor proposes to remove. Uniform DB
access fixed an urgent availability problem; it did not complete canonical
agent construction or reader/writer migration.

## Historical evidence for the construction split

The 2026-08-04 logs demonstrate that this was path-specific rather than an
SQLite implementation failure. “Fixer” below is the historical stage name;
today the parent owns a consolidated Review+Fix contract:

- A normally scheduled Upwork Pulse Fixer registered both
  `query_workflow_db` and `mutate_workflow_db` and completed.
- A Social Media Fixer launched from a `schedule-manual`/converted-chat path
  failed before its provider turn with
  `required tool "mutate_workflow_db" is not registered`.
- The parent Social Media session itself had registered
  `mutate_workflow_db`; the child construction filtered from a different tool
  source and did not receive it.
- The new Pulse Fixer preflight detected the mismatch earlier. It did not
  create the missing capability and therefore did not solve the construction
  split.

PLAT-003 records the uniform workflow-step DB implementation and remains
`runtime_reverify` until producing runs prove ordinary, evaluation,
message-sequence, and child entry points. Its older standalone-Fixer wording is
historical and must not be used to restore that retired launch path.

## Existing foundation that should be reused

`mcpagent` already provides most of the desired end state:

- `AgentDefinition` contains exactly instructions, skills, and tools.
- `ToolDefinition` binds the model-facing schema and executor together.
- `NewAgentFromDefinition` validates and clones the complete identity before
  returning an agent.
- The returned `Agent` is opaque and cannot have its identity mutated by the
  builder.
- `RuntimeConfig` separates identity from infrastructure.
- Attached skills automatically materialize the intrinsic `read_skill` tool.
- Tool names are the address; `DisplayGroup` is presentation metadata only.

The incomplete part is primarily in the builder. `LLMAgentWrapper` still
creates a placeholder `mcpagent.Agent`, incrementally assembles a second
definition, calls `FinalizeDefinition`, creates another agent, and retires the
placeholder. Higher-level constructors still prepare overlapping tool slices,
executor maps, path policies, and allow-lists before reaching that wrapper.

This proposal is therefore a continuation of the public-API simplification,
not a new architecture.

## Ownership boundary

### `mcpagent` owns generic mechanics

- The `AgentDefinition`, `ToolSet`, and `ToolDefinition` contracts.
- Validation of duplicate tool and skill names.
- Atomic construction of a fully formed agent.
- Projection of definition tools into the selected provider transport.
- Intrinsic skill access.
- Session-scoped tool registration and cleanup.
- Applying generic runtime workspace read/write policy.
- Resume handles, streaming, model transport, accounting, and observability.

`mcpagent` must not know what Pulse, a workflow step, Goal Advisor, or
`mutate_workflow_db` means.

### `mcp-agent-builder-go` owns domain policy

- Which workflow role is being launched.
- The instructions and skills for that role.
- Which concrete tool bindings that role receives.
- How `step_config.json` maps into instructions, skills, tools, and workspace
  paths.
- Which workflow/database instance an executor is bound to.
- Business authorization such as "specialist reviewer is read-only" and "the
  parent Review+Fix executor is a bounded writer for this run".
- Pulse lifecycle, finding, approval, and workflow semantics.

The builder computes policy; `mcpagent` materializes it. Neither side should
recompute the other's decision.

## Builder target shape

The builder does not need another exported agent framework. It needs one
internal construction function:

```go
func buildWorkflowAgent(
    ctx context.Context,
    request workflowAgentRequest,
) (mcpagent.AgentDefinition, mcpagent.RuntimeConfig, error)
```

`workflowAgentRequest` is an internal typed request, not a second agent
definition. It contains the domain inputs necessary to make a decision:

```go
type workflowAgentRequest struct {
    Kind          workflowAgentKind
    WorkspacePath string
    Step          *StepConfig
    PulseRunID    string
    Model         LLMSelection
    Session       SessionSelection
}
```

The result is passed directly to `mcpagent.NewAgentFromDefinition`. A factory
must not return a half-built wrapper that callers continue configuring.

Role builders may remain small named functions for readability, but they must
all delegate to this boundary:

```go
buildWorkflowStepAgent(...)
buildPulseReviewFixAgent(...)
buildPulseSpecialistAgent(...)
buildGoalAdvisorAgent(...)
buildKnowledgebaseAgent(...)
buildGenericBackgroundAgent(...)
buildWorkflowBuilderAgent(...)
```

They express domain policy; they do not perform registration.

### Parent/child tool-surface rule

`run_in_background` is not a new tool-selection authority. When it creates a
Builder child, the role builder receives the parent's resolved workshop role
policy (or a typed explicit child role) and produces the child's complete
definition in one call. It must never call a broad default selector such as
`prepareCustomTools(nil)` and then separately register plan, Pulse, guidance,
or human-input tools.

Implementation may use a private immutable builder-side role-policy value to
avoid recomputing domain inputs, but it must be consumed immediately to create
the one public `mcpagent.AgentDefinition`; it is not a second public
`AgentSpec`, mutable tool registry, or list of names copied into prompts.

## Tool and authorization model

### Definition-time capability

If an agent can call a direct tool, its `AgentDefinition.Tools.Direct` contains
one complete `ToolDefinition`: name, schema, executor, and timeout.

There is no additional category lookup or string allow-list required to make
that direct tool exist. Categories may remain temporarily as display metadata,
but cannot participate in addressing or authorization.

### Runtime denial

Runtime security remains defence in depth. A supplied mutation executor must
still verify its trusted session/run authority before changing state. That
check protects against a stolen or misrouted call; it must not be a second
source for whether the tool belongs in the definition.

Prefer one typed session authority owned by the runtime over independent
`WORKFLOW_DB_ACCESS`, KB-access, learnings-access, and path-derived permission
facts. The builder creates either a reader or writer claim from its role
decision; executors validate the claim supplied by the active session.

### Two authority profiles only

The refactor deliberately removes the per-store permission matrix. An agent has
one of two profiles:

| Profile | DB | Knowledgebase | Learnings |
|---|---|---|---|
| Reader | read | read | read |
| Writer | read/write through managed tools | read/write | read/write |

Typical assignments:

| Agent kind | Profile |
|---|---|
| Workflow execution step | Writer |
| Message-sequence worker | Writer |
| Engineering/Operations Review+Fix executor | Writer |
| Optional specialist review child | Reader |
| Strategy Auditor or Goal Advisor analyst | Reader |
| Approved Goal Advisor finalizer | Writer |
| Workflow Builder | Writer |
| KB/learning maintenance agent | Writer |

Reader versus writer is domain policy in the builder. It must not be
reconstructed from folder paths, individual store flags, tool categories,
prompt text, or which registry happened to contain an executor.

This simplification intentionally accepts a broader store surface for writers.
Prompts and skills still define what a writer should change, but the runtime no
longer recomputes separate DB, KB, and learnings grants for every agent and
turn. Independent specialist children remain readers because changing evidence
during analysis would invalidate their review. The consolidated parent
Engineering/Operations Review+Fix executor is a writer because it owns the
bounded repair after recording its evidence.

## Workflow-step impact

Agentic workflow steps are in scope. Existing workflow files do not need a
schema migration for the first cutover.

The builder translates the current saved configuration:

```text
description/system contract  → AgentDefinition.Instructions
selected skills              → AgentDefinition.Skills
enabled custom tools         → AgentDefinition.Tools
reader/writer profile         → store tools and workspace policy
workspace/run paths          → RuntimeConfig.Workspace
model/tier/provider          → RuntimeConfig.Model and Generation
```

Message-sequence items, todo-task parents and children, evaluation agents, and
learning/KB post-agents should use the same translator.

During migration, current DB/KB/learnings access fields are read only to derive
the initial profile. The target workflow contract stores one reader/writer
choice and removes the individual permission fields in a version upgrade.

Saved scripted steps remain normal processes. Only an agent used to create,
repair, review, or interpret a script uses an `AgentDefinition`.

## Message-sequence authority

A message sequence is one identity, one conversation, and one authority
profile. Every item uses the same reader or writer profile. The runtime does not
recalculate DB, KB, or learnings permissions by item kind.

For a normal execution sequence:

```text
message_sequence profile = Writer
all user-message turns   = Writer
prevalidation turn       = Writer, instructed to validate rather than mutate
repair turn              = Writer
```

The same `mcpagent.Session` runs every item, preserving history and the
provider-native resume handle. Item `write_access` and kind-derived store grants
become obsolete and should be removed by workflow-version migration. Item kind
may remain as prompt/routing metadata; it no longer changes authorization.

If a workflow genuinely needs an independent read-only validation boundary,
model it as a separate reader agent/step. Do not add per-item permission
intersection back into message sequences.

`mcpagent.Turn.ToolPolicy` may remain for genuine product-level tool narrowing
(for example Builder modes), but message-sequence store permissions do not need
it. Its current empty-list semantics are therefore not a blocker for this
refactor.

## Migration plan

This should run on a dedicated branch because agent construction affects every
workflow while the intended behavior remains unchanged.

### Phase 0 — characterize before changing

- Inventory every production call to `CreateAndSetupStandardAgentWithConfig`,
  `CreateStandardAgentConfig*`, `FinalizeDefinition`, and direct agent
  construction.
- Classify each by agent kind and entry point.
- Record the expected instructions, skills, direct tools, MCP sources,
  workspace policy, model, session, and resume behavior.
- Add one temporary definition snapshot helper for tests. Do not expose it as a
  new production API.

### Phase 1 — Pulse stages

- Move the scheduled Pulse main conversation, consolidated Engineering/
  Operations Review+Fix executor, and optional specialist review children to
  direct `AgentDefinition` construction.
- Make scheduled, manual `/engineering-review`, background, and converted-chat
  launches call the same role builder. Do not restore the retired standalone
  `/bug-review` command or a separate automatic Fixer.
- Include a Builder-launched background Engineering/QA child: it must receive
  the exact role-approved direct tool surface, not the ordinary workflow-step
  default bundle.
- Bind query/mutation executors before construction.
- Retain the current preflight temporarily as a diagnostic assertion.
- Prove a real read and mutation/read-back through both scheduled and
  converted-chat Review+Fix executions.

This phase closes the immediate PLAT-003 gap without waiting for every
workflow-step constructor to migrate.

### Phase 2 — workflow steps

- Replace `prepareCustomTools`, separate executor filtering, and incremental
  tool registration with one translation from effective `step_config` to
  `AgentDefinition`.
- Cover regular agentic steps, message-sequence agents, todo-task parents and
  children, and evaluation steps.
- Give each agent one reader or writer profile. A writer receives managed
  read/write access to DB, KB, and learnings; a reader receives read access to
  all three.
- Give one message sequence one profile for its complete session. Remove
  per-item store-permission narrowing.
- Add a workflow-version migration that consolidates the current
  `db_access`, KB access, learnings access, and item `write_access` fields into
  the one profile, then removes the obsolete fields from the active contract.

### Phase 3 — remaining background agents

- Migrate plan, artifact, ops, timing/cost, KB, Goal Advisor, Chief of Staff,
  and generic background agent paths.
- Attach their required skills in the definition rather than after agent
  creation.
- Ensure isolated working directories remain transport details and do not
  change the attached definition.
- Delete the temporary split between post-construction parent registration and
  pre-construction child registration once both call the common role builder.

### Phase 4 — Builder/main and conversion paths

- Build the workflow Builder agent directly from a complete definition.
- For per-turn mode/tool changes, create a new definition and resume the same
  provider conversation; never reset or mutate the identity of the existing
  agent.
- Make "convert schedule to chat" reuse the same logical conversation/session
  while rebuilding a current definition through the canonical constructor.

### Phase 5 — delete the old lifecycle

Only after every production constructor has moved:

- delete placeholder-agent creation and `LLMAgentWrapper.FinalizeDefinition`;
- delete builder-side incremental instruction/skill/tool mutation;
- delete `filterWorkspaceToolsByName` as a construction mechanism;
- delete per-role string allow-lists that duplicate actual tool bindings;
- delete categories from authorization/addressing, retaining at most optional
  presentation metadata;
- delete executor maps that exist separately from tool definitions;
- remove DB-tool injection based on launch path;
- replace `WORKFLOW_DB_ACCESS` and the separate DB/KB/learnings permission
  values only after the typed reader/writer session claim covers every writer
  and denial path;
- delete message-item `write_access` and kind-derived authorization after the
  workflow-version migration;
- remove any remaining path-specific capability preflight once impossible
  states cannot be constructed. Until then, keep it as a diagnostic and do not
  call it the fix.

## Test strategy after simplification

The objective is fewer, stronger tests—not a test for every historical seam.

### 1. Builder policy tests

One table-driven test asserts the complete definition for each agent kind:

- exact direct tool names;
- exact attached skill names;
- reader/writer role matrix;
- workspace read/write policy;
- no duplicate tool or skill names.

For message sequences, assert that every item shares the sequence profile and
that obsolete item kinds/`write_access` no longer change runtime authority.

For a Builder parent and a child declared to inherit its workshop role, assert
the exact same approved tool names and attached skill names before transport
projection. Assert that an explicitly Reader child differs only by the
role-policy removal of mutation bindings, never because it fell back to a
default tool bundle.

### 2. `mcpagent` definition contract

Keep the existing tests that prove definitions are immutable, cloned, reject
duplicates, and project tools and skills into the bridge.

### 3. One managed DB bridge integration

Use real WAL-mode SQLite and the production MCP bridge to prove:

- query succeeds with read authority;
- mutation succeeds with write authority;
- mutation is unavailable to a reader definition;
- a misrouted or unauthorized mutation is rejected by the executor.

### 4. Entry-point E2E tests

Only adapter behavior differs, so test the entry points that previously
diverged:

- scheduled Pulse Review+Fix;
- manual `/engineering-review` review-and-fix sequence;
- converted schedule-to-chat Pulse Review+Fix;
- one reader and one writer ordinary workflow agent.
- a Builder-launched background Engineering/QA child that calls
  `get_workflow_command_guidance`, records a typed finding, creates a human
  input request, and performs one authorized plan mutation/read-back through
  the real bridge. This is the regression case for the 2026-08-08 incident.

The tests should assert a definition fingerprint plus one real action, not
source strings or only allow-list membership.

## Acceptance criteria

The refactor is complete only when all of the following are true:

1. Every production LLM agent starts from one complete
   `mcpagent.AgentDefinition` and `RuntimeConfig`.
2. No placeholder `mcpagent.Agent` is created and replaced during definition
   assembly.
3. The same role produces the same definition regardless of scheduled,
   manual, background, or converted-chat entry point.
4. Direct tools cannot be allowed-but-unregistered or
   registered-but-not-allowed because definition and executor are one binding.
5. Tool categories are not addresses or authorization facts.
6. Prompt text does not promise a tool absent from the completed definition.
7. Optional specialist review children cannot mutate the workflow DB.
8. The parent Engineering/Operations Review+Fix executor can query and mutate
   through the managed bridge and can record lifecycle results.
9. Every workflow agent has exactly one reader or writer profile; no independent
   DB, KB, or learnings permission matrix remains.
10. Writers can read/write DB, KB, and learnings through their managed surface;
    readers can read all three and cannot mutate them.
11. Every message-sequence item shares its sequence profile; item kind and
    `write_access` do not alter authorization.
12. Session cleanup removes the definition's bridge registrations when the
    agent ends.
13. Existing workflow files require no migration for the initial cutover.
14. Full real-provider E2E suites for Codex and Claude pass after the complete
    migration.
15. A Builder-launched child cannot have a tool that is present in its declared
    inherited parent surface silently disappear because a default step bundle
    was selected on a different construction path.

## Risks and constraints

### Provider resume and message sequences

Rebuilding an immutable definition must preserve the provider-native resume
handle and warm tmux ownership. A new definition must not mean a new logical
conversation unless the caller requested one. Multi-turn message-sequence
agents must continue using one agent/session for their ordered turns.

### Dynamic Builder modes

The main Builder currently narrows tools per turn. The new design should build
the current turn's definition from mode policy and resume the conversation,
not mutate a long-lived agent. This is likely the most sensitive migration and
belongs after Pulse and workflow steps.

### Security regression

Removing duplicated authorization must not remove defence in depth. Definition
membership answers "should this agent have the tool?"; executor authorization
answers "is this exact call from the authorized active session/run?" Both
remain, but they derive from the same reader/writer decision. Independent
specialist children remain readers; the consolidated parent Review+Fix executor
is the writer that reconciles their evidence.

### Partial migration

A compatibility adapter is acceptable temporarily, but there must be one-way
movement: old configuration into the canonical definition. Do not synchronize
old and new mutable representations in both directions.

### Scope control

Do not combine this change with Pulse product redesign, workflow schema
redesign, UI changes, or SQLite schema changes. Those would make runtime
regressions impossible to attribute.

## Explicit non-goals

- Moving Pulse or workflow business concepts into `mcpagent`.
- Replacing `mcpagent.AgentDefinition` with another public type.
- Removing runtime authorization or folder isolation.
- Making scripted workflow steps into agents.
- Changing Review+Fix scheduling, Pulse selection, or lifecycle semantics.
- Keeping categories as hidden capability addresses.
- Reintroducing independent DB, KB, learnings, or message-item permission
  dimensions after the reader/writer migration.

## Review questions before implementation

1. Can every current agent path be expressed as the existing
   `AgentDefinition` plus `RuntimeConfig` without expanding their public API?
2. Which builder-only type should carry trusted session claims until the final
   definition and runtime are created?
3. Can the current main-agent per-turn tool policy be represented by rebuilding
   a definition while preserving the same provider resume handle?
4. Which existing post-creation registrations are genuinely dynamic runtime
   events, and which are only legacy definition assembly?
5. Is `DisplayGroup` needed by any UI after categories stop participating in
   policy?
6. Can session registry cleanup be tied directly to `Agent.Close` for every
   builder path?
7. What exact legacy-field mapping should the workflow-version migration use
   when old DB, KB, and learnings permissions disagree: any-write becomes
   writer, or should a smaller set of explicit cases require user review?

## Recommendation

Proceed, but treat this as finishing an existing refactor rather than starting
a new one. Implement Phase 1 first and require the scheduled plus
converted-chat Pulse Review+Fix E2Es before expanding scope. If Phase 1 cannot be
expressed cleanly using the current `AgentDefinition` and `RuntimeConfig`, stop
and review the boundary instead of adding another compatibility layer.
