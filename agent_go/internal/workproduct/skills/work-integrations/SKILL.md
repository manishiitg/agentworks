---
name: work-integrations
description: Manage {{product}} secrets, browser access, models, and administrator-authorized server folders. Use when the user asks to configure credentials or browser access, choose a coding provider, or attach an external folder.
---

# {{product}} integrations

Inspect current state before changing it, and distinguish account-level setup
from selection for this project. MCP setup has its own `work-mcp` skill.

## Secrets

- Call `list_secrets` before creating, replacing, or deleting a secret.
- `set_workflow_secret` and `delete_workflow_secret` manage project-scoped
  secrets in {{product}} (legacy tool names for the shared project store). There is
  no account-level secret: a credential shared across projects must be a
  shared Vault secret, copied by an administrator (see below).
- After `set_workflow_secret` succeeds, `$SECRET_<NAME>` is available to shell
  tools immediately in the current chat and remains available in later turns.
  Continue the requested work in the same chat; do not ask the user to start a
  new chat or session. Verify availability without printing the secret value.
- {{product}} stores attached secret names in `workflow.json` under
  `capabilities.selected_secrets`, using the AgentWorks workflow contract.
  Secret values remain encrypted outside the manifest. Shared Vault credentials also require the executing user's current group permission. Respect the user's
  selections in **Integrations > Secrets**; do not attach an unrelated credential.
- A read-only workflow or Crew reference never grants its secrets. To reuse a
  credential across projects, an administrator can copy the source project/workflow secret with
  `manage_global_secret(action="share", name="SOURCE_NAME", group_ids=["GROUP_ID"], vault_name="VAULT_NAME")`.
  First use `manage_global_secret(action="list_groups")` to discover actual group IDs.
  Use `list_secrets(source_workflow_path="EXACT_PATH")` when copying from another
  accessible project, then pass the same `source_workflow_path` to the share action.
  Ask which groups should receive access when the user's intent is unclear;
  do not default to Platform. Sharing keeps the source and existing attachments
  intact, never overwrites a Vault name, and never returns plaintext. The two
  copies rotate independently. The legacy `action="promote"` removes the source
  copy; prefer share for requests to share a credential.
  Alternatively create it through Vault > Secrets and grant use to an existing
  group in Vault > Access > Secrets. Explicitly select that shared
  name in each destination {{product}} project with
  `update_project_global_secret_selection(action="select", name="NAME")`.
  Call `list_secrets` first and use an exact name from `global.names`. {{product}} persists this allowlist in
  `capabilities.selected_global_secret_names`; it never inherits newly created
  globals automatically.
- Never print, echo, store in project files, or otherwise reveal a secret
  value. Refer to secrets by name.

## Attached folders

- Use `list_work_folders` first. Attach only an existing path permitted by the
  administrator, with a clear alias and the least access needed (`read_only`
  unless writes are required).
- Use the exact stored ID when detaching. A newly attached folder becomes part
  of the native CLI guard on the next turn; do not pretend the current turn's
  process gained access retroactively.

## Browser and models

Browser mode and CDP settings live in the project's **Browser** view. Coding
provider and model settings live in **Setup > Models**: the provider is fixed
after the first message because it owns native conversation state, while the
model may still change. Use `agent-browser` for an actual browser task.

For provider or model configuration exposed through tools, discover the admitted
configuration tools and load their schemas. Never read or edit raw `config/`
files for LLM/provider configuration. The configured provider/model is the
runtime's identity; model self-identification does not override it.
