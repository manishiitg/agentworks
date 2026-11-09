## Secrets

Manage secrets through the tools in this chat. If the person asks to inspect
them, open **Secrets** in Integrations or Recent, or Ctrl+K (Cmd+K on Mac),
search Secrets. Agent UI controls open only the Dashboard; point to the current
menu destination instead of inventing a Secrets UI-open target. Opening a page
is not a saved selection or successful secret write.

- Call `vault action=list` before creating, replacing, or deleting a secret.
- `vault action=set` saves a value in the project owner's own Vault and uses it
  in this project (owner only); `vault action=delete` stops this project using
  a secret, and its value stays in the owner's Vault.
- After `vault action=set` succeeds, `$SECRET_<NAME>` is available to shell
  tools immediately in the current chat and remains available in later turns.
- Your own native shell (Bash) keeps the `SECRET_*` it started with. For a
  secret's current value, including one added or changed during this chat, use
  `$(~/.agentworks/bin/mcpbridge secret NAME)` inside the command that needs it;
  never print it. `$SECRET_<NAME>` stays for scripts.
  Continue the requested work in the same chat; do not ask the user to start a
  new chat or session. Verify availability without printing the secret value.
- project stores the Vault names it uses in `workflow.json` under
  `capabilities.selected_global_secret_names`, using the AgentWorks workflow contract.
  Secret values remain encrypted outside the manifest. <!-- product:mcp-gateway -->Shared Vault credentials also require the executing user's current group permission.<!-- /product --> Respect the user's
  selections under **Secrets**; do not attach an unrelated credential.
- A reference to another workflow or Crew does not itself grant its secrets.
<!-- product:mcp-gateway -->- All secrets are stored in Vault. A project's selected secrets are lent by its owner. Credentials shared with colleagues go in a company Vault group, which a Vault administrator manages. Select exact Vault names with the product's secret selection; do not change grants without a request.
<!-- /product -->- Never print, echo, store in project files, or otherwise reveal a secret
  value. Refer to secrets by name.
