---
name: watchers
description: Manage caller-owned condition checks and their history.
---

# Watchers

Use the listed `watchers` actions: list first, then create/update/delete, history, read_script or check as needed. In MCP pass project; product chat binds its source. Preserve the returned revision on an edit. A Watcher belongs to its caller and uses only that caller’s current target access and selected Vault credentials.

Crew/Goal/Flow detectors use script_content in the caller’s own stored detector folder; Code uses its saved script path. Keep target and credential selection in owner configuration, never in incoming event data. A check may dispatch real work. Zero matches is not proof that delivery works.

Read [detectors and delivery](references/operations.md) for the existing script/cursor, inbox, event and once contracts.

For PR CI/deployment waits or ongoing autofix, use native `github_pr` with mode `checks`, `deployment` or `autofix`; no script or secret selection. See the operations reference. Native GitHub checks default to once per minute; ordinary Python checks keep their five-minute minimum.
