---
name: panel-backup
description: Backup panel in Goals and Crews. Configure, run, inspect or restore a Goal backup.
metadata:
  panel-tools:
    agentworks:
    - settings
    - execute_shell_command
    - create_workflow_database_snapshot
  external-tools:
  - update_settings
---

# Backup

Read builder-reference/references/backup-strategy.md before touching backup. Use scoped file and shell tools for the established backup configuration/commands and create_workflow_database_snapshot for a managed database snapshot. Keep secret values and transient runtime data out of Git. Verify actual remote/destination evidence and backup/status.json; a local copy is not a verified off-device backup. Restore only the requested revision and scope.

For Goals, “After every run” is `backup.after_runs`, off by default. Use settings action=update to change it, preserving destination configuration. It applies to every full run and skips unchanged files; the explicit action still works while automatic runs are off. Flows do not use Goal housekeeping.
