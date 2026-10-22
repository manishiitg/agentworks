---
name: panel-database
description: Database panel in Goals and Crews. Inspect tables, query or update the project database.
metadata:
  panel-tools:
    work:
    - query_workflow_db
    - mutate_workflow_db
    - apply_workflow_db_migration
    - create_workflow_database_snapshot
    agentworks:
    - query_workflow_db
    - mutate_workflow_db
    - apply_workflow_db_migration
    - create_workflow_database_snapshot
  external-tools:
  - query_database
---

# Database

Use query_workflow_db for inspection, mutate_workflow_db for authorized data changes and apply_workflow_db_migration for schema changes. Use create_workflow_database_snapshot when a snapshot is needed. These names apply to Crew and Goal databases. Do not modify SQLite/WAL/SHM files directly. Verify the requested rows or schema through the same database tools.

For database-backed dashboard data and project actions, Read [the detailed procedure](references/project-data.md).
