# Workflow Builder over MCP

Workflow owners and editors can edit plans and source files directly over MCP, using the current website roles, connection consent, native validators, revisions and plan changelog. They can also ask the configured AgentWorks Builder model to edit a selected workflow. The message continues that person's existing workflow chat: the owner's main chat, or the editor's own chat. The conversation is shared with the browser; execution permissions belong to each operation independently.

This release provides managed plan and source-file editing. It does not expose native shell, arbitrary connected MCP tools, account administration, sharing changes, secrets, background reviewers, workflow execution, or report/database authoring through the Builder model. Existing separately authorized Run tools remain available for execution. Crew `ask_crew` and typed function-call behavior are unchanged.

## Enable and authorize

CLI and MCP authoring are always available within the enabled products. The legacy `AGENTWORKS_MCP_BUILDER_ENABLED` deployment switch is ignored; existing connection consent and current roles remain required. The endpoint remains `/api/external/v1/mcp`, with the existing outer tools `get_api_spec` and `call_tool`.

For OAuth, explicitly request all four scopes:

```
workflows:read files:read runs:execute builder:chat
```

OAuth Builder consent follows the account: no workflow selection is requested. Each call resolves the current account, project and team permissions. Existing bounded grants keep their workflow limits. A compatible OAuth client must explicitly request Builder scopes; omitted scopes and the ordinary CLI device-login flow retain their previous defaults.

The access-token API also accepts the same four scopes with `workflow_ids` and `all_workflows: false`. Direct source/documentation writes reuse this authoring consent and current owner/editor roles; no separate file-write scope is issued. Typed `plan` operations reuse the same consent; the unused `plan:write` permission is retired. Existing Builder-chat consent also authorizes these direct edits, explained in the consent details and Connect notice. Old plan:write-only grants lose that unused permission without receiving Builder consent. Knowing a workflow ID or owning a workflow does not bypass missing Builder consent. Local full-access tokens include Builder consent; recreate an older local token that lacks it and update the client connection. Local preset tokens coexist with other clients, up to 32 active presets per account; revoke unused tokens in Connect to free capacity. Read-only and explicitly scoped tokens retain their limits.

Managed source-file writes require the workflow documents to be mounted locally on the server. Remote-only workspace deployments fail those writes closed. A shared, server-owned auth state directory outside workflow documents is required for the durable operation records.

## Direct plan and file edits

Team administration uses `workflow_team` directly. Call `action=get` with the
workflow ID to read the policy, stable route IDs and schedule revisions. Authorized
managers with Builder consent also receive IDs/usernames for the complete enabled
user directory, as in the website member picker; emails are omitted. Read-only
workflow connections can discover and use `get`, but cannot read that management
directory or mutate the policy. Call `action=update` with the complete `team` policy and
`expected_revision` (`0` for initial enablement). The policy has one `leader_id`,
`contributors`, `routes`, and optional `read_only_step_ids`. Each Contributor's
`run`, `manage_schedules`, `approve`, `approval_ids`, `backup`, `publish` and
`notify` grants are independent; route entries identify `step_id`, `route_id`
and `contributor_id`. Copy the current policy to preserve intended grants.

The website backend enforces owner-only enablement, current Leader/admin
management, valid members/routes, reassignment and ownership transfer. Updates
use the shared lock and revision check, current Builder consent and manifest
file guard. They do not start a model turn. After a permission change, inspect
schedules with `manage_schedules`, then use `workflow_team` with
`action=reauthorize_schedule`, `schedule_id`, current `expected_revision` and
`expected_schedule_revision` from `get`. This checks standing route/action grants
and the exact schedule while retaining its timing and enabled state. If an update
has an uncertain response, read the saved policy before retrying.

Read `get_plan` for the combined plan/configuration revision. Discover the
current native operations with `plan action=list`; there are 20 operations,
including create, add/update steps, manage routes, change type, maintain and
restore from the existing changelog. Pass the returned operation name as
`action`, its exact `arguments`, `expected_revision` and a unique `request_id`.
These edits use no model turn or chat identity. Current team route ownership
and shared-source rules still apply; readers are refused.

All artifacts are prepared before the shared commit lock validates and writes
them. Identical successful retries return the durable receipt. A commit rejected
before its first write stores a failed receipt, and identical retries return
that same refusal. Interrupted writes require inspecting the plan/changelog;
reuse of a request ID for different arguments is refused.

