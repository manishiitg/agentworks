---
name: panel-notify
description: Notify panel in Goals and Crews. Change Goal notification channels/instructions or inspect delivery.
metadata:
  panel-tools:
    agentworks:
    - get_workflow_config
    - update_workflow_config
    - get_notification_history
    - settings
  external-tools:
  - get_notification_history
---

# Notify

Read builder-reference/references/reporting-policy.md. Use get_workflow_config/update_workflow_config or settings notifications for saved team-run instructions and selected channel secret names; never embed a webhook value in configuration. Use get_notification_history for actual delivery evidence. Setting a destination is not sending, and a stored notification job is not proof of delivery.
