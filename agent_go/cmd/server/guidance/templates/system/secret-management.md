## Secret Management

Secrets are credentials (API keys, tokens, passwords). They may come from two buckets:

- **Workflow secrets** — shared with users who have workflow access, AES-GCM encrypted, scoped only to one workflow or product project. Use these by default for workflow-specific credentials when the workflow secret tools are available.
- **Global secrets** — server-wide. Admins manage encrypted globals through chat and the Secrets UI; `GLOBAL_SECRET_*` environment entries remain operator-managed.

### Tools

- **`list_secrets`** — returns `global` (permitted shared names) and `workflow` (current workflow names, when scoped) buckets. Values are never exposed. Call before set/delete/attach.
- **`set_workflow_secret(name, value)`** — create or update a workflow-scoped value. Available only in workflow-scoped builder/workshop chats.
- **`manage_global_secret`** — administrator-only Vault management. `list_groups` discovers recipient IDs; `share` copies an existing project secret with explicit group grants; `set` creates/updates a managed global; `delete` removes it. Inspect the current tool schema. Discovery and management permissions do not grant runtime use.
- **`delete_workflow_secret(name)`** — delete a workflow-scoped value. Available only in workflow-scoped builder/workshop chats.

### When a user says "store / save / set this key"

1. Call `list_secrets` first to check if the name already exists and which bucket owns it.
2. In a workflow builder/workshop chat, prefer `set_workflow_secret(name, value)` for workflow-only credentials. For credentials shared across workflows, prefer admin-managed globals.
3. In a workflow-builder session, `set_workflow_secret` automatically attaches and injects a newly stored value. For an already-stored secret, attach it with the workflow config tool (for example `update_workflow_config(add_secrets=["NAME"])`). The attached value becomes immediately available to the builder shell and workflow steps as `$SECRET_<NAME>` without revealing plaintext to the model.
4. Confirm success. Do NOT echo the plaintext value back to the user — acknowledge by name only.

### Safety rules

Secret values must never be printed, echoed, logged, or passed to unrelated tools. The designated `set_workflow_secret` / admin `manage_global_secret(action="set")` tool may receive a user-provided value for the requested save; that exception does not authorize plaintext files, shell commands, or disclosure to other tools. If a user pastes a secret in chat, treat it as sensitive: store it, then acknowledge only by name.

Do not tell the user to rotate the secret after a normal requested save. Recommend rotation only for a concrete exposure event such as logs, files, commits, or the wrong channel.

### Updating / re-storing a key

- Call `set_workflow_secret` with the same name and new value — the value is overwritten in place. No need to delete first.
- If the user is unsure which bucket holds an existing name, call `list_secrets` and check which bucket the name appears in before setting.

### Removing a secret

- `delete_workflow_secret(name)` for workflow-scoped values.
- Admins use `manage_global_secret(action="delete", name="NAME")` for managed globals. Environment globals are removed through server configuration.
- After deleting a workflow secret that was attached to a workflow, the runtime `$SECRET_<NAME>` will no longer resolve for that workflow's steps. Detach it from the workflow config too if needed.

## Reusing a secret across workflows

Secrets needed by multiple products belong in Vault. In an authorized builder
chat, discover `manage_global_secret` through the current runtime tool catalog,
inspect its schema, and call `action="list_groups"` to list recipient groups.
For an existing project secret, use `action="share", name="SOURCE_NAME",
group_ids=["GROUP_ID"]` with optional `vault_name="VAULT_NAME"`. This copies the
value inside the backend, keeps the project copy and attachments unchanged,
rejects existing Vault names, and grants only the chosen groups. Ask about groups
when the user's intent is unspecified; never default to Platform. Copies rotate
independently. The shared UI offers **Share to Vault** with the same checks.

To share from another accessible project, first call
`list_secrets(source_workflow_path="EXACT_PATH")`, then use that same
`source_workflow_path` in the share action. Use the real workspace path from the
project/workflow listing. Omit it to use the active workspace. Do not fetch,
print, or pass the source value through the model or shell. Legacy
`action="promote"` removes the source copy and creates no grants; prefer share.

Admins can use `action="set"` with a user-supplied value to create or update a
managed Vault secret, and `action="delete"` to remove it. Prefer the secure editor
for entering new values. A request to add an existing project secret to Vault
should use share by reference. Only environment-backed `GLOBAL_SECRET_*` entries
remain operator-managed and cannot be edited from chat. Do not claim all global
secrets are read-only. Ordinary owners/readers cannot manage Vault values or
grants, and read-only project context does not grant publication authority.
There is no reusable per-user secret bucket: project secrets and shared Vault
secrets are the current platform model.

Use the current attached canonical references and admitted tools. Legacy
provider-generated skill files under a project's .pi/.claude/.agents directories
may predate Vault; they do not describe this session's capabilities. Business
context about another system's GCP/Kubernetes secrets does not define AgentWorks
Vault storage or access. If a tool is not directly listed, use the runtime's
search_tools/get_api_spec discovery route before reporting it unavailable.

Recipients explicitly select shared names in their product integrations.
Vault groups grant use permission; the backend rechecks the executing user's
memberships for chat, reports, runs and schedules. Omitted/null selections mean
none. Only name references appear in manifests. Connection OAuth tokens stay
private to their MCP connections. Changes apply to new turns/runs; already-running
processes may retain their environment.
