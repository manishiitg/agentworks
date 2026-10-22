---
name: panel-local-laptop
description: Local laptop panel in Goals and Crews. Connect a computer or change its allowed folders and command
  access.
metadata:
  panel-tools:
    work:
    - manage_laptop_access
  external-tools:
  - manage_laptop_access
  - list_local_files
  - write_local_file
  - copy_local_file_to_crew
  - execute_local_shell_command
---

# Local laptop

Crew uses manage_laptop_access: devices/get first, browse to find a folder, set for the complete saved choice or remove. All current Crew owners use the same saved connection and grants. Load crew-laptop for setup commands and computer details. Read access and Read & write are different from the separate commands switch. Commands run as the person with their own environment, files and logins when they choose it. Never expose sign-in files or send server secrets to the laptop. Verify saved state; new access applies from the next message.
