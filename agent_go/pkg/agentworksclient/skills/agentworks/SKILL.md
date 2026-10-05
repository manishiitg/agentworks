---
name: agentworks
description: Use AgentWorks workflows, Crews and shared Knowledge Base through MCP (list workflows, read files, plans, runs, guidance, and knowledge; execute steps, workflows, and schedules; ask Crews and call their functions). Load when the task touches an AgentWorks workflow or when AgentWorks MCP tools are available.
---

# AgentWorks

This skill is an entry pointer, not a manual. All substantive guidance lives on the server and is fetched per task — nothing here can go stale.

Use only the tools and actions exposed by this connection. Workflow runs use pinned Run-mode sessions; Builder, Crew edits and Knowledge Base updates require the corresponding permissions.

## Connect

It is a standard MCP server (Streamable HTTP), usable from any MCP client. Add it to the client you are running in, not another one: a Codex agent running `claude mcp add` configures Claude Code, not itself.

```sh
# Claude Code
claude mcp add --transport http agentworks 'https://your-server/api/external/v1/mcp'
# Codex
codex mcp add agentworks --url 'https://your-server/api/external/v1/mcp'
codex mcp login agentworks
# Cursor: add {"mcpServers": {"agentworks": {"url": "<url>"}}} to ~/.cursor/mcp.json, then
cursor-agent mcp login agentworks
# Muse: add the same mcpServers entry to ~/.config/muse/settings.json, then
muse mcp login agentworks
```

For a local single-user instance, open the global Connect page and create the `agentworks-local` token. It includes your account's available access and remains valid until removed. Configure `Authorization: Bearer <ACCESS_TOKEN>` in your client's HTTP MCP headers; use the endpoint shown in Connect. Keep the token out of skill files, URLs, source control and chat messages. Hosted/multi-user servers use browser OAuth approval instead.

Other clients: add the same URL as a remote (streamable HTTP) MCP server. Its scopes allow reading (`workflows:read`, `files:read`) and running (`runs:execute`) workflows the account can access, reading (`crews:read`), asking or calling (`crews:run`), and creating and editing (`crews:write`) its Crews. Admins and Code reviewers may also approve `code:review`: read-only, audited review of every Code workspace. The remote MCP surface has `get_api_spec` to discover available tool names and schemas, then `call_tool` to invoke one by name. Unavailable tools are omitted from the catalog.

## First step

Use `get_api_spec` to inspect the available tools, then call `get_agent_context` through `call_tool` for your capabilities and guidance version. Discover workflow IDs with `list_workflows` first — IDs are never filesystem paths.

## Guidance per task

List topics with `list_guidance_topics` and load only relevant ones via `get_guidance_topic`. Inspect workflow knowledge with `list_workflow_knowledge` / `read_workflow_knowledge` (learnings, knowledgebase notes, workspace skills, skill wiring). Use `get_file_link` for preview/download URLs.

## Shared Knowledge Base

Discover the schemas through `get_api_spec`, then invoke actions through `call_tool`:
- `browse_knowledgebase`: `folders` / `entries` for accessible skills, facts, notes and sources.
- `read_knowledgebase`: `read` / `search`; read the current version before changing content.
- `update_knowledgebase`: `create` / `update` / `delete` / `create_folder`. Use diff patches for large files, `expected_version` for updates/deletes and stable `request_id` values. Saves become readable immediately.
- `backup_knowledgebase`: `status`, then explicitly requested `commit` and `push`. Commit selected versions; push the returned receipt using a different request ID. Keep receipts for safe retries.
- `manage_knowledgebase_access`: `inspect`. Writable unrestricted external connections also expose `list`, `grant`, `revoke`, `create_service_account` and `disable_service_account`. Owners manage their folder grants; service-account administration requires an administrator. Inspect first and use the current `expected_acl_version` plus a stable `request_id` for grant/revoke. Changes apply directly; app chat uses its separate confirmation flow.

Read-only, folder-scoped and managed workflow/Crew connections cannot administer access. Folder grants remain authoritative. Project binding actions remain in the app's access builder. Never treat a content edit as permission to change access, migrate a project or publish a Git backup. Unavailable tools/actions are omitted from the connection's catalog. Do not substitute legacy workflow knowledge files for the shared Knowledge Base.

## Run

To run: call a run-mode tool such as `execute_step` — the reply carries `session_id` — then poll `run_status` for completion. Steer live work with `send_step_message`, stop it with `stop_step` / `stop_all_executions`, and read run evidence with `list_runs`, `get_run`, and `get_logs`. Schedules: `list_schedules`, `get_schedule_runs`, `trigger_schedule`.

## Chat

