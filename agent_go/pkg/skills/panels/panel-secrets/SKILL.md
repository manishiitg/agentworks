---
name: panel-secrets
description: Secrets panel in Goals and Crews. Store a supplied secret or change which secret names this project
  uses.
metadata:
  panel-tools:
    work:
    - list_secrets
    - set_workflow_secret
    - delete_workflow_secret
    - update_project_global_secret_selection
    - settings
    agentworks:
    - list_secrets
    - set_workflow_secret
    - delete_workflow_secret
    - settings
    relays:
    - list_secrets
    - set_workflow_secret
    - delete_workflow_secret
    - settings
  external-tools:
  - get_secret_sources
---

# Secrets

In Crew and Goal chats these resources live together in the Vault panel.
Use panel-vault for the panel inventory, project-use ticks and chat routing;
this procedure covers the underlying operation.

Use list_secrets first. Store a value the person supplies with set_workflow_secret; delete only a requested stored value with delete_workflow_secret. Use update_project_global_secret_selection in Crew or settings in Goals to select/unselect shared names. A name under global.needs_value exists in Vault but has no stored value: ask the person to add the value in the Vault panel first; selecting it will be refused until they do. Shared Vault operations require existing Vault authority. Never return, print or repeat secret values. Verify presence/selection by name only. Server secret tools make a successful write available to later shell calls; do not demand a new chat.

For configuration details and runtime effects, Read [the detailed procedure](references/configuration.md).
