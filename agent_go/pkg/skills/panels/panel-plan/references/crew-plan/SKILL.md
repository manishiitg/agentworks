# Workflows inside a Crew

## Create and edit

Use `crew_workflow_files action=list` to get this Crew's owned definitions, exact workspace paths and available Crew agent IDs. These are executable workflows, separate from linked workflows used as reference material. Use `action=create`, `name` and `kind=workflow` for a sequence or `kind=script` for reusable Python. Keep the returned stable identity; a name or folder is not an execution identity.

Read the saved files before editing. For a workflow, use `action=read/write` with its returned `workspace_path` and `planning/plan.json`, `planning/step_config.json`, `code/...` or `variables/variables.json`. Write the required code before a plan that references it. The server validates plan/input writes. Consult the current schemas and the workflow Builder's `builder-reference/references/plan-editing-tools.md` for unfamiliar step fields; do not call its plan-editing tools unless this session offers them.

Define input names, types, defaults and required values once in Inputs. Optional named presets hold only saved values. No group, preset or input file is required for a workflow with no inputs. Do not submit retired group selectors or invent a default group. Secrets stay in selected Vault bindings, not input values.

An agent step can reference a `crew_agent` ID from the list. It reuses that agent's model and instructions, and the Crew's selected skills, connections and tools as defaults. The list offers the workflow owner's agents visible to you; watching a teammate's agent does not let a workflow copy that person's account or instructions. Message teammates through your own agent instead. Explicit step overrides remain; `enabled_skills: []` deliberately selects no skills. It starts its own run context, without copying the agent's private chat or native session. Ordinary steps without a Crew-agent reference keep their own capability settings.

## Saved scripts

A saved script has one `script.json` input/output definition and `main.py`, with source under the project's `.agentworks/code/<id>/`. Use the scoped tool to edit it; do not save a second plan or variables file for the script. Python executes directly, without an AI turn; a skill is instructions for an agent, not a Python dependency.

A workflow scripted step references `script: "<id>"` (or `script:<id>`) and `script_inputs`. Each input selects one source: `{ "value": 7 }`, `{ "input": "workflow_input_name" }`, or `{ "output": "prior.json", "field": "nested.field" }`. Prior files must be declared context dependencies from this run. Do not copy the script's input schema onto the step. Reuse source only within this Crew; cross-Crew use goes through a shared function.

Check the returned Used by information before changing a shared contract. Edits apply on the next run; completed runs retain their executed source. Keep existing step-local scripts unless the person requests their extraction. A failed run does not authorize silently repairing shared source.

## Run and check

Use `run_workflow_trigger` with the exact saved workspace. Omit `trigger_id` to use its Crew-scoped internal binding. For saved scripts, `inputs` supplies typed values. For workflows, this internal entry accepts its configured payload; use a declared function to pass typed workflow inputs. Read the live schema and do not guess a webhook envelope. Reuse a delivery ID only when retrying the same uncertain delivery. Poll `get_workflow_trigger_run` using the returned trigger/run IDs until terminal, then inspect outputs or the reported failure. Do not start another run merely because the first is still running.

The Crew's Workflows panel exposes Steps (Code for scripts), Runs and Settings. Runs provides logs, Inspect and costs. Keep the Crew chat as the conversation; do not create another project to inspect a child run.

## Automatic and callable work

Read `panel-automations` for schedules, functions and webhooks. Target the saved workflow/script by its returned identity; a message to a Crew agent is a different action. Child entries retain a saved Crew chat (Main by default) for start/result updates and separate run identities. Supply required inputs; presets and fixed values reuse the same input definitions. Functions/envelope webhooks expose only explicitly selected input names. Direct scripts need no model turn just to record their result.

Use only the setup tools this chat exposes. If a child entry cannot be configured here, open that definition's Settings in the Crew rather than writing its private registry or inventing an API. A missing destination chat must be repaired explicitly, never silently redirected. Keep existing enabled/disabled settings and do not enable schedules merely to test them.

Workflow definitions live under `.agentworks/workflows/<id>/` and automation code under `.agentworks/code/<id>/` in the Crew project. Use the workspace identity returned by `crew_workflow_files`; the server resolves these folders. Runs, outputs, logs and chats stay in managed storage outside the project. Ordinary project source, skills and knowledge keep their existing locations.


## Goals and tracking

Goals belong to the Crew, independently of executable workflows. Use `crew_goals action=list` before changing them. `save` updates one goal (stable `id`, `name`, desired `outcome`, explicit `status` active/achieved/stopped) and its complete `metrics` list. Leave metrics empty for a qualitative outcome. Optional `workflow_ids` selects this Crew's workflows or scripts that contribute; one goal may link to several workflows, and a Crew may have several goals. Never move an unrelated project just to track an outcome.

Reuse the saved metric definitions and IDs; changing a metric's source, unit or meaning requires a new ID. User-supplied targets are configuration, not observations. `record` takes actual values, source observation times, collection/run IDs and original evidence; include `source_workspace` when attributing a reading to a linked workflow. Workflow agents use `get_goal_metrics` and `record_goal_observations` with their own workspace; the server routes readings to the parent Crew and requires the workflow to be linked. Keep business data and run files in the workflow's own storage. Scripts still run directly: inspect their real result and record its measurement through the Crew tool, or use an agent step that does this. A workflow finishing successfully never counts as progress by itself and never changes the goal's status. Missing or stale measurements are not zero or proof of success.

The Goals view shows current tracking, workflow links and expandable measurement history/evidence. Use Record update for a verified manual reading and Edit goal for targets, links or explicit status changes. Goal tracking does not create a schedule or run work automatically.
