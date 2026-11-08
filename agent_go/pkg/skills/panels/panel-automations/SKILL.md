---
name: panel-automations
description: Set a schedule, webhook or Watcher that calls existing work. Inspect
  timing and the saved result.
metadata:
  version: 2.2.0
  panel-tools:
    work: [automations]
    agentworks: [automations]
  external-tools: [automations]

---

# Automations

Choose the work first: a saved workflow, a Python script or a Crew chat action. Then choose when it should run: on a schedule, when an HTTP event arrives (webhook), or when a condition changes (Watcher). A function makes saved work callable by people or other agents. None is needed for a direct run. The `automations` tool lists and configures the available ways to start work; inspect its saved result after a change.

Start with the person's request and the current product. Read only the relevant procedure below: in product chat use `read_skill` with this skill name as `skill_name` and the listed `path` (chat has no `help action=topic`); only an MCP client uses `help action=topic` with this name as `topic` and the listed `path`. Use the actions offered by the current tool catalog. A guide does not grant access.

- Functions: read `panel-dashboard/references/functions/SKILL.md` through the panel-dashboard skill.
- Schedules: read `references/schedules/SKILL.md` when needed.
- Webhooks: read `references/webhooks/SKILL.md` when needed.
- Watchers: read `references/watchers/SKILL.md` when needed.

Verify the saved result or returned run result, then give the person one clear next step. Keep their existing notes and omitted settings.
