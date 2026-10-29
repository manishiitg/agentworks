## Workflow UI control

Use `list_ui_capabilities`, `get_ui_state` and `perform_ui_action` only in a
supported interactive Goal or Flow chat that exposes them. Scheduled, bot
and background turns must not manipulate the foreground workspace.
Use the calling product's live contract; a skill never adds a view or access.

`perform_ui_action(view, action="open")` requests a destination; `refresh`
reloads it. Only an `applied` receipt confirms the visible shell acknowledged
the action. It does not prove every data request completed or the user read it.
Prefer one relevant open per reply and respect a view the person selected.

## Menu and panel guidance

The project name is separate from its Builder/agent tabs. The project menu
uses Recent, Build, Use, Share, Integrations and Setup; recent destinations are
not repeated below. Dashboards are collected by project. Goals & metrics shows the outcome and saved measurements. Skills is in Build; Workflow playbooks,
Publish, Notify and Backup are in Setup. Access summarizes ownership, audience
and shared agents; requested changes happen in the visible project chat.

Panel Back returns to the project menu or a nested detail's parent list.
Ask AI prepares a contextual question in the current project's chat. There
are no Help popups or tours. Explain directly and load the relevant attached
`panel-*` skill for an action; opening a panel is not completing that action.

## Goal destinations

| View ID | Current content |
|---|---|
| `context` | Project menu and grouped destinations. |
| `report` | Hosted TSX Dashboard, HTML pages and saved published pages. |
| `flow` | Plan & progress: the saved steps and their dependencies. |
| `knowledge` | Knowledge base or Database, selected by target; no separate Learnings panel. |
| `files` | This workflow's files and file previews. |
| `browser` | Live browser interaction and recordings; it streams without repeated refresh. |
| `workshop` | Chat history, Schedules, Watchers, Webhooks, Functions and supported Inbox sources. |
| `schedules`, `webhooks` | Their advertised schedule/webhook presentation. Prefer the current menu destination and live contract. |
| `goals` | Goal purpose, success criteria and saved measurements. |
| `human-actions` | Decisions and questions waiting for a person; do not duplicate them in Goals & metrics. |
| `mcp` | Vault, Brain, Linked work, Skills, channel setup, Access and AgentBridge through advertised targets. |
| `access` | Goal execution Team and its existing access policy; product Share is in Access at `mcp/share`. |
| `identity` | Settings, Models and Upgrades. Computers and their grants live in Vault. |
| `costs`, `execution-logs` | Recorded Costs and usage, or Run logs for real work. |
| `playbooks`, `publish`, `notify`, `backup` | The corresponding Setup page. |

Use `panel-plan`/`panel-runs` for Goal execution,
`panel-access` for decisions, and the matching
shared panel skill for other tasks. Inspect the actual question before answering
a decision. Discussion alone does not select an option; saving an answer and
implementing the chosen change are separate results.

## Targets

These describe destinations, not an exhaustive allowlist. Use a target only
when `list_ui_capabilities` advertises it for this chat and product.

| View | Target meaning |
|---|---|
| `report` | A real dashboard tab label, when offered. |
| `flow` | A saved Goal step ID; Flow graph/code/run targets use the Flow contract. |
| `files` | Safe workflow-relative file path. |
| `knowledge` | `knowledgebase` or `database`; these are separate menu items. |
| `workshop` | `chats`, `schedules`, `conditions` (Watchers), `triggers` (Webhooks), `functions`, or supported `email`. |
| `goals` | Goal purpose and metrics. Decisions use the existing Decisions destination. |
| `mcp` | `vault`, `skills`, `brain`, `folders`, `slack`, `whatsapp`, `cli` (MCP access), `share` (Access), or `agentbridge`, when offered. |
| `identity` | Advertised General, Models or Upgrades section. |
| `notify` expand | Advertised run-summary section. |

The website can offer a destination before a running server advertises its
UI target. Do not invent support or retry a rejected target under another name.
Explain the concrete limitation and use the project's visible menu if useful.
Vault holds app/Google/Slack account selections, secret names and computers;
MCP access connects an external AI app to AgentWorks. Read `panel-access` and
`panel-access` for those distinct jobs. Do not send people to retired
Connected/Available/Secrets tab rows merely because an older target still exists.

## Flows and boundaries

Flows use `relay-builder`, `panel-plan` and `panel-runs`: a Python/DBOS
graph, source code and versioned calls, not a Goal step plan.
Only the Flow's advertised inventory is available. Crew navigation belongs
to the separate `work-ui-control` skill and its own panel IDs.

- Opening or refreshing a panel changes presentation only. Use the existing
  authorized management tool for configuration, files, data or execution.
- `accepted`, `applying`, `expired`, `browser_disconnected` and other outcomes
  are not an `applied` receipt. Report the actual result.
- Reuse an idempotency key only for the exact same action after an uncertain
  result; do not blindly replay with a new key.
- Builder and Run visibility, personal accounts, unshared chats and tab control
  remain governed by existing server checks. An open view is no access grant.
- Open Browser once for actual browser work. Read `panel-settings` and its
  relevant browser procedure; Builders can interact while work continues,
  and shared Run users keep their existing watch-only presentation.
