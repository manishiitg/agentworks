---
name: panel-slack-bot
description: Slack bot panel in Goals and Crews. Select or configure a Slack bot and its channels for this project.
metadata:
  panel-tools:
    work:
    - get_slack_bot_settings
    - manage_slack_bot
    - configure_slack_bot
    - test_slack_bot_connection
    - create_slack_bot_route
    - update_slack_bot_route_permission
    - remove_slack_bot_route
    - manage_messaging
    agentworks:
    - get_slack_bot_settings
    - manage_slack_bot
    - configure_slack_bot
    - test_slack_bot_connection
    - create_slack_bot_route
    - update_slack_bot_route_permission
    - remove_slack_bot_route
    - manage_messaging
  external-tools:
  - workflow_slack_bot
---

# Slack bot

Read get_slack_bot_settings and manage_slack_bot before changing setup. Use configure_slack_bot for a token the person explicitly provides, without repeating its value. Use the admitted route tools to create/update/remove routes; Goals also expose manage_messaging for bot selection, channels and target display. A bot is separate from a personal Slack account. Use test_slack_bot_connection and the saved settings as verification; never send a test message unless requested.

For this panel’s full setup and operational contract, Read [the detailed procedure](references/operations.md).
