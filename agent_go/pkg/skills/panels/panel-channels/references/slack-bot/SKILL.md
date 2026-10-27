# Slack bot

Read integrations action=slack_bot_get and integrations before changing setup. Use integrations action=slack_bot_configure for a token the person explicitly provides, without repeating its value. Use the admitted route tools to create/update/remove routes; Goals also expose messages for bot selection, channels and target display. A bot is separate from a personal Slack account. Use integrations action=slack_bot_test and the saved settings as verification; never send a test message unless requested.

For this panel’s full setup and operational contract, Read [the detailed procedure](references/operations.md).

The Slack bot tab shows the bot username and workspace verified through Slack, when available. A saved label such as Default is not its Slack identity. Saved credentials, verified identity, token checks and incoming message delivery are distinct: never call a configured bot Connected without evidence. Check connection runs existing token diagnostics without posting a message; channel/display controls and saved-bot management stay below. Local administrator setup is Shared Slack bot setup. Selection, management, routes and scopes retain existing owner/admin checks.
