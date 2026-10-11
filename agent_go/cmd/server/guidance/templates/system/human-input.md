**Plan-editing tool arguments:** Before a plan mutation, read `builder-reference/references/plan-editing-tools.md`. Step fields described below belong inside `add_step.step` or `update_step.changes`; route/group/maintenance fields belong inside `parameters`. Use the live type/action-specific schema; these field descriptions do not authorize flat arguments or extra fields.

## human_input — Asking the User a Question Mid-Workflow

`human_input` is the step type that **blocks the workflow and asks the
user a question**, returning their response to drive subsequent steps.
Load this skill when adding or editing a human_input step, deciding
how to capture a free-form value, or preserving a legacy input step. For
fixed-choice decisions, load `references/branch.md`.

Use `add_step` / `update_step` to manage these
in the plan.

## When to use human_input

**New steps: `human_input` is for capturing a free-form value only.** A
person's *decision* — approve/hold, yes/no, pick one of a few options — is a
`branch` step with `route_source: "human"` (`references/branch.md`): the
routes are the options, schedules answer it up front with
`route_selections`, and unattended runs fall back to `default_route_id`.
`add_step` rejects `response_type: yesno` and `multiple_choice`
and points there. Existing `human_input` steps of every type keep working
unchanged.

- The user must **provide data** the agent can't otherwise discover
  (e.g., a PAN, a confirmation code from email, a month to reconcile, a raw
  note to format) — capture it into `variable_name`.
- Context the agent can't infer and that isn't a choice among fixed
  options.

**Don't use human_input for:**

- Data the agent can derive from variables, prior steps, or tools.
- Validation that can be done deterministically (use `validation_schema`
  + retry).
- "Status checks" the user doesn't actually need to see — those become
  noise and break unattended runs.

## Input types

Set `response_type: "text"` for a new step. Use it when the answer space
is open (e.g., "What is the company name?", "Paste the OTP from email").

`yesno` and `multiple_choice` are legacy response types supported when
reading or updating existing steps. Do not create new steps with them; use
a human-decided branch for confirmations and fixed choices.

## Branching from human_input

Do not add `human_input` just to ask which fixed branch to run when the
user already told the builder in chat. In that common case, model the
choice as a deterministic `routing` step and have the builder/caller
pass `route_selections` when starting the workflow.

Use `human_input` only when the running workflow truly has to stop and
ask a human because the answer is not already available at launch. If the
in-run answer is a choice among a few fixed options, that is a **branch with
`route_source: "human"`**, not a `human_input` with `option_routes` — the
routes are the menu, and a schedule answers it with `route_selections`
instead of a separate `human_inputs` argument. (`option_routes` /
`if_yes_next_step_id` remain supported on existing steps.)

```
routing(
  id="pick-job",
  routes=[
    {route_id: "lead-verification", next_step_id: "ask-target"},
    {route_id: "withdrawals-cleanup", next_step_id: "withdrawals-check"}
  ]
)

run_full_workflow(
  group_name="aayush",
  route_selections={"pick-job": "lead-verification"},
  human_inputs={"ask-target": "Mar26"}
)
```

The `routing` skill has the full deterministic route-structure rules
for workflows that need an explicit router node.

## Schedule / unattended runs

Schedules run **unattended** — human_input steps in a workflow that's
scheduled cannot wait for a real human. Two strategies:

1. **Pre-supply responses via `human_inputs` arg**: `run_full_workflow(group_name, human_inputs={"ask-month": "Mar26"})`.
   The schedule's message provides the response upfront. Required for
   any human_input step in a scheduled run, or the schedule fails with
   "missing human_input responses".
2. **Restructure the workflow** to remove the human_input — e.g.,
   default to a safe choice for scheduled runs and only ask
   interactively.

When you add a human_input step, decide from context whether the
workflow may be scheduled. If yes, plan for response preparation and
state the assumption briefly instead of asking unless the schedule
policy is blocking.

## Validation

`human_input` accepts any value the user types (for `text`) or any
selection from the configured list (for `multiple_choice` / `yesno`).
Validation typically happens on the **downstream** step that consumes
the response — that step's `validation_schema` checks the response
makes sense for its purpose.

If you need the user's response itself to match a format (e.g., a
valid PAN, an email, a number in a range), add explicit validation
guidance in the question prompt AND validate downstream rather than
relying on input-level validation (the human can always type anything
into a text field).

## Anti-patterns

- **Question-as-status-update**: "Do you want me to continue?" without
  a real branch — just continue.
- **Open-ended questions in scheduled runs**: schedules can't answer.
- **Multiple human_inputs in sequence**: feels chatty. Either gather
  context upfront with variables, or use a single agent
  step that has a conversation.
- **Asking twice**: do not add a `human_input` branch picker when the
  builder can infer the route from the user's launch request and pass
  `route_selections`.

## Tools

- **`add_step(id, title, question, next_step_id, insert_after_step_id, reason, response_type="text", ...)`** — add a free-form value step; optionally set `variable_name` and `context_output`.
- **`update_step(existing_step_id, question?, response_type?, ...)`** — edit an existing step. Legacy option fields are only for maintaining existing choice steps.
- **`execute_step(step_id, group_name, human_input="<response>")`** — in workshop mode, test by passing the response directly via `human_input`. Skips the actual prompt UX.

For the full signatures + parameters see
`read_skill(skills=[{"name":"builder-reference","path":"references/workflow-tools.md"}])`.
