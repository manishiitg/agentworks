# Run-mode capability boundary review — 2026-09-06

## Decision

Do not ship the current Run-tool reduction as an allow-list wired through
`GetToolsForWorkshopMode`, and do not treat the current registration gates as a
complete Run or read-only boundary.

The intended product split is sound: a Builder/Workshop chat may change the
workflow, while a Run chat or ordinary schedule may execute the existing
workflow and perform its authorized business actions. The implementation does
not enforce that split end to end. Prompt and built-in reference skills are
narrowed, and several typed Builder tools are omitted, but native CLI writes,
generic database writes, approval writes, and Pulse state writes can cross the
boundary.

This review covers the dirty worktree at `d949571b5`. It does not claim the
uncommitted changes are deployed.

## Confirmed findings

### 1. Critical — a Run agent can record an agent-produced answer as a human approval

Every workflow-phase chat registers all report human-input tools through
`createCustomTools(true)`. The mode-specific `HumanToolNamesForWorkshopMode`
list excludes `answer_human_input_request` in Run, but the list has no production
caller. `GetToolsForWorkshopMode` is likewise read only by tests.

The live executor accepts model-supplied `input_id`, option and note, then writes
`answered_by_kind="human_via_chat"` and `answered_via="agent_chat"`. It does not
verify a user-message-bound approval claim. A Run or scheduled agent can create
a decision, answer it itself, and leave a durable record that downstream
pre-run handling interprets as human authorization.

Evidence:

- `agent_go/cmd/server/tool_setup.go:289-305`
- `agent_go/cmd/server/server.go:5327-5404`
- `agent_go/cmd/server/report_human_inputs.go:957-973,1050-1064`
- `agent_go/cmd/server/virtual-tools/human_tools.go:416-431`

Required correction: an agent must never be able to manufacture the credential
that proves human approval. Keep the UI answer endpoint as the normal writer.
If chat answers remain supported, mint a server-side, single-use authorization
from the exact authenticated user message and require it at the executor. Do
not expose the answer writer to scheduled or deployed-channel Run sessions.

### 2. Critical — generic Run database mutation bypasses approval and Pulse tools

`mutate_workflow_db` is deliberately included in the proposed Run list and is
registered in every workflow phase. Its SQL validation checks only that each
statement is one `INSERT`, `UPDATE`, or `DELETE`. It has no protected-table
policy.

The same SQLite file contains platform-owned tables including
`report_human_inputs`, `report_human_input_events`, `pulse_module_state`,
`pulse_module_audit`, `run_concerns`, `eval_results`, and
`schema_migration_log`. A reserved-table list exists for report inline edits,
but the generic mutation endpoint does not apply it. Removing the typed approval
or Pulse tools from the catalog would therefore leave a direct SQL bypass.

Evidence:

- `agent_go/cmd/server/virtual-tools/workflow_db_tools.go:120-141,427-449`
- `workspace/handlers/query.go:753-777,853-921`
- `workspace/handlers/query.go:1041-1058`

Required correction: split database authority into at least `read`,
`operational-data-write`, `platform-state-write`, and `schema-write`. Enforce
table ownership in the workspace service, after parsing the target table, so a
tool alias or HTTP bridge cannot bypass it. Platform-owned tables must be
writable only through their dedicated validated APIs.

### 3. Critical — Run and read-only native CLIs receive shared-workflow write authority

The workflow CLI may start in a private runtime directory, but the resolved CLI
security policy passes both the private directory and the authoritative shared
workflow directory as write paths. This decision ignores WorkshopMode and the
read-only access flag. Compatibility mode, which is the default, launches Codex
without a filesystem sandbox at all.

The normal workspace folder guard is initially narrowed for a read-only user,
then a later workflow-phase setup overwrites the session guard with
`phaseWorkspacePath` as writable for everyone. The isolated-runtime prompt also
instructs the native CLI to use absolute paths into that shared workflow.

Consequences include direct writes to `workflow.json`, learnings, knowledgebase,
evaluation, reports, and other shared artifacts. The planning folder's typed
tool rule also does not protect native CLI file tools when the later/session or
provider guard grants the root.

Evidence:

- `agent_go/cmd/server/server.go:4773-4796`
- `agent_go/cmd/server/server.go:5082-5199`
- `agent_go/cmd/server/server.go:5675-5705`
- `agent_go/cmd/server/workflow_cli_isolation.go:31-44`
- `agent_go/pkg/clisecurity/store.go:118-147`
- `multi-llm-provider-go/internal/clisandbox/sandbox.go:35-45,151-166`

Required correction: derive both workspace bridge and provider-native
read/write paths from the same effective capability object. Run and read-only
main agents should receive the workflow root as read-only. Give writes only to
private runtime state, chat history, and explicitly approved output locations.
Workflow steps keep their separately scoped execution write grants. A typed
`capture_context` operation should own its narrow write rather than widening the
main Run shell.

### 4. High — owner Run sessions can apply database schema migrations

All database tools are globally registered in workflow phase. The migration
executor accepts any session with `db_access=read-write`. Main-session DB access
is derived from user access only, so an owner in Run mode receives read-write
and can call `apply_workflow_db_migration`. The proposed Run list excludes this
tool, but that list is not connected to the live agent.

Evidence:

- `agent_go/cmd/server/tool_setup.go:266-274`
- `agent_go/cmd/server/server.go:5191-5199,5700-5705`
- `agent_go/cmd/server/virtual-tools/workflow_db_tools.go:457-499`

