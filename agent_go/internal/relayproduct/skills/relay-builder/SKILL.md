# Relay Builder

Use the existing workflow plan tools to create and edit a Relay. Read the current `workflow.json` and `planning/plan.json` before changing an existing Relay. Keep `kind: relay` in the manifest, and set `relay_output_step_id` to the final authored agent.

## Graph

- `message_sequence` with `authored_prompt: true` is the agent node. Store the user's system prompt in `system_prompt` and ordered message templates in `items` as `user_message`. Select the execution model in the step's `execution_llm` configuration.
- `regular` with `script_only: true` is the Python node. Save its `main.py` through the existing script path. For JSON handoff to another node, set `context_output: result.json` and write valid JSON to `os.path.join(os.environ["STEP_OUTPUT_DIR"], "result.json")`. Stdout is a log; `context_output` declares a file and never collects stdout. Let failed output writes raise an error; do not swallow permission errors or invent alternate output locations. A script failure stops the run.
- `branch` is deterministic. Use `value_path` and `value_cases`; put model judgment in an earlier agent that returns JSON.
- Give every nonterminal agent or script an explicit `next_step_id`. Every route must reach the designated output agent. Do not add loops, joins, orphan steps, human-input nodes, or nested route switches.

## Inputs, outputs, and integrations

- User messages and system prompts may reference `{{input}}` for the whole caller JSON object, `{{input.field}}` for a required field, and `{{steps.id.output.field}}` for an earlier output. `INPUT` is already the root object, so do not use `{{input.INPUT}}`. Missing paths fail the run. Each authored agent must produce valid JSON in `result.json`; the designated output agent's JSON is the API result.
- Include required data explicitly in each user message. `context_dependencies` does not inject previous outputs into authored prompts. "Use the previous step output" alone supplies no data. Use a concrete template such as `Extract invoice fields. Source: {{input.filename}}. PDF text: {{steps.extract_pdf_text.output.text}}. Return only the JSON object.` Earlier scripts and agents must have saved result.json for these references. Inspect the rendered message and saved outputs during the sample run.
- Preserve exact prompts provided by the user. When drafting prompts from requirements, define JSON fields, types and what missing values mean. Require a JSON-only final response: the runtime validates it and saves result.json automatically. An agent-created output file does not substitute for its final response. Commentary and code fences fail JSON validation.
- Configure an existing function trigger with object `INPUT` for API calls. On a new Relay, declare `INPUT` in `variables/variables.json` and add a `default` variable group before creating the trigger. Cron and calendar schedules use `trigger_payload` for the same input. Inspect schedule and run results with the existing workflow tools.
- Use Relay-selected MCP tools and per-step skills for execution. Google apps (Drive, Sheets, Calendar and Gmail) may be used through authorized connections with the required service grants; use `google_workspace_cli`. Plan creation does not need a Google connection. The platform `notify_user` tool is workflow-only and is unavailable to Relays. Do not configure Slack or WhatsApp connections, tools, chat routes or notifications.
- Validate the graph with the existing plan tool. For a caller sample, use `run_full_workflow` with a configured `group_name` and `variables.INPUT` as a serialized JSON object, wait for completion, then inspect the saved run and final `result.json` before reporting a pass. Use `execute_step` only when the user wants to test one node in isolation. The Graph pane follows saved plan changes live.
- Use `get_relay_releases` for active and previous published versions. Use `publish_relay` only after validating the draft. Report the exact version and hash returned. Publishing freezes an API version; subsequent chat edits remain in the draft.

## Validation boundaries

- Relay agents accept authored `user_message` items only. Workflow
  `prevalidation`, foreach, repair and scripted message items are unsupported.
- Input/prior-output references must resolve before a turn starts. The final
  response must parse as JSON before the runtime saves it and completes the
  agent node. These checks do not enforce required fields or their types.
- If the user requests more input or output checks, create an explicit Python
  script node using the existing executor. Validate only the agreed contract
  and raise on failure. Do not add a workflow prevalidation/repair loop or
  promise that a successful JSON parse proves the data is correct.

## Custom chat commands

Use `manage_custom_commands` when the user asks to create, edit or delete a
saved chat command. Keep it in this Relay's workspace, set workflow mode for
its command metadata, and use the user's instructions. Product commands are
provided by product.yaml; do not replace them with goal or dashboard commands.

## Published run boundaries

- Anyone with visibility may execute a published Relay and poll their own API runs. Publishing, editing, and schedule configuration require owner or write access. Execution uses the owner's configured credentials and quota; never attach the caller's personal credentials.
- Cron and calendar schedules execute the current draft, so edits affect the next occurrence. Explain this when creating a production schedule.
- Scripts and agent tools must write generated files only into the assigned run folder or runtime data directories (`db/`, `costs/`, `logs/`). Never write the release's graph, prompts, variables, skills, or saved code during execution. Warn that changing executable snapshot files makes the published version fail its next integrity check; a new publish is needed to restore it.
