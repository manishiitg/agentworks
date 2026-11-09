---
name: python-workflows
description: Create, edit and run a saved Python workflow over MCP or the laptop CLI; read its inputs, presets, outline and recorded results.
external: true
external_tools: [workflow, workspace, runs, functions]
---
# Saved Python workflows

A workflow belongs to a Crew. Its saved source is `script.json` (typed inputs,
optional presets and expected outputs), `main.py`, and any nested Python files.
The server runs it in that Crew with its current permissions and connected apps.

A Crew has one workflow. A new task is a new path inside it, not a new
workflow: add an input that picks the task, an `if`/`elif` branch in `main.py`,
and a saved preset (saved setting) for that task. Point the task's schedule,
webhook or action at that preset. Put reusable code in a helper `.py` file
beside `main.py` and import it normally, for example `import gate`, then
`gate.run_gate()`.

The parent workflow and platform-run scripts can import `agentworks` and
`agentworks_db`. A native shell inside `agent()` does not have that helper
environment. Agents use the registered `database` tool (`action="query"` or
`"mutate"`), or write files that the parent checks and stores. When converting
a workflow, keep these imports out of helpers an agent must launch itself.
An optional `timeout_seconds` in `script.json` limits the whole script, including
its helper calls: default 3600 seconds, allowed integers 1 through 21600 (six
hours). Stop can end it sooner.

Declare third-party Python packages in `requirements.txt` beside `main.py`.
Use one plain package list; included files, constraints and installer option
lines are refused before cache reuse. Put each package pin directly in the file.
The runner uses bundled uv (pip if uv is unavailable), caches the installation
per script and requirements hash, and gives its agents the same package path.
Changed requirements create a new entry; normal runs cannot overwrite installed
package files. Package setup uses the existing sandbox, never a global install.
You can ask the Crew's agent to create or change it; no terminal is needed.

`agent(..., reasoning="low" | "medium" | "high")` optionally chooses that
call’s effort where its model supports the level. Unsupported choices return
a named error; omit it to keep the existing default. A continued conversation
keeps its original reasoning choice, recorded with its run.

## Find and edit

1. Use `workflow action=list`. Each saved script has `kind=script`, typed `inputs`
   and `input_presets`. Keep its `id` and `workspace_path`; `workflow action=get`
   returns the same contract and full manifest for one workflow.
2. Inspect this Crew's existing workflow before creating one. Use
   `workspace action=automation_create` with `crew_id` and `name` only when it
   has none; a second workflow is refused with HTTP409. Keep the returned identity.
   Add task paths and saved settings to the existing workflow instead.
3. Use `workspace action=automation_read` or `automation_write` with `crew_id`,
   `workspace_path` and `file`, such as `main.py`, `nested/report.py`, or root
   `script.json`. Writes take UTF-8 `content` and use the existing script editor's
   validation and source access. They require the Crew's Builder and source
   editing consent; being able to run a workflow does not grant source editing.
4. To change presets without replacing the rest of the definition, use
   `workflow action=save_inputs` with `workflow_id` and the complete
   `input_presets` list. Each preset has `name` and `values`, for example
   `[{"name":"weekly","values":{"LIMIT":"7"}}]`. Pass `[]` to remove them.
   Optionally supply the complete typed `inputs` list; omitting it preserves
   current inputs. Values in saved definitions/presets use the script contract's
   string encoding; invocation values below are typed JSON.
5. Use `workflow action=outline` for the saved Python outline.
   This inspects the syntax without executing or importing authored code and
   requires source-editor access. Python `@step` labels are normal function
   calls, not separate runnable or resumable plan steps. Branches and loops
   with no step or agent inside are left out; large scripts can return an outline-limit message.
   Read the saved source for the complete logic.

Never put secret values into source or presets. Use the project's existing
secret selection. Sharing and connected-account authority do not change here.

## Run and inspect

The owning Crew's in-app chat can offer a bound `runs action=start` with
only `input_preset` and typed `inputs`. That tool chooses the workflow from the
Crew and uses the same input validation as MCP. Choose the task through a saved
setting or typed inputs, rather than creating another workflow. It starts a real background
run; the chat's background work control stops it. Workflow run instructions
belong in the saved Python/Crew setup.

Use `runs action=start` with `workflow_id`, optional `input_preset`, and optional
`inputs` as a JSON object, for example `{"LIMIT":3}`. Explicit values override
the selected preset; omitted values use saved defaults. Unknown inputs, wrong
types and missing required values fail before launch. A saved script starts
directly; it does not ask a chat agent to launch it.

Keep the returned `session_id` and poll `runs action=status` with the same
workflow. Use `runs action=list` to find saved run folders and `runs action=get`
with `run_folder` for files, `metadata` (including `execution_id` and
`python_steps`) and `evidence` (checks and notices). The summary contains public
checks/notices; full run files remain available under the same file grants.

