# Connect an AI agent to AgentWorks with MCP

Connect Claude Code, Codex, Cursor, Muse, ChatGPT, Claude Cowork, or another MCP client to
`https://your-server/api/external/v1/mcp`. The client signs in through AgentWorks
OAuth in your browser. Terminal AI agents use this HTTP connection directly;
you do not need to install the AgentWorks CLI for a terminal AI agent.

```sh
claude mcp add --transport http agentworks 'https://your-server/api/external/v1/mcp'
codex mcp add agentworks --url 'https://your-server/api/external/v1/mcp'
codex mcp login agentworks
```

Open **Setup → Integrations → Connect** for commands using your installation's
URL. A local AI agent can connect to a local AgentWorks server using a loopback
URL; a hosted AI app needs a public HTTPS URL. AgentWorks must have `PUBLIC_URL`
configured to that same origin. Remote MCP OAuth accepts HTTP only for a
configured loopback address.

Run-mode MCP tools inspect data and execute workflows or Crew functions. A
connection with the explicit Builder grant can also use the Builder tools to
author workflows it owns or may edit; see [Workflow Builder MCP](../mcp/workflow-builder.md)
for the grant and operation flow. Function calls that pause for a question
expose it through `get_crew_function_call` or `get_workflow_function_call` and
accept an answer through the matching `reply_*_function_call` tool.
Pass a unique `submission_id` for each new function call or Crew ask; reuse it
when retrying an uncertain request to recover the same call ID.

## Legacy CLI for existing installations

Existing scripts and stdio-only MCP clients can continue using the CLI. New
AI agent connections should use HTTP MCP as shown above. The installer downloads the
CLI build matching that server, verifies its checksum, installs it to
`~/.local/bin`, and opens a browser approval link. macOS and Linux on arm64/amd64 are
supported.
Local installs can drive the CLI and local MCP bridges, but ChatGPT and
Cowork need a public server URL — deploy first, then open that server's
Connect tab for the remote URL.

The binaries and installer are served by the server itself at
`/api/downloads/cli/` (public, like the existing launcher downloads), so
the CLI always matches the API it talks to. `agentworks version` prints the
build; `agentworks update` (or `update --check`) self-updates from the
connected server. Confida and other rootless deployments build and package
all supported CLI binaries with each release, then verify the public installer
URL before marking the deploy successful. The local `run_server_with_logging.sh`
script packages the native CLI for its machine before starting the server, so
the same installer command works against a loopback URL. Developers can still
build from source as below.

## Build and server setup

Requires the repository's Go toolchain (Go 1.26) and its normal local module
replacements. From the repository:

```sh
cd agent_go
go build -o bin/agentworks ./cmd/agentworks
```

Put the binary on your PATH. Rebuild and deploy **both the agent server and the
workspace service** from this revision. Existing deployments do not acquire
these endpoints just by installing the client. No server deployment is performed
by building this binary.

Configure the same nonempty `WORKSPACE_API_TOKEN` in the agent and workspace
services. This is a **server-to-server credential**, never a user's CLI token.
The internal `/api/shared-assets` endpoint fails closed when this token is
missing and is blocked by the generic workspace proxy. The former
`/api/workflow-files` revision/write endpoint has been removed; external file
tools read the shared filesystem or use the read-only shared-assets endpoint
when the agent and workspace run on separate volumes. Keep the workspace
service on the internal network; expose only the authenticated AgentWorks server.

## Test locally with the testing workflow

From the repository root, run:

```sh
python3 scripts/test-agentworks-external-local.py
```

This builds the agent server, workspace service, and CLI into a fresh directory
under `.local/workflow-tests/`. It copies only the design inputs from
`workspace-docs/Workflow/testing`, sanitizes its manifest, and starts separate
services on loopback ports with fresh test credentials. It does not use the
running development services or their workspace.

