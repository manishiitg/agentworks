# Share

Use share action=get/share action=set for this product’s Share selection (Only me, Chosen people, Everyone). Share only the requested product with the requested people; a URL itself grants nothing. A Crew has a main owner; while its Team is on, its joined members build it too. They build the whole project and control their own tabs; other people use what is shared. Read back the saved audience/access before reporting completion.

Use `ui action=capabilities`, then the admitted `ui` with view=mcp, action=open, target=share to show Share when advertised. The Crew's Share panel lists the people who can use its shared work and the company-wide audience. Its Team is managed separately in the Team tab. Use the offered tools to inspect and change those existing selections; a workflow script does not have a separate owner list or execution team. There is no separate purpose form to send the person to.

## Shared Crew agents

Share has one audience for the dashboard, published functions and selected existing agents. Inspect sharing first. For a Crew, `shared_agents` replaces the selected stable chat IDs from the returned agents list; an empty list clears them and omission preserves them. `agent_purposes` changes the saved one-line purpose by chat ID, only for tabs this person controls. Read the saved selection back.

People in the audience can read a selected agent's conversation from sharing onward, and send requests as themselves from the website, another Crew or MCP. Messages enter that same existing tab and use its configured context and working tools. Use `workspace action=chats` to discover selected agents, `read_chat` with its chat_id for the transcript (before_seq reads earlier messages), and `ask` to send an attributed request. `messages action=list_agents` and `send` expose the same targets; use the returned target/chat_id or complete agent address. Generic Crew Ask and published functions keep their current behavior.

Sharing an agent never gives the recipient its tab controls, private account credentials, other unselected chats or Crew settings. Only its tab owner can steer, stop, reset, rename, close, change its model or answer owner confirmations. Current audience and agent selection are checked again for reads, sends and queued delivery. A stored message receipt is acceptance, not a completed answer; agent replies remain explicit messages.
