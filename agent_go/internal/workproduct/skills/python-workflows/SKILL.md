---
name: python-workflows
description: Read before creating or changing a workflow in a Crew. New workflows are Python scripts (script.json plus main.py with the agentworks helpers); covers inputs, presets, agents, checks, the database, durable files and mocked test runs.
---

# Write a workflow as a Python script

A new workflow is a Python script: a `script.json` that declares its inputs and outputs, and a `main.py` that does the work with the `agentworks` helpers. There is no plan file to build. Ordinary Python decides the order: functions, `if`/`elif`, loops.

Create it with `crew_workflow_files action=create kind=script`, then write `script.json` and `main.py` with the same tool's `write` action. Run it with `functions action=run_trigger`, then read the result with `functions action=trigger_status`.

## External MCP and CLI

An external MCP agent uses `workspace action=automation_create/read/write` with
`crew_id`; `read` and `write` also take the returned `workspace_path` and `file`
(root `script.json`, `main.py` or a nested Python path). Use `workflow action=list/get`
for script kind, typed inputs and presets; `workflow action=save_inputs` replaces
`input_presets` and optionally `inputs` through the same validated editor.
`workflow action=plan view=outline` inspects saved source without running it and
keeps Builder source-editor authority. `@step` labels are not independently runnable.

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
- `agent(name, task, apps=(...), tools=(...), browser=False, knowledge=None, scripts=(...))`: one agent turn in the run folder. `reply.ok` means the turn finished, not that the work is right, so check its files or `reply.answer_file.read_text()`. Keep task texts as module constants so the Plan can show them. `apps=("google", "slack", "github")` selects existing connected apps; `tools` selects other named tools. Continuing a conversation keeps its original app choices. `browser=True` uses the workflow's browser; `knowledge="write"` lets it write notes (reading is the default).
- `run_script(id, inputs)`: run another saved script of this Crew and get its output files.
- `call_function(crew, name, inputs)`: call another Crew's published function. A running receipt is not the answer; refresh it with `function_status(call_id)`.
- `check(name, ok, detail)`: record a check in the run. A failed check stays visible; your code decides what happens next.
- `notify(text)`: a progress line to the owner in the Crew chat.
- `send_email(to, subject, body)` and `send_slack(route, text)`: explicit sends through the connected account or Slack route. Only send when the workflow's job is to reach people.
- `from agentworks import google, slack, github`: call connected apps directly from Python, with the same accounts and permissions. `google.sheets.read(id, range)`, `.write(id, range, rows)` and `.append(...)`; `google.gmail.search(query)`; `google.calendar.list(start, end)` and `.create(title, start, end)`; `google.drive.read(doc_id)` saves Google Doc text in this run. `slack.read(route)` reads channel messages; `.send(route, text)` and `.reply(route, thread_ts, text)` use the tracked sender. `github.files(repo)`, `.read(repo, path)`, `.issues(repo)`, `.prs(repo)`, `.create_issue(repo, title, body)`, `.comment(repo, number, body)`, `.create_pr(repo, head, base, title)` and `.diff(repo, number)` use the Crew's GitHub connection. JSON results keep the service's shape; failures raise `AutomationError`. Sends and app calls do real work, including in mocked-agent runs.

For a script that needs a service key, add `"secrets": ["API_KEY"]` to its `script.json`, then read it in `main.py`:

```python
from agentworks import secret
api_key = secret("API_KEY")  # Pass to the service's client; never print or save it.
```

## Rules

- **Branches are Python.** Use `if`/`elif` where an old workflow used routes.
- **Repair once.** When an agent's output fails a check, send the error once to the same agent (`conversation_id=reply.conversation_id`), check again, and fail plainly if it still fails.
- **One database per Crew.** Use `agentworks_db` (`query`, `execute`, `transaction`). Create tables with `CREATE TABLE IF NOT EXISTS` and indexes with `CREATE INDEX IF NOT EXISTS`; ALTER and DROP are refused.
- **Durable files** that later runs need go under `WORKFLOW_DATA_ROOT`; everything else stays in `run_folder`.
- **Nobody is asked mid-run.** A run never waits for a person. A missing value fails the run with a clear reason.
- **The run record is the summary.** Do not write a separate run summary; Activity records every outcome. Use `notify` for the owner and the send helpers only for people the workflow exists to reach.
- **Mocked test runs.** Use this only when the installation's current start schema offers `mock_agents`; otherwise do not claim a mocked run. Put canned agent replies in `code/agent_mocks.json` as `{"Agent name": {"text": "...", "files": {"leads.json": "..."}}}` and start a run with mocked agents. Only the agents are mocked: scripts, databases, sends and app calls are real, and an agent missing from the file fails the run.

Existing plan-based workflows keep working until they are converted; edit them with the guide in `references/crew-plan/SKILL.md`.