The probe exercises app-generated PAT login, workflow/tool discovery, document
read/search, plan and guidance reads, and narrow-token permission checks. It
also creates a WAV asset larger than 2 MiB, gets its share link, downloads it
through the CLI, and verifies an authenticated byte-range request through the
browser file endpoint. It then connects the actual stdio MCP bridge, verifies
plan/context reads, and asserts the probe left no changelog entries. It revokes
the token and checks that both the CLI and the existing MCP connection are
denied, and asserts every mutation path answers `unknown_tool`. Run/log
inspection uses explicitly synthetic artifacts.

Each run prints the artifact directory and writes `receipt.json`, including
source-file hashes and results. It stops its own services and verifies the
original workflow inputs stayed unchanged. This test makes no model calls,
does not execute the copied workflow, and does not test a live Builder model
conversation. See [local workflow isolation](isolated-workflow-testing.md) for
the procedure for live agent testing.

## Connect the legacy CLI

Run the installer from `/api/downloads/cli/install-agentworks.sh`, or sign in
with an installed binary:

```sh
agentworks login --server https://agentworks.example.com
```

The CLI opens the AgentWorks sign-in page. Confirm its eight-character code
matches the terminal, approve access, then return to the terminal. On a remote terminal, use `agentworks login --no-browser`
and open the printed link yourself. Each login creates a separate connection
that you can revoke under **Connect → Connected apps**. The CLI renews its
short-lived access automatically. `agentworks logout` revokes that connection
and clears the local credentials.

CLI grants run in full run mode —
`workflows:read`, `files:read`, and `runs:execute` — over all currently and
future accessible workflows. Write permissions
(`files:write`, `plan:write`, `builder:chat`) are not issued in v1. Every
call checks the grant scopes and the user's current workflow
access. CLI grants cannot call account management, the general query endpoint,
or the workspace proxy; only the external tool, asset-content, skill, and
remote MCP endpoints accept them. A token still cannot exceed the user's
normal account permissions.

Precisely, a token authorizes: reading the account's workflows, files,
plans, runs, guidance, and knowledge; starting, steering, observing, and
stopping executions; triggering the workflow's saved schedules (which run
with their owner-configured definition); and the workflow's own outbound
actions (Slack routes, user notifications). It never authorizes authoring
(plans, configs, files, workflows), account management, or account-wide
service shells — `google_workspace_cli` stays out of the external catalog
and token-backed chat sessions for exactly this reason. Slack and WhatsApp
Run-mode bot channels retain it under their own route grants.

Every CLI/MCP HTTP request checks the persisted grant. Revocation rejects
subsequent calls, including calls from an MCP bridge already running.

Configuration is stored in the OS user-config directory under
`agentworks/config.json`, with private permissions. `--config` selects another
file. `AGENTWORKS_SERVER` selects the server for automation. Existing personal
access tokens still work through `AGENTWORKS_TOKEN` or `login --token-stdin`
for older scripts; they are no longer created or displayed in Connect.
HTTPS is required except on loopback development addresses.
Redirects are refused to avoid forwarding credentials to another location.

## Connect a local AI app

Choose **AI agent on this computer** in Connect. Claude Code uses HTTP MCP:

```sh
claude mcp add --transport http agentworks 'https://your-server/api/external/v1/mcp'
```

Codex uses its own registration command:

```sh
codex mcp add agentworks --url 'https://your-server/api/external/v1/mcp'
codex mcp login agentworks
```

Cursor and Muse read the server from a config file, then sign in:

```sh
# Cursor: add to ~/.cursor/mcp.json
#   {"mcpServers": {"agentworks": {"url": "https://your-server/api/external/v1/mcp"}}}
cursor-agent mcp login agentworks

# Muse: add to ~/.config/muse/settings.json
#   {"schema_version": 1, "mcpServers": {"agentworks": {"url": "https://your-server/api/external/v1/mcp"}}}
muse mcp login agentworks
```

