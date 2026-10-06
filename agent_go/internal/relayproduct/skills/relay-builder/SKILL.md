# Relay Builder

Use the existing workflow plan tools to create and edit a Relay. Read the current `workflow.json` and `planning/plan.json` before changing an existing Relay. Keep `kind: relay` in the manifest, and set `relay_output_step_id` to the final authored agent.

## Graph

- `message_sequence` with `authored_prompt: true` is the agent node. Store the user's system prompt in `system_prompt` and ordered message templates in `items` as `user_message`. Select the execution model in the step's `execution_llm` configuration.
- `regular` with `script_only: true` is the Python node. Save its `main.py` through the existing script path. For JSON handoff, set `context_output: result.json` and call the built-in `agentworks_output.set_output(value)` helper shown below. It persists structured data in the assigned output folder; the user need not manage files or install a package. Existing direct result.json writes under STEP_OUTPUT_DIR still work. Stdout is a log; context_output alone never collects it. Let serialization and write errors raise; a script failure stops the run.
- `branch` is deterministic. Use `value_path` and `value_cases`; put model judgment in an earlier agent that returns JSON.
- Give every nonterminal agent or script an explicit `next_step_id`. Every route must reach the designated output agent. Do not add loops, joins, orphan steps, human-input nodes, or nested route switches.

## Inputs, outputs, and integrations

Python structured output:

```python
from agentworks_output import set_output

# Perform the step's work, then publish JSON-compatible data.
set_output({"text": extracted_text})
```

A later authored message uses `{{steps.extract_pdf_text.output.text}}`. The
helper rejects values that cannot be represented as JSON (including NaN and
Infinity), writes atomically, and propagates persistence errors. Multiple
successful calls replace the value; the last call is the step's output. It is
available through the shared sandboxed command runner in all products.

- User messages and system prompts may reference `{{input}}` for the whole caller JSON object, `{{input.field}}` for a required field, and `{{steps.id.output.field}}` for an earlier output. `INPUT` is already the root object, so do not use `{{input.INPUT}}`. Missing paths fail the run. Each authored agent must produce valid JSON in `result.json`; the designated output agent's JSON is the API result.
- Include required data explicitly in each user message. `context_dependencies` does not inject previous outputs into authored prompts. "Use the previous step output" alone supplies no data. Use a concrete template such as `Extract invoice fields. Source: {{input.filename}}. PDF text: {{steps.extract_pdf_text.output.text}}. Return only the JSON object.` Earlier scripts and agents must have saved result.json for these references. Inspect the rendered message and saved outputs during the sample run.
- Preserve exact prompts provided by the user. When drafting prompts from requirements, define JSON fields, types and what missing values mean. Require a JSON-only final response: the runtime validates it and saves result.json automatically. An agent-created output file does not substitute for its final response. Commentary and code fences fail JSON validation.
- Configure an existing function trigger with object `INPUT` for API calls. On a new Relay, declare `INPUT` in `variables/variables.json` with sample/default values in `variables[].value` before creating the trigger. Relays have no cron or calendar schedules. Inspect API runs with the existing execution tools.
- Use Relay-selected MCP tools and per-step skills for execution. Google apps (Drive, Sheets, Calendar and Gmail) may be used through authorized connections with the required service grants; use `google_workspace_cli`. Plan creation does not need a Google connection. The platform `notify_user` tool is workflow-only and is unavailable to Relays. Do not configure Slack or WhatsApp connections, tools, chat routes or notifications.
- Validate the graph with the existing plan tool. For a caller sample, use `run_full_workflow` with `variables.INPUT` as a serialized JSON object, wait for completion, then inspect the saved run and final `result.json` before reporting a pass. Use `execute_step` only when the user wants to test one node in isolation. The Graph pane follows saved plan changes live.
- Use `get_relay_releases` for active and previous published versions. Use `publish_relay` only after validating the draft. Report the exact version and hash returned. Publishing freezes an API version; subsequent chat edits remain in the draft.

## Agent tools: saved Python scripts

An agent that needs live data or an action during its turn (a customer lookup,
a price check) gets a saved Python script it calls as a tool. Relays support
only these script tools on agents; they do not support sub-agents. Prefer a
script node in the graph when the work does not depend on the agent's
reasoning.

- Add the tool with `manage_step_route` on the agent: `route_id` and the
  `sub_agent_step.id` are the same (`lookup-customer`), `type: regular`,
  `script_only: true`, a clear `description` (the agent reads it to decide when
  to call), and `script_parameters` (flat typed list) or
  `script_parameters_schema` (one full JSON Schema), never both.
- Write `code/lookup-customer/main.py` yourself. It reads its inputs from
  `json.loads(os.environ["STEP_PARAMS_JSON"])` and returns its answer by writing
  one JSON value to `os.path.join(os.environ["STEP_OUTPUT_DIR"], "route_result.json")`;
  the agent receives exactly that JSON. A lookup that finds nothing returns e.g.
  `{"found": false}` rather than failing.
- A tool reaches the user's own systems with their client library and a secret
  (for example a connection string in `SECRET_*`), or a file granted through
  `additional_read_paths`.
- The agent's authored system prompt is kept as written; the platform appends a
  short list of its tools. Test the tool with `execute_step`, then the whole
  Relay with `run_full_workflow`, and check the named tool call and its result.
  A script is never rewritten at run time: a failing script fails the tool call
  with its real error, which the agent sees. Publishing requires every tool's
  saved `main.py`.

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

- Anyone with visibility may execute a published Relay and poll their own API runs. Publishing and editing require owner or write access. Execution uses the owner's configured credentials and quota; never attach the caller's personal credentials.
- External products invoke published versions through API function triggers. Do not configure cron/calendar schedules or timed draft execution.
- Scripts and agent tools must write generated files only into the assigned run folder. Never write the release's graph, prompts, variables, skills, or saved code during execution. Warn that changing executable snapshot files makes the published version fail its next integrity check; a new publish is needed to restore it.

## Data handoff

Use INPUT, variables and step outputs to pass data between steps. A user's own database or system is reached through a script tool or an MCP integration with attached secrets.

Relays do not use variable groups. Configuration is flat (`variables[].value`); each call supplies its own INPUT and runs once. Do not call group tools or pass group_name/group_names. Node tests use a run_folder ID; use that same ID to debug/resume a saved node test. Preserve legacy values when consolidating an old draft; never edit frozen releases.
