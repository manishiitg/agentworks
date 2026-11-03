---
name: work-ui-control
description: Show the {{product}} project Dashboard in the side pane after creating or changing it, or when the person asks to see it.
---

# {{product}} Dashboard pane

`perform_ui_action(view="report", action="open")` (view `report`) shows the project Dashboard in
the side pane; `action="refresh"` reloads it. `get_ui_state` reports what the
pane shows. These are the only UI actions: the Dashboard is the one view the
agent opens. In a coding-CLI chat find them with `search_tools(query="ui")`.

- `applied` means the pane showed the Dashboard. `accepted`, `applying`,
  `expired` and other results are not success; say so instead of claiming it opened.
- Reuse an `idempotency_key` only to retry the same action after an uncertain result.
- Opening the Dashboard does not check its content or data. Use the Dashboard
  and database tools for that.
- For any other page (Files, Memory, Automations, Vault, Settings, Models,
  history), tell the person where it is in the project menu, or Ctrl+K (Cmd+K on
  Mac) and the page name. Do changes in chat with the matching tools.
- Only the foreground person's chat may use this; scheduled, webhook, bot and
  background turns must not change the person's pane.
