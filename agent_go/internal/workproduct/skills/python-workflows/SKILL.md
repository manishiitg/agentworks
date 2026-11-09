---
name: python-workflows
description: Read before creating or changing a workflow in a Crew. New workflows are Python scripts (script.json plus main.py with the agentworks helpers); covers inputs, presets, agents, checks, the database, durable files and mocked test runs.
---

# Write a workflow as a Python script

A new workflow is a Python script: a `script.json` that declares its inputs and outputs, and a `main.py` that does the work with the `agentworks` helpers. There is no plan file to build. Ordinary Python decides the order: functions, `if`/`elif`, loops.

Create it with `crew_workflow_files action=create kind=script`, then write `script.json` and `main.py` with the same tool's `write` action. In Crew chat, run it with `functions action=run_trigger`, passing its `workflow:<id>` in `workspace_path`, then read the result with `functions action=trigger_status`. If several saved workflows could match the request, ask which one; never choose silently.

## External MCP and CLI

An external MCP agent uses `workspace action=automation_create/read/write` with
`crew_id`; `read` and `write` also take the returned `workspace_path` and `file`
(root `script.json`, `main.py` or a nested Python path). Use `workflow action=list/get`
for script kind, typed inputs and presets; `workflow action=save_inputs` replaces
`input_presets` and optionally `inputs` through the same validated editor.
`workflow action=outline` inspects saved source without running it and
keeps Builder source-editor authority. `@step` labels are not independently runnable.

In the owning Crew chat, use its bound `runs action=start` when offered:
optional `input_preset` selects saved values and `inputs` carries typed JSON
values (numbers, booleans, objects and arrays stay typed). The workflow is
selected by the Crew, so do not supply a path or workflow ID. This shortcut is
offered only when one saved script matches. With several, ask which one and use
`functions action=run_trigger` with its `workflow:<id>` in `workspace_path`.
Inputs override
the preset and defaults; unknown names and wrong types are refused before a
run starts. Stop it through the chat's background work control. For different
behavior or instructions, edit the saved Python/Crew configuration first.

Start with `runs action=start` (`workflow_id`, optional `input_preset`, typed
`inputs`) and poll `runs action=status` with its `session_id`, or use the existing
`functions action=run_trigger/trigger_status` receipt. Do not start both for one
intended run. Read `runs action=get` for `metadata.python_steps` and canonical
`evidence` checks/notices; full run files keep the same read grants. Use
`runs action=logs` for bounded stdout/stderr tails, failure traceback, saved
inputs, step timings and agent answer/provider-log paths. After editing nested
source, `runs action=rerun` with the old `run_folder` starts current source with
the recorded values/preset and reports changed source files. It repeats real
effects (including database writes and sends), so do not rerun merely to inspect
an uncertain start. External
agents can read `help action=topic topic=python-workflows` for the complete MCP
and named laptop CLI instructions. These operations keep current project,
source-editing, account and file grants.

For a failed run, `runs action=resume` with its `workflow_id` and `run_folder`
keeps the frozen source, input proof and folder, preserves logs and starts a new
attempt from the top of Python. Builder/edit authority is required. Successful
explicitly keyed `agent()` or `run_script()` results may be reused and expose
`reply.reused`; keys name logical calls, so use a unique key per loop iteration.
The same key twice in one attempt is refused. Changed arguments execute fresh.
Other Python code, database/app calls, checks and notices execute again;
uncertain completed effects are not silently retried. If frozen source/hash or
input proof is missing or changed, start a new run. `rerun` uses current source
in a new folder instead. Inspect the failed receipt before choosing either.

During an upgrade, inspect the source outline's notes for calls without keys.
Give expensive or effectful work a semantic key, such as one per record ID;
never insert a fixed key per source line or rewrite loops automatically.
To make long phases resumable, give each `agent()` or `run_script()` a stable
explicit `key`, such as `key="research"` or `key=f"research:{customer_id}"` in
a loop. Keys identify work, so do not reuse one for different calls. Keep sends
and orders as ordinary calls; keys are not permission to resend them. Inspect
an uncertain delivery receipt before deciding what to do next. A second resume
is refused while the same run is active.

## script.json

```json
{
  "id": "weekly-leads",
  "name": "Weekly leads",
  "description": "Finds new leads and saves them.",
  "inputs": [
    {"name": "REGION", "type": "string", "value": "India", "description": "Market to search."},
    {"name": "LIMIT", "type": "integer", "value": "20", "description": "Most leads per run."}
  ],
  "input_presets": [{"name": "USA", "values": {"REGION": "USA"}}],
  "outputs": ["leads.json"]
}
```

