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

# AgentBridge and computer chats

Use list_agents to discover actual reachable agents and send_message/read_agent_messages for authorized messages and receipts. A stored send is not a completed task. Connect or disconnect only the person’s own current native chat when they ask; do not select or attach someone else’s chat. Product chat tabs and the external agent network are distinct. Load crew-agents in Crew or the current network guidance for exact own-chat setup; return the actual setup link/instructions from the backend.

list_agents also lists registered project chats when you have Builder access. Pass their chat_id to send_message with the project target. With Builder access, an omitted Crew chat_id uses its main Builder. Run sharing keeps your own ask history. This sends an attributed message and does not let you type in or control another person's tab. Goal and Relay chats use the same directory. Function calls retain their declared run conversations.
