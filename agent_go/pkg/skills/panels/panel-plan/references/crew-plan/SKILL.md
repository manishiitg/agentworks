# Crew automation examples

The tool schemas are authoritative; these examples are optional. Use `crew_workflow_files action=list` for exact workspace identities and this Crew's available agents.

Each Crew has one workflow. Use routes for different tasks within that plan, not another workflow. Configure steps, inputs, step settings and goals in Crew chat; the screens show the plan, progress and run evidence.

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

## Reuse Python

Create with `kind=script`; read/write its `script.json` and `main.py`. It runs Python directly, without an AI turn or a second plan. Code edits apply next run; previous runs retain their executed source and resolved inputs. Chat calls and workflow steps use this same runner. Read bundled files from `SCRIPT_SOURCE_DIR`; write results to `STEP_OUTPUT_DIR`. `STEP_EXECUTION_DIR` identifies the current execution. Each invocation has its own permanent run folder; explicit shared destinations are still shared.

Read saved-script arguments from `agentworks.inputs` (the typed `STEP_PARAMS_JSON` object); `VAR_*` holds workflow environment values, not per-child argument exports.

Scripts inherit the Crew's model, browser and app selections. To choose a different connected browser for a script, read its `script.json` and use the existing write action to set `capabilities.browser_mode` (`headless`, `extension`, `none`, or local `cdp` when offered by this installation), preserving its other fields. Same-ID conversion copies settings from the original workflow, including selected secret names and provider references; it never copies secret values.

Python scripts use the Crew's one managed database through `agentworks_db`. Use `execute` or `transaction` for rows and additive setup (`CREATE TABLE IF NOT EXISTS` and `CREATE INDEX IF NOT EXISTS`). Chat uses `database action=mutate`. ALTER and DROP are refused. Existing `db/migrations/` files remain history and are not executed.

Replacing an existing workflow needs the approved same-ID conversion so its code, data and complete database stay attached. A separate copy is a trial, not a migration. Agent replies have bounded `text`; check task/status text with `reply.answer_file.read_text()` after `reply.ok`, and use `log_file` for the received provider transcript.


Script receipts include `log_file`, `log_error` and `log_complete`. Read bounded parts of that file for full output beyond the inline reply limit. Native Claude/Codex shell commands use their own tool history and do not return this platform log receipt.

A workflow scripted step can reference `script: "<id>"` and `script_inputs`. Each input selects one source: `{"value":7}`, `{"input":"count"}`, or `{"output":"prior.json","field":"nested.count"}`. Prior outputs must be declared dependencies from this run. Do not copy the script's schema or code into the workflow. Cross-Crew reuse uses a shared function.

For Python workflows, use ordinary functions and modules in the code bundle rather than a saved child script for each step. Label a function with `from agentworks import step` and `@step("Prepare report")`; it runs normally on every call, with no caching or retry. Copy the old scripted step's title as its label, without a separate description. Call `agent("Research", TASK)` for agent work: its name and existing task text supply the Plan label and description. Prefer a string literal or module-level task constant so the Plan can show it; dynamic text is shown as built at run time. Normal Python branches and loops stay normal code. `run_script` is for genuinely reusable saved scripts. Labels record run activity, never arguments or return values.

Carry an old step's named app tools into `agent(..., tools=["human_tools:integrations.google"])`; only tools available to this automation can be selected. `can_ask_person=True` explicitly allows bounded questions, including during a scheduled run. It stays off by default. For another Crew's published action use `call_function(crew, name, inputs)`: it returns a typed receipt with `call_id`, `status`, `value` and `log_file`. A running receipt is not a final answer; refresh it with `function_status(call_id)`. The Crew must be linked and the function must admit the calling Crew. The existing service checks input and result types.

## Run and verify

For this Crew's own workflow, use the normal step tools: `runs action=step`, `runs action=step_status`, `runs action=message`, `runs action=stop_step`, `runs action=stop_all`, `runs action=active`, `runs action=debug`, and `runs action=start`. Start only what the person requested. A step call returns its run folder and execution ID. In this chat, the next `runs action=step` continues the same saved run and its input values, even after a runtime restart. Pass `new_run=true` for a fresh run, or the exact `run_folder` to choose an earlier one; never guess one. Changing run inputs needs a new run. If earlier outputs are missing, run the earlier steps in that run or ask which prior run to use. `runs action=step_status` is one requested status check, not a polling loop: completion returns automatically to this chat once. `runs action=debug` uses the exact run folder. Stop or steer the returned execution ID only when requested. Full workflow runs remain explicit new runs. These controls appear only to people who can edit this Crew and its workflow, and only when it has one.

For a saved reusable script or an internal trigger, call `functions` with `action=run_trigger`, `workspace_path` and optional `inputs`, such as `{"count":7}`. Omit `trigger_id` for a direct run. Defaults, required values and types are validated for workflows and scripts alike. Reuse a delivery ID only to retry the same uncertain delivery. Use the returned run identity, never guess an iteration folder. The run picker selects the latest started run. Existing retention still applies. Poll with `functions action=trigger_status` using the returned identities until it finishes, then report its actual output or failure.

## Track outcomes

Use an ordinary workflow step to measure progress from its source and save the result, actual time and evidence in the workflow database. A dashboard reads that data. Missing readings are unavailable, never zero; a completed run alone does not prove an outcome.


Automation starts/results go to their saved Crew chat. If it is missing, repair that destination explicitly. Preserve paused schedules. Definitions and code are resolved by the server inside `.agentworks`; ordinary project source, skills and knowledge stay in place.
