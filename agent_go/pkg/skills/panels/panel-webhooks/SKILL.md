---
name: panel-webhooks
description: Webhooks panel in Goals and Crews. Configure or inspect an authenticated incoming webhook.
metadata:
  panel-tools:
    work:
    - list_project_triggers
    - create_project_trigger
    - update_project_trigger
    - delete_project_trigger
    agentworks:
    - manage_workflow_webhook
    relays:
    - manage_workflow_webhook
  external-tools:
  - manage_triggers
---

# Webhooks

Crew uses list_project_triggers and create/update/delete_project_trigger; Goal uses manage_workflow_webhook. Relays use the same tool for their published function entry; read relay-builder and do not create Goal routes. List targets/routes and existing triggers first, then use the current native schema. Keep secret values out of chat logs and files, selecting an existing secret name when supported. A test may execute real work. Read receiver/run evidence rather than claiming a saved URL proves delivery.

For this panel’s full setup and operational contract, Read [the detailed procedure](references/operations.md).
