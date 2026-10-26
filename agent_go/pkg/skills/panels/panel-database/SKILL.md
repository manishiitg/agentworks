---
name: panel-database
description: Database panel in Goals and Crews. Inspect tables, query or update the project database.
metadata:
  panel-tools:
    work:
    - database
    agentworks:
    - database
  external-tools:
  - database
---

# Database

Use `database action=query` to inspect rows, `describe` for tables/columns and `integrity_check` for the existing database check. Use `mutate`, `migrate` or `snapshot` only where offered; each keeps its current owner, connection, step and file-access checks. Linked workflow reads use query_linked/describe_linked/integrity_check_linked where available. Discover the current schema and target fields first. Keep SQL parameters separate from the statement. Do not modify SQLite/WAL/SHM files directly. Read back the requested rows or schema through the same database service.

For database-backed dashboard data and project actions, Read [the detailed procedure](references/project-data.md).

Crew Builder and Run chats receive the managed database as a direct tool. Shared people can query, describe and check its integrity; changing data stays with the existing Builder checks. A published function keeps its owner's authority. Use the offered tool directly; no terminal is needed.
