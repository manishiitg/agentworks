---
name: panel-settings
description: Instructions and identity panel in Goals and Crews. Change the project name, purpose, instructions
  or settings.
metadata:
  panel-tools:
    work:
    - set_work_identity
    - manage_custom_commands
    - execute_shell_command
    - settings
    agentworks:
    - manage_custom_commands
    - execute_shell_command
    - get_workflow_config
    - update_workflow_config
    - manage_project
    - settings
    relays:
    - manage_custom_commands
    - execute_shell_command
    - get_workflow_config
    - update_workflow_config
    - manage_project
    - settings
  external-tools:
  - get_settings
  - update_settings
  - manage_project
---

# Instructions and identity

Read current project identity and instructions first. Crew uses set_work_identity plus its scoped instruction file tools; Goals and Relays use get_workflow_config/update_workflow_config and manage_project for label/description/icon. Use settings for its offered model, connection, secret-name and notification fields. Preserve unrelated settings and verify the saved result. Discover the current schema; never edit provider secrets or managed runtime configuration files directly.


## App chat instructions

Use `settings` action=get to read the optional `chat_instructions` on a Goal, Crew or Relay. When its owner asks, set it with action=update and `chat_instructions` (empty clears; omitted preserves). The same field appears as **Chat instructions** beside Purpose in Settings. Keep it under 4,000 characters: useful behavior, tone and what to ask first, in plain words. It is visible to people who can use the app; never store secrets here. It is shared app guidance composed with Purpose and platform policy, not a complete system prompt override, tool grant or replacement for Builder instruction files. Existing permissions and confirmation rules still apply. Verify the saved value; a changed instruction refreshes retained chat guidance on the next turn.