Use `files action=read|write` with `workflow_id` for permitted source files,
current `expected_revision` and `request_id`. Relay Python uses these guarded
writes and `relay action=test|get_run|publish|run|releases`, retaining the native
runtime, draft/release separation and durable execution receipts. Relays have
no workflow plan. No separate file, dashboard or plan write permission is issued.

## Model-assisted Builder operations

Discover the available names with `get_api_spec`. Get a specific schema with `get_api_spec({"names":["builder_chat"]})`, then invoke the inner operation with `call_tool`:

```json
{
  "name": "builder_chat",
  "arguments": {
    "workflow_id": "invoices",
    "submission_id": "edit-invoice-validation-001",
    "message": "Add validation for missing invoice numbers. Explain the changes."
  }
}
```

`builder_chat` returns `operation_id`, `session_id`, `submission_id`, and an initial status. It accepts an optional `session_id` belonging to the same user and workflow. When omitted, it uses the workflow UI's live/latest conversation selection; it creates a conversation only if none exists. It uses the workflow's configured model, not a caller-supplied provider or connection.

Reusing a submission ID with the same payload returns the same operation. Reusing it with different text, workflow or an explicitly different chat returns a conflict. A retry can repair an interrupted reservation-to-queue handoff without submitting the edit twice. Messages are limited to 32,000 bytes and there may be at most 16 queued/running Builder operations per person.

| Operation | Arguments in addition to `workflow_id` | Behavior |
|---|---|---|
| `builder_status` | `operation_id` | Returns the operation's state, final `answer`, error, and `pending_inputs`. It does not stream the shared conversation's other turns. |
| `builder_reply_input` | `operation_id`, `request_id`, `response` | Answers an operation-owned question; `request_id` is the pending input's `unique_id`. Choices, expiry and current access are checked. |
| `builder_cancel` | `operation_id` | Removes that operation's queued turn, or interrupts its active foreground work and withdraws its questions. Other queued browser turns and unrelated questions remain intact. Completed edits are not rolled back. |
| `builder_file_history` | `path` | Lists up to 30 recent Builder edits for one source path, including revision and edit IDs, sizes, and whether each edit is restorable. A replacement credential from the same account can recover eligible prior edits. |
| `builder_restore_file` | `path`, `edit_id`, `expected_revision` | Restores the file content from before a restorable edit. It refuses to overwrite a file whose current revision differs from `expected_revision`. Restoring the creation of a new file removes it. |

Status is `queued`, `running`, `completed`, `failed`, `canceled`, or `interrupted`. Pending questions are returned only for a running operation. Poll until terminal or until a question needs a reply. Repeating a completed cancel is harmless. Another connection may submit to the same person's chat, but cannot read/control an operation created by a different connection.

Polling works without native MCP elicitation. A successful HTTP 202/204 is treated as a successful MCP result rather than a tool error.

## Execution and storage

Operations persist in server-owned SQLite state beside OAuth state. Records contain user, stable grant ID, workflow, chat, submission, message, status and final answer; they do not contain bearer tokens. The existing durable conversation queue stores the operation selector. Before execution, the worker reloads the trusted record and reconstructs the request, so editing queue JSON cannot change the operation's model or runtime policy.

The queue restores both PAT and OAuth principals. Account access, current workflow role, token/family validity, selected workflow bounds and Builder scope are checked on ingress, dequeue and tool dispatch. A watcher interrupts active work when authority is revoked or the feature is disabled. Revocation does not undo effects already performed.

Browser follow-ups queue while a Builder MCP operation owns the foreground. They retain their own principal. Retained model runtimes are rebuilt when authority changes; the visible transcript stays in the same chat. Questions carry an explicit operation ID attached by trusted tool dispatch, so another browser/background question in the same chat is not exposed or answered accidentally.

The model uses managed read/list/search/write tools and target-bound plan tools. File writes require `expected_revision`, reject traversal/symlinks, and refuse private runtime files, raw database files, workflow manifests and raw plan files. Plan changes use existing typed plan operations. Writes are staged and revisions rechecked before replacement. MCP file edits are serialized with each other, but replacement is not an atomic compare-and-swap with the browser's independent direct file editor; avoid editing the same file concurrently there.

