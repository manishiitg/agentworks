# Project menu and Dashboard pane

The agent's UI control opens or refreshes only the Dashboard:
`perform_ui_action(view="report", action="open")` or `action="refresh"` where
admitted. Read `work-ui-control` in a Crew. Only an `applied` receipt means it
opened; accepted, unavailable, expired or ambiguous results are not success.
Scheduled, bot and background turns must not change the person's pane.

For other pages, name the current Crew menu destination or use Search
(Cmd/Ctrl+K). Do authorized changes through management tools, not invented UI
views. Opening a page changes presentation, not settings or permissions.

- Workflow source, Steps and recorded runs: `python-workflows`, `panel-runs`.
- Files, Database, Memory, Skills and Brain: `panel-data`.
- Share, Team, selected agents, accounts and secrets: `panel-access`.
- Schedules, Webhooks and Watchers: `panel-automations`.
- Slack bot: Share → Slack; personal Slack: Connections. Read `panel-channels`.
- Browser, Terminal, model and project settings: `panel-settings`.

Ask AI prepares a contextual request in the current project's chat. A page's
stored data or old view ID does not prove that a retired Goal/Flow destination
is offered. Use current capabilities and the shared panel procedures; do not
recreate old plan menus or an upgrade panel. A loaded guide grants no access.
