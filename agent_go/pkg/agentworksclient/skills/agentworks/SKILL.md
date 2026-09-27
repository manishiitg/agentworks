---
name: agentworks
description: Read and run AgentWorks workflows and Crews through MCP (list workflows, read files, plans, runs, guidance, and knowledge; execute steps, workflows, and schedules; ask Crews and call their functions). Load when the task touches an AgentWorks workflow or when AgentWorks MCP tools are available.
---

# AgentWorks

This skill is an entry pointer, not a manual. All substantive guidance lives on the server and is fetched per task — nothing here can go stale.

This connection reads and runs, like the Slack and WhatsApp run-mode channels: tools read, and run-mode tools execute in pinned Run-mode sessions. Nothing creates, edits, or authors.

## Connect

```sh
claude mcp add --transport http agentworks 'https://your-server/api/external/v1/mcp'
```

Approve the MCP connection in your browser. Its scopes allow reading (`workflows:read`, `files:read`) and running (`runs:execute`) workflows the account can access, reading (`crews:read`), asking or calling (`crews:run`), and creating and editing (`crews:write`) its Crews. The remote MCP surface has `get_api_spec` to discover available tool names and schemas, then `call_tool` to invoke one by name. Unavailable tools are omitted from the catalog.

## First step

Use `get_api_spec` to inspect the available tools, then call `get_agent_context` through `call_tool` for your capabilities and guidance version. Discover workflow IDs with `list_workflows` first — IDs are never filesystem paths.

## Guidance per task

List topics with `list_guidance_topics` and load only relevant ones via `get_guidance_topic`. Inspect workflow knowledge with `list_workflow_knowledge` / `read_workflow_knowledge` (learnings, knowledgebase notes, workspace skills, skill wiring). Use `get_file_link` for preview/download URLs.

## Run

To run: call a run-mode tool such as `execute_step` — the reply carries `session_id` — then poll `run_status` for completion. Steer live work with `send_step_message`, stop it with `stop_step` / `stop_all_executions`, and read run evidence with `list_runs`, `get_run`, and `get_logs`. Schedules: `list_schedules`, `get_schedule_runs`, `trigger_schedule`.

## Chat

`chat` asks the workflow assistant anything — analysis, explanations, follow-ups — in a pinned Run-mode session. Pass `session_id` to continue the conversation; sessions are shared with the run tools, so one conversation can ask, run, and ask about the run. Read replies with `run_status`, and answer waiting human-input steps with `run_reply_input`.

## Crews

Workflows expose typed **functions** (their Builder defines them): `list_workflow_functions` shows each one's inputs, and `call_workflow_function` runs it. Inputs are checked first, so a missing, unknown or mistyped input is refused before anything runs; pass every required input and never a free-text task. The result is the run's outcome (status, error, each step's output) within `wait_seconds` (max 25), otherwise poll `get_workflow_function_call` with the returned `call_id`. Every workflow also offers `ask` (function `ask`, args `{message}`): it reaches the workflow's Run-mode assistant in one continuing thread per user, which answers questions and starts runs with the right variables itself. Needs `runs:execute` and edit access to the workflow.

Crews are persistent AgentWorks agents. Discover them with `list_crews` (IDs, never paths); `get_crew` shows identity, model, and functions. Read project files with `list_crew_files` / `read_crew_file` (private chat transcripts and databases are never exposed). Call a Crew's typed functions with `call_crew_function` (arguments must match `list_crew_functions`), or ask anything with `ask_crew`. Both run in your own continuing conversation with that Crew (one per AgentWorks user; never the Crew's main chat), so repeated `ask_crew` calls are a chat: the Crew remembers your earlier asks. The result returns within `wait_seconds` (max 25), otherwise poll `get_crew_function_call` with the returned `call_id` for progress and the result. Needs `crews:read` / `crews:run` on a token that includes the Crew. To author, `create_crew` makes a Crew you own from a spec (name, icon, role, purpose, skills, functions, schedules, files), `update_crew` edits one you own section by section, and `export_crew` / `import_crew` move a Crew between accounts or servers as a portable spec (needs `crews:write`; only the owner edits).

## Answer from reading

If the task needs a change, say so instead of attempting one — authoring is not exposed.
