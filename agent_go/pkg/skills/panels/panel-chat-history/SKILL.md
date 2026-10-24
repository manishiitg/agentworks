---
name: panel-chat-history
description: Chat history and named tabs in Goals, Crews and Relays. Find, read or open earlier conversations in this project.
metadata:
  panel-tools:
    work:
    - execute_shell_command
    - get_ui_state
    - perform_ui_action
    agentworks:
    - manage_builder_chats
    - execute_shell_command
    - get_ui_state
    - perform_ui_action
    relays:
    - manage_builder_chats
    - execute_shell_command
    - get_ui_state
    - perform_ui_action
  external-tools:
  - builder_file_history
  - builder_tabs
---

# Chat history

Search this project’s saved conversation files by date/keyword with scoped file tools. Crew owner history is builder/conversation/; other callers’ history stays under their own crew-chats location. Goals use their saved Builder history, never another product’s chat folders. Use the current UI contract only if asked to open a conversation. Do not claim that a recalled summary is the complete transcript.

Goals and Relays have named Builder tabs. Use manage_builder_chats (MCP: builder action=tabs with tab_action=list/open/read/rename/pin/order/close). Name a new agent before opening it. Pin controls whether it stays on the tab bar; order saves your named tabs in first-to-last order while your main Builder stays first. All project owners see named tabs; only each tab's person controls it. Run records are history: use Builder to chat. Agent messages are separate from human control. Use list_agents to see reachable projects and their authorized chat_id values, then send_message(target, chat_id, message). With Builder access, omit a Crew chat_id to message its main Builder; Run sharing keeps your own ask history; function calls keep their declared execution conversation. Planning, steps, schedules and Autopilot stay on the server.
