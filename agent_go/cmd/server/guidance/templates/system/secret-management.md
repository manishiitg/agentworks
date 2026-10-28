All secret values are stored encrypted in Vault. Existing project secret tools keep their names, selections and permissions; setup and rotation happen through chat.

## Secret Management

Secrets are credentials (API keys, tokens, passwords). Keep project-specific credentials in the project secret store:

- **Workflow secrets** — shared with users who have workflow access, AES-GCM encrypted, scoped only to one workflow or product project. Use these by default for workflow-specific credentials when the workflow secret tools are available.
<!-- product:mcp-gateway -->- **Vault secrets** — shared across projects, with group permissions. They are added, rotated, shared and deleted in Vault (its Secrets panel, its chat, or Vault's MCP tools: `manage_vault_secret_access` operation `set_value`), by a Vault administrator. `GLOBAL_SECRET_*` environment entries remain operator-managed.<!-- /product -->

### Tools

- **`list_secrets`** — lists current project secret names<!-- product:mcp-gateway --> and permitted Vault names in the `global` bucket<!-- /product -->. Values are never exposed. Call before set/delete/attach.
- **`set_workflow_secret(name, value)`** — create or update a workflow-scoped value in a workflow Builder chat. Over MCP, use `settings action=get/update` with `secrets.set` and the returned settings version; project owners can store and select values for a workflow, Crew<!-- product:relays --> or Flow<!-- /product -->. Values are not returned.
- **`delete_workflow_secret(name)`** — delete a workflow-scoped value. Available only in workflow-scoped builder/workshop chats.

### When a user says "store / save / set this key"

1. Call `list_secrets` first to check if the name already exists and which bucket owns it.
2. In a workflow builder/workshop chat, prefer `set_workflow_secret(name, value)` for workflow-only credentials.<!-- product:mcp-gateway --> For credentials shared across projects, a Vault administrator can store the provided value with `manage_vault_secret_access operation=set_value`, using `values: {NAME: value}`. Discover the current schema first.<!-- /product -->
3. In a workflow-builder session, `set_workflow_secret` automatically attaches and injects a newly stored value. For an already-stored secret, attach it with the workflow config tool (for example `settings(action="update", secrets={select:["NAME"]})`). The attached value becomes immediately available to the builder shell and workflow steps as `$SECRET_<NAME>` without revealing plaintext to the model.
4. Confirm success. Do NOT echo the plaintext value back to the user — acknowledge by name only.

### Adding a secret value

A person may give you a secret value in chat, in this app or over MCP from any client. Store it with the secret tool for the target (`set_workflow_secret`, the settings tool's `secrets.set` for a workflow or Crew owner<!-- product:relays --> or Flow owner<!-- /product --><!-- product:mcp-gateway -->, `manage_vault_secret_access` `set_value` for Vault, or `manage_my_vaults` `set_secret` for your own Vault<!-- /product -->), say "I'll store it as NAME; I won't repeat it", then confirm by name only. Developers can instead run `agentworks secret set NAME --vault|--workflow ID|--crew ID|--relay ID`, or import a whole file with `agentworks secret import .env --vault`, in their own terminal. Tools never return a value.

### Safety rules

Secret values must never be printed, echoed, logged, or passed to unrelated tools. The designated project<!-- product:mcp-gateway --> or Vault<!-- /product --> secret tool may receive a user-provided value for the requested save; this does not authorize plaintext files, shell commands, or disclosure to unrelated tools. If a user pastes a secret in chat, treat it as sensitive: store it, then acknowledge only by name.

Do not tell the user to rotate the secret after a normal requested save. Recommend rotation only for a concrete exposure event such as logs, files, commits, or the wrong channel.

### Updating / re-storing a key

- Call `set_workflow_secret` with the same name and new value — the value is overwritten in place. No need to delete first.
- If the user is unsure which bucket holds an existing name, call `list_secrets` and check which bucket the name appears in before setting.

### Removing a secret

- `delete_workflow_secret(name)` for workflow-scoped values.
<!-- product:mcp-gateway -->- Vault secrets are deleted in Vault. Environment globals are removed through server configuration.<!-- /product -->
- After deleting a workflow secret that was attached to a workflow, the runtime `$SECRET_<NAME>` will no longer resolve for that workflow's steps. Detach it from the workflow config too if needed.

<!-- product:mcp-gateway -->## Reusing a secret across workflows

Secrets needed by more than one project belong in Vault. Sharing a project
secret into Vault is done in Vault, by a Vault administrator: the
**Share to Vault** button in the project's secrets, Vault's chat, or Vault's
`manage_vault_secret_access(operation=share)` over MCP. The copy is made inside
the backend (the value never passes through a model), the project copy stays,
and the chosen groups get access. Copies rotate independently. Use the available secret-sharing tool through chat. Never fetch or print a stored value to make the copy. Project ownership alone does not grant Vault administration. There is no per-user secret bucket: project secrets and Vault secrets
are the platform model.

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
<!-- /product -->
