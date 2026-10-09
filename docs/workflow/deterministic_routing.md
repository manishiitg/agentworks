# Deterministic Routing (route-by-file)

**Status:** Runtime deterministic-only; active workflow-plan migrations applied; caller/schedule migrations still pending
**Owners:** workflow / orchestrator
**Last updated:** 2026-06-06

## Summary

Workflow **routing steps are deterministic**: the router reads a JSON file that names the
route, and a few lines of Go pick the matching route and jump. **No LLM evaluation
inside routing.** Any judgment needed to choose a route is done *upstream* (by an
agent step or by the caller) and recorded as data in the route file.

This replaces the previous behavior, where the routing step called an LLM ("conditional
agent") to evaluate a `routing_question` and select a route — even in cases where the
decision is already fixed by a variable.

## Motivation

An audit of all workflow plans found **12 routing steps across 5 workflows**
(`upwork`, `linkedin`, `social-media`, `security-review`, `HRMS`). Of those:

| Category | Count | Notes |
|----------|------:|-------|
| Deterministic dispatch (switch on a variable/file) | 5 | LLM call is pure overhead |
| Terminal/chaining routers | 5 | phase-end switches; 2 are pure all-routes-to-`end` no-ops |
| Genuine LLM judgment | 2 | and even these read a file/JSON signal |

So **10 of 12 routers are not doing real LLM branching** — they're switches,
phase-chain dispatchers, or no-ops. Several already used a legacy pattern where
the routing step `description` told an agent to write `route_selection.json`,
after which the routing LLM was asked to *copy* that value into its answer. That
put the LLM in the loop for no real branching decision and forced a
`default_route_id` fallback because the copy could fumble.

The two "genuine judgment" cases collapse too: the upstream step that produced the
signal (e.g. `connection_test.json`, or a findings analysis) is the natural place to
emit the route decision as data.

**Conclusion:** routing should be a deterministic switch; judgment belongs in the
step that produces the signal.

## Design

### The routing step contract

A routing step reads one JSON route file and switches on it:

```
route file (canonical):   { "select_route": "<route_id>" }

routing step logic (pure Go, no LLM):
  1. read route_selection.json from the first available source:
     a. routing step's output dir (runner/caller preseed)
     b. declared prior-step route source
  2. find the route in routes[] whose route_id == select_route
  3. jump to that route's next_step_id
  4. if no source file exists → use default_route_id
  5. if a source file exists but value matches no route → hard error
  6. if there is no usable default → hard error (surfaced to caller)
```

After resolving the route, every routing and branch step persists its selected
route in its own run-scoped execution folder. This is execution evidence for
that iteration even when the input came from a deliberately shared
`route_source_file`. Dynamic decisions produced by an earlier step must remain
run-scoped and be connected with `context_dependencies`; do not mirror them
through `db/assets`.

The step keeps its existing shape (`routes[]`, `default_route_id`,
`next_step_id` per route). What is removed is the LLM evaluation
(`routing_question` is no longer evaluated by a model). A routing step's
`description` must be empty; any agent/probe/judgment work belongs in a prior
regular step that writes `route_selection.json`.

### Where the route file comes from (producers)

The route file is a **data sink**. Callers can preseed the router's own output
folder, while prior steps write to their own output folders and expose that file as
a declared route source.

| Producer | When | Who decides |
|----------|------|-------------|
| **`run_workflow` / `run_step` param** | Caller already knows the flow | the caller; orchestrator writes the router's own route file |
| **A prior agent step** | The route needs judgment | the prior step writes a route file in its own output folder |
| **Builder / plan default** | A fixed default baked into the plan | the plan author |
| _(none)_ → `default_route_id` | No source file exists | fallback |

### Precedence

```
1. run_workflow / run_step param          (caller's explicit choice — wins)
2. routing step's own route_selection.json        (caller-preseeded file)
3. declared prior-step route source               (agent judgment)
4. default_route_id                        (only when no route file exists)
→ if an explicit value is present but invalid → error (no silent default)
```

### `run_workflow` / `run_step` / `run_full_workflow` parameter

`run_workflow`, `run_step`, and the workshop builder's `run_full_workflow`
tool have an **optional** `route_selections` parameter. The runner carries that
map into execution options; the workflow controller pre-seeds route files after
run-folder/group resolution and cleanup, before the first step executes.

```jsonc
run_workflow(
  workflow_path   = "Workflow/upwork",
  group_name      = "default",
  route_selections = { "route-by-mode": "search" }   // new, optional
)
```

- Shape: `route_selections : { <routing_step_id> : <route_id | next_step_id> }`
  — a map, so workflows with multiple routers (and chaining) are supported.

### Value resolution (route_id vs destination step_id)

The value may name **either** the abstract `route_id` **or** the route's
`next_step_id` (the destination step). The runner resolves and validates:

```
value matches a route_id in that step's routes[]              → use it
else value matches exactly one next_step_id in that step's routes[] → use that route
else value matches multiple next_step_id entries              → error (ambiguous)
else → error (not a valid route for this router)
```

- **Primary form is `route_id`** (stable semantic label; survives step renames).
- `next_step_id` is accepted as an alias only when it maps to exactly one route.
- Either way the value is validated to be **one of that router's declared routes** —
  so this is a real branch, never an arbitrary `goto`.
- On disk we always normalize to the canonical `route_id`:
  `{ "select_route": "<route_id>" }`.

## Runtime changes implemented

| Layer | Change |
|-------|--------|
| `controller_routing.go` (`executeRoutingStep`) | Replaced the `conditionalAgent.EvaluateRouting(...)` call path with deterministic file/default resolution. The branch selection is now "read file → validate → switch"; routing descriptions are rejected. |
| `controller_routing_deterministic.go` | Added the deterministic resolver, route value normalization, prior-step source lookup, and `route_selections` pre-seeding. |
| `workflow_run_tools.go` (`run_workflow`, `run_step`) | Added optional `route_selections` to the tool schemas and parser. No scalar `route` sugar was added. |
| `planning_exports.go` (`run_full_workflow`) | Added optional `route_selections` so the workflow builder can select fixed branches from the user's chat request without adding a redundant `human_input` step. |
| `runWorkflowInternal` / server request path | Parses `route_selections` and carries it through `execution_options`; file writing happens later because final group run folders are resolved during execution. |
| execution controller / batch setup | Validates `route_selections` against each router's `routes[]`, then writes `route_selection.json` into each routing step's output dir after cleanup and before step execution. |
| routing source resolution | Checks the router's own preseeded route file first, then `route_source_file`, then `context_dependencies` entries named `route_selection.json`, preserving folder ownership. |
| `routing.md` guidance | Rewritten: routing is a deterministic switch; judgment goes in an upstream step or the caller; document the file contract and `route_selections`. |
| active workflow plans | Migrated active `upwork`, `social-media`, `linkedin`, `HRMS`, and `security-review` plans away from routing-step LLM classification. Variable-mode routers now expect caller `route_selections`; judgment routers consume producer-owned `route_selection.json`. |

The old conditional-agent helper has been removed. Runtime workflow routing resolves
only deterministic route files, explicit caller selections, or declared defaults.

The loop-guard in `controller_execution.go` (a route may be selected at most twice
before an "infinite loop" error) is unaffected and still bounds backward-jump loops.

## Migration

### upwork

Active `upwork` plan migration is applied.

1. Drop the routing **LLM** (deterministic read replaces it).
2. `route-by-mode`: the `FLOW_MODE` variable and legacy routing description that copied it
   into the file are replaced by the `run_workflow` `route_selections` param (the runner
   writes the file). Remove `FLOW_MODE` from `variables/variables.json` groups.
3. Repoint upwork's schedules from "group with `FLOW_MODE=search`" to
   `route_selections = { "route-by-mode": "search" }`.
4. Verify: `route_selections={"route-by-mode":"search"}` runs the search block;
   omitting the param falls back to `default_route_id` (`profile`).

### Other plans

- `social-media`: browser preflight now writes `route_selection.json`; run-mode
  routing expects caller `route_selections`.
- `linkedin`, `HRMS`: variable-driven dispatchers now expect caller
  `route_selections`.
- `security-review/review-and-expand`: the upstream analysis step writes
  `{ "select_route": "..." }` in its own output folder, and the routing step reads
  that file through an explicit `route_selection.json` dependency.
- Pure all-routes-to-`end` terminators: out of scope here, but they should be
  collapsed to a plain end/regular step rather than a routing step.

### Migrating another laptop / workspace

Workflow plan files under `workspace-docs/Workflow/.../planning/plan.json` may be
local workspace state, not git-tracked source. Another laptop must have **both**
the new runtime code and migrated local plan JSON.

1. **Update the runtime code first.** Pull/deploy the code that includes
   deterministic routing and `route_selections`. Old runtime code will ignore the
   new contract and still try to LLM-evaluate routing.
2. **Find active routing steps.**

   ```bash
   find workspace-docs/Workflow -path '*/planning/plan.json' -print \
     | xargs jq -r '
       input_filename as $file
       | .steps[]?
       | select(.type == "routing")
       | [$file, .id, .title, (.description // ""), (.default_route_id // "")]
       | @tsv'
   ```

3. **Migrate variable routers to caller `route_selections`.** Remove routing-step
   descriptions that read variables like `FLOW_MODE`, `RUN_MODE`, or
   `WORKFLOW_MODE`. Keep the router deterministic (`description: ""`) and pass the choice
   when starting the workflow:

   ```jsonc
   {
     "route_selections": {
       "route-by-mode": "search",
       "step-run-mode-router": "propose_new",
       "step-workflow-router": "route-post",
       "workflow-mode-router": "route-monthly"
     }
   }
   ```

   Use only the routing steps present in that workflow run. Values may be either a
   route's `route_id` or a unique `next_step_id`; `route_id` is preferred.

4. **Migrate judgment routers to producer-owned files.** The prior step that makes
   the decision must be a normal step before the routing step. It writes
   `route_selection.json` in its own output folder and declares it in
   `context_output`:

   ```json
   {
     "context_output": "analysis.json, route_selection.json"
   }
   ```

   The routing step then consumes only that route file:

   ```json
   {
     "type": "routing",
     "context_dependencies": ["route_selection.json"]
   }
   ```

   The file body must be:

   ```json
   { "select_route": "<route_id>" }
   ```

5. **Update schedules/callers.** Any cron, script, or saved run config that used
   variables for route choice must pass `route_selections` instead:

   | Old variable | New route selection |
   |--------------|---------------------|
   | `FLOW_MODE=search` | `{ "route-by-mode": "search" }` |
   | `RUN_MODE=propose_new` | `{ "step-run-mode-router": "propose_new" }` |
   | `VAR_RUN_MODE=engage` | `{ "step-workflow-router": "route-engage" }` |
   | `WORKFLOW_MODE=monthly` | `{ "workflow-mode-router": "route-monthly" }` |

6. **Validate the migrated workspace.**

   ```bash
   find workspace-docs/Workflow -path '*/planning/plan.json' -print \
     | xargs jq empty
   ```

   Then run a structural route check:

   ```bash
   node <<'NODE'
   const fs = require("fs");
   const { execSync } = require("child_process");
   const files = execSync("find workspace-docs/Workflow -path '*/planning/plan.json' -print", { encoding: "utf8" })
     .trim()
     .split("\n")
     .filter(Boolean);
   let errors = [];
   const matchesOutput = (output, dep) => String(output || "").split(",").map(s => s.trim()).includes(dep);
   for (const file of files) {
     const steps = JSON.parse(fs.readFileSync(file, "utf8")).steps || [];
     const ids = new Set(steps.map(s => s.id).filter(Boolean));
     for (let i = 0; i < steps.length; i++) {
       const step = steps[i];
       if (step.type !== "routing") continue;
       const routes = step.routes || [];
       const routeIDs = new Set(routes.map(r => r.route_id));
       if (String(step.description || "").trim()) errors.push(`${file}: ${step.id} must clear routing description`);
       if (routes.length < 2) errors.push(`${file}: ${step.id} has fewer than 2 routes`);
       if (step.default_route_id && !routeIDs.has(step.default_route_id)) errors.push(`${file}: ${step.id} invalid default_route_id`);
       for (const route of routes) {
         if (route.next_step_id !== "end" && !ids.has(route.next_step_id)) {
           errors.push(`${file}: ${step.id}/${route.route_id} points to missing ${route.next_step_id}`);
         }
       }
       if ((step.context_dependencies || []).includes("route_selection.json")) {
         const hasProducer = steps.slice(0, i).some(s => matchesOutput(s.context_output, "route_selection.json"));
         if (!hasProducer) errors.push(`${file}: ${step.id} depends on route_selection.json but no prior producer declares it`);
       }
     }
   }
   if (errors.length) {
     console.error(errors.join("\n"));
     process.exit(1);
   }
   console.log("routing plan validation passed");
   NODE
   ```

7. **Smoke-test one route per migrated workflow.** For example:

   ```jsonc
   run_workflow("Workflow/upwork", "default", route_selections={ "route-by-mode": "search" })
   run_workflow("Workflow/social-media", "default", route_selections={ "step-run-mode-router": "propose_new" })
   run_workflow("Workflow/linkedin", "default", route_selections={ "step-workflow-router": "route-engage" })
   run_workflow("Workflow/HRMS", "default", route_selections={ "workflow-mode-router": "route-monthly" })
   ```

   If a workflow has a preflight/judgment router, confirm the producer step writes
   `route_selection.json` before the router runs.

## Decisions made

1. **File name / field.** Canonical file is `route_selection.json` with
   `select_route`. Compatibility aliases `route_id` and `selected_route_id` are
   accepted. Values are normalized to route IDs.
2. **JSON file always.** Routing never reads variables directly. Caller overrides are
   written as `route_selection.json` by the runner.
3. **Keep `routing_question`.** It remains for compatibility and plan readability,
   but it is not evaluated by a model.
4. **No scalar `route` sugar.** Use the explicit `route_selections` map:
   `{ "<routing_step_id>": "<route_id | next_step_id>" }`.

## Remaining follow-ups

1. Migrate callers/schedules that currently pass routing variables (for example
   `FLOW_MODE`, `RUN_MODE`, or `WORKFLOW_MODE`) to `route_selections`.
2. Collapse pure all-routes-to-`end` terminators where they add no real branch.

## Example (end to end)

```jsonc
// caller
run_workflow("Workflow/upwork", "default", route_selections={ "route-by-mode": "search" })

// runner (before execution) writes:
//   runs/<run>/execution/route-by-mode/route_selection.json
//   { "select_route": "search" }

// routing step "route-by-mode" (deterministic):
//   reads select_route = "search"
//   routes[]:  search → next_step_id "search-scrape-jobs"
//   → jumps to search-scrape-jobs; runs the search block

// no param next time? → falls back to default_route_id ("profile")
```