- `inputs` are typed (`string`, `integer`, `number`, `boolean`) with an optional default in `value`; add `"required": true` when there is no sensible default.
- `input_presets` are optional named sets of values, for example one per market. A run picks one preset or passes its own values.
- `outputs` names the files the run must leave in its run folder.
- Optional `timeout_seconds` limits the whole script, including its agent and script calls. The default is 3600 (one hour); choose an integer from 1 to 21600 (six hours). Stop can still end it sooner.
- Put third-party Python packages in one plain `requirements.txt` beside `main.py`, preferably with versions such as `httpx==0.28.1`. List pins directly; included files, constraints and installer option lines are refused. The runner installs them with bundled uv (pip when uv is unavailable), then reuses this script's read-only package cache. Changing the file creates a new cache entry. Its agents use the same packages. No system-wide install is needed.
- Declare needed Vault names in `"secrets": ["API_KEY"]`. The existing selected/lent Vault account supplies them; a missing name stops the run before any step. `from agentworks import secret`, then `secret("API_KEY")`, reads its existing `SECRET_API_KEY` environment value. An undeclared name is refused. Never put values in inputs, files, helper arguments or logs.

## main.py

```python
import json
from agentworks import agent, check, inputs, notify, run_folder, step
from agentworks_db import execute, query

FIND_LEADS = """Find up to {limit} companies in {region} that are hiring for operations roles.
Write leads.json in the run folder: a list of {{"company", "website", "reason"}}."""


@step("Find leads")
def find_leads():
    task = FIND_LEADS.format(limit=inputs["LIMIT"], region=inputs["REGION"])
    reply = agent("Lead researcher", task)
    problem = lead_problem(reply)
    if problem:
        # One repair turn with the exact check error, then a plain failure.
        reply = agent("Lead researcher", f"Your output failed a check: {problem}. Fix leads.json.",
                      conversation_id=reply.conversation_id)
        problem = lead_problem(reply)
    check("leads.json is valid", not problem, problem)
    if problem:
        raise RuntimeError(problem)
    return json.loads((run_folder / "leads.json").read_text())


def lead_problem(reply):
    if not reply.ok:
        return reply.error or "the agent did not finish"
    path = run_folder / "leads.json"
    if not path.is_file():
        return "leads.json is missing"
    leads = json.loads(path.read_text())
    if not isinstance(leads, list) or any("company" not in lead for lead in leads):
        return "leads.json must be a list of objects with a company"
    return ""


@step("Save leads")
def save_leads(leads):
    execute("CREATE TABLE IF NOT EXISTS leads (company TEXT PRIMARY KEY, website TEXT, reason TEXT)")
    for lead in leads:
        execute("INSERT OR IGNORE INTO leads VALUES (?, ?, ?)",
                [lead["company"], lead.get("website", ""), lead.get("reason", "")])
    return query("SELECT count(*) AS total FROM leads")[0]["total"]


leads = find_leads()
if leads:
    total = save_leads(leads)
    notify(f"Saved {len(leads)} new leads ({total} in total).")
else:
    notify("No new leads this week.")
```

## Helpers

- `inputs`: this run's typed values, by name.
- `run_folder`: this run's own folder. Write outputs here.
- `@step("Title")` on a function: a label in the Plan and Runs views. The function runs normally every time; nothing is cached or retried.
- A comment on the line above an `if`, `elif`, `else` or `with` names that branch in the outline, like a step title (`# Corporate accounts`); without one the outline words the condition itself.
- `agent(name, task, apps=(...), tools=(...), browser=False, knowledge=None, scripts=(...))`: one agent turn in the run folder. `reply.ok` means the turn finished, not that the work is right, so check its files or `reply.answer_file.read_text()`. Keep task texts as module constants so the Plan can show them. `apps=("google", "slack", "github")` selects existing connected apps; `tools` selects other named tools. Continuing a conversation keeps its original app choices. `browser=True` uses the workflow's browser; `knowledge="write"` lets it write notes (reading is the default).
- `run_script(id, inputs)`: run another saved script of this Crew and get its output files.
- `ask_agent(crew, message, wait_seconds=25)`: ask an accessible Crew by its exact name or ID. `ok` means sent; use `text` only when `status == "replied"`. Pending keeps its `inbox_id` and `message_id`: no reply yet, do not resend. The wait is at most 25 seconds within the run's deadline. Use a published function when you need a tracked result.
- `call_function(crew, name, inputs)`: call another Crew's published function. A running receipt is not the answer; refresh it with `function_status(call_id)`.
- `check(name, ok, detail)`: record a check in the run. A failed check stays visible; your code decides what happens next.
- `notify(text)`: a progress line to the owner in the Crew chat.
- `send_email(to, subject, body, cc=(), html=None)` and `send_slack(route, text)`: explicit sends through the connected account or Slack route. Optional CC and email-safe HTML use the same selected mail account and recipient rules. Pass an empty plain body for HTML-only mail. Only send when the workflow's job is to reach people.
- `from agentworks import google, slack, github`: call connected apps directly from Python, with the same accounts and permissions. `google.sheets.read(id, range)`, `.write(id, range, rows)` and `.append(...)`; `google.gmail.search(query)`; `google.calendar.list(start, end)` and `.create(title, start, end)`; `google.drive.read(doc_id)` saves Google Doc text in this run. `slack.read(route)` reads channel messages; `.send(route, text)` and `.reply(route, thread_ts, text)` use the tracked sender. `github.files(repo)`, `.read(repo, path)`, `.issues(repo)`, `.prs(repo)`, `.create_issue(repo, title, body)`, `.comment(repo, number, body)`, `.create_pr(repo, head, base, title)` and `.diff(repo, number)` use the Crew's GitHub connection. JSON results keep the service's shape; failures raise `AutomationError`. Sends and app calls do real work, including in mocked-agent runs.

