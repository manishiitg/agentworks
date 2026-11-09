# AgentBridge beside the project chat

The project menu's AgentBridge entry shows named project agents and reachable
computer/network agents. Point the person to that entry when they want it open;
agent UI controls only open or refresh the Dashboard. The full directory is a
secondary link. Use the visible project chat to inspect recent messages or ask an agent for work; a displayed connection status is not a message receipt. Sharing selected agents belongs in Access, not a second AgentBridge sharing setting.

Use messages action=list_agents to discover actual reachable agents and messages action=send/messages action=read for authorized messages and receipts. A stored send is not a completed task. Connect or disconnect only the person’s own current native chat when they ask; do not select or attach someone else’s chat. Product chat tabs and the external agent network are distinct. Load crew-agents in Crew or the current network guidance for exact own-chat setup; return the actual setup link/instructions from the backend.

messages action=list_agents lists the registered Crew agents this person may reach. Pass the returned chat_id to messages action=send with its target. The backend decides whether an omitted chat_id addresses the Crew's main agent. Sharing keeps the person's own Ask history and selected-agent boundaries. A message does not let its sender type in or control another person's tab. Workflows run as Python scripts; discuss them with the Crew agent and inspect their Runs.
