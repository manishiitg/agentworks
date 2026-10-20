---
name: "crew-authoring"
description: "Author a Crew over MCP: create and update, typed functions with input and result schemas, isolated calls and result files, messages and memory, files and shared folders, schedules and settings."
external: true
external_tools: ["*crew*", "workspace"]
external_scopes: ["crews:write"]
---
# Authoring a Crew over MCP

Crew combines private project and coding work with reusable agents. Start with
`workspace action=list` and keep the returned `workspace_id`; `get`, `create`,
`update`, `ask` and chat actions work across both former Code and Crew origins.
New projects start **Only me**. Share changes the same hosted project's audience;
it does not copy or convert it. Old Crew/Code tool names and IDs remain supported.
A Crew can have standing instructions (`purpose`), skills, typed **functions**,
schedules, a dashboard and project files. Memory is optional; existing projects
keep their setting. Every Crew runs on the server; an optional laptop connection adds local command and local-to-Crew copy tools without changing server features.
A Crew has one main owner (its creator) and can have co-owners; owners build and edit it together, and everything runs as the main owner. All Builders can see every Builder chat in their project. People with Run access see their own chat and dashboard. Only the main owner adds or removes owners (`crew_owners`); `crew_owners action=lock_status` shows who is editing, because one owner edits at a time. Use Share to give others access to its dashboard, Ask and published functions. Shared people can also list, search and read its files over MCP, including skills, memory, knowledge, instructions, runs and logs. Secret values and private logs stay excluded.

Over MCP, use `share action=get` to inspect the audience, then `share action=set` with `crew_id` and the owner's chosen mode: `private` (Only me), `people` (Chosen people, with `emails`) or `everyone`. Only the owner changes sharing, with existing authoring consent. The people list replaces the saved list: preserve people the owner did not ask to remove. A standard dashboard is created when needed. `get_dashboard_share` and `share_dashboard` remain callable aliases.

## Create and edit

`crew_execution` runs native tools directly without a Builder model turn. Call
`action=list` with `crew_id` for the enabled names, descriptions and exact input
schemas, then use that tool name as `action` and its parameters as `arguments`.
Available tools include `execute_shell_command`, `agent_browser`,
`mutate_workflow_db`, `apply_workflow_db_migration` and
`create_workflow_database_snapshot` and `call_selected_mcp_tool`. For connected
MCP, discover this Crew's actual servers/tools first through `crew_mcp` and
`crew_diagnostics`; live attachment, selected-tool and Vault checks apply; authorized Vault defaults
remain available under their current account/group permissions.
`crew_integrations action=list` exposes the enabled shared Slack, Google account
and incoming-email tools with their native schemas. Attached workflow tools also
appear in `crew_execution`: list attachments/triggers, run a trigger with a stable
`delivery_id`, then poll its returned `run_id` and `trigger_id`. The attachment,
exact Crew binding and current workflow access are checked. Workflow read/file
consent and target bounds apply; a run additionally requires `runs:execute`.
Function progress is reported only by the receiving execution, whose final
message or checked result file completes the call; external callers use
`functions action=status` and `reply` for their own pending questions. Account-wide Google grant
changes require a current administrator and all-Crew consent; Google sign-in
and company receiving setup retain their human consent flows. Every call requires Crew ownership,
existing `crews:write` consent and a current editable account. Shell commands
run inside the Crew sandbox with the connection's file guard, default timeout
25 seconds. Browser, database and connected integration operations require unrestricted Crew file
consent. Author migration files in `db/migrations/` through the sandboxed shell,
then apply their bare filenames with the managed migration tool. Database
reads remain available through `query_database`. These calls do not borrow or
start the owner's chat; inspect results before retrying a mutation. Use
`crew_execution action=history` for the last 100 owner-only audit records: connection,
operation, timing and outcome, with commands, values and payloads withheld.

