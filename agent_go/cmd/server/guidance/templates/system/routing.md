**Plan-editing tool arguments:** Before a plan mutation, read `builder-reference/references/plan-editing-tools.md`. Step fields described below belong inside `add_step.step` or `update_step.changes`; route/group/maintenance fields belong inside `parameters`. Use the live type/action-specific schema; these field descriptions do not authorize flat arguments or extra fields.

## ROUTING STEP DESIGN

**Routing is now the "route" concept: a major, self-contained sub-workflow
fork** — not a small in-flow decision. For a lightweight next-step choice,
use a **`branch` step** instead (`references/branch.md`); it shares the file-based
selection mechanics below and additionally supports `route_source="human"`, so guidance,
reporting, and eval tooling can tell a major fork apart from a small
decision. Everything else on this page still applies unchanged to routing
steps. See PLAT-259.

A routing step is a deterministic switch. It reads `route_selection.json`, resolves the selected value to one of its `routes[]`, and branches to that route's `next_step_id`.

Use routing when the workflow must run **exactly one** of N major downstream sub-workflows. The common case is a fixed branch selected from the user's request to the builder; the builder/caller passes that choice as `route_selections` when starting the workflow. Do not put judgment inside the routing step itself; put judgment in an earlier agent or caller-provided `route_selections`. If an agent decision is needed, add a agent before routing that writes `route_selection.json` in its own output folder.

**A plan has at most one routing step.** Routing is the workflow's mode
selector: the single fork whose route a schedule or caller picks via
`route_selections` (e.g. `full_audit` vs `apply_approved_fixes`, `post` vs
`engage` vs `measure`). Each route is a **sub-workflow of the main work** —
many steps with their own downstream identity — so every route must start at
a real step; a route straight to `end` is rejected, because a "mode" that does
nothing is a simple if-condition. Every further fixed choice inside the flow —
a skip/continue gate, an approval outcome, a "did the probe succeed" check —
is a `branch`, however late in the plan it sits. `add_step` rejects a
second routing step or a route to `end`, and `change_step_type`
rejects a conversion to routing in the same cases; plans authored before this
rule keep the routing steps they already have (PLAT-294).

### When to use routing

- The alternatives are major workflow modes (e.g., acquire customers, process approved work, or measure outcomes); small signal-driven gates such as login/MFA use branch
- The user already told the builder which fixed workflow mode/job/branch to run
- There are 2-N mutually exclusive paths and only one should run
- The selected path can be represented as a stable `route_id` or a unique `next_step_id`

### Route selection contract

The router reads this file shape:

```json
{
  "select_route": "route_id_here"
}
```

Compatibility aliases are accepted: `route_id` and `selected_route_id`.

The value may be:

- a `routes[].route_id`
- a unique `routes[].next_step_id`

If the file exists but is invalid, routing fails. If no file exists, `default_route_id` is used when set; otherwise routing fails. Routing never silently chooses the first route.

### Single mode

Routing steps never execute agents. Leave `description` and `context_output` empty. The step reads a caller-preseeded `route_selection.json`, an explicit `route_source_file`, a `context_dependencies` entry named `route_selection.json`, or `default_route_id`.

When an agent/probe/classifier must decide the route, model it as:

- prior `agent` step: performs the probe/classification and writes `route_selection.json`
- routing step: declares `route_source_file` or `context_dependencies: ["route_selection.json"]` and branches from that file

### Route structure

A routing step has:

- `routing_question` — **REQUIRED (non-empty)**: the runtime errors if it is missing. It is no longer evaluated by an LLM; it is kept for plan readability/compatibility, but you must still set it.
- `routes[]` — minimum 2 entries (required)
- `default_route_id` — optional fallback `route_id` used when no route file exists
- `route_source_file` — optional explicit route file source produced by a prior step

Each entry in `routes[]` has:

- `route_id` / `route_name` — stable identifier the route file selects
- `condition` — short prose explaining when this route should be selected
- `next_step_id` — the ID of an existing step in the plan that this route branches to

Routing routes do **not** define inline sub-agents. Unlike a agent
agent's `predefined_routes` (which may embed a `sub_agent_step`), routing
`routes[]` are pointers—every `next_step_id` must reference an existing plan
step. Add downstream scripted, agent, or human-input steps separately,
then point the routes at their IDs.

### Convergence — branches MUST rejoin via `next_step_id`

A routing step jumps execution **into** the selected branch, but it does not stop the engine from continuing in list order afterwards. So each branch must explicitly route **out** to the shared continuation (or `end`), or execution will fall through into the *next* branch in the list and run a non-selected path.

- Give the **terminal step of each branch** a `next_step_id` pointing to the shared downstream step (e.g. each portal branch → `normalize`), or `"end"` to finish.
- The engine honors `next_step_id` on `routing`, `human_input`, and
  `agent` steps (including legacy orchestrator compatibility
  records): the selected branch runs, jumps to the shared step, and sibling
  branches are skipped.
- A branch whose terminal step has **no** `next_step_id` falls through to the next step in the list — the classic "the non-selected branch also ran" bug. Always wire the convergence.

If an agent must decide whether or which specialists to call from live evidence,
use an `agent` with `predefined_routes`. Use routing when an already
made decision selects exactly one genuinely different downstream path.

### Routing vs. other primitives

- **Routing vs. agent with specialists**: a routed agent may call
  zero, one, or several specialists adaptively. Routing runs exactly one fixed
  downstream alternative.
- **Routing vs. plain agent**: a plain sequence is one ordered conversation; add `predefined_routes` only for adaptive delegation.
- **Routing vs. human_input**: do not ask the user again when the builder already knows the requested branch. Use `route_selections`. Use `human_input` only when the workflow must pause mid-run for information that was not available at launch.

### Example

After a login probe, write:

```json
{
  "select_route": "mfa_required"
}
```

Then route among:

- `success` -> `next_step_id: "step-extract-data"`
- `mfa_required` -> `next_step_id: "step-mfa-prompt"` (a human_input step that asks for the code)
- `failed` -> `next_step_id: "step-retry-or-abort"`

Each `next_step_id` must already exist as a step in the plan.

### Anti-patterns

- Routing with only one route, or with a generic catch-all route that should be normal step logic.
- Asking the routing step to infer the route from prose without writing `route_selection.json`.
- Setting `description` on a routing step. Use a prior agent for probe/judgment work.
- `next_step_id` pointing to a step that does not exist yet.
- Routing with no caller `route_selections`, no `route_source_file`, no `route_selection.json` dependency, and no `default_route_id`.
