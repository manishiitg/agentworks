[← goals / step-execution](index.md)

# PLAT-269 — `todo_task` ran on its own duplicated turn loop, carried an unused scripted fast path, and was named for a todo list that no longer existed

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | goals |
| Area | step-execution |
| Summary | P2 maintainability with a P1 side effect: every fix to the |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `implemented, pushed, live e2e verified 2026-09-02` |
| Last synchronized | `2026-09-02` |

- **Priority:** P2 maintainability with a P1 side effect: every fix to the
  conversational turn loop had to be made twice. PLAT-189 (proactive
  validation-schema surfacing) and PLAT-255 (pre-validation notifications)
  were each patched separately in `controller_message_sequence.go` and
  `controller_todo_task.go`.
- **Owner:** `pkg/orchestrator/agents/workflow/step_based_workflow/`
  (`controller_orchestrator.go` — was `controller_todo_task.go` —,
  `controller_message_sequence.go`, `sub_agent_async.go`,
  `orchestrator_step_type_migration.go`), `cmd/server/virtual-tools/sub_agent_tools.go`,
  `cmd/server/workflow_version_upgrades.go`, guidance `orchestrator.md` /
  `optimize-playbook.md`, frontend `stepConfigMatching.ts`.
- **Commits on `main` (2026-09-02):** `a79c68371` remove scripted orchestrator
  path · `53dc8a439` delete dead todo list, `todo_id`→`task_id` · `c0ae21cc3`
  run the orchestrator on the message_sequence executor · `5716be7bc` Go
  identifier/file rename · `a04bde4ae` plan type `orchestrator` + read alias,
  tool aliases, drift ID, guidance, contract v1.0.35 migration.

## What was wrong

**Two executors for one shape.** A `todo_task` step is a conversational agent
plus a delegation capability. `controller_todo_task.go` (2019 lines)
re-implemented the message_sequence executor's turn loop, folder guard,
per-turn logging, prevalidation, and learnings/KB contribution turns. The
`messages` field on a `todo_task` even reused `MessageSequenceItem`, then ran
it through a private copy of the item loop (`runTodoTaskMessageSequence`).

**A scripted fast path nothing used.** A `todo_task` could declare
`declared_execution_mode="scripted"` and run `learnings/{step-id}/main.py`
instead of the LLM orchestrator. Its eligibility rule — a fixed set of route
calls branching only on success/failure — is exactly the case PLAT-068 says
must not be an orchestrator. A scan of every `Workflow/*/planning/plan.json`
found 12 live `todo_task` steps; none declared scripted mode and none had a
`main.py`. The path also lacked the regular scripted step's repair loop and
save-back, and a failure fell back to a fresh LLM run that re-ran every child.

**A dead todo list.** `todo_tools.go` (`create_todo`, `update_todo`,
`complete_todo`, `list_todos`) was never registered anywhere; the three
`todo_task_item_*` events were never emitted. Yet `call_sub_agent` and
`call_generic_agent` required a `todo_id` described as an "existing durable
todo ID", and PLAT-322's merged identifier-boundary text justified it as a link to
that todo item.

## What changed

1. **Scripted orchestrator path removed.** `executeTodoTaskStep` (now `executeOrchestratorStep`) no longer
   consults `declared_execution_mode`; `update_step_config` rejects `scripted`
   on a `todo_task` with a message pointing at a regular scripted step that
   calls routes. `controller_scripted.go` is untouched and still serves regular
   scripted steps. The guidance sections describing the mode are deleted; the
   PLAT-068 render test now asserts they are gone.
2. **Dead todo list deleted; `todo_id` → `task_id`.** The tools, the three
   event types, their event-store allowlist entries, and the frontend render
   paths that could never fire are removed. The sub-agent tool argument is now
   `task_id`, a plain label for tracking and artifact naming. The
   route-selected and step-completed events keep their field names for wire
   compatibility. `todo_id_to_execute` on the orchestrator decision record is
   a different field and is untouched.
3. **Orchestrator runs on the message_sequence executor.**
   `messageSequenceCallOptions.Delegation` is a new seam holding the
   `SubAgentExecutionContext`, the narrower orchestrator folder guard, an agent
   factory (the orchestrator agent type, so PLAT-027's progress bridge keeps
   working), the orchestrator's template variables, and the `todo_task`
   execution-log writer (so `ExecutionLogsPopup`'s route-selection view and
   `stepLogs.todo_task` keep rendering). The executor:
   - builds the agent once and reuses it for every item;
   - after every conversational turn, waits for the children that turn
     launched and feeds one completion batch back (`reconcileAsyncSubAgentCalls`)
     before the next item — on the error path too;
   - refuses non-conversational items for an orchestrator even if plan
     validation was bypassed;
   - guarantees `cancelOutstandingAndWait` on every exit path;
   - skips the global learnings mutex on the reflection turn, because a parent
     that waits for children inside the turn would deadlock against a child's
     own learnings turn (the orchestrator never held that lock before either).
   `executeTodoTaskStep` (renamed `executeOrchestratorStep` in the follow-up identifier rename; file `controller_orchestrator.go`) is now a ~150-line wrapper. Deleted:
   `runTodoTaskMessageSequence`, `runTodoTaskPreValidation`,
   `runTodoTaskContributionTurns`, `persistCompletedTodoTaskSummary`,
   `executeTodoTaskOrchestratorAgent`, and the restart-the-step retry loop.