For Python runs, `metadata.source_snapshot_hash` identifies the main script's
executed frozen bundle, including `script.json`, nested source and binary files.
It is a SHA-256 of sorted relative file names and raw bytes, each followed by a
NUL byte; `__pycache__` is excluded.
`runs action=logs` diagnoses that run in one bounded reply: script stdout/stderr
tails with full paths, per-label status/durations, checks/notices, saved typed
inputs/preset, failing labels and traceback, and agent answer/provider-log paths.
Python labels share their program log; they do not each run a separate program.
Read the returned paths for full content under the same file grants. A
successful start is acceptance, not proof of completion.

To fix a failure, read its saved code and edit the current nested source through
`workspace action=automation_read/automation_write`. Then use `runs action=rerun`
with `workflow_id` and the previous `run_folder`. It starts the current saved
source with the recorded values/preset and reports changed source files and
revisions versus the previous execution snapshot. This is a new real run:
database writes, app calls and sends happen again. The current input contract
is revalidated; a removed or changed input can require a new explicit start.
A bounded or unavailable source comparison is stated in the receipt rather than
silently claiming nothing changed.

A Crew can also use `functions action=run_trigger` with `crew_id`, the saved
`workspace_path`, optional `input_preset` and typed `inputs`. Omit `trigger_id`
for a direct run without a published function. Keep the returned `trigger_id`
and `run_id`; poll `functions action=trigger_status` with those and the same
Crew/workflow. Choose one start path per intended run. Inspect uncertain
receipts before starting again; reuse `delivery_id` only for an identical retry
of `run_trigger`.

Runs do real work, including database writes, app calls and sends. Collect
needed answers in Crew chat before starting: a workflow does not wait for
someone to answer. Ordinary chat question cards do not pause a workflow.
Individual Python labels are not separately executable steps.

## Laptop CLI

The CLI calls these same server operations; it is not a local workflow engine.
Use the identities returned by your server in place of these sample values:

```sh
agentworks workflows list
agentworks workflows get --workflow sample
agentworks workflows create --crew team --name 'Weekly report'
agentworks workflows source --crew team --workspace Workflow/sample --file nested/report.py
agentworks workflows save-source --crew team --workspace Workflow/sample --file nested/report.py --content-file report.py
agentworks workflows save-inputs --workflow sample --input-presets '[{"name":"weekly","values":{"LIMIT":"7"}}]'
agentworks workflows outline --workflow sample
agentworks runs start-workflow --workflow sample --preset weekly --inputs '{"LIMIT":3}'
agentworks runs status --workflow sample --session returned-session
agentworks runs get --workflow sample --run-folder returned-run
agentworks runs logs --workflow sample --run-folder returned-run
agentworks runs rerun --workflow sample --run-folder returned-run
agentworks functions run-trigger --crew team --workspace Workflow/sample --preset weekly --inputs '{"LIMIT":3}'
agentworks functions trigger-status --crew team --workspace Workflow/sample --trigger-id returned-trigger --run-id returned-run
```

`--content-file -` reads source from stdin. `workflows save-inputs --inputs`
accepts the complete typed input-definition array when you mean to replace it.
`tools spec workflow workspace runs functions` reads the server's current
schemas; `tools call` and `mcp serve` use the same catalog and access checks.

## Webhook and schedule inputs

Workflow timers are Crew schedules that run the Crew's workflow. A message schedule sends a message to an agent instead. Read the current schedules guidance before creating or changing either; older workflow timers may still need conversion.

Each webhook or timer chooses one saved `input_preset` plus optional fixed
`inputs`. The sender cannot choose another preset. For raw webhook JSON, map
allowed input names with `payload_mappings.inputs`, for example:

```json
{"input_preset":"weekly","input_mode":"raw","allowed_variables":["LIMIT"],
 "payload_mappings":{"inputs":{"LIMIT":{"source":"event.limit"}}}}
```

The JSON value keeps its type. An optional `values` object translates source
choices to typed input values; `default` supplies a fallback. Types and schema
come from `script.json`. Missing, disallowed or invalid fields are refused by
name before starting; fixed inputs cannot be overridden. Timers use their saved
values through the same direct script execution. Slack raw events use this same
mapper and input validation.

Saved definition defaults (`value`) and preset values are text containing the
value (`"2"`, `"false"`, or JSON for objects). Invocation bodies and raw mapped
fields use actual JSON types (`2`, `false`, objects), as declared by the input.

Discovery also returns `secrets`: required Vault names declared by the script.
These are names, never values or new grants. Connect or select missing names
through the existing Vault settings before running.

Parent Python can read fresh own-Crew text with `from agentworks import files; note = files.read("notes/owner-handled.md")`. Each call reads again through the existing file policy, it does not read a frozen copy or grant the shell access. Use paths relative to the Crew's Files. UTF-8 text up to 256 KiB is supported; private files, credential paths, other Crews and symlinks are refused. Retired local knowledge folders do not become accessible through this helper. It is not a general Brain reader. Native agents use their existing file tools, not this import. Only the parent workflow receives its durable `WORKFLOW_DATA_ROOT`; do not assume a report executor or native shell has that environment.