For a script that needs a service key, add `"secrets": ["API_KEY"]` to its `script.json`, then read it in `main.py`:

```python
from agentworks import secret
api_key = secret("API_KEY")  # Pass to the service's client; never print or save it.
```

## Rules

- **Branches are Python.** Use `if`/`elif` where an old workflow used routes.
- **Repair once.** When an agent's output fails a check, send the error once to the same agent (`conversation_id=reply.conversation_id`), check again, and fail plainly if it still fails.
- **Python and agent shells.** The parent workflow and platform-run scripts can import `agentworks` and `agentworks_db`. Native shells inside `agent()` do not have that helper environment. Agents use the registered `database` tool (`action="query"` or `"mutate"`), or write artifacts for the parent to check and persist. Do not ask an agent to run a helper that imports either module.
- **Fresh Crew files.** Parent Python can use `from agentworks import files` and `files.read("knowledgebase/notes/owner-handled.md")` for current text from this Crew. Every call reads again; do not derive a live knowledge path from the frozen `WORKFLOW_CODE_ROOT` or copy a note into the run as its source of truth. Paths are relative to this Crew's Files, text is capped at 256 KiB, and private files, other Crews and symlinks are refused. This adds no shell grant; native agents keep their file tools.
- **One database per Crew.** In parent Python or platform-run scripts, use `agentworks_db` (`query`, `execute`, `transaction`). Create tables with `CREATE TABLE IF NOT EXISTS` and indexes with `CREATE INDEX IF NOT EXISTS`; ALTER and DROP are refused.
- **Durable files** that later runs need go under `WORKFLOW_DATA_ROOT`; everything else stays in `run_folder`.
- **Check command output.** For a Docker command that must return output, require both exit code zero and nonempty output. Empty output is a failed check, not evidence that a database has no rows. Record the return code and stdout/stderr byte counts without secret values.
- **Nobody is asked mid-run.** A run never waits for a person. A missing value fails the run with a clear reason.
- **The run record is the summary.** Do not write a separate run summary; Activity records every outcome. Use `notify` for the owner and the send helpers only for people the workflow exists to reach.
- **Mocked test runs.** Use this only when the installation's current start schema offers `mock_agents`; otherwise do not claim a mocked run. Put canned agent replies in `code/agent_mocks.json` as `{"Agent name": {"text": "...", "files": {"leads.json": "..."}}}` and start a run with mocked agents. Only the agents are mocked: scripts, databases, sends and app calls are real, and an agent missing from the file fails the run.

Old plan-based workflows must be converted to Python before they can run.

## Converting an old workflow

Read the current source, inputs, callers, selected resources and an actual failed
run before converting. Keep the workflow identity and business database. Write
one `main.py` with ordinary Python branches and `@step` labels, retaining business
rules, required files, SQL checks and real notifications. A retired run-summary
tool may also have delivered a business email or Slack message: preserve that
send explicitly through the current helpers. Do not keep a plan runner or read
`planning/plan.json`; record unreachable old nodes rather than inventing routes.

- **Frozen code, durable data.** In parent Python, resolve packaged helpers from
  `Path(__file__).resolve().parent`. Native agent shells receive the backend's
  frozen `WORKFLOW_CODE_ROOT`. Never point either at mutable author files or
  broaden shell access to repair a path. Durable records use
  `WORKFLOW_DATA_ROOT`; current outputs use `run_folder` / `STEP_OUTPUT_DIR`.
  Replace old `runs/<iteration>/<group>/execution/<step>` parsing and historical
  output scans with explicit typed inputs and current-run files.
- **Run identity.** When a native helper needs the producing run's identity,
  write a small nonsecret context file into `run_folder` before calling it.
  Read it through `STEP_OUTPUT_DIR` and validate against the current
  `run_metadata.json`. Do not assume parent-only environment variables are
  exported to native agents, recreate old step-session IDs, or call a provenance
  revision a full source hash.
