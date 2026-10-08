---
name: agentworks
description: Use AgentWorks workflows, Crews and shared Brain through MCP (list workflows, read files, plans, runs, guidance, and knowledge; execute steps, workflows, and schedules; ask Crews and call their functions). Load when the task touches an AgentWorks workflow or when AgentWorks MCP tools are available.
---

# AgentWorks

This skill is an entry pointer, not a manual. All substantive guidance lives on the server and is fetched per task — nothing here can go stale.

Use only the tools and actions exposed by this connection. Workflow runs use pinned Run-mode sessions; Builder, Crew edits and Brain updates require the corresponding permissions.

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

## Shared Brain

Discover the schemas through `get_api_spec`, then invoke actions through `call_tool`:
- `brain_browse`: `folders` / `entries` for accessible skills, facts, notes and sources.
- `brain_read`: `read` / `search`; read the current version before changing content.
- `brain_update`: `create` / `update` / `delete` / `create_folder`. Use diff patches for large files, `expected_version` for updates/deletes and stable `request_id` values. Saves become readable immediately.
- `brain_skills`: company skills. `list` finds skills you can read; `get` returns one skill's files: install it by writing each file under your own skills folder as `.claude/skills/<name>/<path>` (Claude Code) or `.agents/skills/<name>/<path>` (Codex, Cursor), decoding `content_base64`; `publish` uploads a skill package (`SKILL.md` plus `references/`, `scripts/`, assets) into a Brain folder and replaces its previous files (Editor; a skill with scripts needs Owner). Re-run `get` to update.
- Files of any type (images, PDF, PPTX, XLSX; not programs) can be stored: send text in `content`, anything else in `content_base64`.
- `brain_access`: `inspect`. Writable unrestricted external connections also expose `list`, `grant`, `revoke`, `create_service_account` and `disable_service_account`. Owners manage their folder grants; service-account administration requires an administrator. Inspect first and use the current `expected_acl_version` plus a stable `request_id` for grant/revoke. Changes apply directly; app chat uses its separate confirmation flow.

Read-only, folder-scoped and managed workflow/Crew connections cannot administer access. Folder grants remain authoritative. Authorized project Owners with Builder/Crew permission can use `brain_access` actions `inspect_project` and `set_project_access` (off, read, write), with the current `expected_manifest_version` and stable `request_id`; this never grants folder access, and steps say in their descriptions which folders they use. Never treat a content edit as permission to change access, migrate a project or publish a Git backup. Unavailable tools/actions are omitted from the connection's catalog. Do not substitute legacy workflow knowledge files for the shared Brain.

## Run

To run: call a run-mode tool such as `execute_step` — the reply carries `session_id` — then poll `run_status` for completion. Steer live work with `send_step_message`, stop it with `stop_step` / `stop_all_executions`, and read run evidence with `list_runs`, `get_run`, and `get_logs`. Schedules: `list_schedules`, `get_schedule_runs`, `trigger_schedule`.

## Chat

`chat` asks the workflow assistant anything — analysis, explanations, follow-ups — in a pinned Run-mode session. Pass `session_id` to continue the conversation; sessions are shared with the run tools, so one conversation can ask, run, and ask about the run. Read replies with `run_status`, and answer waiting human-input steps with `run_reply_input`.

## Crews

