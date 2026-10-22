---
name: work-integrations
description: Manage {{product}} secrets, browser access, models, and administrator-authorized server folders. Use when the user asks to configure credentials or browser access, choose a coding provider, or attach an external folder.
---

# {{product}} integrations

Inspect current state before changing it, and distinguish account-level setup
from selection for this project. MCP setup has its own `work-mcp` skill.

## Personal Google and Slack accounts

Manage saved accounts in **My Vault → Your connected accounts**. In this
project’s **Integrations → Google apps** or **Integrations → Slack account**, choose
**Use here** or **Remove here** for a saved account. It is one login reused across products, separate from shared vaults.
Use `manage_google_account` or `manage_slack_account` to inspect and manage
these same accounts from chat; connect returns the real sign-in link. Never
create a second MCP connection for an account already saved there. Only its
owner signs in again. Existing Builder and runtime permissions still apply;
Google access follows its saved grants, and only the Slack account’s owner can
read or search it. Other people using this project can use its selected Slack
to send as that owner. Removing here leaves other items connected; disconnect
in My Vault removes the saved account everywhere. Company Google accounts and
Slack bots keep their administrator setup.

For Crew inbox setup, list saved accounts first and use `set_inbox_account` on
the requested owned, connected account with Gmail read access. If an older
private account is already selected here but that operation says to choose
your own account, verify its owner and reapply `attach` to the same account
in this project, then retry `set_inbox_account` and read back the result.
The normal **Use here** operation records that explicit personal selection;
it does not create another login. Do not edit account flags in files, choose
a different account, or treat this refusal as a website-only restriction.

## Secrets

Manage secrets through the tools in this chat. If the person asks to inspect
them, open **Secrets** in Integrations or Recent, or Ctrl+K (Cmd+K on Mac),
search Secrets. To show it in the current project with
`perform_ui_action`, use `view="mcp", action="open", target="secrets"`.
Secrets is a direct menu entry, not a tab to find under Connections. Never claim it opened unless
the UI action confirms success.

- Call `list_secrets` before creating, replacing, or deleting a secret.
- `set_workflow_secret` and `delete_workflow_secret` manage project-scoped
  secrets in the project’s Vault scope (existing tool names and permissions).
- After `set_workflow_secret` succeeds, `$SECRET_<NAME>` is available to shell
  tools immediately in the current chat and remains available in later turns.
  Continue the requested work in the same chat; do not ask the user to start a
  new chat or session. Verify availability without printing the secret value.
- {{product}} stores attached secret names in `workflow.json` under
  `capabilities.selected_secrets`, using the AgentWorks workflow contract.
  Secret values remain encrypted outside the manifest. <!-- product:mcp-gateway -->Shared Vault credentials also require the executing user's current group permission.<!-- /product --> Respect the user's
  selections under **Secrets**; do not attach an unrelated credential.
- A read-only workflow or Crew reference never grants its secrets.
<!-- product:mcp-gateway -->- All secrets are stored in Vault. Project tools manage the project's isolated Vault scope, preserving its owner and use permissions. For reuse, a Vault administrator uses `manage_vault_secret_access(operation=share)` to copy a project secret server-side and grant existing groups; `manage_my_vaults promote_secret` copies into a vault the person owns. Values never pass through the model during copying. Select exact shared names with the product's global-secret selection tool. Copies rotate independently; do not change grants without a request.
<!-- /product -->- Never print, echo, store in project files, or otherwise reveal a secret
  value. Refer to secrets by name.

## Attached folders

- Use `list_work_folders` first. Attach only an existing path permitted by the
  administrator, with a clear alias and the least access needed (`read_only`
  unless writes are required).
- Use the exact stored ID when detaching. A newly attached folder becomes part
  of the native CLI guard on the next turn; do not pretend the current turn's
  process gained access retroactively.

## Browser and models

Browser mode and CDP settings live in the project's **Browser** view. In Crew
chat, `browser_connections` lists the person's saved Chrome and Edge browsers,
returns their setup link, and selects or disconnects one for this Crew. It never
disconnects the person's other projects. Use `agent-browser` for an actual
browser task after reading its skill; connecting is separate from browsing.

In Crew chat, use `manage_crew_model(action="list")` for the offered providers,
models and this chat's saved choice. When the person asks, use `action="set"`
with an offered engine, model and reasoning level. An empty engine returns this
chat to the Crew's default. The change applies from the next message, including
messages that reach this chat from another channel; it does not change other
chats or the Crew default. Do not claim the current reply already uses it.

Use `get_crew_costs` to read this Crew's Costs and usage from the app's ledger.
Crew owners, including co-owners, get costs under the existing access rules.
Use `days` and `before` to page through dates;
never add another mirrored ledger to these totals.

For provider or model configuration exposed through tools, discover the admitted
configuration tools and load their schemas. Never read or edit raw `config/`
files for LLM/provider configuration. The configured provider/model is the
runtime's identity; model self-identification does not override it.
