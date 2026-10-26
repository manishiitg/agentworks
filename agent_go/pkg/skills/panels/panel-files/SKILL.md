---
name: panel-files
description: Files panel in Goals and Crews. Read, create, edit, move or link a project file from chat.
metadata:
  panel-tools:
    work:
    - execute_shell_command
    - files
    agentworks:
    - execute_shell_command
    - files
    relays:
    - execute_shell_command
    - files
  external-tools:
  - files
---

# Files

Use the offered `files` actions to list, search, read, write, patch, move or delete within the current project and grants. Native chats bind their project; external MCP uses the returned project ID. Keep revision/request IDs where the schema requires them. Preserve unrelated files and the existing layout. Use action=link for a real existing artifact and return its URL verbatim. Action=image retains the current image-analysis provider/account where offered. Shared reads keep the existing private-chat, secret and file-root exclusions. Use the platform terminal only when an operation is already allowed there and no file action offers it.

For existing-file links and attached-root access, Read [the detailed procedure](references/links.md).
