# Editing an existing plan-based workflow

New workflows are Python scripts: use the `python-workflows` skill. This guide only covers workflows created before that, until they are converted.

The tool schemas are authoritative; these examples are optional. Use `crew_workflow_files action=list` for exact workspace identities and this Crew's available agents.

Configure steps, inputs, step settings and goals in Crew chat; the screens show the plan, progress and run evidence.

## Change a step without replacing the plan

Read `planning/plan.json`, then call `crew_workflow_files` with `action=edit`, the returned `workspace_path`, `operation=update_step` and:

```json
{"step_id":"summarize","changes":{"title":"Summarize approved results","reason":"Clarify the result"}}
```

Use `describe` with `operation=add_step` for a new step's schema. Only `delete_plan_steps` removes named steps. Use `update_step_config` for a step's model/tools/skills and `update_variable` to add, update or delete one input. These use the same validators and change history as the workflow Builder. Other steps and inputs remain unchanged.

An agent step can select a `crew_agent` returned by list. It reuses its saved role and model, with a separate run context. It does not import private chat history or another person's credentials. Explicit settings override defaults; an empty skills list deliberately selects none.

Agent steps have one initial task (`description`), optional ordered follow-ups or required script calls (`items`), and explicit settings. Optional script tools remain available for the agent to choose when needed. `inputs` maps a name to one fixed value, workflow input or declared earlier output, using the same mapping shape as `script_inputs` below. An output without `field` gives the agent a file reference; choosing `field` supplies that JSON value. Do not paste previous output files into task instructions.

## Schedule or expose the workflow

Use `crew_workflow_files action=describe` with `operation=manage_schedules` or `operation=manage_webhooks`, then `action=edit` with the same operation, child `workspace_path` and its arguments. List first and preserve existing IDs and paused state. These operate on the workflow shown under Automations, not another Crew. For functions, use `functions` with `project` set to the child's exact workspace path; define its saved run action and named inputs. All entries deliver to their saved Crew chat.

## Python

Scripts and Python workflows are covered by the `python-workflows` skill: `script.json`, `main.py`, the `agentworks` helpers, the database and checks.

A plan step can still call a saved script with `script: "<id>"` and `script_inputs`. Each input selects one source: `{"value":7}`, `{"input":"count"}`, or `{"output":"prior.json","field":"nested.count"}`. Prior outputs must be declared dependencies from this run.

Replacing an existing workflow needs the approved same-ID conversion so its code, data and complete database stay attached. A separate copy is a trial, not a migration.

## Run and verify

For this Crew's own workflow, use the normal step tools: `runs action=step`, `runs action=step_status`, `runs action=message`, `runs action=stop_step`, `runs action=stop_all`, `runs action=active`, `runs action=debug`, and `runs action=start`. Start only what the person requested. A step call returns its run folder and execution ID. In this chat, the next `runs action=step` continues the same saved run and its input values, even after a runtime restart. Pass `new_run=true` for a fresh run, or the exact `run_folder` to choose an earlier one; never guess one. Changing run inputs needs a new run. If earlier outputs are missing, run the earlier steps in that run or ask which prior run to use. `runs action=step_status` is one requested status check, not a polling loop: completion returns automatically to this chat once. `runs action=debug` uses the exact run folder. Stop or steer the returned execution ID only when requested. Full workflow runs remain explicit new runs. These controls appear only to people who can edit this Crew and its workflow, and only when it has one.

For a saved reusable script or an internal trigger, call `functions` with `action=run_trigger`, `workspace_path` and optional `inputs`, such as `{"count":7}`. Omit `trigger_id` for a direct run. Defaults, required values and types are validated for workflows and scripts alike. Reuse a delivery ID only to retry the same uncertain delivery. Use the returned run identity, never guess an iteration folder. The run picker selects the latest started run. Existing retention still applies. Poll with `functions action=trigger_status` using the returned identities until it finishes, then report its actual output or failure.

## Track outcomes

Use an ordinary workflow step to measure progress from its source and save the result, actual time and evidence in the workflow database. A dashboard reads that data. Missing readings are unavailable, never zero; a completed run alone does not prove an outcome.


Automation starts/results go to their saved Crew chat. If it is missing, repair that destination explicitly. Preserve paused schedules. Definitions and code are resolved by the server inside `.agentworks`; ordinary project source, skills and knowledge stay in place.