- `crew` action=create takes `name`, `role`, `purpose` (the Crew's standing instructions; what it is
  for and how it works) and optionally `icon`, `selected_skills`, `files` (path -> text content),
  `functions`, `schedules`. action=update edits one you own section by section (omitted parts stay):
  `skills` {set, add, remove}, `functions` {upsert, delete}, `schedules` {add, update, remove},
  `files`, `remove_files`. The reply is a compact summary; pass `return_spec: true` for the whole Crew.
  action=export and action=import move a Crew between accounts or servers as a portable spec.
- Only owners edit. Needs `crews:write`.

## Custom commands

- `manage_crew_commands` with `crew_id` and `action=list|create|update|delete`
  uses the same custom-command tool as Crew chat, directly without a model turn.
  Create/update take `name`, `description`, `prompt` and optional `icon`;
  delete takes `name`. Custom commands appear in the Crew's slash menu.
  Product-defined commands remain immutable. Listing needs `crews:read`;
  changes need `crews:write` and current Crew ownership. Accounts without editing access cannot change them, even if they own the Crew.

## Functions

A function is a typed entry point: `name` (snake_case), `description` (for callers), `instructions`
(what the Crew does when called), `input_schema` and optionally `result_schema` (JSON Schema subset:
type, properties, required, items, enum).

- By default, a function runs in its own named tab, keeps earlier calls' conversation and queues
  calls to take turns. It never runs in the Crew's main chat. Set `agent` to share a named tab
  between functions. Set `execution: "fresh"` for a new chat for every call, especially when inputs
  are private or untrusted: earlier input in a shared tab can influence later answers.
- Inputs are checked before anything starts. Each call has its own output folder
  (`.calls/<call_id>/`). A function runs with the OWNER's authority for every caller,
  so write instructions that are safe for whoever may call it. Fresh calls may run in parallel;
  when their running limit is reached they return a busy error without queueing.
- With a `result_schema`, the function writes its JSON result to the file named by
  `$FUNCTION_RESULT_FILE` before it finishes (the call message also gives the path, `.calls/<call_id>/result.json`; create the folder if it is missing). The platform validates it after the run, allows one
  correction turn, then fails the call with the reason. Without a `result_schema` the final message is
  the answer. Other outputs (reports, PDFs, images) go in the call's output folder
  (`$FUNCTION_OUTPUT_DIR`, the same `.calls/<call_id>/` folder); callers read them by `call_id`.
- Callers: `functions` action=list|call|status|read|reply|calls. `calls` lists recent calls (the owner
  sees all, others their own); the owner's `status` and `read` replies for a call also carry `commands_run`, the commands that call ran (the list itself does not). Entries of other people's calls show who called, the function, status and times, but no answer, result or files.

## Messages and memory

- Builders change the Crew; people with Run access use its shared chat, dashboard and published functions under the current server checks. Share never grants Builder tools or access to Builder conversations.
- For a conversation, use `workspace action=ask` with the discovered workspace ID and optional `chat_id`. Keep the returned chat and submission receipt; read progress and replies with `workspace action=read_chat`, `chat_id`, `after` and `wait_seconds` (up to 25). Ask without a chat ID opens or uses the caller's own conversation. A started turn is not a finished answer; inspect its returned status.
- Legacy `ask_crew`/message tools remain available through discovery; their returned inbox receipts belong to those operations, not to `workspace` chat replies. Ask is always open to people the Crew is shared with. Inside a Crew, `schedule_message_wakeup` can set one timed wakeup in its own conversation; it does not fire while the scheduler is paused.
- Named function tabs keep earlier calls' conversation; `execution: "fresh"` starts without it.
  Put lasting instructions and shared knowledge in `MEMORY.md` or the Crew's files,
  and say how to use them in `purpose` and function `instructions`.

## Files

People using the shared Crew on the website see chat and its dashboard. File-tool access follows the current MCP schema and server guards; this guide does not add access.

