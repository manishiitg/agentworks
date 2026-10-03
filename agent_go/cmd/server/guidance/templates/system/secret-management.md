## Secret Management

Secrets are credentials (API keys, tokens, passwords). They may come from two buckets:

- **Workflow secrets** — shared with users who have workflow access, AES-GCM encrypted, scoped only to one workflow or product project. Use these by default for workflow-specific credentials when the workflow secret tools are available.
- **Global secrets** — server-wide. Admins manage encrypted globals through chat and the Secrets UI; `GLOBAL_SECRET_*` environment entries remain operator-managed.

### Tools

- **`list_secrets`** — returns `global` (read-only names) and `workflow` (current workflow names, when scoped) buckets. Values are never exposed. Call before set/delete/attach.
- **`set_workflow_secret(name, value)`** — create or update a workflow-scoped value. Available only in workflow-scoped builder/workshop chats.
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

Secrets needed by multiple workflows belong in Global Secrets. Server admins can use `manage_global_secret(action="promote", name="NAME")` to move an existing active-workflow secret into the encrypted global store without reading or printing its value. To promote from another workflow without switching chats, first call `list_secrets(source_workflow_path="Workflow/rts-latency")`, then `manage_global_secret(action="promote", name="NAME", source_workflow_path="Workflow/rts-latency")`. Use the actual workspace path from the workflow listing, not a guessed label. Both calls check current admin rights and source workflow access. Omit the source path to use the active workflow. After promotion, attach the global name in the current destination workflow with the workflow config tool. Promotion makes it available server-wide, so only do this when the user intends that scope. The Secrets UI offers **Make global** with the same permission check. Existing source attachments keep working; other workflows select the global name. Name collisions fail without overwriting.

Admins can use `action="set"` with a new value to update a managed global, or `action="delete"` to remove it. Values are never returned. Changes apply to new turns and runs; running sessions may retain their existing environment. Environment-backed `GLOBAL_SECRET_*` entries remain operator-managed and cannot be overwritten here. Ordinary owners and readers cannot manage globals, and attaching read-only workflow context grants no authority to publish its secrets.


Shared credentials are managed in Vault > Secrets and selected in each product's Integrations > Secrets > Vault. Global names are not automatically authorized. Vault groups grant use permission; the backend rechecks the executing user's memberships for chat, reports, workflow runs, and schedules. An omitted or null global selection means none. Never request values in chat; use the secure Secrets editor. Prefer exact existing names returned by list_secrets; do not grant or select unrelated credentials. Managed connection OAuth tokens remain private to their MCP connections.
