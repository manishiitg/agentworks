# Relay Builder

Use the existing workflow plan tools to create and edit a Relay. Read the current `workflow.json` and `planning/plan.json` before changing an existing Relay. Keep `kind: relay` in the manifest, and set `relay_output_step_id` to the final authored agent.

## Graph

- `message_sequence` with `authored_prompt: true` is the agent node. Store the user's system prompt in `system_prompt` and ordered message templates in `items` as `user_message`. Select the execution model in the step's `execution_llm` configuration.
- `regular` with `script_only: true` is the Python node. Save its `main.py` through the existing script path. A script failure stops the run.
- `branch` is deterministic. Use `value_path` and `value_cases`; put model judgment in an earlier agent that returns JSON.
- Give every nonterminal agent or script an explicit `next_step_id`. Every route must reach the designated output agent. Do not add loops, joins, orphan steps, human-input nodes, or nested route switches.

## Inputs, outputs, and integrations

- User messages and system prompts may reference `{{input}}` for the whole caller JSON object, `{{input.field}}` for a required field, and `{{steps.id.output.field}}` for an earlier output. `INPUT` is already the root object, so do not use `{{input.INPUT}}`. Missing paths fail the run. Each authored agent must produce valid JSON in `result.json`; the designated output agent's JSON is the API result.
- Configure an existing function trigger with object `INPUT` for API calls. On a new Relay, declare `INPUT` in `variables/variables.json` and add a `default` variable group before creating the trigger. Cron and calendar schedules use `trigger_payload` for the same input. Inspect schedule and run results with the existing workflow tools.
- Use Relay-selected MCP tools and per-step skills for execution. Slack and Gmail may be used as configured. Do not create Slack or WhatsApp chat routes or WhatsApp notifications.
- Validate the graph with the existing plan tool. For a caller sample, use `run_full_workflow` with a configured `group_name` and `variables.INPUT` as a serialized JSON object, wait for completion, then inspect the saved run and final `result.json` before reporting a pass. Use `execute_step` only when the user wants to test one node in isolation. The Graph pane follows saved plan changes live.
- Use `get_relay_releases` for active and previous published versions. Use `publish_relay` only after validating the draft. Report the exact version and hash returned. Publishing freezes an API version; subsequent chat edits remain in the draft.

## Custom chat commands

Use `manage_custom_commands` when the user asks to create, edit or delete a
saved chat command. Keep it in this Relay's workspace, set workflow mode for
its command metadata, and use the user's instructions. Product commands are
provided by product.yaml; do not replace them with goal or dashboard commands.

## Published run boundaries

- Anyone with visibility may execute a published Relay and poll their own API runs. Publishing, editing, and schedule configuration require owner or write access. Execution uses the owner's configured credentials and quota; never attach the caller's personal credentials.
- Cron and calendar schedules execute the current draft, so edits affect the next occurrence. Explain this when creating a production schedule.
- Scripts and agent tools must write generated files only into the assigned run folder or runtime data directories (`db/`, `costs/`, `logs/`). Never write the release's graph, prompts, variables, skills, or saved code during execution. Warn that changing executable snapshot files makes the published version fail its next integrity check; a new publish is needed to restore it.
