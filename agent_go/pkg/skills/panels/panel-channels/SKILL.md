---
name: panel-channels
description: Connect Google, Slack, WhatsApp or email and use the selected account.
  Share working entry points through those channels.
metadata:
  version: 2.0.1
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
    - manage_slack_account
    - get_agent_inbox
    - configure_agent_inbox
    - manage_google_account
    - manage_condition_triggers
    - list_gmail_connections
    - update_gmail_connection_grants
    agentworks:
    - get_slack_bot_settings
    - manage_slack_bot
    - configure_slack_bot
    - test_slack_bot_connection
    - create_slack_bot_route
    - update_slack_bot_route_permission
    - remove_slack_bot_route
    - manage_messaging
    - manage_slack_account
    - get_agent_inbox
    - manage_google_account
    - manage_condition_triggers
    - list_gmail_connections
    - update_gmail_connection_grants
    relays:
    - manage_slack_account
    - manage_messaging
    - manage_google_account
    - list_gmail_connections
    - update_gmail_connection_grants
  external-tools:
  - integrations
  - messages
---

# Channels

Connect Google, Slack, WhatsApp or email and use the selected account. Share working entry points through those channels.

Start with the person's request and the current product. Read only the relevant procedure below: in product chat use `read_skill` with this skill name as `skill_name` and the listed `path` (chat has no `help action=topic`); only an MCP client uses `help action=topic` with this name as `topic` and the listed `path`. Use the actions offered by the current tool catalog. A guide does not grant access.

- Slack bot: read `references/slack-bot/SKILL.md` when needed.
- Slack account: read `references/slack-account/SKILL.md` when needed.
- WhatsApp: read `references/whatsapp/SKILL.md` when needed.
- Inbox: read `references/inbox/SKILL.md` when needed.
- Google apps: read `references/google-apps/SKILL.md` when needed.

Verify the saved result or returned run result, then give the person one clear next step. Keep their existing notes and omitted settings.

Configured team messages and explicitly selected recipients still receive messages. Goals no longer send automatic owner summaries or daily digests. Notify → “Tell me when something breaks” controls informational run failures in Needs you; questions needing an answer remain.
