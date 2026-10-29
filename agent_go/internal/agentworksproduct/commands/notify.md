# Notifications

- Read the current settings with `settings` action=get. Explain which team messages are configured and whether delivery is working. Never show or save webhook values in files, prompts or logs.
- Use one setting, `settings(action="update", notifications={"break_alerts": true|false})`, for “Tell me when something breaks”. It controls informational run failures in Needs you. Questions requiring an answer and failing Gmail delivery notices remain available.
- AgentWorks handles progress reports. There are no automatic per-Goal daily digests, Autopilot reports or owner-account summary emails.
- Keep the team messages the person configured. Save run content and channels with `run_notification_instructions` and `run_notification_channels`. Save email recipients with `run_notification_recipients`; an empty list stops summary emails. Explicitly chosen recipients, including the owner, stay chosen. Denylists still apply. Select the sending Gmail account with `notification_gmail_connection_id`.
- A Slack Incoming Webhook reaches one channel. Store each link with `vault` action=set and configure `run_notification_slack_webhooks` with secret names. Use `vault` action=list first. Never read webhook secret values or post to them through a shell; the server delivers messages.

The person's request, as typed after the command: {{context}}