Add AgentWorks to the client you are actually using: `claude mcp add` run from
another agent configures Claude Code, not that agent. Every client approves
access through the browser. Existing stdio registrations
using `agentworks mcp serve` continue to work, but new setups do not need them.

Example request:

> Find the invoice workflow, read its process documents, and summarize what its
> fetch step does.

The agent discovers the workflow ID, then reads the plan, files, and runs. If
the task needs a change, it says so instead of attempting one.

## Connect hosted assistants

All supported AI apps connect to the server over MCP Streamable HTTP at
`POST/GET/DELETE /api/external/v1/mcp`. Unlike the legacy CLI and stdio bridge,
which list every tool, the remote surface is exactly two self-describing
tools: `get_api_spec` (no arguments lists every available tool, names return
JSON schemas) and `call_tool` (executes by name). The full catalog —
product.yaml's external tools plus run tools — resolves internally, so the
surface stays tiny no matter how run mode grows. Choose **Hosted AI app**
in Connect to see the ready-to-paste URL for the active
installation:

```text
https://your-server/api/external/v1/mcp
```

- ChatGPT: Settings → Apps & Connectors → Developer Mode → add a custom MCP
  connector with that URL and choose OAuth authentication.
- Claude Cowork: in AgentWorks Connect → Hosted AI app → Claude Cowork,
  download `agentworks.plugin`. In Cowork, open Customize → Plugins, upload
  the plugin, then connect AgentWorks and approve OAuth in your browser. The
  plugin contains the remote MCP connector and the AgentWorks skill. It
  contains no credential. The manual alternative is Customize → Connectors
  → Add custom connector with the URL above and OAuth authentication.

The assistant discovers AgentWorks OAuth metadata from the server. Sign in to
AgentWorks when prompted, review the requested permissions, and allow access.
The connection uses short-lived MCP-only access tokens and rotating refresh
tokens. Revoke it under **Connect → Connected apps**. The CLI and local stdio
MCP bridge use their own browser-approved OAuth connections.

For ChatGPT, the optional **Give the assistant workflow guidance** section downloads the
same guidance as an
uploadable skill zip (`GET /api/external/v1/skill.zip`, a SKILL.md following
the Agent Skills layout ChatGPT, Claude, and Cowork accept) or copies its
text (`GET /api/external/v1/skill.md`). Upload it via ChatGPT's Plugins →
Skills → Create → Upload from your computer (eligible plans), or paste the
text into Custom Instructions / the connector's Instructions field. The skill
names the installation but carries no credential. Both endpoints accept the
app session or a PAT.

Schemas, scopes, and per-request authorization are identical to the REST
external API: `get_api_spec` only lists and describes tools the grant may
use, and every `call_tool` runs through the same dispatcher. Existing PAT
connections remain supported for older integrations; direct PAT integrations send it in the
`Authorization: Bearer` header. The legacy `?token=` form is supported for
older clients, but credentials in URLs can leak into proxy logs and history.

## CLI examples

All output is JSON: pretty by default, compact with `--json`. IDs below are
examples; discover actual IDs first.

```sh
agentworks workflows list --json
agentworks workflows get --workflow WORKFLOW_ID
agentworks files list --workflow WORKFLOW_ID --path docs
agentworks files search --workflow WORKFLOW_ID --query invoices
agentworks files read --workflow WORKFLOW_ID --path docs/process.md
agentworks plan get --workflow WORKFLOW_ID
```

Find workflow-owned Python test code without paging through installed
packages or caches:

```sh
agentworks files list --workflow WORKFLOW_ID --path code --glob '**/*.py' --depth 8
agentworks files search --workflow WORKFLOW_ID --path code --glob '**/*.py' --query 'test_login' --depth 8
agentworks files code --workflow WORKFLOW_ID
agentworks files code --workflow WORKFLOW_ID --step-id run-basic-smoke
agentworks files read --workflow WORKFLOW_ID --path code/run-basic-smoke/modules/auth.py
```

