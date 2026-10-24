---
name: panel-sharing
description: People and access panel in Goals and Crews. Inspect or change who may use this project.
metadata:
  panel-tools:
    work:
    - get_dashboard_share
    - share_dashboard
    agentworks:
    - get_dashboard_share
    - share_dashboard
    - manage_project
    relays:
    - get_dashboard_share
    - share_dashboard
    - manage_project
  external-tools:
  - get_dashboard_share
  - share_dashboard
---

# People and access

Use get_dashboard_share/share_dashboard for this product’s Share selection (Only me, Chosen people, Everyone). Goal owner/editor lists use manage_project get_access/set_access under existing owner checks. Share only the requested product with the requested people; a URL itself grants nothing. Crew has one main owner; using a Crew is through its shared dashboard. Read back the saved audience/access before reporting completion.

## Shared Crew agents

Share has one audience for the dashboard, published functions and selected existing agents. Inspect sharing first. For a Crew, `shared_agents` replaces the selected stable chat IDs from the returned agents list; an empty list clears them and omission preserves them. `agent_purposes` changes the saved one-line purpose by chat ID, only for tabs this person controls. Every selected agent needs a purpose. Read the saved selection back.

People in the audience can read a selected agent's conversation from sharing onward, and send requests as themselves from the website, another Crew or MCP. Messages enter that same existing tab and use its configured context and working tools. Use `workspace action=chats` to discover selected agents, `read_chat` with its chat_id for the transcript (before_seq reads earlier messages), and `ask` to send an attributed request. `messages action=list_agents` and `send` expose the same targets; use the returned target/chat_id or complete agent address. Generic Crew Ask and published functions keep their current behavior.

Sharing an agent never gives the recipient its tab controls, private account credentials, other unselected chats or Builder setup panels. Only its tab owner can steer, stop, reset, rename, close, change its model or answer owner confirmations. Current audience and agent selection are checked again for reads, sends and queued delivery. A stored message receipt is acceptance, not a completed answer; agent replies remain explicit messages.
