---
name: panel-automations
description: Set a schedule, webhook or Watcher that calls existing work. Inspect
  timing and the saved result.
metadata:
  version: 2.0.0
  panel-tools:
    work:
    - list_project_schedules
    - create_project_schedule
    - update_project_schedule
    - delete_project_schedule
    - list_project_triggers
    - create_project_trigger
    - update_project_trigger
    - delete_project_trigger
    - manage_condition_triggers
    agentworks:
    - list_schedules
    - create_schedule
    - update_schedule
    - delete_schedule
    - trigger_schedule
    - get_schedule_runs
    - manage_workflow_webhook
    - manage_condition_triggers
    relays:
    - manage_workflow_webhook
    - manage_condition_triggers
  external-tools:
  - manage_schedules
  - list_schedules
  - get_schedule_runs
  - trigger_schedule
  - manage_triggers
  - manage_condition_triggers
---

# Automations

Set a schedule, webhook or Watcher that calls existing work. Inspect timing and the saved result.

Start with the person's request and the current product. Read only the relevant procedure below: in product chat use `read_skill` with this skill name and path; over MCP use `help action=topic` with this name as `topic` and the listed `path`. Use the actions offered by the current tool catalog. A guide does not grant access.

- Schedules: read `references/schedules/SKILL.md` when needed.
- Webhooks: read `references/webhooks/SKILL.md` when needed.
- Watchers: read `references/watchers/SKILL.md` when needed.

Verify the saved result or returned run result, then give the person one clear next step. Keep their existing notes and omitted settings.
