[← goals / step-execution](index.md)

# PLAT-027 — an async todo-task turn falsely completes its parent

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P0 |
| Product | goals |
| Area | step-execution |
| Summary | workflow progress completion gating and terminal execution-tree projection |

| Coordination | Value |
|---|---|
| Assigned agent | `Codex` |
| Ticket state | `implemented; runtime parent-gate verified` |
| Last synchronized | `2026-08-04` |

- **Priority:** P0
- **Owner:** workflow progress completion gating and terminal execution-tree projection
- **Evidence:** in the Social Media run, the route/orchestrator turn reported
  completion after dispatching `execute-allocate`, while the asynchronous child
  remained live. The execution tree knew about the child, but the terminal rail
  showed no active agent, making a live workflow appear stopped.
- **Root cause (two layers):** the rail was built only from retained terminal
  snapshots, so a child could register in the execution tree before its
  terminal/transcript snapshot existed. More importantly, the workshop progress
  bridge treated every successful `orchestrator_agent_end` as completion of a
  `todo_task` workflow step. For an asynchronous orchestrator that event is only
  the end of one LLM turn: the controller is still waiting for the child and
  will continue the same conversation after reconciliation. This produced the
  contradictory state `state=completed, process_state=live`, sent a false
  `[AUTO-NOTIFICATION]`, and made the parent terminal appear settled while
  `execute-allocate` was still running.
- **Implementation:** `TerminalCenter` polls the session execution tree
  while the surrounding session expects activity and projects every visible
  live child into the rail. A projected row is an ephemeral running placeholder
  identified by the real execution ID. When the terminal snapshot arrives it
  enriches/replaces that identity rather than creating a second tab. Session
  roots, main agents, synthetic turns, and completed historical children are
  not projected. The workflow progress bridge now keeps a successful
  `todo_task_orchestrator` turn open and completes it only when the controller's
  existing `todo_task_step_completed` event arrives, after all owned children
  have settled and their results have been reconciled. Failed turn endings keep
  their existing failure path. A projected child is also treated as a loading
  navigation entry: the UI neither requests its unpublished `/events` endpoint
  nor lets the generic synthetic-transcript branch override its waiting view.
  This removes the misleading `Conversation could not be loaded (404)` screen
  while the real child terminal is still being published.
- **Primary files:** `frontend/src/utils/terminalExecutionProjection.ts`,
  `frontend/src/components/TerminalCenter.tsx`,
  `frontend/src/hooks/useSessionExecutionTree.ts`, and
  `frontend/src/services/api-types.ts`; backend completion gating is in
  `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/planning_exports.go`.
- **Verification:** frontend unit tests cover (1) a running asynchronous child
  with no terminal yet, (2) reconciliation with a later real terminal without a
  duplicate, and (3) suppression of completed historical children. Frontend
  typecheck, lint, and the full test suite passed during the first
  implementation. The backend regression sends a successful
  `orchestrator_agent_end` representing an asynchronous dispatch, proves no
  completion is emitted and the registry remains running, then sends
  `todo_task_step_completed` and proves exactly one completion reuses the
  original execution identity. The 2026-08-04 Social Media rerun supplied the
  runtime evidence for the completion gate: the dispatch turn ended without a
  false completion notification or workflow-progress completion while
  `execute-allocate` remained live. A new frontend regression proves an
  execution-tree placeholder cannot request terminal events; the focused tests
  and production build pass.
- **Acceptance:** when a parent turn ends after dispatch, its still-running
  child remains visible and the parent step remains running until the child
  settles and the controller reconciles its result. No workshop
  `[AUTO-NOTIFICATION]` may say that the step completed merely because the
  orchestrator ended its dispatch turn. The parent gate passed a real Social
  Media runtime recheck; the rebuilt placeholder UI still needs visual runtime
  confirmation.
