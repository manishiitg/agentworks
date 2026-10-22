---
name: panel-backup
description: Backup panel in Goals and Crews. Configure, run, inspect or restore a Goal backup.
metadata:
  panel-tools:
    agentworks:
    - execute_shell_command
    - create_workflow_database_snapshot
  external-tools: []
---

# Backup

Read builder-reference/references/backup-strategy.md before touching backup. Use scoped file and shell tools for the established backup configuration/commands and create_workflow_database_snapshot for a managed database snapshot. Keep secret values and transient runtime data out of Git. Verify actual remote/destination evidence and backup/status.json; a local copy is not a verified off-device backup. Restore only the requested revision and scope.
