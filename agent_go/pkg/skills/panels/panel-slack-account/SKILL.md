---
name: panel-slack-account
description: Slack account panel in Goals and Crews. Connect, select, remove or inspect a saved personal Slack account.
metadata:
  panel-tools:
    work:
    - manage_slack_account
    agentworks:
    - manage_slack_account
    relays:
    - manage_slack_account
  external-tools:
  - workflow_slack_account
---

# Slack account

Use manage_slack_account action=list_saved, then attach/detach for this project or connect/reconnect for a real provider sign-in link. Only the account owner completes provider sign-in. Removing here leaves other projects connected; disconnect affects the saved account everywhere. Use the selected account through the registered Slack tools; respect its current read/send grants and verify the selection before reporting success.

For this panel’s full setup and operational contract, Read [the detailed procedure](references/operations.md).
