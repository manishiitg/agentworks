# Notifications

- A workflow run's outcome is recorded in its Activity. Nothing is sent to Slack, Gmail or WhatsApp automatically.
- To tell the owner in a Crew chat when a run fails, stops partway or never ran, set `settings(action="update", notifications={"wake_tab": {"crew_id": "<crew>", "chat_id": "<chat>"}})`. An empty crew_id clears it.
- A workflow that must reach other people sends explicitly from its script with `send_email(to, subject, body)` or `send_slack(channel, text)`, using the accounts and Slack routes already connected.
- Runs never wait for a person. Never show or save credential values in files, prompts or logs.

The person's request, as typed after the command: {{context}}
