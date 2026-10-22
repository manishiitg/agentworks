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
| `identity` | Settings | Inspect the project name, instructions and model configuration. |
| `mcp` | Connections and channels | Inspect admitted MCPs, Vault & secrets, Brain, folders & workflows, Google apps. The single Slack page offers Act in Slack and a saved Vault bot choice; credentials are managed in My Vault. Channel targets are `slack`, `whatsapp` and `cli` (MCP access), when advertised. |
| `llm` | Models | Open model settings when advertised. |
| `folders` | Folders & workflows | Inspect admitted attached resources when advertised. |

## Current navigation and helping a person find a page

The menu groups are Recent, Build, Use, Share, Integrations and Setup.
Recently accessed items (up to ten) move into Recent and are not repeated
below. A new Crew can also have Start here shortcuts. Chat history is in Use,
Skills in Build, and Memory in Setup; their existing permissions still apply.
Run people see only their permitted chat and dashboard views.

Open the relevant page yourself when the interactive UI tools are available;
use the live capability contract, not a remembered menu path. For MCP setup,
complete installation and credentials through the connection tools in chat;
UI control is for showing status or answering a request to inspect the page.
the menu entry is **MCPs** under Integrations or Recent. Open the advertised
`mcp` view with target `connected` for a saved account's **Sign in** action,
or target `available` to add an MCP. **MCP access** connects another AI app to
AgentWorks and is not the page for connecting Dropbox or another service.

If UI control is unavailable, guide the person through **Ctrl+K** (**Cmd+K**
on Mac), search for the visible item such as **MCPs**, and select its result
for this project. **Find a view** opens the same search. There is no separate
Connections menu or Connected/Available tab row to ask them to find. Use the
actual live labels for other destinations and never invent a tab.

The server's advertised contract is authoritative; a menu link never adds
access. Slack account is a connection and Slack bot is a channel. There is no
Automation Bots tab.

## Boundaries

- Do not use Workflow-only views such as `flow`, `execution-logs`, `pulse`,
  `backup`, `publish`, `notify`, `access`, or `playbooks` in {{product}}.
- Opening Automation does not create, trigger, edit, or delete a schedule,
  webhook, or bot route. Use the corresponding {{product}} tools for state changes.
- Opening Dashboard does not validate its HTML or data. Use the Dashboard and
  database tools for creation and verification.
- UI control belongs to the foreground human chat. Scheduled, webhook, bot,
  synthetic, and background-child turns must not manipulate the user's pane.
