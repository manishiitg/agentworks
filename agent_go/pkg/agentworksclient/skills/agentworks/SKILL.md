---
name: agentworks
description: Use AgentWorks workflows, Relays, Crews, dashboards, Brain and Vault over MCP to find workflows, read and edit files, plans and settings, run and chat, manage schedules, triggers and Pulse, answer what needs you, build dashboards, drive the Builder and Relays, ask Crews and call their functions, and manage shared Brain and Vault access. Use when the task touches an AgentWorks workflow or when AgentWorks MCP tools are available.
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

Other clients: add the same URL as a remote (streamable HTTP) MCP server. Its scopes allow reading (`workflows:read`, `files:read`) and running (`runs:execute`) workflows the account can access, reading (`crews:read`), asking or calling (`crews:run`), and creating and editing (`crews:write`) its Crews. Admins and Code reviewers may also approve `code:review`: read-only, audited review of every Code workspace. The remote MCP surface has `get_api_spec` to discover available tool names and schemas, then `call_tool` to invoke one by name. Unavailable tools and actions are omitted from the catalog.

## First step

Use `get_api_spec` to see the tools you may use (their names are also in its description), then again with names for their JSON schemas, and invoke them with `call_tool`. There is one tool per object, each with an `action` (for example `dashboard` action=publish, `files` action=read); you see only the actions your role and token allow. Older per-action names such as `list_workflows` still work but are not listed. Call `help` action=context for your capabilities and the guidance version. Discover workflow IDs with `workflow` action=list: IDs are never filesystem paths.

## Guidance per task

List topics with `help` action=topics and load only the relevant ones with action=topic. To install or refresh this skill, call `help` action=skill and save its `content` as <your skills folder>/agentworks/SKILL.md (Claude Code: ~/.claude/skills/agentworks/SKILL.md). Inspect workflow knowledge with `list_workflow_knowledge` / `read_workflow_knowledge` (learnings, knowledgebase notes, workspace skills, skill wiring). Use `get_file_link` for preview/download URLs.

## Workflows, files and data

`workflow` action=list finds workflows and Relays (compact by default; pass `compact: false` for every manifest), action=get reads one manifest with your access level, action=plan reads its plan. `files` browses and edits project files: action=list|search|read|write|code with `workflow_id` for a workflow or Relay, or `crew_id` for a Crew's project files (read only; private chats and databases are never exposed). Find a file by name with `glob` (e.g. `**/*CHECKLIST*`, `depth` up to 8) or by content with action=search instead of paging a listing; action=code lists a step's saved code. A write needs `files:write` and the file's current revision; send binary files in `content_base64` (up to 11 MiB). There is no move, rename or delete over MCP. `query_database` reads a workflow's, Relay's or Crew's database read-only (SQL, describe, integrity_check); to change data, ask the Builder.

`dashboard` builds and publishes live dashboards: action=list|get|link|create|update|validate|preview|publish|restore. Read the `dashboard-authoring` guidance topic first, pass the current `expected_revision` on every edit and publication, and note that preview also needs `runs:execute`. Links grant no access: recipients need project access.

## Run