Every managed Builder plan mutation and source-file write writes an audit row before the change. Rows identify the user, credential grant, operation, tool, workflow and target, and record whether the call completed. Plan changelog entries also carry `external_builder` origin, operation ID and `via_token`. File rows retain revisions and byte sizes, plus at most 128 KiB of the prior UTF-8 content in server-owned SQLite. They do not retain the resulting content. An edit whose prior file exceeds that cap has `restorable: false` in `builder_file_history`; `builder_restore_file` returns `restore_unavailable` for it. Restoring a newly created file remains possible because its prior state is absence. At most 30 edits per user, workflow and path are kept, and rows older than 30 days are removed during store maintenance. A new credential from the same account can recover eligible prior edits even after the old credential is revoked. This is separate from the workspace UI's Git-based file-history view, which does not automatically commit ordinary editor writes. The server-owned state and its backups can therefore contain bounded copies of edited source files. Direct file restoration does not roll back plan changes or effects of any workflow run.

The workflow's connected MCP servers and account tools are removed for external Builder turns after manifest selection, and the final tool gate rejects tools outside the managed set. The current account product permission and per-user workflow allow-list are rechecked during execution.

An operation that had started before a server restart is not automatically replayed: its outcome may be uncertain, so it becomes interrupted. Inspect the workflow before sending a new edit. Durable questions/model execution recovery remain separate work. Unstarted queued operations can resume only after fresh grant validation.

## Verification

The regression coverage includes actual Streamable HTTP MCP discovery/submit/poll/cancel, owner main-chat continuity, editor isolation, cross-grant denial, duplicate and conflicting submissions, reservation recovery, OAuth restoration, consent/refresh/migration, canceled and revoked work, scoped questions, managed file path and revision checks, file restoration and mode preservation, audit binding, runtime policy changes and connected/native-tool denial.

Useful focused commands, from `agent_go/`:

```sh
go test ./cmd/server -run '^(TestExternal|TestAccessToken|TestMCPOAuth|TestCLIOAuth|TestDurableConversationTurnQueue|TestWorkflowChatPolicy|TestWorkflowRetained|TestToolExecutionContext|TestWorkflowBrowserFollowup|TestChatPolicyRoleRequiresReconnect|TestWorkflowLiveInput)' -count=1
go test ./internal/agentworksproduct ./pkg/accesstokens ./cmd/server/virtual-tools -count=1
go test -race ./cmd/server -run '^TestExternalBuilderOperations' -count=1
```

The automated lifecycle tests use a deterministic model hook. Before enabling this on a deployment, smoke-test with its configured model and an actual external AI client against a throwaway workflow: submit a small edit, confirm it appears in the same browser chat, answer a question, cancel a second operation, and inspect the resulting plan/files. Record live checks against the deployment revision in PLAT-856; remote regression tests alone do not establish hosted model or deployment validation.

## Workflow Crew attachments

Use `workflow_crews` with `workflow_id` and `action=list` to inspect a workflow's
read-only Crew attachments. Detach an alias explicitly with `action=detach` and
`alias` from that list. Existing Builder consent and the current workflow
owner/editor role apply, including connection bounds and file guards.
This removes the read binding and its live grants; it leaves plan steps unchanged.
Crew deletion still refuses remaining attachments and Crew steps. `crew_references`
handles Crew-to-workflow references instead. New workflow Crew attachments continue
through the native Builder's `manage_crew_attachment` tool.
# Product guide and Brain starter skill

The MCP entry skill includes a short product-choice overview. Use
`help action=topic topic=product-guide` for purpose, abilities, access and examples,
or `help action=skills topics=[agentworks-mcp]` for the MCP usage skill and its
references. The canonical source lives in `agent_go/pkg/productguide`.

Brain's Shared bootstrap offers `Shared/AgentWorks/product-guide.md` and the
installable `Shared/Skills/agentworks-mcp` package through the native public Brain
tools. Each existing note or user skill package is preserved. Starter offers are
recorded in platform state, so deleting one keeps it deleted after a restart or
source update; a creation with an ambiguous outcome requires explicit operator
reconciliation. Bootstrap never replaces an old copy with a new source release.

Guide and skill source versions start at `1.0.0`; their `references/versions.json`
records exact content SHA-256 fingerprints. MCP topic/package versions identify
the current rendered content for conditional refresh. Brain's returned opaque
entry version is the value for `expected_version`; source versions cannot replace
it. Installed CLI skills and hosted ZIP/plugin packages include the product
reference and versions file. Bump the relevant source version when editing either
canonical source; the fingerprints still detect exact content or product changes.
