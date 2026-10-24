---
name: panel-agentbridge
description: AgentBridge and computer chats panel in Goals and Crews. Find or message connected agents, or show
  how to connect the person’s own chat.
metadata:
  panel-tools:
    work:
    - list_agents
    - send_message
    - read_agent_messages
    agentworks:
    - list_agents
    - send_message
    - read_agent_messages
    relays:
    - list_agents
    - send_message
    - read_agent_messages
  external-tools:
  - list_agents
  - send_message
  - read_agent_messages
---

# AgentBridge beside the project chat

Inspect `list_ui_capabilities`, then use `perform_ui_action` with view=mcp, action=open and target=agentbridge only if advertised. The project menu's AgentBridge entry shows named project agents and reachable computer/network agents in the right panel. If that target is absent, explain the UI-control limitation and point to this project's menu entry. The full directory is a secondary link. Use the visible project chat to inspect recent messages or ask an agent for work; a displayed connection status is not a message receipt. Sharing selected agents belongs in Access, not a second AgentBridge sharing setting.

Use list_agents to discover actual reachable agents and send_message/read_agent_messages for authorized messages and receipts. A stored send is not a completed task. Connect or disconnect only the person’s own current native chat when they ask; do not select or attach someone else’s chat. Product chat tabs and the external agent network are distinct. Load crew-agents in Crew or the current network guidance for exact own-chat setup; return the actual setup link/instructions from the backend.

list_agents also lists registered project chats when you have Builder access. Pass their chat_id to send_message with the project target. With Builder access, an omitted Crew chat_id uses its main Builder. Run sharing keeps your own ask history. This sends an attributed message and does not let you type in or control another person's tab. Goal and Relay chats use the same directory. Function calls retain their declared run conversations.
