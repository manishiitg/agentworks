---
name: panel-dashboard
description: Build and share dashboards, offer functions, answer an incoming
  function call, or configure publishing and backups. Use the current product
  services.
metadata:
  version: 2.1.1
  panel-tools:
    work:
    - functions
    - dashboard
    agentworks:
    - functions
    - dashboard
    - settings
    - execute_shell_command
    - database
    relays:
    - functions
    - dashboard
  external-tools:
  - dashboard
  - functions
  - dashboard.export_static
  - settings
---

# Dashboard

Build and share dashboards, offer functions, answer an incoming function call, or configure publishing and backups. Use the current product services.

Start with the person's request and the current product. Read only the relevant procedure below: in product chat use `read_skill` with this skill name as `skill_name` and the listed `path` (chat has no `help action=topic`); only an MCP client uses `help action=topic` with this name as `topic` and the listed `path`. Use the actions offered by the current tool catalog. A guide does not grant access.

- Dashboard: read `references/dashboard/SKILL.md` when needed.
- Functions, including answering an incoming function call (its result file and outputs): read `references/functions/SKILL.md`.
- Publish: read `references/publish/SKILL.md` when needed.
- Backup: read `references/backup/SKILL.md` when needed.

Verify the saved result or returned run result, then give the person one clear next step. Keep their existing notes and omitted settings.
