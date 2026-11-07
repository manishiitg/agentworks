All secret values are stored encrypted in Vault. A product never keeps its own copy: it uses the Vault secrets its settings select, lent by its owner. Setup and rotation happen through chat.

## Secret Management

Secrets are credentials (API keys, tokens, passwords).

- **Your own Vault (My accounts)** — a secret you add to your Crew, Goal or Flow is saved here and selected on that project in one step. Everyone who uses the project gets it lent by you, without seeing the value. It stays yours: other projects of yours can use it too, and saving the same name again replaces its value everywhere.
<!-- product:mcp-gateway -->- **Company Vault secrets** — shared credentials with group permissions. They are added, rotated, shared and deleted in Vault (its Secrets panel, its chat, or Vault's MCP tools: `manage_vault_secret_access` operation `set_value`), by a Vault administrator. `GLOBAL_SECRET_*` environment entries remain operator-managed.<!-- /product -->

### Tools

- **`vault action=list`** — lists the Vault names you may use (the `global` bucket) and the ones this project uses (the `project` bucket). Values are never exposed. Call before saving, removing or selecting.
- **`vault action=set`** (name, value) — saves a value in the project owner's Vault and uses it in this project (owner only). Over MCP, use `settings action=get/update` with `secrets.set` and the returned settings version. Values are not returned.
- **`vault action=delete`** (name) — stops this project using a secret. Its value stays in the owner's Vault.

### When a user says "store / save / set this key"

1. Call `vault action=list` first to check whether the name already exists.
2. In a project chat, use `vault action=set` (name, value).<!-- product:mcp-gateway --> For credentials shared with colleagues, a Vault administrator can store the provided value with `manage_vault_secret_access operation=set_value`, using `values: {NAME: value}`. Discover the current schema first.<!-- /product -->
3. `vault action=set` selects the saved secret and injects it at once. To use a Vault secret that already exists, select it (for example `settings(action="update", secrets={select:["<its Vault name>"]})`). It becomes available to the builder shell and workflow steps as `$SECRET_<NAME>` (the name after the Vault prefix) without revealing plaintext to the model.
4. Confirm success. Do NOT echo the plaintext value back to the user — acknowledge by name only.

### Adding a secret value

A person may give you a secret value in chat, in this app or over MCP from any client. Store it with the secret tool for the target (`vault action=set`, the settings tool's `secrets.set` for a workflow or Crew owner<!-- product:relays --> or Flow owner<!-- /product --><!-- product:mcp-gateway -->, `manage_vault_secret_access` `set_value` for company Vault, or `manage_my_vaults` `set_secret` for your own Vault<!-- /product -->), say "I'll store it as NAME; I won't repeat it", then confirm by name only. Developers can instead run `agentworks secret set NAME --vault|--workflow ID|--crew ID|--relay ID`, or import a whole file with `agentworks secret import .env --vault`, in their own terminal. Tools never return a value.

### Safety rules

Secret values must never be printed, echoed, logged, or passed to unrelated tools. The designated project<!-- product:mcp-gateway --> or Vault<!-- /product --> secret tool may receive a user-provided value for the requested save; this does not authorize plaintext files, shell commands, or disclosure to unrelated tools. If a user pastes a secret in chat, treat it as sensitive: store it, then acknowledge only by name.

Do not tell the user to rotate the secret after a normal requested save. Recommend rotation only for a concrete exposure event such as logs, files, commits, or the wrong channel.

### Updating / re-storing a key

- Call `vault action=set` with the same name and new value — the value is replaced in the owner's Vault. No need to delete first.

### Removing a secret

- `vault action=delete` (name) stops this project using it; the value stays in the owner's Vault.
<!-- product:mcp-gateway -->- Values are deleted in Vault. Environment globals are removed through server configuration.<!-- /product -->

<!-- product:mcp-gateway -->## Reusing a secret across projects

A secret in your own Vault can be selected by any of your projects. Credentials shared with colleagues belong in a company Vault group, which a Vault administrator manages. Project ownership alone does not grant Vault administration.

Use the current attached canonical references and admitted tools. Legacy
provider-generated skill files under a project's .pi/.claude/.agents directories
may predate Vault; they do not describe this session's capabilities. Business
context about another system's GCP/Kubernetes secrets does not define AgentWorks
Vault storage or access. If a tool is not directly listed, use the runtime's
search_tools/get_api_spec discovery route before reporting it unavailable.

Projects explicitly select the Vault names they use. A project's selected
secrets are lent by its owner; Vault groups decide what the owner may use, and
the backend rechecks it for chat, reports, runs and schedules. Omitted/null
selections mean none. Only name references appear in manifests. Connection
OAuth tokens stay private to their MCP connections. Changes apply to new
turns/runs; already-running processes may retain their environment.
<!-- /product -->