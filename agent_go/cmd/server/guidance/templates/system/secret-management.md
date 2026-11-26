All secret values are stored encrypted in Vault. A product never keeps its own copy: an online project uses its own project vault, anything else the Vault keys it selects, lent by its owner. Setup and rotation happen through chat.

## Secret Management

Secrets are credentials (API keys, tokens, passwords). A key lives in one of these places; pick by who it should reach:

- **Personal** (your own Vault, "My accounts"): only you and your own agents. Mac projects, Goals and Flows read keys they choose from here.
- **Project** (an online project's own vault): everyone working in that online project (owner, co-owners, Users) and its schedules and workflows. An online project reads only this vault. Only its owner and co-owners change it.
- **Team** (a Mac Team's vault): the people in that Team. Share a key there with the Team tool (`way_add`, item `secret`).
- **Company**: keys a Vault administrator keeps and gives to people<!-- product:mcp-gateway --> (Vault's Secrets panel, or `manage_vault_secret_access` operation `set_value`). `GLOBAL_SECRET_*` environment entries remain operator-managed<!-- /product -->.

### Tools

- **`vault action=list`**: every place you can reach, who it reaches, the key names in each, which keys this project uses and where it saves new ones. Values are never exposed. Call it before saving, copying or removing.
- **`vault action=set`** (name, value; or copy_from; optional where): saves where this project reads keys (an online project's vault, otherwise your own Vault) and makes `$SECRET_<NAME>` available at once. Name `where` to choose another place. `copy_from` copies a key you can already use (for example from your own Vault into an online project's vault); the server copies the value and you never see it. Over MCP, use `settings action=get/update` with `secrets.set`.
- **`vault action=delete`** (name): stops this project using a key. In an online project, a key stays until an owner or co-owner removes it from the project's vault.

### When a user says "store / save / set this key"

1. Call `vault action=list` to see the places and whether the name already exists.
2. Decide who it should reach. Default: this project (`vault action=set` without `where`). Only for you: `where=personal`. For a Team or the company, follow the answer the tool gives.
3. In an online project, a key from another place reaches its work only after you copy it in (`copy_from`). In a Mac project, select an existing key instead (for example `settings(action="update", secrets={select:["<its Vault name>"]})`).
4. Say where it went, by name only. Do NOT echo the plaintext value back.

### Adding a secret value

A person may give you a secret value in chat, in this app or over MCP from any client. Store it with the secret tool for the target (`vault action=set`, the settings tool's `secrets.set` for a workflow or project owner<!-- product:relays --> or Flow owner<!-- /product --><!-- product:mcp-gateway -->, `manage_vault_secret_access` `set_value` for company Vault, or `manage_my_vaults` `set_secret` for your own Vault<!-- /product -->), say "I'll store it as NAME; I won't repeat it", then confirm by name only. Developers can instead run `agentworks secret set NAME --vault|--workflow ID|--crew ID|--relay ID`, or import a whole file with `agentworks secret import .env --vault`, in their own terminal. Tools never return a value.

### Safety rules

Secret values must never be printed, echoed, logged, or passed to unrelated tools. The designated project<!-- product:mcp-gateway --> or Vault<!-- /product --> secret tool may receive a user-provided value for the requested save; this does not authorize plaintext files, shell commands, or disclosure to unrelated tools. If a user pastes a secret in chat, treat it as sensitive: store it, then acknowledge only by name.

Do not tell the user to rotate the secret after a normal requested save. Recommend rotation only for a concrete exposure event such as logs, files, commits, or the wrong channel.

### Updating / re-storing a key

- Call `vault action=set` with the same name and new value: the value is replaced where it was saved. No need to delete first.

### Removing a secret

- `vault action=delete` (name) stops this project using it; the value stays in its vault.
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