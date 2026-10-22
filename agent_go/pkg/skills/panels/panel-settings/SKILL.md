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

Read the current value before changing it. The person’s Builder request authorizes the requested change under existing server checks. Discover the current tool schemas; use only tools offered in this chat. Opening a panel is optional and does not perform its action. Verify the saved result or execution receipt. If a capability is absent, name the concrete missing action rather than invent a website-only restriction. Provider sign-in and phone QR scanning still need the person.

Read current project identity and instructions first. Crew uses set_work_identity plus its scoped instruction file tools; Goals and Relays use get_workflow_config/update_workflow_config and manage_project for label/description/icon. Use settings for its offered model, connection, secret-name and notification fields. Preserve unrelated settings and verify the saved result. Discover the current schema; never edit provider secrets or managed runtime configuration files directly.