- **Resources.** Keep existing app/account choices and installed task skills.
  Remove retired `builder-reference`; it is Builder guidance, not an execution
  skill. Agent knowledge choices are `"read"` and `"write"`, never `"read-write"`.
  Preserve an explicit provider/model with `model="provider/model-id"` when
  needed; do not pass `tier` with `model`. Optional `reasoning="low"`,
  `"medium"` or `"high"` overrides that call’s effort only when its model offers
  the level; unsupported choices refuse by name. Follow-ups retain it. Carry old
  explicit effort choices onto their matching calls. Use `timeout_seconds` for an intended
  agent deadline, not `timeout`; omit it to use the parent deadline.
- **Inputs and callers.** Put choices under `schema.enum`. Saved defaults and
  preset values are strings; calls and raw payload mappings use typed JSON.
  Map old routes to declared inputs using each caller's actual ID. Give optional
  mapped fields a deliberate typed default when absence should be accepted;
  check both a missing field and an invalid supplied value. Save input/caller
  changes together through the canonical writer as described below. Preserve
  caller authentication, enabled state and schedule timing.
- **Agent-run helpers.** Keep `agentworks` and `agentworks_db` imports in parent Python or platform-run scripts, never in helpers an agent must launch from its native shell. Preserve each helper's business work: have the agent use the `database` tool, or return files that the parent validates and stores.
- **Managed database.** Replace retired `query_workflow_db` and
  `mutate_workflow_db` calls with `database` and `action="query"` / `"mutate"`,
  or use `agentworks_db`. Keep SQL, parameters and paging intact, check mutation
  receipts and read back the affected records. Never open the database file.
- **Real acceptance.** Check `reply.ok`, the full final answer and the required
  artifacts; completed transport alone is not task success. Repair invalid agent
  output once in the same conversation without repeating completed external
  actions. Verify one authorized entry through the actual runtime, then its next
  turn and dependent script. Keep account/browser refusals, partial collection
  and unavailable measurements honest; do not weaken guards to get a green run.

### Calling from timers, webhooks and Slack

Use one static saved input preset plus fixed inputs for each caller. For a raw
webhook body use `payload_mappings.inputs` with declared names selected in
`allowed_variables`, and a fixed dotted `source` path per name. JSON types are
preserved; optional `values`/`default` map body choices to typed values. Do not
let a payload select a preset or replace fixed inputs. Slack event mappings use
the same input validator. Timers supply their saved typed values directly.

When editing `script.json`, defaults in input `value` and saved preset `values`
are strings (including `"2"`, `"false"` and JSON text for objects). Calls use
actual JSON types. Use canonical source/input tools so the server checks them.


### Change inputs and their callers together

If an input rename, type or schema change affects a saved function or webhook,
include `entry_mappings` in the same `workflow action=save_inputs` call, or in
`workspace action=automation_write` / `crew_workflow_files action=write` when
writing `script.json`. Do not temporarily clear a binding or edit storage files.
Read the current entries first; use their existing IDs. The server validates
the complete result before saving it through the shared managed writer.

```json
{
  "entry_mappings": {
    "functions": {
      "saved-function-id": {
        "inputs": ["scenario", "probe_count"],
        "input_mapping": {
          "mode": "raw",
          "variables": ["scenario", "probe_count"],
          "payload": {"inputs": {
            "scenario": {"source": "$.scenario", "default": "full"},
            "probe_count": {"source": "$.probe.count"}
          }}
        }
      }
    },
    "webhooks": {
      "saved-webhook-id": {
        "input": {"input_preset": "Saved", "inputs": {"region": "USA"}}
      }
    }
  }
}
```

Only include fields being changed: `inputs` selects function caller names;
`input.input_preset` and `input.inputs` set the preset and fixed values;
`input_mapping` replaces an existing bound function or webhook mapping. Ordinary
functions use `inputs` and do not acquire a bound mapping. Webhook selections
live in `input_mapping.variables`. Omitted entries/fields stay unchanged. This
cannot change identity, callers, authentication, ownership, enabled state or
timers. `functions action=define` can also update an existing bound function's
input fields while keeping its saved action and caller.

Put constraints such as `enum` inside each input's `schema`. Defaults in `value`
and fixed input values remain strings; mapped choices/defaults use typed JSON.
A missing raw payload source uses the mapping's default first, then the script
input's declared `value`; a supplied invalid value still refuses. An input with
neither default is named in the refusal. A mapped payload cannot choose a preset
or replace fixed inputs.

Failed runs keep native agent conversation history for 72 hours. After that, a continued agent call starts fresh and the run records “conversation expired: this call restarts fresh”. Completed or stopped runs release that history immediately.
