## WORKFLOW UI CONTROL

UI-control tools belong only to supported interactive Workflow Builder chats,
including read-only Builders. Scheduled runs, manually triggered schedules,
Pulse/child agents, and bot conversations do not manipulate the foreground
workspace. Observing one of those conversations does not promote it; an
explicit supported interactive continuation is required.

In a coding-CLI chat these tools are bridge tools, not entries in the direct
tool list: find them with `search_tools(query="ui")`, read the schema with
`get_api_spec(tool_name="perform_ui_action")`, and call them over the bridge.
A missing direct entry does not mean they are unavailable.

The right-hand pane shows one Workflow view at a time.
`perform_ui_action(view, action="open")` requests a view and
`perform_ui_action(view, action="refresh")` invokes that view's reload. Only an
`applied` receipt confirms that the visible shell acknowledged the action; it
does not prove every data request inside the view completed or that the user
read it. Prefer one open per reply and do not repeatedly override a view the
user selected.

## Workflow views

| View ID | Shows | Open it when |
|---|---|---|
| `report` | The workflow's live HTML Dashboard | You built or edited the report, or the user asks to see results or metrics. |
| `flow` | The workflow plan canvas | You changed steps or routes, or the user asks how the workflow is structured. |
| `costs` | Token and cost usage by run, group, and step | The user asks what a run cost or which step is expensive. |
| `execution-logs` | Outputs, validation, and timing for the selected run | A step failed or the user asks what happened during execution. |
| `knowledge` | Consolidated Learnings, Knowledgebase, and Database inspector | You changed or need to inspect one of those stores; use an advertised section target. |
| `webhooks` | Authenticated workflow webhook endpoints | You created or changed a webhook. |
| `schedules` | Scheduled runs and compatibility access to webhooks | You created or changed a schedule; use an advertised section target. |
| `files` | Workflow files | The user should inspect a file or folder. |
| `browser` | Live managed browser or watch-only Playwright test stream | Browser work begins or the user asks to watch it. The stream updates without repeated refreshes. |
| `workshop` | Automation chats, schedules, triggers, and bots | The user should inspect automation activity or configuration; use an advertised section target. |

## Pulse and delivery views

| View ID | Shows | Open it when |
|---|---|---|
| `pulse` | Decisions, saved answers, findings, work areas, and gate state | The user asks about a decision or what Pulse found. |
| `human-actions` | Everything waiting for a person: open decisions and questions, and user suggestions for the owner | The user asks what needs their decision, or about a suggestion someone left. |
| `backup` | Backup configuration and history | You configured or ran a backup. |
| `publish` | Published Dashboard state | You published or refreshed the public report. |
| `notify` | Notification settings and recent deliveries | You changed notification behavior; `expand` may expose an advertised instruction section. |

“Needs your decision” belongs to `human-actions` (and `pulse`); it is not part of the HTML Dashboard.
Read the current request before explaining it. Discussion alone does not choose
an option. Once the user gives a clear final answer, save it with the dedicated
decision tool; saving means answered, not applied or consumed.

## Workflow setup views

| View ID | Shows | Open it when |
|---|---|---|
| `access` | Workflow sharing and user access | You changed or need to explain who can access the workflow. |
| `identity` | Workflow identity and identity-owned setup | You changed the workflow's identity or related settings. |
| `mcp` | Consolidated Integrations surface | You changed MCP, skills, secrets, provider connections, bots, email, or attached integrations represented there. |
| `playbooks` | Workflow playbook catalog and installed playbooks | The user wants to inspect, install, or update a playbook. |

## Targets

Use a target only where the live capability contract advertises one:

| View/action | Target meaning |
|---|---|
| `report` open/refresh | Top-level tab label from the report itself. |
| `flow` open/refresh | Exact plan step ID. |
| `files` open/refresh | Safe workflow-relative file path. |
| `knowledge` open/refresh | `learnings`, `knowledgebase`, or `database`. |
| `schedules` open/refresh | `schedules` or `webhooks`. |
| `workshop` open/refresh | `chats`, `schedules`, `triggers`, or `bots`. |
| `notify` expand | `run_summary` or `pulse_review`. |

Do not use retired direct view IDs such as `learnings`, `knowledgebase`,
`database`, `skills`, `secrets`, `llm`, `bots`, `email`, or `folders` unless a
future live contract advertises them again. Do not use Crew-only `memory` or
Crew's direct `database` panel; Workflow database content is
`view="knowledge", target="database"`.

## Safe operation

- Call `list_ui_capabilities` when the available action or target is uncertain;
  never infer support from this document alone.
- `accepted`, `applying`, `expired`, `browser_disconnected`, and other failures
  are not success. Report the bounded receipt.
- Reuse an idempotency key only for the exact same action after an uncertain
  result; never blindly replay with a new key.
- Opening a view changes no workflow state. Use the relevant workflow tool for
  mutations and UI control only to present the result.
- When starting managed browser work, open `browser` once. For Playwright
  fixture rules, read
  `builder-reference/references/playwright-scripted.md`. Playwright sessions
  are watch-only.

Crew uses the same protocol through its separate `work-ui-control` skill and a
different view contract.
