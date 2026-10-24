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

# Access

Use get_dashboard_share/share_dashboard for this product’s Share selection (Only me, Chosen people, Everyone). Goal owner/editor lists use manage_project get_access/set_access under existing owner checks. Share only the requested product with the requested people; a URL itself grants nothing. Crew has a main owner and may have co-owners. Owners build the whole project and control their own tabs; other people use what is shared. Read back the saved audience/access before reporting completion.

Use `ui_control` view=mcp target=share to show Access. The website's Access panel summarizes owners, who can use the project, and shared existing agents. Changes happen in the visible project chat. Use `crew_owners` for Crew owners and `manage_project get_access/set_access` for Goal and Relay ownership, after reading `panel-owners`. Goal execution-team assignments remain separate from Share. There is no separate Crew owners form or purpose form to send the person to.

## Shared Crew agents

Share has one audience for the dashboard, published functions and selected existing agents. Inspect sharing first. For a Crew, `shared_agents` replaces the selected stable chat IDs from the returned agents list; an empty list clears them and omission preserves them. `agent_purposes` changes the saved one-line purpose by chat ID, only for tabs this person controls. Every selected agent needs a purpose. Read the saved selection back.

People in the audience can read a selected agent's conversation from sharing onward, and send requests as themselves from the website, another Crew or MCP. Messages enter that same existing tab and use its configured context and working tools. Use `workspace action=chats` to discover selected agents, `read_chat` with its chat_id for the transcript (before_seq reads earlier messages), and `ask` to send an attributed request. `messages action=list_agents` and `send` expose the same targets; use the returned target/chat_id or complete agent address. Generic Crew Ask and published functions keep their current behavior.

Sharing an agent never gives the recipient its tab controls, private account credentials, other unselected chats or Builder setup panels. Only its tab owner can steer, stop, reset, rename, close, change its model or answer owner confirmations. Current audience and agent selection are checked again for reads, sends and queued delivery. A stored message receipt is acceptance, not a completed answer; agent replies remain explicit messages.
