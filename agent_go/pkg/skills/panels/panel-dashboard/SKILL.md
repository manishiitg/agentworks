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

## Finished pages belong in Dashboards

For a finished HTML page, proposal, report, comparison, mock-up or small interactive demo, create an Artifact in the project’s Dashboards by default—even if the person says “create an HTML file”. Do not stop at a local file, file download or localhost preview, and do not ask separately whether to add it to Dashboards. Read `references/dashboard/SKILL.md` before authoring; use the dashboard tools to create with `artifact=true`, validate and publish, then return the `dashboard action=link` URL so chat shows Open. Update the same Artifact for revisions. An Artifact is HTML with its CSS, images and fonts only; the server refuses TSX, scripts/ and project data calls (report.query, report.run, loadData). Live data or functions need a Dashboard instead. To share a published Artifact with the project's Team, call `share_dashboard_with_team` (mode on; off takes the copy back): it saves one self-contained HTML copy in the Team's shared folder under `artifacts/`, which teammates see under From the Team on their Artifacts page. Share again after changing the page.

Claude artifacts: in Claude (desktop, web or Cowork) the person may ask for a Claude artifact instead. Make it in Claude as asked; it lives in their own Claude account, so after it offer once to save a copy to the project so the Team can open it. When you make one of ours from Claude, show it inline with `open_dashboard` (pass its `resource_id` from `dashboard action=list`) so it reads like a Claude artifact. To save a Claude artifact to the project (“save this to the project”), create an Artifact from its code: an HTML artifact goes in as `index.html` unchanged; a React artifact is rewritten as one `index.html` with plain inline JavaScript and CSS (no imports or outside script, font or style links: they don't load). Then validate, publish and reply with the link as for any Artifact. A Claude artifact can't appear on the Artifacts page while it stays in Claude; only a saved copy can.

Exceptions: an explicit source-code/download-only request, or HTML that belongs inside an existing software project. Do not scan or import arbitrary repository HTML files. If the tools refuse or are unavailable, explain the limitation; do not claim a local file is an Artifact. Publishing here uses the project’s existing access, not an anonymous public link.

A public link (`dashboard action=public_link`, mode on, update, off or status) is for when the project's owner asks for a page anyone can open without signing in. It is a frozen copy with no Ask and no buttons; it changes only on update, and off stops it at once. Say first that everything on the page goes public, including the data behind it (its query results, even columns the page doesn't show). A project on a Mac gets it later.

Start with the person's request and the current product. Read only the relevant procedure below: in product chat use `read_skill` with this skill name as `skill_name` and the listed `path` (chat has no `help action=topic`); only an MCP client uses `help action=topic` with this name as `topic` and the listed `path`. Use the actions offered by the current tool catalog. A guide does not grant access.

- Dashboard: read `references/dashboard/SKILL.md` when needed.
- Functions, including answering an incoming function call (its result file and outputs): read `references/functions/SKILL.md`.
- Publish: read `references/publish/SKILL.md` when needed.
- Backup: read `references/backup/SKILL.md` when needed.

Verify the saved result or returned run result, then give the person one clear next step. Keep their existing notes and omitted settings.
