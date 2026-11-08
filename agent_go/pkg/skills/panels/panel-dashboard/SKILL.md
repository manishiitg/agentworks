---
name: panel-dashboard
description: Build and share dashboards and Artifacts, offer functions, answer an incoming
  function call, or configure publishing and backups. Use the current product
  services.
metadata:
  version: 2.2.0
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
  external-tools:
  - dashboard
  - functions
  - dashboard.export_static
  - settings
---

# Dashboard

Build and share dashboards, offer functions, answer an incoming function call, or configure publishing and backups. Use the current product services.

A public link (`dashboard action=public_link`, mode on, update, off or status) is for when the Crew's owner asks for a page anyone can open without signing in. It is a frozen copy with no Ask and no buttons; it changes only on update, and off stops it at once. Say first that everything on the page goes public, including the data behind it (its query results, even columns the page doesn't show). A Crew on a Mac gets it later.

Start with the person's request and the current product. Read only the relevant procedure below: in product chat use `read_skill` with this skill name as `skill_name` and the listed `path` (chat has no `help action=topic`); only an MCP client uses `help action=topic` with this name as `topic` and the listed `path`. Use the actions offered by the current tool catalog. A guide does not grant access.

- Dashboard: read `references/dashboard/SKILL.md` when needed.
- Functions, including answering an incoming function call (its result file and outputs): read `references/functions/SKILL.md`.
- Publish: read `references/publish/SKILL.md` when needed.
- Backup: read `references/backup/SKILL.md` when needed.

Verify the saved result or returned run result, then give the person one clear next step. Keep their existing notes and omitted settings.