Workflows expose typed **functions** (their Builder defines them): `list_workflow_functions` shows each one's inputs, and `call_workflow_function` runs it. Inputs are checked first, so a missing, unknown or mistyped input is refused before anything runs; pass every required input and never a free-text task. It returns at once with `status: running` and a `call_id`; poll `get_workflow_function_call` for the run's outcome (status, error, each step's output). Pass `wait_seconds` (max 25) only for a function you expect to finish quickly. Every workflow also offers `ask` (function `ask`, args `{message}`): it reaches the workflow's Run-mode assistant in one continuing thread per user, which answers questions and starts runs with the right variables itself. Needs `runs:execute` and edit access to the workflow. To ask a workflow's owner for a change instead, use `suggest_workflow_change` (any user with access, including read-only): it lands in the owner's decisions panel and changes nothing by itself.

Crews are persistent AgentWorks agents. Discover them with `list_crews` (IDs, never paths); `get_crew` shows identity, model, and functions. Read project files with `list_crew_files` / `read_crew_file` (private chat transcripts and databases are never exposed); find a file by name with `list_crew_files`' `glob` (e.g. `**/*CHECKLIST*`, with `depth` up to 8) or by content with `search_crew_files` instead of paging the whole listing. Call a Crew's typed functions with `call_crew_function` (arguments must match `list_crew_functions`), or ask anything with `ask_crew`. `ask_crew` is your own message in your own chat of that Crew, the same chat your web chat, Slack DMs and WhatsApp continue (for a Crew you own, its main chat), so repeated asks are a chat and you see them in the app. `call_crew_function` runs in a separate conversation for your calls. Functions are agentic and usually take minutes: a call returns at once with `status: running` and a `call_id`, then poll `get_crew_function_call` for progress and the result (pass `wait_seconds`, max 25, only for a quick one). Never call again for the same work: repeating an identical call while it runs returns the same `call_id`. Needs `crews:read` / `crews:run` on a token that includes the Crew. To ask the owner of a Crew you use for a change, use `suggest_crew_change` (`crews:run`); the owner reviews it in the Crew's Suggestions view. To author, `create_crew` makes a Crew you own from a spec (name, icon, role, purpose, skills, functions, schedules, files), `update_crew` edits one you own section by section, and `export_crew` / `import_crew` move a Crew between accounts or servers as a portable spec (needs `crews:write`; only the owner edits).

Code review (`code:review`; admins and Code reviewers only, re-checked on every call): `list_code_workspaces` lists every user's Code workspaces (owner, ID, sharing); `get_code_costs` gives each Code's cost and tokens by person and model for a `from`/`to` range; `list_code_files` / `read_code_file` and `list_code_chats` / `read_code_chat` read a workspace's files and chats; `get_code_audit` reads the review log. Everything is read-only and every call, lists included, is recorded in the audit log with the token that made it.

Shared-account token limits: `get_token_usage` (`code:review` with an admin or Code reviewer account, or `users:manage` with an admin account) shows each active person's tokens on the server's shared accounts today and this week (UTC, weeks start Monday) against their `daily_limit` / `weekly_limit` (0 = unlimited), with `state` ok, warning or over and the reset times; pass `user_id` or `email` for one person, or `from` / `to` (YYYY-MM-DD) for range totals. `set_token_limits` (`users:manage`, administrators only) sets a person's `daily` and/or `weekly` limit (0 or null = unlimited, omitted = unchanged) and returns the new limits with current usage. Each shared account also has its own per-person limit: usage includes `accounts` (per provider: use, effective limits, `default_limits`, the person's `override`) and `account_defaults`; pass `account` (e.g. `codex-cli`) to read one account, to set a person's override on it, or, with no person, the account's default for everyone. The top-level limit stays an overall cap across all shared accounts. Allowed models sit next to the limits: usage includes `account_allowed_models` (each shared account's model list; null = all models) and, per person, `allowed_models` (per account that limits them: `models`, their effective list, and `source` `account` or `person`). `set_allowed_models` (`users:manage`, administrators only) takes `account` and `models`: with no person it sets the account's list (null or [] = all models); with `user_id` or `email` it sets that person's override (a list replaces the account's for them, null clears it, `["*"]` or `all_models: true` = every model). Model ids are checked against the provider's model list. A turn on a model that is not allowed runs on the first allowed one. All are recorded in the Code review audit log.

## Workflow creation and Builder

When `create_workflow` appears in `get_api_spec`, invoke it through `call_tool` with `folder_name` (kebab-case), `workflow_json` (`schema_version`, unique `id`, `label`) and `plan_json` (a valid non-empty steps graph). It reuses the app creator and returns `workflow_id`; the new workflow belongs to the authenticated user. Account creation rights and unrestricted `builder:chat` permission are required. The local `agentworks-local` Owner token qualifies when Builder is enabled. Existing folders and IDs are never overwritten.

Creation writes structure only. Use `builder_chat` with the returned ID to author/test scripted-step code before running; poll `builder_status`, answer questions with `builder_reply_input`, and reuse `submission_id` for uncertain Builder delivery. Set Brain access through `brain_access` actions `inspect_project` / `set_project_access` using that ID; steps name the Brain folders they use in their descriptions. Creation cannot preconfigure host-folder grants.

## Answer from reading

Use only authoring operations shown in this connection’s catalog. If the needed operation is absent, describe or suggest the change.

Administrators may configure an initial Git backup with `brain_access`
`action=configure_backup`, the user's exact HTTPS `remote_url`, `username`, optional `pat` and `branch`
(default `main`), and stable `request_id`. Never invent a destination or ask for
credentials in app chat: use its secure confirmation field for the optional PAT.
Setup is private and durable; it cannot redirect an existing backup or perform
commit/push. KB encrypts the PAT in its own private storage, with no Vault
dependency. Omit `pat` to retain it or send an empty string to remove it. SSH URLs
require deployment configuration and use host SSH credentials; app/MCP setup is HTTPS only.

Git backup is not a tool: people who own the whole Brain run git in Brain's folder from the Brain chat.

## Vault management

When `get_api_spec` lists `manage_vault_access`, the connection has `vault:manage` and the account is a current Vault administrator. These global tools need no `workflow_id`:

- `manage_vault_access`: inspect connections/users/tool schemas; connect, sign in, sync or disconnect an MCP; apply tool permissions and regex rules immediately. Regex conditions require a human-readable description.
- `manage_vault_groups`: list/create/update groups and list/add/remove active platform members. Does not create platform accounts or provision product slots.
- `manage_vault_secret_access`: list secret names and grant/revoke a group's access. Values are never accepted or returned. Add/rotate values in Vault's secure Secrets UI.

- `query_vault_db` / `mutate_vault_db`: inspect or atomically change allowlisted governance tables through the gateway. Never supply a database path.
- `list_vault_mcp_servers` / `call_vault_mcp_tool`: discover active approved connections and perform administrator setup lookups to resolve resource IDs before writing rules. Upstream mutations require an explicit user request.

Vault's `product.yaml` declares this same management tool surface for the builder and platform MCP. These setup calls use administrator authority independently of group grants; ordinary product calls and the separate Vault runtime MCP endpoint continue to enforce user/group/tool/regex permissions. Secret values and other users' private connections remain unavailable.