`chat` asks the workflow assistant anything — analysis, explanations, follow-ups — in a pinned Run-mode session. Pass `session_id` to continue the conversation; sessions are shared with the run tools, so one conversation can ask, run, and ask about the run. Read replies with `run_status`, and answer waiting human-input steps with `run_reply_input`.

## Crews

Workflows expose typed **functions** (their Builder defines them): `list_workflow_functions` shows each one's inputs, and `call_workflow_function` runs it. Inputs are checked first, so a missing, unknown or mistyped input is refused before anything runs; pass every required input and never a free-text task. It returns at once with `status: running` and a `call_id`; poll `get_workflow_function_call` for the run's outcome (status, error, each step's output). Pass `wait_seconds` (max 25) only for a function you expect to finish quickly. Every workflow also offers `ask` (function `ask`, args `{message}`): it reaches the workflow's Run-mode assistant in one continuing thread per user, which answers questions and starts runs with the right variables itself. Needs `runs:execute` and edit access to the workflow. To ask a workflow's owner for a change instead, use `suggest_workflow_change` (any user with access, including read-only): it lands in the owner's decisions panel and changes nothing by itself.

Crews are persistent AgentWorks agents. Discover them with `list_crews` (IDs, never paths); `get_crew` shows identity, model, and functions. Read project files with `list_crew_files` / `read_crew_file` (private chat transcripts and databases are never exposed); find a file by name with `list_crew_files`' `glob` (e.g. `**/*CHECKLIST*`, with `depth` up to 8) or by content with `search_crew_files` instead of paging the whole listing. Call a Crew's typed functions with `call_crew_function` (arguments must match `list_crew_functions`), or ask anything with `ask_crew`. `ask_crew` is your own message in your own chat of that Crew, the same chat your web chat, Slack DMs and WhatsApp continue (for a Crew you own, its main chat), so repeated asks are a chat and you see them in the app. `call_crew_function` runs in a separate conversation for your calls. Functions are agentic and usually take minutes: a call returns at once with `status: running` and a `call_id`, then poll `get_crew_function_call` for progress and the result (pass `wait_seconds`, max 25, only for a quick one). Never call again for the same work: repeating an identical call while it runs returns the same `call_id`. Needs `crews:read` / `crews:run` on a token that includes the Crew. To ask the owner of a Crew you use for a change, use `suggest_crew_change` (`crews:run`); the owner reviews it in the Crew's Suggestions view. To author, `create_crew` makes a Crew you own from a spec (name, icon, role, purpose, skills, functions, schedules, files), `update_crew` edits one you own section by section, and `export_crew` / `import_crew` move a Crew between accounts or servers as a portable spec (needs `crews:write`; only the owner edits).

Code review (`code:review`; admins and Code reviewers only, re-checked on every call): `list_code_workspaces` lists every user's Code workspaces (owner, ID, sharing); `get_code_costs` gives each Code's cost and tokens by person and model for a `from`/`to` range; `list_code_files` / `read_code_file` and `list_code_chats` / `read_code_chat` read a workspace's files and chats; `get_code_audit` reads the review log. Everything is read-only and every call, lists included, is recorded in the audit log with the token that made it.

## Answer from reading

If the task needs a change, say so instead of attempting one — authoring is not exposed.

Administrators may configure an initial Git backup with `manage_knowledgebase_access`
`action=configure_backup`, the user's exact SSH `remote_url`, optional `branch`
(default `main`), and stable `request_id`. Never invent a destination or ask for
credentials. Setup is private and durable; it cannot redirect an existing backup
or perform commit/push. The server must already have SSH repository access.

For explicitly requested repository-wide Git work, use `backup_knowledgebase(action=git, op=...)`. Root Reader permits repository history/diff; unrestricted root Editor permits staging, commit/push, pull, branches and stashes. Pull and checkout update live knowledge and require a clean tree; stash/discard also affect live content. Preserve the original request ID when retrying an uncertain push. Scoped or managed workflow/Crew connections keep selected-version receipt backups. Never infer a Git push or destructive restore from a content edit.

## Vault management

When `get_api_spec` lists `manage_vault_access`, the connection has `vault:manage` and the account is a current Vault administrator. These global tools need no `workflow_id`:

- `manage_vault_access`: inspect connections/users/tool schemas; connect, sign in, sync or disconnect an MCP; apply tool permissions and regex rules immediately. Regex conditions require a human-readable description.
- `manage_vault_groups`: list/create/update groups and list/add/remove active platform members. Does not create platform accounts or provision product slots.
- `manage_vault_secret_access`: list secret names and grant/revoke a group's access. Values are never accepted or returned. Add/rotate values in Vault's secure Secrets UI.

Use the separate Vault MCP connection to execute upstream tools with the caller's live group/tool permissions. Management does not grant a runtime bypass.
