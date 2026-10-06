# Local and Server Agent Design

**Status: guarded public MCP writes, the file executor and minimal Local chat mode implemented. Updated 2026-10-06.**

This document replaces the workspace-router proposal. Support two directions:

- **Local → server:** a local agent uses the existing public AgentWorks MCP to
  work with server-owned workflows. Extend that MCP with guarded file writes.
- **Server → local:** the agent and LLM run in the website's server backend;
  a connected laptop executes authorized file operations in one selected folder.
  Use an internal authenticated device connection for these file operations.

Keep each workspace authoritative on its owning machine. Neither direction
requires a synchronized filesystem or a second working copy.

The router prototype, placement/move APIs, special server authentication and
remote-only `mcp_only` enforcement have been removed. Local → server uses the
public MCP. Server → local has a dedicated `agentworks executor connect` command
and an authenticated outbound WebSocket connection for file operations.
The retired proposal remains available in Git history.

Implemented now:

- Public `write_file`, explicit optional `files:write` consent, persisted token
  folder caps, workflow access checks, revision conflicts and durable receipts.
- File-owning workspace service writes, including deployments where the agent
  server does not share its filesystem. The internal service route is not exposed
  through the general workspace proxy.
- Managed document, diff, upload, move/delete and Builder writes share the same
  cross-process serialization boundary. Unmanaged local editors or shell commands
  do not participate in that lock; reads and writes remain individually atomic.
- Executor login with `devices:connect`, owner-scoped device discovery and file
  list/read/write tools for interactive Code chat agents, local guard enforcement,
  heartbeat/reconnect, token revocation and pending-request failure on disconnect.
- Local file writes retain private receipts across reconnects/restarts. The
  server never blindly resends a write after a timeout or disconnect.
- Code **Settings → General → Local CLI connection** offers **Connect local files**.
  The browser-scoped binding takes over file access for the current Code chat.
  The right side shows only **Local CLI connection**, **Costs**, and **Models**.
  The connection panel provides CLI setup, folder selection, status and disconnect.
  File reads and edits happen through the agent in chat, with guarded local tools.
- Local-connected turns exclude MCP connections, skills, project/Vault secrets,
  background agents, server terminal/browser tools and other server adapters.
  Dashboards/databases, schedules/triggers and messaging stay disabled. Saved
  settings are excluded from the turn without changing project configuration.
  Offline bindings retain these restrictions. Disconnect restores normal Code.
  Other chats and existing server schedules/connections are unchanged.

The executor is file-only. Shell/browser execution, laptop workflow routing,
schedules, device-management panels, public history/restore APIs and retention
policies are outside this change.
Before-content history is capped at 128 KiB per write; full revisions are retained.
Device connections currently live in one backend process. Deployments with
multiple backend instances need routing affinity for device and chat requests.
No production deployment or external LLM call is performed by this change.

## 1. Decisions and boundaries

| Topic | Decision |
|---|---|
| Local agent accessing server workflows | Use the existing separate public MCP, `/api/external/v1/mcp` |
| Public MCP file writes | Add `write_file` with explicit permission, folder guards, revision checks and audit history |
| Plans and configuration | Keep direct file writes blocked; change them through typed tools |
| Transparent local workspace routing | Retire the branch's router approach; do not introduce a second remote file-access system |
| Server agent accessing laptop resources | Use a laptop executor connected to the same backend that serves the website |
| MCP for the website's own laptop executor | Not required; reuse internal tool handlers and their policy |
| Connection establishment | Laptop initiates an authenticated outbound connection |
| Files | Remain on their owning machine; reads return content to the agent |
| Browser-only website | Can use explicitly selected files where the browser supports it; cannot supply arbitrary local shell/application access |
| Full local tools | Require an installed/running laptop companion |

The public MCP and the CLI tool bridge are different interfaces. This design's
local → server direction uses the **separate public MCP**, not the coding CLI's
internal `mcpbridge`. Our own server → local executor does not need to convert
every internal tool call into an external MCP call.

## 2. Use cases

| Direction | Good fit | Dependency |
|---|---|---|
| Local → server | A person's Claude/Codex/other agent reads and edits shared workflow code or documents using that person's LLM | Server endpoint is reachable and the connection has the required grants |
| Local → server | Inspect runs, call workflow functions, ask Crews or manage permitted Vault resources from an external agent | Existing product tools and their independent permissions |
| Server → local | The website supplies the model and conversation while a developer keeps source files on their laptop | Laptop executor is connected |
| Server → local | The website agent reads and edits files within an explicitly shared local folder | Explicit device/folder/tool grants and an appropriate local executor |