To run, call a run-mode tool such as `execute_step`; the reply carries `session_id`. Poll `runs` action=status for completion: it returns `turn_status` (running, waiting_for_input, idle), any pending question and, once idle, `final_answer`, without events (pass `compact: false` for a size-bounded event page). `runs` action=list|get|logs reads run evidence (for a Relay, pass `version` to read a published version's runs), and action=reply_input answers a waiting human-input step. Steer live work with `send_step_message`; stop it with `stop_step` / `stop_all_executions`. Schedules: `list_schedules`, `get_schedule_runs` and `trigger_schedule` read and trigger; `manage_schedules` (`workflow_id` or `crew_id`) creates, changes, enables, disables, runs, stops and reads the history of schedules (changes need the owner; Relays use triggers instead).

## Chat

`chat` (pass `wait_seconds` up to 25 to get the reply in the same call) asks the workflow assistant anything — analysis, explanations, follow-ups — in a pinned Run-mode session. Pass `session_id` to continue the conversation; sessions are shared with the run tools, so one conversation can ask, run, and ask about the run. Read replies with `runs` action=status.

## Functions and Crews

Workflows and Crews expose typed **functions** (their Builder defines them). `functions` takes `workflow_id` or `crew_id`: action=list shows each function's inputs, action=call runs one, action=status polls a call, action=reply answers its pending question. Inputs are checked first, so a missing, unknown or mistyped input is refused before anything runs; pass every required input and never a free-text task. A call returns at once with `status: running` and a `call_id`; poll action=status for progress and the result (pass `wait_seconds`, max 25, only for a quick one). Never call again for the same work: repeating an identical call while it runs returns the same `call_id`. When a call returns `pending_inputs`, answer a listed `request_id` with action=reply. Pass a fresh `submission_id` for each new call or Crew ask, and reuse it after an uncertain delivery. To ask the owner for a change instead, use `suggest_change` (`workflow_id` or `crew_id`; any user with access, including read-only): it lands in the owner's decisions and changes nothing by itself.

Crews are persistent AgentWorks agents. `crew` action=list|get|costs|create|update|export|import: list and get show IDs (never paths), identity, model and functions; costs shows what a Crew you own has spent (total, per day, by activity and by model; omit `crew_id` for one row per Crew you own, most expensive first; `days` up to 90, `before` pages back); create makes a Crew you own from a spec, update edits one you own section by section, and export / import move a Crew between accounts or servers as a portable spec (`crews:write`; only the owner edits). Ask anything with `ask_crew`: it is your own message in your own chat of that Crew, the same chat your web chat, Slack DMs and WhatsApp continue, so repeated asks are a chat you also see in the app; pass `chat_id` to talk in a side chat. `manage_crew_chats` lists, reads, starts and deletes your chats with a Crew, opens and closes side chats, and stops a running turn. Needs `crews:read` / `crews:run` on a token that includes the Crew.

## Setup and operations

These follow your role on each workflow, Relay or Crew; use them instead of asking the Builder to change setup.
- `settings` action=get|update (`workflow_id` or `crew_id`): models per role, MCP servers and tools, skills, secrets, Pulse (on/off, autonomy 0-5, pace), browser mode, notifications and what runs after your own runs (`after_manual_run`); get also shows backup, publish and notify status. Owners and editors change workflow and Relay settings, a Crew's owner its settings; secret values are owner-only, write-only and never returned. Pass the `expected_version` from get when updating.
- `manage_triggers` (`workflow_id`, also for Relays, or `crew_id`): webhooks, function triggers and internal triggers; list first.
- `needs_you` action=list|answer: what waits on the person (live agent questions, decisions with Pulse's recommendation, suggestions); answer or dismiss one. Check it when asked what needs them.
- `manage_pulse`: Pulse status, run now, goal check now, focus areas and goal memory. Talk to a workflow's Pulse with `builder` action=pulse_chat and read its reply with action=pulse_status.
- `manage_project` renames, duplicates, deletes (permanent; `confirm` repeats the ID) and shares a workflow, Relay or Crew, and installs Crew templates.
- `run_after_run` backs up or publishes now; setup stays in the app.
- `manage_messaging` shows and routes Slack channels (dry-run tested), picks an existing bot and links the app's WhatsApp pairing page; connecting a new bot and pairing stay in the app.

## Builder and Relays

When `create_workflow` is listed, create a workflow with `folder_name` (kebab-case), `workflow_json` (`schema_version`, unique `id`, `label`) and `plan_json` (a valid non-empty steps graph). It reuses the app creator, assigns ownership to this user and returns `workflow_id`. It needs account creation rights and unrestricted `builder:chat` permission (the local `agentworks-local` Owner token qualifies when Builder is enabled). Existing folders and IDs are never overwritten. Creation writes structure only; author and test scripted-step code through the Builder before running. Set Brain access with `brain_access` actions `inspect_project` / `set_project_access` using the returned ID.

When `builder` is listed, the connection can delegate plan and code edits to the workflow's configured Builder model on workflows where you have write access. `builder` action=chat continues your existing workflow chat (the owner's main chat); send a unique `submission_id` with each new request and reuse it to retry an uncertain delivery (never resend the same edit with a new ID). Pass `wait_seconds` (up to 25) to action=chat and action=status to get the answer or a pending question in the same call; on queued/running call action=status again with `operation_id`. Answer that operation's questions with action=reply_input and cancel only that operation with action=cancel. action=file_history and action=restore_file read and undo edits. Native shell and account tools are unavailable to this Builder mode.

`relay` action=create|update|test|publish|run|get_run|releases builds and runs Relays: create one, edit its code with `builder` action=chat, test the draft, publish an immutable version, then run a published version (reuse `idempotency_key` on retries) and read its runs and releases.

If Builder is absent, describe or suggest the needed workflow change; do not attempt an unavailable authoring operation.

## Shared Brain

Discover the schemas through `get_api_spec`, then invoke actions through `call_tool`:
- `brain_browse`: `folders` / `entries` for accessible skills, facts, notes and sources, and `changes` for what changed since a commit.
- `brain_read`: `read` / `search` / `diff`; read the current version before changing content.
- `brain_update`: `create` / `update` / `delete` / `move` / `restore` / `create_folder`. `move` moves or renames an entry and keeps its ID and history (Editor on both folders); `restore` saves an earlier commit's content as the next version. Use diff patches for large files, `expected_version` for changes and stable `request_id` values. Saves become readable immediately. Folders are not moved or deleted over MCP.
- `brain_skills`: company skills. `list` finds skills you can read; `get` returns one skill's files: install it by writing each file under your own skills folder as `.claude/skills/<name>/<path>` (Claude Code) or `.agents/skills/<name>/<path>` (Codex, Cursor), decoding `content_base64`; `publish` uploads a skill package (`SKILL.md` plus `references/`, `scripts/`, assets) into a Brain folder and replaces its previous files (Editor; a skill with scripts needs Owner). Re-run `get` to update.
- Files of any type (images, PDF, PPTX, XLSX; not programs) can be stored: send text in `content`, anything else in `content_base64`.
- `brain_access`: `inspect`. Writable unrestricted external connections also expose `list`, `grant`, `revoke`, `create_service_account` and `disable_service_account`. Owners manage their folder grants; service-account administration requires an administrator. Inspect first and use the current `expected_acl_version` plus a stable `request_id` for grant/revoke. Changes apply directly; app chat uses its separate confirmation flow.

Read-only, folder-scoped and managed workflow/Crew connections cannot administer access. Folder grants remain authoritative. Authorized project Owners with Builder/Crew permission can use `brain_access` actions `inspect_project` and `set_project_access` (off, read, write) with the current `expected_manifest_version` and a stable `request_id`; this never grants folder access. Never treat a content edit as permission to change access or migrate a project. Do not substitute legacy workflow knowledge files for the shared Brain.

Backup is automatic: every save is a Git commit by the person who made it, and Brain pushes to the configured backup remote a few minutes after the last save. There is no commit or push tool. Administrators may configure the backup destination once with `brain_access` `action=configure_backup`, the user's exact HTTPS `remote_url`, `username`, optional `pat` and `branch` (default `main`), and a stable `request_id`. Never invent a destination or ask for credentials in chat: in the app use its secure confirmation field for the PAT. Omit `pat` to retain it or send an empty string to remove it. The PAT is encrypted in Brain's own private storage and never returned. SSH URLs need deployment configuration and use the host's SSH credentials.

## Code review and accounts

Code review (`code:review`; admins and Code reviewers only, re-checked on every call): `code_review` action=workspaces lists every user's Code workspaces (owner, ID, sharing); action=costs gives each Code's cost and tokens by person and model for a `from`/`to` range; action=files / file and chats / chat read a workspace's files and chats; action=audit reads the review log. Everything is read-only and every call, lists included, is recorded in the audit log with the token that made it.

Run your own Code (`code:run`, never in the default scopes; own projects only; Local-mode Code is refused; every ask is logged): `code` action=projects lists your Code projects; action=chats lists a project's chats (tabs); action=ask sends `message` into a chat (`chat_id`, default main) as a turn there and returns a `call_id`, or the reply when `wait_seconds` (up to 25) is set; poll with action=ask and `call_id`; action=state shows the project's folder guard (read and write paths), the account slot commands run as, and each chat's working flag. Use it to test what the agent can and cannot do: ask it to run a command or read a path and check the reply.

Shared-account token limits: `account` action=usage (`code:review` with an admin or Code reviewer account, or `users:manage` with an admin account) shows each active person's tokens on the server's shared accounts today and this week (UTC, weeks start Monday) against their `daily_limit` / `weekly_limit` (0 = unlimited), with `state` ok, warning or over and the reset times; pass `user_id` or `email` for one person, or `from` / `to` (YYYY-MM-DD) for range totals. action=set_limits (`users:manage`, administrators only) sets a person's `daily` and/or `weekly` limit (0 or null = unlimited, omitted = unchanged) and returns the new limits with current usage. Each shared account also has its own per-person limit: usage includes `accounts` (per provider: use, effective limits, `default_limits`, the person's `override`) and `account_defaults`; pass `account` (e.g. `codex-cli`) to read one account, to set a person's override on it, or, with no person, the account's default for everyone. The top-level limit stays an overall cap across all shared accounts. Allowed models sit next to the limits: usage includes `account_allowed_models` (each shared account's model list; null = all models) and, per person, `allowed_models` (per account that limits them: `models`, their effective list, and `source` `account` or `person`). action=set_allowed_models (`users:manage`, administrators only) takes `account` and `models`: with no person it sets the account's list (null or [] = all models); with `user_id` or `email` it sets that person's override (a list replaces the account's for them, null clears it, `["*"]` or `all_models: true` = every model). Model ids are checked against the provider's model list. A turn on a model that is not allowed runs on the first allowed one. All are recorded in the Code review audit log.

