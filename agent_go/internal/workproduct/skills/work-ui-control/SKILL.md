---
name: work-ui-control
description: Show the {{product}} project Dashboard in the side pane after creating or changing it, or when the person asks to see it.
---

# {{product}} Dashboard pane

Opening the Dashboard shows it in the project's right panel; refreshing reloads it.
In the app's own chat the tools are `perform_ui_action(view="report", action="open")`
(or `action="refresh"`) and `get_ui_state` (what the panel shows). In a coding-CLI
chat (Claude Code, Codex) the same actions are one tool, `ui`: `action="open"`
(`view="report"`), `action="refresh"`, `action="get"` and `action="capabilities"`.
Find it with `search_tools(query="ui")`. The Dashboard is the one view an agent
opens; in the project menu it is called Artifacts.

- `applied` means the panel showed the Dashboard. `accepted`, `applying`,
  `expired` and other results are not success; say so instead of claiming it opened.
- Reuse an `idempotency_key` only to retry the same action after an uncertain result.
- Opening the Dashboard does not check its content or data. Use the Dashboard
  and database tools for that.
- For any other page, tell the person where it is in the project menu, or
  Ctrl+K (Cmd+K on Mac) and the page name. Project menu names: Files,
  Database, Workflow, Automations, Chat history, Memory, Skills, Models,
  Instructions & identity, Costs & usage. Vault is its own product in the app
  switcher. Do changes in chat with the matching tools.
- Only the foreground person's chat may use this; scheduled, webhook, bot and
  background turns must not change the person's panel.
