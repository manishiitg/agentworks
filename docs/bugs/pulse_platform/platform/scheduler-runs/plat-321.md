[← platform / scheduler-runs](index.md)

# PLAT-321 — Add a simple explicit human-approved parallel-schedule opt-in

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | platform |
| Area | scheduler-runs |
| Summary | P1 throughput after PLAT-320. |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented on main; authenticated approval provenance, complete UI confirmation, and live acceptance remain open` |
| Last synchronized | `2026-09-14` |

- **Priority:** P1 throughput after PLAT-320.
- **Depends on:** [PLAT-320](plat-320.md).
- **Owner:** schedule schema/tools/UI, human approval, scheduler admission,
  collision/dependency behavior, generated guidance and audit history.

## Decision

Keep the concurrency contract simple:

1. Workflow-producing schedules are sequential by default.
2. Do not build an agent-authored resource-claim system. A declared file or
   resource list cannot prove safety because actual writes and side effects can
   be dynamic or omitted.
3. A schedule may opt into parallel execution only through an explicit platform
   field and only after a human approves a fixed risk disclosure.
4. The opt-in belongs to the independent schedule. An approved parallel
   schedule may overlap the sequential schedule lane; the other schedules do
   not need to become globally parallel merely to coexist with it.
5. `after_schedule_ids` always wins: a dependent occurrence waits even when
   both schedules allow parallel execution.
6. Two occurrences of the same schedule do not overlap. Collision policy still
   queues, coalesces, retries or skips the later occurrence.

PLAT-320's immutable `iteration-N-sched` folders prevent two scheduled runs
from sharing their run-output tree. They do **not** isolate the rest of the
workflow. Before approval, the agent and UI must state plainly that parallel
runs can concurrently mutate the workflow database, knowledge base, learnings,
reports/files, planning state and browser/CDP state, and can duplicate or race
external actions. The opt-in accepts that risk; it does not describe parallel
execution as safe.

## Proposed schedule contract

Use one explicit mode rather than resource declarations:

```json
{
  "concurrency_mode": "sequential"
}
```

Allowed values are `sequential` (default) and `parallel`. Enabling `parallel`
must persist authenticated approval provenance and the risk-disclosure version;
the backend supplies identity/timestamps rather than trusting agent-authored
audit fields. Create/update tools must reject `parallel` when the approval step
has not completed.

The exact approval transport may reuse the platform's existing human-input
lifecycle or a UI confirmation, but it must produce one durable approval record
that both paths consume. Do not create separate, inconsistent chat and UI
approval rules.

## Implementation status (2026-09-14)

Implemented, verified, and pushed to `main` in `70660472b`:

- `concurrency_mode` (`sequential` default / `parallel`) and
  `parallel_risk_acknowledged` in manifests, REST responses, schedule history,
  frontend types and create/update tool schemas;
- validation that rejects parallel without acknowledgement and excludes webhook
  and Pulse-only schedules;
- a schedule-specific durable parallel lane, same-schedule exclusion, coexistence
  with the sequential lane in either start order, and dependency checks before
  admission; and
- fixed shared-state/external-action risk language in AgentWorks, schedule,
  Run, Pulse Gate, Architecture, Technical Review and Fixer guidance.

Remaining before closure:

- authenticated approval actor/timestamp/disclosure-version provenance shared
  by chat and UI, rather than the current persisted acknowledgement boolean;
- a complete UI create/edit confirmation flow; and
- end-to-end pairwise/restart/stop/live Social Media acceptance evidence.

## Scheduler admission

When schedule B fires while schedule A owns the workflow:

- if B depends on A, B waits;
- if B has approved `parallel`, B may start outside the sequential lane;
- if B is sequential and A is parallel, B may acquire the sequential lane and
  coexist with A; and
- if A and B are both sequential, B follows its existing `collision_policy`
  against the workflow-wide busy state.

This makes a genuinely independent schedule, such as an approved measurement
schedule, parallel without forcing Daily Normal, Growth and other producing
schedules to overlap one another. Human approval accepts the disclosed risk of
that independent schedule interacting with whichever sequential run is active.

Manual **Run now** uses the saved schedule's mode and approval. Pulse-only and
maintenance occurrences remain non-producing evidence reviewers under their
existing rules. Interactive Builder/manual workflow execution remains exclusive
with producing schedules unless a separate policy explicitly changes it.

## Agent and human awareness

Generated AgentWorks system instructions, the Builder schedule reference,
Pulse Gate, Architecture Review, Technical Review and Fixer must all agree:

- sequential is the default;
- dependencies mean waiting, not overlap;
- resource claims are not used;
- the agent must show the fixed risk disclosure before requesting parallel;
- only explicit human approval can enable the persisted opt-in; and
- separate `-sched` folders do not prevent shared-state overwrites or duplicate
  external actions.

Guidance must not advertise the parallel field until the runtime schema and
approval path are implemented. Before then, agents preserve the existing lock.

## Acceptance criteria

- Existing and newly created schedules default to `sequential` without a
  migration prompt.
- An agent cannot enable `parallel` without a durable human approval record.
- The UI shows the same warning and writes the same approval record as chat.
- Sequential schedules remain serialized with one another, while an approved
  parallel schedule may coexist with the sequential lane in either start order.
- Approved parallel schedules can overlap only after PLAT-320 gives every
  producing occurrence a different immutable `-sched` identity.
- `after_schedule_ids` blocks overlap even when both schedules are parallel.
- The same schedule cannot overlap itself.
- Removing approval or changing back to sequential immediately prevents new
  overlap without interrupting runs already in progress.
- Schedule history records concurrency mode and approval provenance used at
  admission time.
- Generated prompt/skill tests assert the fixed risk language and never claim
  that resource declarations make execution safe.
- End-to-end tests cover every pairwise mode combination, dependency override,
  Run now, collision policies, restart, stop and simultaneous fire admission.
