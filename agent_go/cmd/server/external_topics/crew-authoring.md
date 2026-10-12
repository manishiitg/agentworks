---
name: "crew-authoring"
description: "Author a Crew over MCP: create and update, typed functions with input and result schemas, isolated calls and result files, messages and memory, files and shared folders, schedules and settings."
external: true
external_scopes: ["crews:write"]
---
# Authoring a Crew over MCP

A Crew is a persistent, shareable agent: standing instructions (`purpose`), skills, typed
**functions** other people and agents can call, schedules, and a project folder of files.
Think of it as a shareable MCP with structure: callers use its declared functions; only the owner
edits it.

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
and incoming sender approval retain their human app flows. Every call requires Crew ownership,
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
- Only the owner edits. Needs `crews:write`.

## Custom commands

- `manage_crew_commands` with `crew_id` and `action=list|create|update|delete`
  uses the same custom-command tool as Crew chat, directly without a model turn.
  Create/update take `name`, `description`, `prompt` and optional `icon`;
  delete takes `name`. Custom commands appear in the Crew's slash menu.
  Product-defined commands remain immutable. Listing needs `crews:read`;
  changes need `crews:write` and current Crew ownership. Read-only accounts
  cannot change them, even if they own the Crew.

## Functions

A function is a typed entry point: `name` (snake_case), `description` (for callers), `instructions`
(what the Crew does when called), `input_schema` and optionally `result_schema` (JSON Schema subset:
type, properties, required, items, enum).

- Each accepted call runs in a fresh, isolated execution with its own output folder
  (`.calls/<call_id>/`), never in the Crew's main chat. A function runs with the OWNER's authority
  for every caller, so write instructions that are safe for whoever may call them.
- Inputs are checked before anything starts. Calls run in parallel, up to 3 running at once per Crew; beyond that
  the caller gets a busy error and nothing is queued.
- With a `result_schema`, the function writes its JSON result to the file named by
  `$FUNCTION_RESULT_FILE` before it finishes (the call message also gives the path, `.calls/<call_id>/result.json`; create the folder if it is missing). The platform validates it after the run, allows one
  correction turn, then fails the call with the reason. Without a `result_schema` the final message is
  the answer. Other outputs (reports, PDFs, images) go in the call's output folder
  (`$FUNCTION_OUTPUT_DIR`, the same `.calls/<call_id>/` folder); callers read them by `call_id`.
- Callers: `functions` action=list|call|status|read|reply|calls. `calls` lists recent calls (the owner
  sees all, others their own); the owner's `status` and `read` replies for a call also carry `commands_run`, the commands that call ran (the list itself does not). Entries of other people's calls show who called, the function, status and times, but no answer, result or files.

## Messages and memory

- A non-owner's ask is read-only (it reads files, memory, skills and functions; it writes nothing and uses no MCP tools); to make a Crew do something, call one of its functions. The owner's own ask has full access. Only the owner changes a Crew. Expose a function for everything others may make the Crew do.
- Conversational contact is separate from functions: `messages`/`ask_crew` send a message to the Crew,
  it may reply or not, and replies are read from the inbox. The Crew's **Agent messaging** setting
  (`free_text_ask` in settings) turns incoming programmatic messages off; declared functions and people
  chatting in the app keep working. Read replies with `messages` action=read, `inbox_id`, `after` (a
  cursor) and `wait_seconds` (up to 25). Inside a Crew, an agent can also set one timed wakeup in its own
  conversation with `schedule_message_wakeup`; it does not fire while the scheduler is paused.
- Runs have no earlier conversation. Anything a later run must know belongs in `MEMORY.md` at the
  Crew's root or in the Crew's files; say so in `purpose` and in function `instructions`.

## Files

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
  browser mode, free_text_ask.

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
tools in Crew, workflow and Relay, including the owner's Crew chat, function and
message runs and external MCP callers. Pass a catalog name or `name` plus an
HTTPS `url`; reuse a saved custom connection by its exact name. Existing roles,
product authoring consent and project bounds still apply. Setup reuses the
website's URL validation and public-only auth probing; private-network endpoints
and arbitrary local process configuration remain refused. Enter credentials
and OAuth app setup in Integrations, never in tool arguments.

## Read-only diagnostics

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
for parameter metadata (Crew owner only; readers receive 403 for this option).
Readers can inspect attached names/status and cached tool names/descriptions,
as in the Crew settings connection inventory. `limit`/`offset` page sessions, connection summaries or
the named connection's tools. Cached metadata does not prove live reachability;
refresh through `crew_mcp action=discover`. Credentials, endpoint configuration,
unattached private accounts and server-wide raw logs are excluded.

For local/external agent coordination, discover account-owned addresses with `messages action=list_agents`. Register your own stable `agent_name` using `action=register`, send to `target=agent:<name>`, and poll `action=read` with your name and no `inbox_id` at the start and end of work. Save `next_cursor`; replies use the returned `reply_address` as `inbox_id`. Registration defaults to the creating connection; additional connection IDs must be explicitly allowed. Named addresses do not bypass Crew/project scopes or access, and messages do not wake external clients.

Use `manage_crew_chats action=tools` with an explicit `chat_id` from your own live chat list for native UI/background schemas. Execute a returned `tool_name` through `action=tool` and its exact `arguments`. UI actions are presentation only and return the existing browser receipt, including unavailable/ambiguous states. Starting background work needs editable Crew ownership, `crews:write`, unrestricted file consent and an initialized manual chat; retained filesystem grants must fit this connection. Native completion notifications return to that chat. Session status/cancellation never crosses to another person's session. Human confirmation and account sign-in continue through their existing Needs you/Integrations flows.