MCP `list_files` and `search_files` accept the same optional `path` and
`glob` arguments. The glob is relative to `path`; `**` matches any number
of directories, including zero. Filtering happens before pagination and,
for `search_files`, before file content is scanned. Hidden workspace paths,
runtime caches, and installed packages (including `.cache`, `.local`,
`__pycache__`, `.venv`, `node_modules`, and `site-packages`) are unavailable
to file listing, search, direct reading, and preview links. To identify
a workflow step for a test script, use MCP `list_step_code` or CLI
`files code`. The inventory defaults to Python files and annotates each
entry with its step ID, plan title, and whether that step is still in the
plan. Current workflows read `code/<step-id>/`; legacy workflows read
`learnings/<step-id>/`.

Load guidance and knowledge for the task:

```sh
agentworks guidance context --workflow WORKFLOW_ID
agentworks guidance topics
agentworks guidance topic --topic plan-change-impact
agentworks knowledge list --workflow WORKFLOW_ID
agentworks knowledge read --workflow WORKFLOW_ID --path learnings/_global/SKILL.md
```

`--input -` reads JSON arguments from stdin. `--set key=JSON` supplies
additional native fields. `tools list` is authoritative for the current
server's schemas:

```sh
agentworks tools list
agentworks tools call get_guidance_topic --input ./topic.json
```

```sh
agentworks runs list --workflow WORKFLOW_ID
agentworks runs get --workflow WORKFLOW_ID --run-folder iteration-0/group-name
agentworks runs logs --workflow WORKFLOW_ID --run-folder iteration-0/group-name
```

Saved run/log artifacts are browsed with `runs list|get|logs`; retrieve
selected paths with `files read`. Workflow creation/deletion stays outside
this surface.

## Running steps, workflows, and schedules

Run-mode chat tools from `product.yaml` are callable here under the
`runs:execute` scope, except names in `external_denylist`. A tool added to
run mode appears in `tools list` and `mcp serve` unless it is denylisted.
Each proxied call starts a new pinned Run-mode session (or continues
`--session`), and its reply carries `session_id`; poll `runs status` for
completion. Structured arguments travel via `--set key=JSON`.

```sh
agentworks runs start-step --workflow WORKFLOW_ID --step-id fetch-invoices --set 'script_parameters={"limit":10}'
agentworks runs start-workflow --workflow WORKFLOW_ID --group group-1
agentworks runs status --workflow WORKFLOW_ID --session SESSION_ID
agentworks runs executions --workflow WORKFLOW_ID
agentworks runs message --workflow WORKFLOW_ID --session SESSION_ID --execution-id EXEC_ID --message "slow down"
agentworks runs stop --workflow WORKFLOW_ID --session SESSION_ID --execution-id EXEC_ID
agentworks runs stop-all --workflow WORKFLOW_ID --session SESSION_ID
agentworks schedules list --workflow WORKFLOW_ID
agentworks schedules runs --workflow WORKFLOW_ID --schedule-id daily
agentworks schedules trigger --workflow WORKFLOW_ID --schedule-id daily
```

`runs:execute` implies workflow visibility (`list_workflows`, `get_plan`,
run evidence, status). Direct file content (`list_files`, `search_files`,
`list_step_code`, `read_file`, `get_file_link`, knowledge reads) stays behind
`files:read` —
but a run or chat session necessarily reads its own workflow's files to
execute, so `runs:execute` includes those in-session reads and the results
derived from them. Sessions are scoped to the single workflow they run:
even a token allowed many workflows cannot reach another workflow's files
through an assistant turn.
A read-only token sees neither the run tools in `tools list` nor their MCP
entries, and calling one returns `insufficient_scope`. Sessions are owned by
the token that started them: revoking the token cancels its runs, and one
token can never status, message, or stop another token's session. New tools
added to run mode later work immediately through `tools call` unless
denylisted; typed
subcommands cover the core operations above.