- `files` with `crew_id`: anyone with access reads; the owner writes anywhere except protected paths
  (manifests, `functions.json`, `builder/`, `db/`); other callers who can run the Crew write under
  `shared/<their id>/`. Text in `content`, binary in `content_base64` (up to 11 MiB). Put input files
  there for a function to use, and pass their paths as arguments.
- The owner can also write the Crew's `MEMORY.md` and project-local
  `skills/<name>/SKILL.md` or supporting files through `files action=write`,
  with `crews:write`. Read first and pass `expected_revision` plus a stable
  `request_id` to detect concurrent edits and retry safely. Other users
  cannot modify the Crew's memory or skills, even with a write-capable
  connection; their writable area remains `shared/<their id>/`.
  To select a newly authored skill, use `settings` or `crew action=update`
  with `skills` after writing it. This does not edit server-wide or built-in skills.

## File lifecycle, packages and references

All tools below take `crew_id`. Reads require `crews:read`; changes require
`crews:write`, current Crew ownership and an enabled editable account. The
connection's Crew bounds apply. These calls execute directly without a model turn.

- `crew_files action=patch`: `path`, `diff`, `expected_revision` from `files`
  read and stable `request_id`. The shared diff parser and durable edit receipts
  support retries. `mkdir` needs `path` and `expected_revision: "missing"`.
  `move` needs `path`, `destination`, and a revision for files. Both paths and
  all children are checked against guards and protected paths. The destination
  must be absent and its parent must exist. `delete` removes a file with its
  revision, or an empty folder. Omit `expected_revision` when moving or deleting
  an existing folder. Lifecycle operations return applied status;
  inspect current paths before retrying after a lost response.
- `crew_skills action=list|search|install|import|uninstall`: `query` for search,
  `source` for install (a public registry source or an authorized `brain:`
  folder), `github_url` and optional private-repository `token` for GitHub import,
  `folder_name` for uninstall.
  Packages live in this Crew's `skills/` folder. Select them through existing
  `settings`. Built-in skills stay immutable. File guards restrict inventory
  and installation; uninstall across native-provider folders needs unrestricted
  Crew file consent.
- `crew_mcp action=list|search|install|remove|discover`: search takes `query`;
  install accepts the exact `name` returned by list, a catalog name (case-insensitive),
  or `catalog` alone, with optional `label`; use `name` and a
  custom remote HTTPS `url` for a server outside the catalog; remove/discover
  take the exact connection `name`. Install attaches the authenticated person's
  connection to this Crew. Complete sign-in and credentials in Integrations.
  Remove detaches only this Crew, preserving other projects and its login.
  Discovery is limited to attached connections; select through `settings`.
- `crew_folders action=list|attach|detach` manages account-wide grants. Attach
  takes an existing absolute `path`, unique `alias`, `access` (`read_only` or
  `read_write`) and optional `reason`; detach takes the returned `id`. The
  owner needs all-Crew consent without a file guard. Administrator-assigned
  roots still apply; revocation refreshes existing account runtimes.
- `crew_references action=list|attach|detach`: optional `query` for discovery;
  attach/detach use the exact returned `workspace_path`. Lists include attached
  flags. Workflow references stay read-only and need workflow/file read consent
  plus target bounds. Crew references enable shared writes and need Crew read
  and write consent plus target bounds. A file guard cannot be widened through
  references. Detach permits cleanup of an obsolete saved reference.

## Schedules and settings

- A schedule sends an instruction (`message` or `messages`) on a cron or cadence, with a timezone; a
  schedule whose name already exists counts as added. Scheduler pauses hold timed runs.
- `settings` (with `crew_id`): models, MCP servers and tools, skills, secrets (owner, write-only),
  browser mode.

## Checking your work

Call `crew` action=get for the Crew's functions, call a function with `wait_seconds` and read
`status`, then check the result against the schema. A function that finishes without a valid result
is a failed call: read its `answer` and `files`, fix the instructions, and call again with a fresh
`submission_id`.

## Existing file and folder links

