---
name: panel-files
description: Files panel in Goals and Crews. Read, create, edit, move or link a project file from chat.
metadata:
  panel-tools:
    work:
    - execute_shell_command
    - diff_patch_workspace_file
    agentworks:
    - execute_shell_command
    - diff_patch_workspace_file
    - get_file_link
    relays:
    - execute_shell_command
    - diff_patch_workspace_file
    - get_file_link
  external-tools:
  - list_files
  - search_files
  - read_file
  - write_file
  - get_file_link
---

# Files

Use the current project’s scoped file readers and diff_patch_workspace_file or execute_shell_command for requested file changes. Preserve unrelated files and the project’s existing layout. Use get_file_link for a real existing artifact; return its URL verbatim. Shared file access excludes private chats, secret values and other products’ internals.

For existing-file links and attached-root access, Read [the detailed procedure](references/links.md).
