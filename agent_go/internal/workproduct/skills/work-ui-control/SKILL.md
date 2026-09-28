---
name: work-ui-control
description: Present the correct {{product}} project panel with acknowledged UI control. Use when opening, refreshing, or checking a {{product}} workspace view; do not use Workflow view IDs or semantics.
---

# {{product}} UI control

Use `list_ui_capabilities`, `get_ui_state`, and `perform_ui_action` only in an
interactive {{product}} project chat that actually exposes them. They control the
visible right-side project pane; they do not edit project data, run automation,
or prove that the user read the result.

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
| `memory` | Memory | `MEMORY.md` or project-local reusable skills were reviewed or changed. |
| `database` | Database | Project-owned managed tables or rows were created, changed, or requested. |
| `files` | Files | The user should inspect project files. {{product}} opens the panel; it does not deep-link to an individual file through UI control. |
| `browser` | Browser | Managed browser work begins or the user asks to watch it. The stream updates without repeated refresh actions. |
| `costs` | Costs and usage | The user asks about this {{product}} project's model usage or cost. |
| `workshop` | Automation | The user should inspect project chats, schedules, triggers, or bots. Use only an advertised section target. |
| `schedules` | Automation compatibility route | Open the schedules or webhook-trigger section using only an advertised target. Prefer `workshop` when its target expresses the destination. |
| `identity` | Identity and project setup | The user should inspect project identity, selected model, secrets, or attached folders. |
| `mcp` | Integrations | The user should inspect MCP servers, skills, bots, or connected accounts. |

The contract may also advertise compatibility aliases such as `skills`,
`secrets`, `llm`, `bots`, or `folders`. Use them only when advertised; the host
may route them into the consolidated Identity or Integrations panel.

## Boundaries

- Do not use Workflow-only views such as `flow`, `execution-logs`, `pulse`,
  `backup`, `publish`, `notify`, `access`, or `playbooks` in {{product}}.
- Opening Automation does not create, trigger, edit, or delete a schedule,
  webhook, or bot route. Use the corresponding {{product}} tools for state changes.
- Opening Dashboard does not validate its HTML or data. Use the Dashboard and
  database tools for creation and verification.
- UI control belongs to the foreground human chat. Scheduled, webhook, bot,
  synthetic, and background-child turns must not manipulate the user's pane.
