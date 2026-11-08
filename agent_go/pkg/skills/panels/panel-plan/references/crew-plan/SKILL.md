# Older plan-based workflows

New workflows are Python scripts: use the `python-workflows` skill. A workflow created before that keeps running, but its plan is no longer edited. When someone asks to change its steps, inputs or step settings, say plainly that it can't be edited: converting an existing workflow to a Python script is done by the platform operator for now (it keeps its name, data and schedules), and new workflows are scripts.

Use `crew_workflow_files action=list` for exact workspace identities. To inspect one, `read` its `planning/plan.json` (pass `step_id` or `step_ids` for named steps); the screens show the plan, progress and run evidence.

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
