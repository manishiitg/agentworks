---
name: work-ui-control
description: Present the correct {{product}} project panel with acknowledged UI control. Use when opening, refreshing, or checking a {{product}} workspace view; do not use Workflow view IDs or semantics.
---

# {{product}} UI control

Use `list_ui_capabilities`, `get_ui_state`, and `perform_ui_action` only in an
interactive {{product}} project chat that actually exposes them. They control the
visible right-side project pane; they do not edit project data, run automation,
or prove that the user read the result.

In a coding-CLI chat these are bridge tools, not direct tools: find them with
`search_tools(query="ui")`, as bridge tool routing says. Say they are unavailable
only after `search_tools` returns none.

## Receipt contract

- Use only view IDs, actions, and targets returned by `list_ui_capabilities`.
- `applied` confirms that the visible panel shell acknowledged the action. It
  does not prove that every request inside the panel finished successfully.
- `accepted`, `applying`, `expired`, `browser_disconnected`, and other failures
  are not success. Report the bounded result instead of claiming the panel
  opened.
- Reuse an `idempotency_key` only when retrying the exact same action after an
  uncertain outcome. Never replay an uncertain action with a new key.
- Open the one panel that best supports the current reply. Do not repeatedly
  override a panel the user selected.

## {{product}} views

These are {{product}} views, not AgentWorks Workflow views:

| View ID | {{product}} panel | Open it when |
|---|---|---|
| `report` | Dashboard | A project Dashboard was created or changed, or the user asks to see its visual project information. |
| `memory` | Memory & skills | Inspect durable memory or project-local skills. Use `memory` or `skills` as the section target when advertised. |
| `database` | Database | Inspect project-owned managed tables or rows. |
| `files` | Files | Inspect project files. |
| `browser` | Browser | Watch managed browser work; the stream updates without repeated refresh actions. |
| `costs` | Costs and usage | Inspect this project's model usage or cost. |
| `history` | Chat history | Find and open earlier project conversations. |
| `workshop` | Automations | Inspect `schedules`, `conditions` (Watchers), `triggers` (Webhooks) , `functions` or `email` (Incoming email). Only use advertised targets. |
| `schedules` | Schedules | Inspect a scheduled caller or its advertised webhook target. |
| `identity` | Settings | Inspect Instructions & identity. Models has its own menu entry; use the advertised Models view or target. |
| `mcp` | Vault and project integrations | `vault` shows apps, accounts, secret names and computers used here. Other advertised targets include `brain`, `folders`, `slack`, `slack-bot`, `whatsapp`, `cli` (MCP access), `agents` (Chats from your computer), `share` (Access) and AgentBridge when offered. Read the matching panel skill. |
| `llm` | Models | Open model settings when advertised. |
| `folders` | Folders & workflows | Inspect admitted attached resources when advertised. |

## Current navigation and helping a person find a page

The menu groups are Recent, Build, Use, Share, Integrations and Setup.
Recently accessed items (up to ten) move into Recent and are not repeated
below. A new Crew can also have Start here shortcuts. Chat history is in Use,
Skills in Build, and Memory in Setup; their existing permissions still apply.
Run people see only their permitted chat and dashboard views.

The project title is separate from Builder and named agent tabs; owner details
appear on hover. Back returns to the project menu or a nested detail's parent
list. Access summarizes owners, audience and selected shared agents; changes
happen in the visible project chat. AgentBridge is a project right panel with
a secondary link to the full directory. Help popups and tours are removed.

Open the relevant page when interactive UI tools are available. Inspect the
live capability contract, not a remembered menu path. The website's Plan,
Suggestions, Terminal or AgentBridge menu entry is not proof that a matching
UI-control view/target is advertised. Do not invent `plan`, `suggestions`,
`shell` or another view; use the menu when the live contract has no destination.

Vault holds saved apps/accounts, secret names and computers. Use its existing
connection tools in chat to install, sign in, select or remove an item. UI
control shows the result and never completes a configuration change itself.
MCP access connects an external AI app to AgentWorks; it is separate from
selecting an app connection in Vault. A computer connection and its allowed
folders/commands are also separate choices.

If UI control is unavailable, guide the person through the current project
menu, or Ctrl+K (Cmd+K on Mac) and the page's current name. Use only destinations
visible for this project. Never invent a tab row or ask them to use Help or a tour.

The server's advertised contract is authoritative; a menu link never adds
access. Slack account is a connection and Slack bot is a channel. There is no
Automation Bots tab.

## Boundaries

- Do not use Workflow-only views such as `flow`, `execution-logs`, `pulse`,
  `backup`, `publish`, `notify`, `access`, or `playbooks` in {{product}}.
- Opening Automation does not create, trigger, edit, or delete a schedule,
  webhook, or bot route. Use the corresponding {{product}} tools for state changes.
- Opening Dashboard does not validate its TSX, rendered output or data. Use the Dashboard and
  database tools for creation and verification.
- UI control belongs to the foreground human chat. Scheduled, webhook, bot,
  synthetic, and background-child turns must not manipulate the user's pane.
