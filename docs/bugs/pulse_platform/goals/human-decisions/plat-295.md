[← goals / human-decisions](index.md)

# PLAT-295 — Human-decided branch (`route_source: "human"`) replaces yesno/multiple_choice `human_input` for new steps

| Field | Value |
|---|---|
| State | open |
| Priority | - |
| Product | goals |
| Area | human-decisions |
| Summary | — `branch` gains `route_source: "human"`: the routes are the options, `branch_question` the prompt; preseeded sources (`route_selections`, `route_selection.json`, `route_source_file`, `context_dependencies`) still win s… |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `implemented locally (new steps only, no migration); tests pass; not deployed` |
| Last synchronized | `2026-09-05` |

## 2026-09-05 — Decision and implementation

### Why

`human_input` bundled two concerns: *acquiring* an answer from outside the
plan (a live person, or a schedule/workshop override) and *routing* on it
(`if_yes_next_step_id` / `if_no_next_step_id` / `option_routes`). The routing
half duplicated what `branch` already does, with its own validation, its own
schedule override (`human_inputs`) alongside `route_selections`, and its own
canvas/edit UI. Every decision-type use in the workspace (approve/hold,
publish/redraft, skip/continue) is a small fixed choice — exactly a branch
whose selector happens to be a person.

Owner decisions: branch can take the place of human_input **for decisions**;
`human_input` stays for **free-form value capture** (finding-ID lists, a month,
a raw note) because that is not a fork; and **existing workflows are not
migrated** — only new steps are steered.

### What changed

- `BranchPlanStep.RouteSource` (`route_source`, only accepted value `"human"`),
  exposed through `routeSwitchStep.GetRouteSource()`; routing always returns
  `""` — only a branch can ask a person.
- `resolveDeterministicRoutingSelection` gained an `execCtx` parameter and,
  for a human-routed branch, hands off to `resolveHumanBranchSelection`
  (`controller_branch_human.go`) **after** every preseeded source and
  **before** `default_route_id`, so:
  1. caller `route_selections`, preseeded `route_selection.json`,
     `route_source_file`, `context_dependencies` entry → used, no prompt
     (schedules answer up front exactly as for any branch);
  2. workshop `execute_step(step_id, human_input=...)` → test answer;
  3. unattended run (`skipHumanInput`) → `default_route_id`, else an
     actionable error naming `route_selections[step_id]` and `default_route_id`;
  4. interactive run → `RequestMultipleChoiceFeedback` with the routes'
     `route_name`s as options (same `HumanFeedbackStore`, same
     `BlockingHumanFeedback` UI, same 10-minute wait as `human_input`).
  Answers resolve from `option<N>`, an index, a `route_name`
  (case-insensitive), a `route_id`, or a `next_step_id`. The blocking call is
  a swappable package var so the ordering is unit-tested without the store.
- `validateBranchStepFieldsTyped` rejects any `route_source` other than
  `""`/`"human"`. `add_branch_step`/`update_branch_step` schemas and
  `PartialPlanStep` carry the field; update logs a `route_source` field change.
- `convert_routing_branch_step_type → routing` rejects a human-routed branch.
- **Add-time gate on `add_human_input_step`**
  (`validateNewHumanInputStepIsTextOnly`): `yesno` and `multiple_choice` are
  rejected with a message that spells out the equivalent `add_branch_step`
  call; `text` still works. Add-time only — load, update and execution of
  existing `human_input` steps are untouched.
- Canvas: `RoutingStepNode` shows an "asks human" badge when
  `route_source === 'human'`; `usePlanToFlow`/`stepConfigMatching` carry the
  field.
- Guidance: `branch.md` (new "Human-decided branch" section and resolution
  order), `human-input.md` (text-only for new steps; decisions → branch),
  `plan-design.md` step-type table, tool descriptions, canonical workshop
  prompt.

### Verification

`go build ./...` clean; full `step_based_workflow` package passes with 11 new
tests (`branch_human_route_test.go`): answer-shape resolution; interactive run
asks with the right question/options and ignores `default_route_id`; prompt
failure surfaces; unattended run uses default and never prompts; unattended
run without default fails actionably; workshop input wins; human-routed
detection; `route_source` validation; `add_branch_step` persists
`route_source`; `add_human_input_step` rejects yesno/multiple_choice and keeps
text; convert-to-routing rejected. `cmd/server/guidance` render test passes.
Frontend `tsc` reports no errors in the touched files. Not committed, not
deployed, no workflow plan modified.

### Not done (deliberately)

No migration of existing `human_input` steps; `update_human_input_step` is
not gated; `HumanInputPlanStep`'s routing fields remain for existing plans;
no live end-to-end run of a human-routed branch through the real UI yet —
that is the next verification step before deploy.

## Register notes

[PLAT-295](plat-295.md) — `branch` gains `route_source: "human"`: the routes are the options, `branch_question` the prompt; preseeded sources (`route_selections`, `route_selection.json`, `route_source_file`, `context_dependencies`) still win so schedules answer up front, unattended runs use `default_route_id` or fail actionably, interactive runs ask through the existing `HumanFeedbackStore` prompt. `add_human_input_step` now rejects `yesno`/`multiple_choice` (points at the equivalent `add_branch_step` call) and keeps `text` for free-form value capture. Add-time only — **no migration, existing `human_input` steps untouched by owner decision.** 11 new tests pass with the full package; canvas badge added; not committed, not deployed, no live UI run yet.