## Vault

Vault has two levels. A Vault manager (an administrator with Vault) holds `vault:manage` and can change anything; a Vault reader (the Vault reader account flag, no admin needed) holds `vault:read` and can only look. A call gets the lower of the person's role and the connection's scope, checked on every call; tools and actions you cannot use are not listed. Access is given to groups only, never to one person (a group may have one member). These global tools need no `workflow_id`:
- `manage_vault_access`: inspect the environment (connections, tools with their read/write mark, groups), users, a group (`inspect_group`) or one person (`inspect_user`: their groups, every tool they can call and which group gives it), and a tool's schema; connect, sign in, sync or disconnect an MCP; apply tool permissions and regex rules immediately (`save_permissions`). Regex conditions require a human-readable description.
- `manage_vault_groups`: list/create/update groups, list/add/remove active platform members, and `attach_server` / `detach_server` to give a group a whole connection (`read_only: true` = only its read tools). Does not create accounts.
- `manage_vault_tools`: list tools with status and read/write access, see a tool's versions, `approve` exactly the reviewed fingerprint and version after a sync (a later change quarantines it again), and `set_access` to label a tool read or write (unmarked tools count as write).
- `manage_vault_secret_access`: list secret names; set a group's access; `share` copies a workflow's or Crew's secret into Vault on the server (`source_workflow_id` or `source_crew_id`, `group_ids`, optional `vault_name`; the project copy stays); `delete` removes a Vault secret (`confirm` repeats the name). Secret values are never accepted or returned: add or rotate them in Vault's Secrets panel.
- `read_vault_audit`: audit events or the usage summary with filters (user, group, tool, decision, outcome, time range). For who changed access, query the `policy_history` table with `query_vault_db`.
- `query_vault_db` reads the governance tables with read-only SQL; `mutate_vault_db` (managers) changes groups, members and group grants atomically; direct per-person grants can only be deleted. `list_vault_mcp_servers` / `call_vault_mcp_tool` (managers) resolve resource IDs and run approved setup calls as the administrator; upstream mutations need an explicit user request.

Ordinary Vault MCP calls and the separate Vault runtime endpoint continue to enforce user, group, tool and regex permissions. Other users' private connections remain unavailable.
