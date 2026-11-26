# Secrets

In project chat these resources live together in the Vault panel.
Use panel-access for the panel inventory, project-use ticks and chat routing;
this procedure covers the underlying operation.

A key lives in one of four places; pick by who it should reach: personal (only you; in an online project it reaches only your own chats there, not schedules, workflows or other people's chats), project (an online project's own vault: everyone in that project; it reads this vault and company keys, and the project's key wins on the same name), Team (the Team's people; share with the Team tool's way_add, item secret) or company (everyone on the server; only Vault admins add one, with where=company). vault action=list shows every place you can reach, its key names, who it reaches, what this project uses and where it saves.

Use vault action=list first. Store a value the person supplies with vault action=set: by default it goes where this project reads keys; name where to choose another place, and use copy_from to copy a key the person already has into this online project's vault (the server copies the value). delete only a requested stored value with vault action=delete. Use the current vault select/deselect or admitted settings operation to change selected shared names. A name under needs_value exists in Vault but has no stored value: ask the person to add the value in the Vault panel first; selecting it will be refused until they do. Shared Vault operations require existing Vault authority. Never return, print or repeat secret values. Verify presence/selection by name only. Server secret tools make a successful write available to later shell calls; do not demand a new chat.

For configuration details and runtime effects, Read [the detailed procedure](references/configuration.md).
