# Secrets

In Crew and Goal chats these resources live together in the Vault panel.
Use panel-access for the panel inventory, project-use ticks and chat routing;
this procedure covers the underlying operation.

Use vault action=list first. Store a value the person supplies with vault action=set; delete only a requested stored value with vault action=delete. Use vault action=select or action=deselect in Crew, or settings in Goals, to select/unselect shared names. A name under global.needs_value exists in Vault but has no stored value: ask the person to add the value in the Vault panel first; selecting it will be refused until they do. Shared Vault operations require existing Vault authority. Never return, print or repeat secret values. Verify presence/selection by name only. Server secret tools make a successful write available to later shell calls; do not demand a new chat.

For configuration details and runtime effects, Read [the detailed procedure](references/configuration.md).