4. **Retry semantics changed deliberately.** The orchestrator used to restart
   the whole step up to three times with validation feedback, and an exhausted
   retry returned success=false without an error, which the caller then marked
   completed. It now uses the sequence model: per-item `prevalidation` with
   in-place repair turns, a synthetic final validation gate with the same
   repair loop, and a hard error when the gate is exhausted. Repairs keep the
   conversation and its sub-agent results instead of re-running every child.

5. **Plan type renamed to `orchestrator`** (follow-up phases in the same
   series). `StepTypeOrchestrator = "orchestrator"`; every parse site accepts
   `todo_task` (`StepTypeTodoTaskLegacy`, `IsOrchestratorStepType`) and
   normalizes it, `MarshalJSON` writes the new name. Planning tools are
   `add_orchestrator_step`, `update_orchestrator_step`,
   `add/update/delete_orchestrator_route`; the five `*_todo_task_*` names stay
   registered as deprecated aliases for one contract version. Drift review
   requires `orchestrator_best_practices`; records carrying
   `todo_task_best_practices` still satisfy it. Guidance `todo-task.md` is now
   `orchestrator.md` (registry key `orchestrator`). Contract **v1.0.35** adds
   `migrate_orchestrator_step_type`, a raw-text rewrite of the `"type"`
   discriminators only (key order and unknown fields survive), validated and
   written through the managed planning writer with a changelog entry. Go
   identifiers and files were renamed (`controller_orchestrator.go`,
   `orchestrator_agent.go`, `OrchestratorPlanStep`, `OrchestratorDecision`).
   Event wire names, execution kinds, cost phases and log file names are
   deliberately unchanged.

## Not changed (by decision)

- The event wire names (`todo_task_route_selected`, `todo_task_step_completed`,
  `todo_task_orchestrator`), execution kind `todo_task`, cost phase, and the
  `execution-attempt-N-iteration-M` log files. 211 historical run files and
  the frontend event consumers depend on them; a rename there buys nothing.
- The frontend compound-node graph (`usePlanToFlow.ts`) and the
  `ExecutionLogsPopup` route-selection view: the log shape they read is kept
  by the delegation seam's log writer. Only the plan-type guards widened.
- The PLAT-068 eligibility invariant. Reworded from "use todo_task only when"
  to the same rule about giving a step routes; the render test still asserts
  it in all four guides.

## Verification

- `go build ./...`, `go vet`, `go test` for `step_based_workflow`,
  `cmd/server`, `cmd/server/virtual-tools`, `cmd/server/guidance`,
  `internal/terminals`, `internal/events`: green. Frontend `tsc -b && vite
  build`: green.
- Full suites green after every phase: `step_based_workflow`, `cmd/server`,
  `cmd/server/guidance`, `cmd/server/virtual-tools`, `internal/terminals`,
  `internal/events`, `cmd/server/services`; frontend `tsc -b && vite build`.
- New deterministic tests: `orchestrator_step_type_migration_test.go`
  (rewrite only discriminators, idempotent, legacy parse normalizes); the
  upgrade-ladder tests gained the v1.0.35 rung; the PLAT-068 render test
  asserts the new invariant wording.
- **Live e2e run 2026-09-02 11:09–11:13 (local backend, Workshop chat, session
  `query_1788327581474529000`, `testing` / `execution-regression-router`,
  run folder `iteration-0/default`):**
  - Step completed on the sequence executor: `execution/execution-regression-router/session.json`
    shows the opening turn and the synthetic `__automatic_final_validation__`
    gate both `completed`; the orchestrator's own summary ends `STATUS: COMPLETED`.
  - Delegation: all four routes ran (`math-solver`, `text-processor`,
    `browser-probe`, `nested-manager`); the nested orchestrator ran its two
    routes (`calc-task`, `word-task`) and has its own completed `session.json`.
    The orchestrator conversation log holds 55 messages with **2 completion
    batches**, i.e. two async waves reconciled through the seam.
  - Log shape kept for the UI: `logs/execution-regression-router/execution/execution-attempt-1-iteration-0{,-conversation,-timing}.json`,
    `todo-task-prompts.json`, and `execution-final-summary.json` all present.
  - Events: the builder session received `todo_task_route_selected` ×6 and
    `todo_task_step_completed` ×2 (top-level + nested), `pre_validation_completed` ×7.
  - Folder guard: the routes' deliberate forbidden reads were blocked (the
    `[TOOL_ERROR]` lines 11:10:01–11:10:32 are those probes); the step's
    summary reports zero unexpected allows.
  - Tool surface: `call_sub_agent` was registered only in the two orchestrator
    sessions (`sub-todo-execution-regression-router-…`, `sub-todo-nested-manager-…`);
    the plain route sessions (`msgseq-…-math-solver…`, 521 log lines) received none.
  - Scripted-mode rejection (session `query_1788330178737018000`, 11:53):
    `update_step_config(declared_execution_mode="scripted")` on the
    orchestrator returned the new error ("… not supported on orchestrator
    (todo_task) step … belongs in a regular scripted step whose main.py calls
    the routes") and `step_config.json` stayed `agentic`.
  - Not exercised live: an orchestrator with a non-empty `messages` list (no
    live workflow has one; covered by the executor's item loop, which the
    standalone message_sequence path already exercises), and the v1.0.35
    migration on a real plan (unit-tested; runs at the next scheduled preflight).
