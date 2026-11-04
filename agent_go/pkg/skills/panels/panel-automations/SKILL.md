---
name: panel-automations
description: Set a schedule, webhook or Watcher that calls existing work. Inspect
  timing and the saved result.
metadata:
  version: 2.2.0
  panel-tools:
    work: [automations]
    agentworks: [automations]
    relays: [automations]
  external-tools: [automations]

---

# Automations

One `automations` tool makes work start by itself: `scope=schedule` says when, `scope=webhook` receives HTTP events, and `scope=watcher` checks for a condition. Reusable work belongs to functions. List the current project’s callers first and inspect saved status or run evidence after a change.

Start with the person's request and the current product. Read only the relevant procedure below: in product chat use `read_skill` with this skill name as `skill_name` and the listed `path` (chat has no `help action=topic`); only an MCP client uses `help action=topic` with this name as `topic` and the listed `path`. Use the actions offered by the current tool catalog. A guide does not grant access.

- Schedules: read `references/schedules/SKILL.md` when needed.
- Webhooks: read `references/webhooks/SKILL.md` when needed.
- Watchers: read `references/watchers/SKILL.md` when needed.

Verify the saved result or returned run result, then give the person one clear next step. Keep their existing notes and omitted settings.
