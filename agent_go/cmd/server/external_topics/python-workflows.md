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
2. To create one, use `workspace action=automation_create` with `crew_id` and
   `name`. New workflows are Python scripts. Keep the returned identity.
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
5. Use `workflow action=plan` with `view=outline` for the saved Python outline.
   This inspects the syntax without executing or importing authored code and
   requires source-editor access. Python `@step` labels are normal function
   calls, not separate runnable or resumable plan steps. Branches and loops
   appear as “Other code”; large scripts can return an outline-limit message.
   Read the saved source for the complete logic.

Never put secret values into source or presets. Use the project's existing
secret selection. Sharing and connected-account authority do not change here.

## Run and inspect

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
NUL byte; `__pycache__` is excluded. Child script calls do not replace it.
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

Runs do real work, including database writes, app calls and sends. No new
mid-run question or individual Python step execution is introduced.

## Resume a failed run

Use `runs action=resume` with `workflow_id` and the failed `run_folder`.
Only the Crew's Builder or an admitted workflow editor can resume. It keeps
that folder and the captured source and typed inputs, starts a new execution
attempt, and preserves the old logs. Python starts at the top again.

Only successful `agent()` and `run_script()` calls with an explicit `key=`
can reuse their recorded results. Use a distinct logical key for each call,
including each loop iteration; reusing a key within one attempt is refused.
The same key and arguments can reuse a successful result even if unrelated
calls move. Changed arguments execute fresh and are recorded as such.
Returned replies expose `reused=true` when reused.

Ordinary Python, database writes, app calls, checks and notices execute again.
An interrupted call whose completion is uncertain keeps its existing refusal;
resume never silently repeats an uncertain send. Missing or changed frozen
source, source hash or input proof says to start a new run. Older runs without
that proof cannot resume. Use `rerun` when you want current source and a new
run folder instead.

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
agentworks runs resume --workflow sample --run-folder failed-run
agentworks functions run-trigger --crew team --workspace Workflow/sample --preset weekly --inputs '{"LIMIT":3}'
agentworks functions trigger-status --crew team --workspace Workflow/sample --trigger-id returned-trigger --run-id returned-run
```

`--content-file -` reads source from stdin. `workflows save-inputs --inputs`
accepts the complete typed input-definition array when you mean to replace it.
`tools spec workflow workspace runs functions` reads the server's current
schemas; `tools call` and `mcp serve` use the same catalog and access checks.

## Webhook and timer inputs

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

Failed runs keep native agent conversation history for 72 hours. After that, a continued agent call starts fresh and the run records “conversation expired: this call restarts fresh”. Completed or stopped runs release that history immediately.
