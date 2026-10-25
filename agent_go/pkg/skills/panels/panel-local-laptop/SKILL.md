---
name: panel-local-laptop
description: Local laptop panel in Goals and Crews. Connect a computer or change its allowed folders and command
  access.
metadata:
  panel-tools:
    work:
    - manage_laptop_access
    agentworks:
    - manage_laptop_access
  external-tools:
  - manage_laptop_access
  - list_local_files
  - write_local_file
  - copy_local_file_to_project
  - execute_local_shell_command
---

# Local laptop

In Crew and Goal chats these resources live together in the Vault panel.
Use panel-vault for the panel inventory, project-use ticks and chat routing;
this procedure covers the underlying operation.

Goals and Crews use manage_laptop_access: devices/get first, browse to find a folder, set for the complete saved choice or remove. All Builders of a project share its saved connection and grants. The devices reply gives install and connect steps when needed. External MCP passes workflow_id for a Goal or workspace_id for a Crew, exactly one. Read access and Read & write are different from the separate commands switch. Commands run as the person with their own environment, files and logins when they choose it. Never expose sign-in files or send server secrets to the laptop. Verify saved state; new access applies from the next message.

Use list_local_files, write_local_file, copy_local_file_to_project (local_path to project_path) and execute_local_shell_command for the connected computer. Normal files and shell stay on the server. Laptop tools are available only in a person’s interactive Builder chat or own MCP connection, never steps, schedules, Autopilot, child agents or Run callers.

The one-time computer connection is in Integrations → Local laptop → Connect a computer. Builder chat uses manage_my_vaults operation=laptops to see your computers and setup steps without choosing a project. External MCP uses manage_laptop_access action=devices without a project selector for the same personal list. Then use manage_laptop_access in the chosen Goal or Crew for its saved selection and grants. Connecting in Integrations → Local laptop → Connect a computer never gives workflow steps a laptop; Gmail and Slack selections for steps remain independent.