`runs:execute` authority, stated precisely: a token may invoke the run
operations of the workflows it can see, converse with those workflows'
Run-mode assistant, and trigger those workflows' own saved schedules.
"Never authors" means no plan, configuration, schedule, secret, or file
change outside the run's own execution outputs — but executing a run
still performs the workflow's configured steps, including its configured
notifications. Only account-scoped tools are withheld from direct calls:
`google_workspace_cli` (arbitrary commands against the account's Google
connection) is unavailable to token-backed sessions, including `chat`.
Deliberately kept: `send_slack_message`
(configured workflow routes only), `notify_user` (the user's own
channels), and `trigger_schedule` (this workflow's own schedules).

## Chatting with the workflow assistant

`chat` asks the assistant anything — explanations, analysis, follow-ups — as
a free-form turn on a pinned Run-mode session, the CLI/MCP equivalent of the
Slack and WhatsApp bot channels. Pass `--session` to continue the
conversation; sessions are shared with the run tools, so one conversation can
ask, run, and ask about the run. Replies arrive through `runs status`; when
it reports waiting human input, answer with `runs reply`.

```sh
agentworks chat ask --workflow WORKFLOW_ID --message "why did step 1 fail?"
agentworks chat ask --workflow WORKFLOW_ID --session SESSION_ID --message "retry it with tier high"
agentworks runs status --workflow WORKFLOW_ID --session SESSION_ID
agentworks runs reply --workflow WORKFLOW_ID --session SESSION_ID --request-id REQUEST_ID --response "yes"
```

Chat turns run with the same `runs:execute` scope and the same ownership
rules as run tools. The assistant can call run-mode tools to answer, so a
chat turn may start work; watch `runs status` and `runs executions` to see
what it started.

## Asset links and downloads

Use the existing Share file viewer for a clickable output link:

```sh
agentworks files link --workflow WORKFLOW_ID --path db/assets/report.pdf
agentworks files download --workflow WORKFLOW_ID --path db/assets/report.pdf \
  --output ./report.pdf
```

MCP exposes the same `get_file_link` tool with `workflow_id` and `path` arguments.
It returns the file size, content type, `preview_url`, and `download_url` without
loading the asset into model context. An external agent can call `get_file_link`
and give the user its `preview_url` for any existing output.

The preview opens `/file?path=…` in AgentWorks. Local installations initialize
the local app session before fetching; hosted installations preserve the file
or folder URL through password or OAuth sign-in. Images, audio, video, PDF,
Markdown, and text have previews. HTML renders in a sandbox without scripts;
other binary formats offer a download. Markdown workspace images use authenticated
requests, and linked workspace documents open their own shared viewer.

**A share link identifies a file; it does not grant permission.** Workflow owners
and readers can view/download it. Every file, folder listing, and ZIP request
checks the recipient's current workflow access. Removing access also blocks old
links. Personal Chats/Downloads remain private to their owner; an old `uid` link
cannot grant another user access. Private files and symlinks are excluded.

Preview URLs contain no credentials. The `download_url` requires a PAT or app
session in the `Authorization: Bearer …` header; clicking that API URL alone does
not supply a header. Use `preview_url` for people and `files download` for a local
agent. Downloads require `files:read` and access to the selected workflow, stream
without the tool's 2 MiB read limit, and refuse to overwrite existing local files.
Folder listings are bounded at 10,000 scanned entries; ZIP downloads are bounded
at 512 MiB of uncompressed files. Choose a smaller folder when needed.

Set `PUBLIC_URL` to the externally reachable AgentWorks origin when running
behind a proxy. The normal hosted/local app serves the viewer and API on that
origin. A separate frontend development server needs the appropriate `PUBLIC_URL`
and API runtime configuration. A localhost link works on the machine running
that installation; sharing it with someone on another machine requires a reachable
hosted address.

## Workflow Builder chat

Not exposed. Builder chat runs the existing builder runtime with authoring
tools, so it stays out of the catalog alongside file writes and plan
mutations; external execution runs in pinned Run-mode sessions instead. The
server keeps its session binding, ownership checks, and revocation-driven
cancellation for a future write-enabled API version.

## External agent guidance

The local implementation now gives MCP clients short initialization
instructions and exposes five guidance and knowledge operations. Builder chat
is not exposed, so the external agent relies on these operations plus the
read tools; runtime steps separately receive their explicitly enabled step
skills.

The external surface is intended to add the decision context that bare tool
schemas do not provide: which guidance applies, what other files and
configuration are worth checking, and how to answer from reading. AgentWorks
builds the canonical `builder-reference`, `workflow-commands`, and
`system-tools` bundles in
`agent_go/cmd/server/guidance/materialize.go`; the external implementation
reuses those renderers rather than maintaining another complete body of
guidance.

Two constraints define the intended boundary:

- Builder guidance cannot be exposed unchanged. Some documents instruct the
  Builder to call internal tools the external catalog does not provide, so the
  external surface needs a guidance profile filtered by actual tools and
  permissions.
- Permissions must be split per tool. Canonical server-owned guidance can use
  `workflows:read`, but workflow-authored skills and learnings must require
  `files:read`. Workflow `skills/` projection paths (`.pi/skills`,
  `.agents/skills`, `.claude/skills`) are generated provider artifacts and must
  not become a public API; `learnings/_global/` is a real workflow path but
  requires file-read permission.

The five implemented operations are:

- `get_agent_context`: role, token capabilities, available tools, and guidance
  version, plus the preparation checklist (with a run section when the token
  allows `runs:execute`). This is a global tool;
  pass `workflow_id` to include the caller's role on a workflow. CLI:
  `agentworks guidance context [--workflow ID]`.
- `list_guidance_topics` / `get_guidance_topic`: server-owned guidance for
  `plan-change-impact`, `plan-design`, `planning-steps`, `step-description`,
  `step-config`, `skill-management`, `file-layout`, and `secure-share-links`.
  Each topic is rendered live from the canonical Builder reference, and every
  internal-only operation named by served content is disclosed in that topic's
  external mapping note (enforced by test). Topics documenting internal-only
  tool names are excluded from the profile.
- `list_workflow_knowledge` / `read_workflow_knowledge`: workflow learnings,
  knowledgebase notes, workspace skill folders, and skill wiring
  (workflow-selected skills plus per-step `enabled_skills`). Reads are confined
  to `learnings/`, `knowledgebase/`, and `skills/<folder>/<file>` content, and
  skill folders are further restricted to the workflow's selected and
  step-enabled skills — a workflow ticket never grants the whole shared skill
  catalog. An unavailable skill catalog returns a `warnings` entry rather than
  an empty list. CLI: `agentworks knowledge list|read --workflow ID
  [--path PATH]`.

MCP initialization delivers short instructions that tell the client whether
the bridge reads only or also runs (chosen from the scope-filtered catalog),
to call `get_agent_context` first, and to load
relevant topics. The companion
skill source lives at
`agent_go/pkg/agentworksclient/skills/agentworks/SKILL.md`, embedded in the
CLI; `agentworks skills install --dir <skill-dir> [--force]` writes it to
`<dir>/agentworks/` (default `.agents/skills`, refusing to clobber without
`--force`). The guidance version is computed from the allowlist, mapping
notes, and rendered content, so cached clients detect canonical changes
without a manual bump. `get_agent_context` with `workflow_id` also returns
`effective_tools`, filtered by the caller's role on top of token scopes.

Canonical guidance tools require `workflows:read`; knowledge tools require
`files:read`. Nothing authors, so the follow-up contract is small: the agent
answers from what it reads (and what its runs report) and says so when a task
needs a change.

### Local implementation review (2026-09-20, second pass)

The implementation is committed and pushed as `ba5f282ae` on `main`, which
matches `origin/main`. The working tree is clean apart from the unrelated
untracked `tmp/` directory.

The second review confirmed these completed fixes:

- Workspace skill discovery decodes the shared-assets `filepath` field and has
  a non-empty discovery test.
- Skill listing and reads are restricted to the workflow's selected and
  step-enabled skills; unrelated global skill folders return `forbidden`.
- The skill catalog reports a warning when it is unavailable instead of looking
  empty.
- The guidance version is derived from the allowlist, mapping notes, and
  rendered canonical content.
- `get_agent_context` returns role-filtered `effective_tools` in addition to
  token-level availability.
- Global topic commands do not expose the inapplicable `--workflow` flag.
- `agentworks skills install --dir <skill-dir> [--force]` installs the embedded
  AgentWorks skill and refuses to overwrite it unless requested.
- `required_followups` are documented consistently as advisory receipts rather
  than server-enforced completion state.

Two functional blockers remain:

1. **Per-step skills are parsed from the wrong file shape.** Production
   `planning/step_config.json` uses
   `{ "steps": [{ "id": "...", "agent_configs": { "enabled_skills": [...] } }] }`,
   while `externalStepSkills` currently expects a top-level array with
   `step_id` and `enabled_skills`. A skill enabled only on a step is therefore
   absent from `step_skills`, omitted from `workspace_skills`, and rejected by
   `read_workflow_knowledge`. Parse the canonical `StepConfigFile` structure and
   make the external guidance test fixture use the production format.
2. **External guidance still contains undisclosed internal instructions.** The
   mapping test checks a fixed denylist rather than comparing rendered guidance
   with the actual external catalog. Current served documents still mention
   unsupported operations omitted from that denylist, including `add_step`,
   `update_step`, `run_workflow`, `create_human_input_request`,
   `update_validation_schema`, `query_workflow_costs`, and
   `get_workflow_config`, as well as reference topics unavailable to external
   clients. Render an external-specific form or validate every operation and
   reference against the actual external catalog and topic allowlist.

The external plan schemas need the same compatibility treatment. For example,
the `update_step_config` description tells callers about `execute_step` and
`run_full_workflow`, and its `enabled_skills` field recommends `list_skills` and
`get_workflow_config`; none of those operations are in the external catalog.
External schema descriptions should map these instructions to supported tools
or remove them.

Current validation status:

- `go build ./cmd/server` passes.
- The AgentWorks CLI and client tests pass, including skill installation and
  MCP coverage.
- `git diff --check` passes.
- `go test ./cmd/server ...` cannot compile because the pre-existing Crew test
  calls an undefined `mock.hasFolder`. This is unrelated to the external-agent
  change, but it prevents the focused server tests from being rerun against the
  current tree. The previously reported `registerWorkCrewProfile` error is no
  longer present.

Review acceptance now requires fixing the canonical step-config parsing,
eliminating unsupported instructions from returned guidance and external tool
schemas, adding production-shaped step-skill coverage, and restoring the server
test build.

## Architecture and limits

- `agent_go/pkg/agentworksclient`: hosted HTTP client, credential config, and MCP bridge.
- `agent_go/cmd/agentworks`: CLI argument handling.
- `agent_go/cmd/server/external_tools.go`: authenticated discovery, permissions,
  schema validation, workflow resolution, and operation dispatch.
- `step_based_workflow/external_plan_tools.go`: native plan schemas, kept for
  a future write-enabled API; unexposed.
- `external_builder.go`: existing query, event, human-input, and cancellation
  adapters; unexposed.
- `external_run.go`: run-mode tool proxy (pinned Run-mode sessions),
  `run_status` poller, `chat` turns, human-input replies, and JSON-direct
  execution, schedule, and trigger reads.
- `workspace/handlers/workflow_files.go`: workflow-confined file access.

The exposed tool set has one source of truth:
`agent_go/internal/agentworksproduct/product.yaml`, `chat.run`. The server
exposes `external_tools` first, in yaml order, then every non-denylisted `tools` name without
a native implementation, proxied to a pinned Run-mode session in yaml order;
names with a native implementation (`get_file_link`, `list_executions`,
`list_schedules`, `get_schedule_runs`, `trigger_schedule`, `stop_step`,
`stop_all_executions`) keep it. Go defines
the implementations (schemas, dispatch) while the yaml admits them. A yaml
name without an implementation — or an implementation missing from both
lists — fails server startup, and the CLI subcommand mappings are test-pinned
to the union. Changing the surface means editing the yaml and the golden test
together, deliberately; adding a tool to run mode exposes it externally unless
it appears in `external_denylist`.

For webhook runs, `get_schedule_runs` returns the accepted delivery's
`commit_sha`, `component`, `env`, and `deployed_at` under `webhook` when those
fields were present in the delivery body. `get_run` returns the same `webhook`
metadata for that run folder. These fields identify the deploy ping that
started the run; overlapping pings skipped by the trigger do not create runs.
`get_schedule_runs` can read retained history for a deleted schedule ID and
marks that case with `schedule_deleted: true`. `list_workflow_knowledge` pages
the learnings and knowledgebase inventories together with `limit` and `offset`;
follow `next_offset` while `has_more` is true. Directory listing tools return
`exists: true` when the requested path exists, even if the result page is empty.
Workflow schedule history uses the same `limit` and `offset` paging and keeps
terminal run records for at least 90 days. Older records are pruned when a new
run is recorded; run artifacts have a separate retention policy.

Public tool endpoints are `GET /api/external/v1/tools`,
`POST /api/external/v1/call`, and the MCP Streamable HTTP endpoint
`POST/GET/DELETE /api/external/v1/mcp` (get_api_spec + call_tool over the same catalog). The CLI
uses a browser-approved OAuth access token in the Bearer header; app sessions
can also use these endpoints with their normal JWT. Account token management is
`GET/POST /api/auth/access-tokens` and `DELETE /api/auth/access-tokens/{id}`, using
an app session only. Call bodies
are `{ "name": "TOOL_NAME", "arguments": { ... } }`.

Tool file reads are capped at 2 MiB; asset streaming uses
`GET/HEAD /api/external/v1/files/content?workflow_id=…&path=…`. Text is UTF-8; binary files return base64. Search
is literal and case-insensitive, with bounded depth, entry counts, and scanned
bytes. Pagination uses `next_offset` only when another result was found;
`truncated` can also mean the depth/scan budget was reached. Narrow the directory
or increase depth in that case. Symlinks, private credential directories, and
builder transcripts are excluded, as is coding-agent infrastructure:
AGENTS.md-style prompt files and the .claude, .agents, .codex, .cursor,
.gemini, and .pi tool directories, including the skills beneath them. Skills
stay readable through the knowledge tools, which serve the skill catalog;
learnings and ordinary documents are readable in their workflow's workspace.
Nothing is writable: run tools execute; they never author plans, files, or
configuration.

Errors use `{ "error": { "code": "...", "message": "..." } }`. CLI exit
codes are 3 for authentication/permission failure, 4 for conflicts, and 1 for
other failures (including `unknown_tool` for removed mutation paths).

## Token persistence and deployment

The server stores SHA-256 token hashes and metadata in SQLite under its private
`AGENTWORKS_STATE_ROOT/auth/` directory (with the normal durable runtime root as
a fallback). The directory is 0700 and database is 0600, outside workspace files.
The database is bound to `AUTH_SECRET`; rotating that secret invalidates previous
PATs, CLI and MCP OAuth grants, and app sessions. OAuth access and refresh tokens are
hashed in a separate SQLite database in the same directory. Tokens are not
stored in workflow documents or returned by listing endpoints. Creation
responses use `Cache-Control: no-store`.

Persist this state directory across agent-server restarts/redeployments. This
version supports a single hosted agent instance with persistent local storage;
it does not introduce a distributed token store for independent replicas.
Do not deploy independent token databases behind a load balancer. Multi-host
replication requires a shared transactional authentication store.
