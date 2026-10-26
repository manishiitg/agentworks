{{context}}

Help me let people and other tools use this workflow from where they already are.
- First say what it can be asked today: call functions action=list and tell me what the workflow offers, in plain words. If it offers nothing yet, offer to define a simple function or just let people ask it in chat.
- Slack: if I want a Slack channel to reach this workflow, use configure_slack_bot, create_slack_bot_route and test_slack_bot_connection. Explain which channel will reach it and who can use it. Slack is only another way to ask the workflow; it does not give anyone more access.
- Other AI tools (Claude, ChatGPT and similar): the workflow is reachable over the AgentWorks MCP connection with the same access rules. Tell me that in one line. Do not paste connection secrets into chat.
- If I name a channel you cannot set up from here, say so and tell me the next step I can take.
