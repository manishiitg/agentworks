# Workflow definition

A workflow belongs to a Crew. Its editable source is `script.json` plus `main.py`, with optional helper files. Use the current workflow tools to create or change it; keep the identity returned by the server so existing callers still reach the same workflow.

## Inputs and presets

`script.json` declares the values a run accepts:

```json
{
  "id": "weekly-review",
  "name": "Weekly review",
  "inputs": [
    {"name": "REGION", "type": "string", "value": "India"},
    {"name": "LIMIT", "type": "integer", "value": "20"},
    {"name": "ACTION", "type": "string", "value": "research", "schema": {"enum": ["research", "review"]}}
  ],
  "input_presets": [{"name": "USA", "values": {"REGION": "USA"}}],
  "outputs": ["review.json"]
}
```

Defaults and saved preset values are strings, including numbers and JSON text. Runtime calls supply typed JSON. Put constraints such as `enum` inside the input's `schema`. Use a required input when there is no sensible default.

A preset is one named set of values, not another workflow or a branch graph. Python reads `inputs` and chooses what to do. Optional raw-event mappings can provide a declared input; fixed inputs and the chosen preset stay under the saved caller's control. A missing mapped field needs a deliberate default when absence is acceptable; an invalid supplied value remains an error.

## Steps and agents

`main.py` uses ordinary Python functions, conditions and loops. A function decorated with `@step("Title")` is labeled in Steps and Runs. It runs like a normal function and is not separately runnable or scheduled.

Use `agent()` for work that needs an AI agent. Read its full saved answer and validate the required files or database results. `reply.ok` says the turn finished; it is not proof that the task succeeded. Repair an invalid agent output once in the same conversation, preserving completed external actions. A script failure stays a clear failure.

The bundled `python-workflows` skill documents helpers and current authoring tools. Workflows have no separate chat, plan file or graph editor; ask the Crew agent to explain or change the saved source.

## Resources and data

Declare needed Vault names in `secrets`, never values. The existing selected account and grants supply them. Installed skills, connected apps and browser access remain scoped to the Crew and the particular agent call; a source file cannot grant an unavailable connection.

Use `agentworks_db` or the managed database tools for Crew data. Keep durable files under the runtime's workflow data root and each run's forward outputs in its returned run folder. Native helper code uses the frozen `WORKFLOW_CODE_ROOT`; do not point it at mutable author files or guess older execution-folder layouts.

The platform retains identity, ownership, connection selections and run metadata outside the editable source contract. Do not replace those managed records or edit database files to change a workflow. Read the current schema from the canonical tools.

## Update source and callers together

A timer, webhook or function may depend on input names and types. Read those existing entries and include any required `entry_mappings` in the same canonical input/source update. Keep their IDs, authentication, owner, enabled state and schedule timing. The server validates the combined result before saving it.

A run uses its saved inputs and frozen source. Editing the workflow prepares the next run; it does not rewrite the evidence of an earlier run.
