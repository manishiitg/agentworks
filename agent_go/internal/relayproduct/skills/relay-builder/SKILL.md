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
