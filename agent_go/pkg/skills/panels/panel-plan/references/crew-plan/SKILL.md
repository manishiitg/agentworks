# Crew automation examples

The tool schemas are authoritative; these examples are optional. Use `crew_workflow_files action=list` for exact workspace identities and this Crew's available agents.

## Change a step without replacing the plan

Read `planning/plan.json`, then call `crew_workflow_files` with `action=edit`, the returned `workspace_path`, `operation=update_step` and:

```json
{"step_id":"summarize","changes":{"title":"Summarize approved results","reason":"Clarify the result"}}
```

Use `describe` with `operation=add_step` for a new step's schema. Only `delete_plan_steps` removes named steps. Use `update_step_config` for a step's model/tools/skills and `update_variable` to add, update or delete one input. These use the same validators and change history as the workflow Builder. Other steps and inputs remain unchanged.

An agent step can select a `crew_agent` returned by list. It reuses its saved role and model, with a separate run context. It does not import private chat history or another person's credentials. Explicit settings override defaults; an empty skills list deliberately selects none.

## Reuse Python

Create with `kind=script`; read/write its `script.json` and `main.py`. It runs Python directly, without an AI turn or a second plan. Code edits apply next run; previous runs retain their executed source.

A workflow scripted step can reference `script: "<id>"` and `script_inputs`. Each input selects one source: `{"value":7}`, `{"input":"count"}`, or `{"output":"prior.json","field":"nested.count"}`. Prior outputs must be declared dependencies from this run. Do not copy the script's schema or code into the workflow. Cross-Crew reuse uses a shared function.

## Run and verify

Call `run_workflow_trigger` with `workspace_path` and optional `inputs`, such as `{"count":7}`. Omit `trigger_id` for a direct run. Defaults, required values and types are validated for workflows and scripts alike. Reuse a delivery ID only to retry the same uncertain delivery. Poll the returned run until it finishes and report its actual output or failure.

## Track outcomes

Use `crew_goals action=list`, then save the goal's desired outcome, optional metrics and contributing workflow IDs. Record actual observations with their evidence and time. Keep metric IDs stable; use a new ID when its meaning or unit changes. Missing readings are not zero. Goal status changes explicitly; a successful workflow run is not proof that its goal was achieved.

Automation starts/results go to their saved Crew chat. If it is missing, repair that destination explicitly. Preserve paused schedules. Definitions and code are resolved by the server inside `.agentworks`; ordinary project source, skills and knowledge stay in place.