Using a server LLM alone does not require moving the agent loop. A local loop
with a server model gateway is a separate, simpler option if centralized model
credentials and billing are the only requirement.

## 3. Local → server through public MCP

```mermaid
flowchart LR
    A[Local agent and LLM] -->|Public MCP| B[Server AgentWorks]
    B --> C[Server workspace and product tools]
    C -->|Results| A
```

The local agent discovers operations through `get_api_spec` and invokes them
with `call_tool`. Workflow IDs identify resources; caller-supplied filesystem
roots must not select arbitrary server directories.

The existing catalog already includes raw file reads/listing/search, plans,
run evidence and controls, workflow functions, Crew operations, Brain and
authorized Vault administration. The effective catalog depends on the
connection's scopes and the caller's live permissions.

Add direct file authoring so the local model can decide an edit and submit it
without asking a second model to rewrite the file. Existing `builder_chat`
remains an explicit delegation to the **server's configured Builder model**.
Likewise, calling run/step/Crew operations invokes the configured service-side
execution; adding file writes does not relocate those agents to the laptop.

Provider-native tools on the laptop still operate on laptop resources. Remote
operations must name the server's MCP tools. This direction does not require a
blanket shutdown of native tools for an otherwise local external agent.

### `write_file` contract

```json
{
  "name": "write_file",
  "arguments": {
    "workflow_id": "workflow-id-from-list_workflows",
    "path": "code/task.py",
    "content": "print('updated')\n",
    "expected_revision": "revision-returned-by-read_file",
    "request_id": "unique-id-for-this-intended-write"
  }
}
```

This tool is available to explicitly authorized connections. The first slice creates or
replaces bounded UTF-8 source/document files. Use `expected_revision: missing`
for creation. A successful response includes the resulting revision and audit
receipt. Delete, move, patch and binary upload are separate future decisions.

### Permission and folder-guard rules

Effective write authority is the intersection of:

**connection scope ∩ current user access ∩ granted write folders ∩ operation
policy**, with blocked/read-only/protected paths taking precedence.

- Require explicit `files:write` consent and current workflow edit access.
  Existing read/run connections do not silently acquire writes.
- Resolve the workflow and guard on the server. A public MCP request has no
  automatic right to inherit another chat's session guard, and a client-sent
  allowlist cannot widen access.
- Derive authoring-folder grants from the selected workflow and connection
  restrictions. Carry any legitimate execution-session narrowing as trusted
  server state. Define how folder caps are stored and issued before rollout;
  folder caps are persisted as `file_guard` on personal access tokens. OAuth write
  consent defaults to the workflow's public authoring paths; tokens can narrow it.
- Apply `WritePaths`, `BlockedPaths`, `BlockedWritePaths` and read-only grants
  using shared policy evaluation. Missing or unreadable required policy refuses
  the write.
- Normalize and confine paths before opening them; reject absolute paths,
  traversal, private paths and symlinks, including symlink substitution during
  the operation. Match path components, not a raw string prefix.
- Preserve the protected-file rules below even when the containing workflow
  folder is writable. Reading a plan can remain permitted.

| Example target | Direct file-write result |
|---|---|
| `code/task.py`, `docs/process.md` | Allowed within effective write grants |
| A folder granted only for reading | Rejected |
| `planning/*`, including `planning/plan.json` and step configuration | Rejected; use typed plan tools |
| `workflow.json` | Rejected; use typed workflow/configuration tools |
| Databases and their journal files | Rejected; use scoped database tools |
| Secret stores, authentication files, private runtime/audit state | Rejected |
| Another workflow, a path outside the grant, or a symlink escape | Rejected |

A `files:write` grant permits authoring executable source: later runs may execute
that source. It must therefore require authoring authority, not merely run or
read permission. It does not grant shell execution, plan writes or Vault access.

### Revisions, durability and retries

The file-owning service must check the revision and replace the file under one
shared SQLite serialization boundary outside the documents root. MCP, browser
editing, document patch/move/delete/upload and Builder file edits use it. Direct
filesystem edits by other processes cannot be made transactional by this API.

Stage and atomically replace the file while preserving appropriate file modes.
Record caller, workflow, path, request ID, previous/resulting revision and
recoverable history. Fail closed when required authorization or audit storage
is unavailable. If the file changed but receipt confirmation failed, report an
uncertain outcome that can be resolved by request ID instead of retrying blindly.