Use `get_file_link` with `crew_id` and a canonical Crew-relative `path` for
an existing artifact. This requires existing `crews:read` consent and current
Crew access; connection Crew bounds and file guards also apply. File results
include `preview_url`, `download_url`, size and content type; folder results
include a browser preview. Recipients sign in with their own current access.
Creating a link grants no access and never makes the artifact public.

Downloads accept a signed-in session or CLI/PAT Bearer header. MCP OAuth tokens
are restricted to the MCP endpoint; use browser sign-in or the existing files
reader instead. Never put a credential in a URL. Folder listings and archives
exclude blocked descendants and symlinks; private manifests, chats and databases
remain protected. Check `shareable`/`warning` for a same-machine preview.

## MCP installation policy

Catalog and custom remote MCP servers are allowed through the same authoring
tools in Crew, workflow and Flow, including the owner's Crew chat, function and
message runs and external MCP callers. Pass a catalog name or `name` plus an
HTTPS `url`; reuse a saved custom connection by its exact name. Existing roles,
product authoring consent and project bounds still apply. Setup reuses the
website's URL validation and public-only auth probing; private-network endpoints
and arbitrary local process configuration remain refused. Complete MCP connection sign-in and OAuth app setup in Integrations. Project secret values supplied by the person use the designated settings or Vault secret tools, then are referenced by name only.

## Diagnostics

`crew_diagnostics` uses existing `crews:read` consent and current Crew access.
Use `action=browser` for configured mode, live capability status and the Crew's
scoped browser sessions. It does not launch or control a browser. The managed
Crew browser and Crew Playwright sessions/recordings follow shared project
access; these are labelled shared_crew, while private caller sessions are labelled
caller. Unrelated Crew and other users' private sessions are excluded. Status
omits host endpoints, ports, raw probe/recording errors, commands and owner chat
IDs. Chrome extension status remains
account scoped and is returned only to the Crew owner.

Use `action=connections` to list attached MCP connection status. Pass an exact
attached `name` for cached tool names/descriptions, plus `include_tools=true`
for parameter metadata (Crew owner only; Run callers receive 403 for this option).
Run callers can inspect attached names/status and cached tool names/descriptions,
as in the Crew settings connection inventory. `limit`/`offset` page sessions, connection summaries or
the named connection's tools. Cached metadata does not prove live reachability;
refresh through `crew_mcp action=discover`. Credentials, endpoint configuration,
unattached private accounts and server-wide raw logs are excluded.

Agent-to-agent messages (AgentBridge) use an inbox for apps without a terminal, through their normal MCP sign-in and the discovered `messages` schema. Nothing is pushed; check when asked or at the start of a conversation. For live delivery, install the CLI from Connect AI agents and run `agentworks attach` for this chat. Recipients show Live now or Inbox. Named addresses do not bypass Crew/project scopes or access.

Use `manage_crew_chats action=tools` with an explicit `chat_id` from your own live chat list for native UI/background schemas. Execute a returned `tool_name` through `action=tool` and its exact `arguments`. UI actions are presentation only and return the existing browser receipt, including unavailable/ambiguous states. Starting background work needs editable Crew ownership, `crews:write`, unrestricted file consent and an initialized manual chat; retained filesystem grants must fit this connection. Native completion notifications return to that chat. Session status/cancellation never crosses to another person's session. Human confirmation and account sign-in continue through their existing Needs you/Integrations flows.

## Import work from your computer

Import a laptop-native coding project's files and exact chat into a new Crew or a named agent in an existing Crew. Adding keeps that Crew's Main, instructions and Share. Raw history stays private to Builders; server continuation uses a summary and the disclosed company account. Login files and `.env` are excluded. Native chat resume comes later.

Crews run on the server. An optional laptop connection in Settings adds local command and local-to-Crew file copy tools for its owner's interactive Builder chat. Sharing a Crew does not share the laptop. Initial imports of native laptop coding chats use the CLI import flow; a hosted Crew does not publish a second copy.
