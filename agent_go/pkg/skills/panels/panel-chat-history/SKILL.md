---
name: panel-chat-history
description: Chat history panel in Goals and Crews. Find, read or open earlier conversations in this project.
metadata:
  panel-tools:
    work:
    - execute_shell_command
    - get_ui_state
    - perform_ui_action
    agentworks:
    - execute_shell_command
    - get_ui_state
    - perform_ui_action
    relays:
    - execute_shell_command
    - get_ui_state
    - perform_ui_action
  external-tools:
  - builder_file_history
---

# Chat history

Search this project’s saved conversation files by date/keyword with scoped file tools. Crew owner history is builder/conversation/; other callers’ history stays under their own crew-chats location. Goals use their saved Builder history, never another product’s chat folders. Use the current UI contract only if asked to open a conversation. Do not claim that a recalled summary is the complete transcript.
