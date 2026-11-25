---
name: panel-automations
description: Set a schedule, webhook or Watcher that calls existing work. Inspect
  timing and the saved result.
metadata:
  version: 2.7.0
  panel-tools:
    work: [automations]
    agentworks: [automations]
  external-tools: [automations]

---

# Automations

Choose the work first: the project’s Python workflow or a message to an agent. Then choose when it should run: on a schedule, when an HTTP event arrives (webhook, which always starts the workflow), or when a condition changes (Watcher). An Action (a published function) makes saved work callable by people or other agents. None is needed for a direct run. The `automations` tool lists and configures the available ways to start work; inspect its saved result after a change.

**On a Mac project, ask where it runs first:** "On this Mac (only while it is on and awake) or Online (24/7, even with the Mac off)?" On this Mac: set it up here as below. Online: it runs in an online project the Team uses; read `panel-access/references/team/SKILL.md`, section "Online projects a Team uses", and follow "Run something online". A project on the server is already online; don't ask there.

Start with the person's request and the current product. Read only the relevant procedure below: in product chat use `read_skill` with this skill name as `skill_name` and the listed `path` (chat has no `help action=topic`); only an MCP client uses `help action=topic` with this name as `topic` and the listed `path`. Use the actions offered by the current tool catalog. A guide does not grant access.

- Functions: read `panel-dashboard/references/functions/SKILL.md` through the panel-dashboard skill.
- Schedules: read `references/schedules/SKILL.md` when needed.
- Webhooks: read `references/webhooks/SKILL.md` when needed.
- Watchers: read `references/watchers/SKILL.md` when needed.
- Setting up Project tasks (a dedicated lead agent fed by Watchers or schedules on Slack, mail, Drive, task trackers and GitHub): read `references/project-activity/SKILL.md`.
- Working from Project tasks (as the lead, working a task, adding your own, good practice): read `references/project-tasks/SKILL.md`.

Verify the saved result or returned run result, then give the person one clear next step. Keep their existing notes and omitted settings.
