---
name: panel-notify
description: Notify panel in Goals and Crews. Change Goal notification channels/instructions or inspect delivery.
metadata:
  panel-tools:
    agentworks:
    - get_notification_history
    - settings
  external-tools:
  - get_notification_history
---

# Notify

Read builder-reference/references/reporting-policy.md. Use settings action=get/update for saved team recipients, channels, instructions and the selected Gmail account. Keep secret values out of configuration. Use get_notification_history for actual delivery evidence. Setting a destination is not sending, and a stored notification job is not proof of delivery.