Retrying the same request ID and payload returns the recorded outcome. Reusing
that ID with a different payload is rejected. A revision conflict requires
rereading and reconciling; it must not turn into an unconditional overwrite.

The shared `workflowfiles` editor owns guarded writes and durable receipts at
`WORKSPACE_FILE_STATE_DIR`, or under configured `AGENTWORKS_STATE_ROOT`, with
`.<docs-folder>-file-edits/` as the private sibling fallback. Records
include authenticated user ID/name, connection ID, source, logical root, path,
request ID, before-content up to 128 KiB and
both revisions. Prepared records reconcile an interrupted atomic replacement.
Builder's existing operation audit remains separate; its mounted file writer
participates in the same serialization lock. Public writes always use the
file-owning service and therefore support separate service volumes.

## 4. Server → local through a file executor

```mermaid
flowchart LR
    U[Code website chat] --> A[Server agent and LLM]
    L[Local CLI] -->|Authenticated outbound WebSocket| G[Server device gateway]
    A -->|Selected folder file request| G
    G -->|Existing connection| L
    L --> F[Granted local folder]
    F -->|Result and write receipt| A
```

The server owns the agent loop and LLM. The CLI lists, reads and writes files
under locally approved folder aliases. It exposes no local listener, shell,
browser, application control or credential export. Requested file contents
reach the server and LLM.

### Connection and website experience

1. Open a Code chat and switch **Server → Local** in its composer (or use
   **Settings → General → Local CLI connection**). Local has a separate minimal
   tool policy, applied before a folder is selected. The selected model stays the same.
2. Install the CLI, sign in with `devices:connect`, and run
   `agentworks executor connect` with named folder grants.
3. Select the computer and shared folder. The browser remembers this binding
   for the account, server workspace and current chat only.
4. The right-side toolbar offers **Local CLI connection**, **Costs**, and
   **Models**. The connection panel shows setup, folder permissions, status
   and disconnect. No file editor, terminal, browser, dashboard, automation,
   integrations or general project settings are shown.
5. The agent uses local file tools from chat. MCP connections, skills,
   project/Vault secrets and background agents are excluded from local turns.
   Provider authentication remains available for the selected server model.
6. Disconnect restores normal Code mode. Saved configuration and other chats
   remain unchanged; Ctrl-C in the CLI ends folder sharing.

Only interactive Code turns receive local tools. Crew, Brain, Vault, schedules,
messaging turns and unattended agents do not inherit this binding. Native
server filesystem/terminal/browser tools are disabled for connected turns.

### Authority and failures

Every dispatch validates website ownership and live connection authorization.
The CLI independently confines requests to the approved root, checks folder
permissions and applies protected-file rules. Absolute paths and credentials
never cross the connection.

An offline device retains the binding and restrictions. Ordinary chat can
continue; local file calls fail without server-file fallback. Connection controls
remain available during setup and offline.

Writes require a revision and unique request ID. Private receipts survive CLI
reconnects and restarts. Retry an uncertain write with its identical request;
never resend it blindly under a new ID. Typed plan/configuration edits, laptop
workflow execution and builds/tests are outside this file-only mode.

The device registry currently lives in one backend process. Multiple instances
need routing affinity for device and chat requests. This change does not deploy
the service or invoke an external LLM.

## 5. Existing implementation references

| Area | Source |
|---|---|
| Public MCP transport and discovery | [external_mcp.go](../../agent_go/cmd/server/external_mcp.go) |
| External catalog and dispatch | [external_tools.go](../../agent_go/cmd/server/external_tools.go) |
| Public tool admission | [product.yaml](../../agent_go/internal/agentworksproduct/product.yaml) |
| Token scopes and issuance | [store.go](../../agent_go/pkg/accesstokens/store.go), [mcp_oauth.go](../../agent_go/cmd/server/mcp_oauth.go) |
| Existing bounded file reads | [external_file_reads.go](../../agent_go/cmd/server/external_file_reads.go) |
| Existing managed Builder file writer | [external_builder_workspace.go](../../agent_go/cmd/server/external_builder_workspace.go) |
| Builder audit/history | [external_builder_audit.go](../../agent_go/cmd/server/external_builder_audit.go) |
| Session folder-guard state | [types.go](../../agent_go/pkg/common/types.go) |

Related documentation: [public MCP and CLI](../getting-started/agentworks-cli-mcp.md),
[folder guards](folder_guard_system.md), [workflow scheduling](../workflow/workflow_scheduling.md).
