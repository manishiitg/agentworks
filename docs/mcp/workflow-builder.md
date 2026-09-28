# Workflow Builder over MCP

Workflow owners and editors can ask the configured AgentWorks Builder model to edit a selected workflow through MCP. The message continues that person's existing workflow chat: the owner's main chat, or the editor's own chat. The conversation is shared with the browser; execution permissions belong to each operation independently.

This release provides managed plan and source-file editing. It does not expose native shell, arbitrary connected MCP tools, account administration, sharing changes, secrets, background reviewers, workflow execution, or report/database authoring through the Builder model. Existing separately authorized Run tools remain available for execution. Crew `ask_crew` and typed function-call behavior are unchanged.

## Enable and authorize

Set `AGENTWORKS_MCP_BUILDER_ENABLED=true` on the server. The default is disabled. The endpoint remains `/api/external/v1/mcp`, with the existing outer tools `get_api_spec` and `call_tool`.

For OAuth, explicitly request all four scopes:

```
workflows:read files:read runs:execute builder:chat
```

The consent page requires selection of workflows the signed-in person currently owns or may edit. Every workflow permission on this Builder connection is bounded to those selections. Access and refresh tokens retain the bounds; changing them requires a new authorization. A compatible OAuth client must explicitly request Builder scopes; omitted scopes and the ordinary CLI device-login flow retain their previous defaults.

The access-token API also accepts the same four scopes with `workflow_ids` and `all_workflows: false`. Direct `files:write` and `plan:write` scopes remain unavailable. Old connections are not upgraded. Knowing a workflow ID or owning a workflow does not bypass missing Builder consent.

Managed source-file writes require the workflow documents to be mounted locally on the server. Remote-only workspace deployments fail those writes closed. A shared, server-owned auth state directory outside workflow documents is required for the durable operation records.

## Operations

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

Status is `queued`, `running`, `completed`, `failed`, `canceled`, or `interrupted`. Pending questions are returned only for a running operation. Poll until terminal or until a question needs a reply. Repeating a completed cancel is harmless. Another connection may submit to the same person's chat, but cannot read/control an operation created by a different connection.

Polling works without native MCP elicitation. A successful HTTP 202/204 is treated as a successful MCP result rather than a tool error.

## Execution and storage

Operations persist in server-owned SQLite state beside OAuth state. Records contain user, stable grant ID, workflow, chat, submission, message, status and final answer; they do not contain bearer tokens. The existing durable conversation queue stores the operation selector. Before execution, the worker reloads the trusted record and reconstructs the request, so editing queue JSON cannot change the operation's model or runtime policy.

The queue restores both PAT and OAuth principals. Account access, current workflow role, token/family validity, selected workflow bounds and Builder scope are checked on ingress, dequeue and tool dispatch. A watcher interrupts active work when authority is revoked or the feature is disabled. Revocation does not undo effects already performed.

Browser follow-ups queue while a Builder MCP operation owns the foreground. They retain their own principal. Retained model runtimes are rebuilt when authority changes; the visible transcript stays in the same chat. Questions carry an explicit operation ID attached by trusted tool dispatch, so another browser/background question in the same chat is not exposed or answered accidentally.

The model uses managed read/list/search/write tools and target-bound plan tools. File writes require `expected_revision`, reject traversal/symlinks, and refuse private runtime files, raw database files, workflow manifests and raw plan files. Plan changes use existing typed plan operations. Writes are staged and revisions rechecked before replacement. MCP file edits are serialized with each other, but replacement is not an atomic compare-and-swap with the browser's independent direct file editor; avoid editing the same file concurrently there.

An operation that had started before a server restart is not automatically replayed: its outcome may be uncertain, so it becomes interrupted. Inspect the workflow before sending a new edit. Durable questions/model execution recovery remain separate work. Unstarted queued operations can resume only after fresh grant validation.

## Verification

The regression coverage includes actual Streamable HTTP MCP discovery/submit/poll/cancel, owner main-chat continuity, editor isolation, cross-grant denial, duplicate and conflicting submissions, reservation recovery, OAuth restoration, consent/refresh/migration, canceled and revoked work, scoped questions, managed file path and revision checks, runtime policy changes and native-tool denial.

Useful focused commands, from `agent_go/`:

```sh
go test ./cmd/server -run '^(TestExternal|TestAccessToken|TestMCPOAuth|TestCLIOAuth|TestDurableConversationTurnQueue|TestWorkflowChatPolicy|TestWorkflowRetained|TestToolExecutionContext|TestWorkflowBrowserFollowup|TestChatPolicyRoleRequiresReconnect|TestWorkflowLiveInput)' -count=1
go test ./internal/agentworksproduct ./pkg/accesstokens ./cmd/server/virtual-tools -count=1
go test -race ./cmd/server -run '^TestExternalBuilderOperations' -count=1
```

The automated lifecycle tests use a deterministic model hook. Before enabling this on a deployment, smoke-test with its configured model and an actual external AI client against a throwaway workflow: submit a small edit, confirm it appears in the same browser chat, answer a question, cancel a second operation, and inspect the resulting plan/files. This implementation has not been deployed or validated against a live hosted model.
