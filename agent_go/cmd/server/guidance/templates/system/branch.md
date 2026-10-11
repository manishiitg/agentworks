**Plan-editing tool arguments:** Before a plan mutation, read `builder-reference/references/plan-editing-tools.md`. Step fields described below belong inside `add_step.step` or `update_step.changes`; route/group/maintenance fields belong inside `parameters`. Use the live type/action-specific schema; these field descriptions do not authorize flat arguments or extra fields.

## BRANCH STEP DESIGN

A branch step is a deterministic switch — a small in-flow next-step
decision. It reads `route_selection.json`, resolves the selected value to
one of its `routes[]`, and jumps to that route's `next_step_id`. File-based
selection follows routing (`references/routing.md`); branches additionally
support a human decision via `route_source: "human"`. **Routing is now the "route" concept: a major,
self-contained sub-workflow fork.** Use branch instead when the decision is
small — a lightweight fork inside an otherwise normal plan, not a whole
alternate path through the workflow. See PLAT-259.

Use branch when the workflow must run **exactly one** of a few existing
downstream steps, and the fork itself is minor — not a candidate for its
own eval breakdown or a top-level tab in reporting. If the decision is
actually a major sub-workflow fork (each option leads to a substantially
different, self-contained continuation of the plan), use a routing step
instead.

A plan has at most one routing step — the mode selector whose route a
schedule or caller picks via `route_selections`, each route a sub-workflow of
many steps. Once a plan has it, every further fixed choice is a branch by
definition. A simple if-condition — skip or continue, probe ok or failed,
publish or hold, any option that goes straight to `end` — is always a branch;
`add_step` refuses a second routing step and any route to `end`
(PLAT-294).

### Human-decided branch (`route_source: "human"`)

When the decision is a person's — approve or hold, publish or redraft, pick
one of a few options — set `route_source: "human"` on the branch. The
`routes[]` are the options shown to the person (`route_name` is the label)
and `branch_question` is the prompt. This is the successor to a `yesno` /
`multiple_choice` `human_input` step: same blocking prompt, same UI, but the
answer is a route, so there is no second copy of routing fields to keep in
sync. Resolution order is unchanged for everything a schedule or caller can
see:

1. caller `route_selections[step_id]`, a preseeded `route_selection.json`,
   `route_source_file`, or a `context_dependencies` entry — used as-is, no
   prompt (this is how a schedule answers the question up front);
2. workshop `execute_step(step_id, human_input=...)` — test answer;
3. unattended run (`start_from_beginning_no_human`) — `default_route_id`, or
   the run fails with an actionable error if none is set. Set
   `default_route_id` to the safe do-nothing route (typically the one that
   goes to `end`) so scheduled runs never auto-approve;
4. interactive run — the person is asked; the run waits (same 10-minute
   window as `human_input`).

Use `add_step(..., route_source="human")` for any new yes/no or
pick-one decision; `add_step` now rejects `yesno` and
`multiple_choice` and points here. Keep `human_input` (`text`) only for
capturing a free-form value into a variable. Existing `human_input` steps are
untouched.

### When to use branch

- The path forward is conditional on a known signal (e.g., "logged in",
  "MFA required", "document type is invoice")
- A person must decide between a few fixed options mid-run — use
  `route_source: "human"` (above)
- The fork is small: a couple of steps differ, then the plan converges back
  to a shared continuation
- There are 2-N mutually exclusive paths and only one should run
- The selected path can be represented as a stable `route_id` or a unique
  `next_step_id`

### Route selection contract

Identical to routing. The branch step reads this file shape:

```json
{
  "select_route": "route_id_here"
}
```

Compatibility aliases are accepted: `route_id` and `selected_route_id`.

The value may be:

- a `routes[].route_id`
- a unique `routes[].next_step_id`

If a supplied file is invalid, the branch fails. With no supplied answer,
a file-based branch uses `default_route_id` or fails; a human-decided branch
follows the interactive/unattended resolution above. Branch never silently
chooses the first route.

### Single mode

Branch steps never execute agents. Leave `description` and `context_output`
empty. The step reads a caller-preseeded `route_selection.json`, an explicit
`route_source_file`, a `context_dependencies` entry named
`route_selection.json`, or `default_route_id`. Human-decided branches can
also obtain an answer through the prompt flow above; they still run no LLM.

When an agent/probe/classifier must decide the route, model it as:

- prior `agent` step: performs the probe/classification and
  writes `route_selection.json`
- branch step: declares `route_source_file` or
  `context_dependencies: ["route_selection.json"]` and branches from that
  file

### Branch structure

A branch step has:

- `branch_question` — **REQUIRED (non-empty)**: the runtime errors if it is
  missing. It is not evaluated by an LLM; it is kept for plan
  readability and used as the prompt for a human-decided branch.
- `routes[]` — minimum 2 entries (required)
- `default_route_id` — optional fallback `route_id` used when no route file
  exists
- `route_source_file` — optional explicit route file source produced by a
  prior step

Each entry in `routes[]` has:

- `route_id` / `route_name` — stable identifier the route file selects
- `condition` — short prose explaining when this route should be selected
- `next_step_id` — the ID of an existing step in the plan that this route
  jumps to

Branch routes do **not** define inline sub-agents. Every `next_step_id`
must reference a step that already exists in the plan. Add those
downstream steps separately (as scripted, agent, or human_input
steps), then point the routes at their IDs.

### Convergence — options MUST rejoin via `next_step_id`

A branch step jumps execution **into** the selected option, but it does not
stop the engine from continuing in list order afterwards. So each option
must explicitly route **out** to the shared continuation (or `end`), or
execution will fall through into the *next* option in the list and run a
non-selected path. Same rule as routing (see `references/routing.md`'s
"Convergence" section for the full explanation) — give the terminal step of
each option a `next_step_id` pointing to the shared downstream step, or
`"end"` to finish.

### Branch vs. other primitives

- **Branch vs. routing**: routing is now the "route" concept — a major,
  self-contained sub-workflow fork; use it when the alternatives lead to
  substantially different continuations of the plan. Branch is the small
  in-flow decision.
- **Branch vs. agent with specialists**: a agent agent with routes may
  call zero, one, or several specialists adaptively. Branch runs exactly one
  fixed alternative.
- **Branch vs. plain agent**: a plain sequence is one ordered
  conversation; add routes only for adaptive delegation.
- **Branch vs. human_input**: do not ask the user again when the builder
  already knows the requested option. Use `route_selections`. Use
  `human_input` only when the workflow must pause mid-run for information
  that was not available at launch.

### Anti-patterns

- Branch with only one route, or with a generic catch-all route that should
  be normal step logic.
- Asking the branch step to infer the route from prose without writing
  `route_selection.json`.
- Setting `description` on a branch step. Use a prior agent for
  probe/judgment work.
- `next_step_id` pointing to a step that does not exist yet.
- File-based branch with no caller `route_selections`, no `route_source_file`,
  no `route_selection.json` dependency, and no `default_route_id`. A human
  branch may prompt interactively; unattended runs still need an answer or
  safe non-approval default.
- Using branch for what is actually a major sub-workflow fork — use routing
  instead.
