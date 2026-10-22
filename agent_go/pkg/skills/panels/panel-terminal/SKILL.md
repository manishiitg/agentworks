---
name: panel-terminal
description: Terminal panel in Goals and Crews. Run a requested project command or show the live agent terminal.
metadata:
  panel-tools:
    work:
    - execute_shell_command
    - perform_ui_action
  external-tools: []
---

# Terminal

Use execute_shell_command for server/project work within the admitted sandbox. Crew’s selected computer uses execute_local_shell_command only with its saved command access. The agent’s live terminal is a view of the running CLI; opening it does not start a second terminal session or grant access. Use list_ui_capabilities/perform_ui_action only for advertised views and acknowledged receipts. Verify command exit/output; a timed-out command may already have run.
