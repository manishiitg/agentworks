[← platform / coding-agent-bridge](index.md)

# PLAT-100 — Workshop orchestrator descendants detached from the initiating message

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P0 |
| Product | coding-agents |
| Area | bridge |
| Summary | P0 — a schedule can advance to its next message while the full |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented` — launch propagation plus live-steered continuation ownership and regression tests pass; live reverify pending |
| Last synchronized | `2026-08-12` |

- **Priority:** P0 — a schedule can advance to its next message while the full
  workflow or another workshop background execution is still running.
- **Owner:** workshop execution launch, background execution registration,
  exact conversation-turn lifecycle.
- **Depends on:** [PLAT-095](../../schedules/runs/plat-095.md)), which made the `/api/query` ID the
  exact lifecycle root but could only wait for descendants that were linked to
  it.

## Actual defect

Workflow `todo_task` orchestrators already implement message sequences
correctly. Each message runs in the same orchestrator conversation, every
asynchronous child is reconciled before the next message, and outstanding
children are canceled and joined on failure.

The defect was outside that orchestrator. A workshop session intentionally owns
a detached `sessionCtx` created from `context.Background()` so a long-running
workflow can outlive the HTTP tool call that launched it. `run_full_workflow`,
`run_full_evaluation`, `execute_step`, and workshop background/review launches
derived their execution context from that detached session context and then
asked it for `ParentExecutionID`. The initiating `/api/query` root was no longer
present, so the first background execution was registered with an empty parent.

PLAT-095's exact waiter therefore saw this invalid tree:

```text
query root (completed)

full workflow (running, no parent)
  └─ orchestrator step
      └─ sequence children
```

The internal orchestrator could still be working correctly while the scheduler
believed the query-rooted tree was finished and sent the next scheduled message.

## Implemented repair

### Preserve identity without preserving request cancellation

Workshop launch paths now copy only the lifecycle parent ID from the live tool
call into the long-lived execution context. Cancellation still derives from the
workshop session, so returning the HTTP request cannot kill the workflow.

Parent resolution recognizes, in precision order:

1. the direct background-agent parent;
2. `ParentExecutionIDKey`.

Correlation IDs are deliberately not treated as semantic parents: workshop
correlation IDs group display events and frequently do not name a tracked
execution. A missing top-level query parent is resolved from the active
conversation-turn record at the server registration boundary.

This applies to full workflow, full evaluation, step execution, generic
background agents, and workshop review agents through the shared launch helper.

### Fail-safe registration at the server boundary

The workshop execution notifier now repairs an empty first-level parent by
looking up the exact currently running conversation turn for the same session.
It writes the resolved parent to both:

- the background-agent registry used for completion notifications; and
- tracked-execution metadata used by the exact lifecycle tree, Global Monitor,
  and execution-tree UI.

This fallback protects older/custom workshop launch paths without relying on
tmux, quiet-time, or session-wide busy inference.

### Close the live-steered retry gap

Live verification on `social-media` exposed one continuation path that the
initial repair did not cover. When a failed workflow's completion notification
was injected directly into a busy coding CLI, the text reached the agent but no
tracked continuation was added beneath the scheduled message. A retry launched
from that injected text could therefore remain outside the exact tree while the
scheduler advanced to Pulse.

A confirmed live-steer now registers a synthetic continuation under the
completed child's original parent **before** releasing the child's notification
hold. The existing retained-terminal stream observer settles that continuation
only when the coding CLI returns to its idle composer. Normal `/api/query` and
separate synthetic turns also install their exact execution ID in the tool-call
context, so a retry or any other nested launch inherits the scheduled-message
tree rather than reconstructing ownership from session activity. A live-steered
message continues an already-running CLI turn, so a retry from it remains a
direct child of the original query root while the continuation independently
keeps that same root open until the CLI is idle.

If that CLI was already being observed as a retained follow-up turn, the
continuation joins the existing idle boundary as an additional lifecycle owner;
it does not replace the original query ID. Both IDs settle together. This
prevents fixing the schedule race by creating a permanently-running original
message.

The resulting tree is:

```text
scheduled message
├─ failed workflow attempt
├─ live completion continuation
└─ retry workflow
```

This remains exact-message lifecycle tracking. It does not wait for unrelated
work merely because it shares the session.

## Regression coverage

- lifecycle identity survives detaching from the HTTP request context;
- direct and explicit semantic execution context keys resolve to the same
  parent ID, while a display correlation ID is not misused as a parent;
- a workshop execution started without an explicit parent attaches to the
  active query root;
- completing the query root while that workshop child is running does not make
  the tree terminal;
- the tree becomes terminal only after the workshop child completes;
- a failed child whose completion is live-steered creates a continuation under
  the same scheduled-message root;
- completing that continuation cannot advance the schedule while its retry is
  still running;
- tool calls in normal and synthetic turns receive the exact owning execution
  ID through both supported lifecycle context keys;
- a live-steered continuation arriving during an already-tracked retained turn
  preserves the original root and settles both lifecycle IDs at the same real
  CLI completion boundary;
- existing workflow-orchestrator message-sequence and asynchronous-child tests
  continue to pass.

## Verification

- Focused `step_based_workflow` execution-parent, todo-task orchestrator,
  asynchronous-child, and background-message-sequence tests pass.
- Focused server exact-tree and workshop-parent lifecycle tests pass.
- `go build ./...` passes.
- The broad server/workflow run has no new PLAT-100 failure. It retains 24
  failures already present in the shared in-flight refactor worktree: one
  scheduler tracking-window test, 20 guidance contract tests, one virtual-tool
  size contract, and two workflow prompt-contract tests.
- Live scheduled workflow re-verification remains pending a server restart with
  this implementation.

## Acceptance

- A full workflow launched by one scheduled message is a descendant of that
  message's query ID.
- Every nested orchestrator/message-sequence child remains recursively under
  that root.
- The scheduler cannot advance until the full descendant tree is terminal and
  required child completion notifications have been processed.
- Long-running workshop work remains independent of HTTP request cancellation.

## 2026-09-17 — Notification delivery ownership integration

The [PLAT-106 notification-ownership follow-up](../../app/ui/plat-106.md#notification-ownership-follow-up--2026-09-17) hardens this ticket's registration and continuation boundary. Background-agent registration binds a missing legacy session owner once and rejects re-registration into a different session. Completion delivery also rejects a known tracked parent whose session differs from the destination.

Keep copying the original parent identity at launch and through child steps. The new checks do not reconstruct missing historical parents; legacy entries without a tracked parent still rely on their registered session. Chat-owned results remain deliverable after a later human turn starts in that chat. Existing live-steered continuation/tree tests and the new ownership tests passed locally. This is not new live evidence for the original scheduling race.

Follow-up status: **implemented and tested locally; not deployed; runtime re-verification pending**. PLAT-106 remains the canonical implementation/test record. This note does not close the original ticket or change its assigned agent.