Required correction: schema authority must be a distinct server-side
capability, granted only to the Builder or a specifically authorized maintenance
turn. A generic read-write bit is insufficient.

### 5. High — Pulse mutation is registered broadly and has no Pulse capability check

All Pulse tools are registered in every workflow phase. The runtime validator
explicitly checks only for a non-empty correlation id and says Pulse owns no
lease or authorization layer. A normal Run chat can call
`record_pulse_worklist`, `record_pulse_result`, `record_pulse_impact`,
`record_pulse_module_due`, `resolve_run_concern`, and related writers. The only
extra wrapper blocks one narrow case: recording a failed result while child
executions are still active.

There is a `pulse_write_authority.go` delegation seam and tests, but no current
production installation of its delegator. Comments elsewhere that describe
session-keyed Pulse authority are therefore stale.

Evidence:

- `agent_go/cmd/server/tool_setup.go:298-305`
- `agent_go/cmd/server/pulse_runtime_guard.go:39-47`
- `agent_go/cmd/server/pulse_result_guard.go:14-36`
- `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/pulse_write_authority.go`

Required correction: issue a server-owned Pulse writer claim only for an
internally scheduled/manual Pulse lifecycle turn and its explicitly delegated
writer children. Every Pulse mutation executor must verify that claim. A
client-supplied `pulse_lifecycle_turn` boolean is context, not authority.

### 6. High — the current tests prove a dead list, not the live Run surface

`GetToolsForWorkshopMode` says it is applied as a per-turn policy, while
`installWorkflowPhaseTools` says no such narrowing occurs. The function's only
current consumers are tests. `toolset_invariant_test.go` can therefore prove
that dangerous names are absent from a list that production never reads.

The focused tests all passed during this review:

```text
go test ./cmd/server ./pkg/orchestrator/agents/workflow/step_based_workflow \
  -run 'TestToolSetInvariants|TestMutatingWorkshopToolsAreGatedByRunMode|TestWorkshopRegistrationIsNotParameterizedByMode|TestEveryRegisteredWorkshopToolIsAllowedInSomeMode' \
  -count=1
```

That green result is false assurance about the effective Run agent.

Evidence:

- `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/interactive_workshop_manager.go:1364-1413`
- `agent_go/cmd/server/workflow_phase_tools.go:23-35,512-525`
- `agent_go/cmd/server/toolset_invariant_test.go:136-180,224-241`
- `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/workshop_mode_no_narrowing_test.go`

Required correction: snapshot the completed live `AgentDefinition` for a fresh
Builder chat, owner Run chat, read-only Run chat, ordinary schedule, Pulse turn,
and each writer child. Add negative execution tests through both direct calls
and the HTTP bridge. Test native CLI filesystem denials separately.

## Why the old allow-list also failed

The historical files correctly record a different problem: a per-turn string
allow-list filtered the catalog cached by coding CLIs and also wrote into a
session-wide HTTP bridge registry shared with child actors. Missing entries made
real tools undiscoverable and broke secrets, `list_llm_capabilities`, and
background children. Reintroducing that mechanism would repeat those failures.

Relevant records:

- `docs/design/agent_tool_surface_single_source.md`
- `docs/refactor/canonical_agent_definition_construction.md`
- `docs/refactor/mcpagent_public_api_simplification.md`
- `docs/bugs/pulse_platform/sandbox/access/plat-262.md`
- `docs/bugs/pulse_platform/sandbox/access/plat-296.md`

The design documents also state the correct principle: focus guidance can live
in prompts, while authority must be enforced in code. The regression was
removing or weakening authority controls alongside the duplicate catalog list.

## Safe replacement

1. Define one immutable capability set from the intersection of authenticated
   user access, conversation role, caller origin, and lifecycle claim.
2. Build a fresh agent definition per fixed-role chat or schedule. Builder and
   Run tabs already have separate sessions; mode changes already start a fresh
   native CLI conversation, so this does not require a mutable per-turn list.
3. Register definitions and executors together from capability metadata rather
   than a second list of tool-name strings. Fail construction if the promised
   tool, executor, DB authority, or path policy is inconsistent.
4. Enforce the same capabilities again in mutating executors and the workspace
   service. Discovery cannot be the authorization boundary.
5. Give each child actor its own session and capability set. Never narrow the
   parent's session-wide HTTP bridge to control a child.
6. Keep unavailable-tool discovery explicit: return a catalog entry with
   `available=false` and a reason, or expose a capability-inspection tool that
   is separate from callable registration. Do not make forbidden tools callable
   merely so the agent can discover their names.
7. Keep prompt and skill filtering as usability controls. Treat custom skills
   and workflow learnings as untrusted instructions; server enforcement must
   remain correct if they tell the agent to cross a boundary.

## Minimum release gate

Do not mark the Run/schedule separation complete until all of these fail closed:

- owner Run and read-only Run cannot write shared workflow configuration or
  planning files through native CLI tools, bridge shell, or patch tools;
- Run cannot answer a human decision or update its underlying rows with SQL;
- Run cannot mutate Pulse lifecycle tables or invoke Pulse writers;
- Run cannot apply migrations or change secrets, schedules, skills, LLM config,
  evaluation design, or report source;
- normal execution steps can still perform their declared business actions and
  write only their scoped outputs;
- an actual Pulse lifecycle and an approved Builder repair retain the exact
  capabilities they need;
- conversation JSON, token usage, cost attribution, resume, and automatic
  completion remain separated by the same session/runtime identity.
