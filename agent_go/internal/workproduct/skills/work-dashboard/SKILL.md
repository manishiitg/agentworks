---
name: work-dashboard
description: Create or update a {{product}} project's visual Dashboard for tasks, notes, plans, status, research, project information, or any other content the user wants to manage visually.
---

# {{product}} Dashboard

Use this skill for a dashboard, board, tracker, visual home or other project view.
The shared contract below defines the HTML runtime, files, inline attachments,
data, scripts, validation and publication.

<!-- product:code -->
## Create it with the dashboard tools

When the user asks you to create a dashboard, build it with the platform's dashboard tools, not only as a loose HTML
file, so it can be checked and published. Do not leave a standalone `.html` file in the project root and call it done.

1. Discover once: `search_tools` with `group="dashboard"` (ask for the whole group; do not page one tool at a time), then
   `get_api_spec` for `create_dashboard`, `validate_dashboard`, `preview_dashboard` and `publish_dashboard`.
2. Write the request body to a file first, so shell quoting cannot break it:
   `{"dashboard_id":"<slug>","title":"<title>","files":{"index.html":"<the whole page>"}}`.
3. Create the draft: `curl --fail-with-body -sS --json @body.json -H "$MCP_AUTH" "$MCP_CUSTOM/create_dashboard"`. The
   reply carries the dashboard id and its revision.
4. Check it with `validate_dashboard` (the dashboard id and revision), fix what it reports, try `preview_dashboard`, then
   `publish_dashboard` with the revision it asks for (see each tool's spec for its arguments).
5. Tell the user what exists and what state it is in. If a step fails, show its error text and say it is still a draft;
   never say it is published unless `publish_dashboard` succeeded. `list_dashboards` shows what already exists.
<!-- /product -->

<!-- dashboard:shared -->

## {{product}}-specific rules

- This is a general project artifact. Do not create a workflow, plan, phase,
  step, route or Pulse configuration merely to provide a Dashboard.
- Crew readers follow current Crew project access; Code Dashboards and scripts
  are owner-only. Viewing a project does not grant its MCP/Vault credentials.
- The Dashboard is available from the {{product}} **Dashboard** button.
- For requested animation or MP4 export, read `references/motion-guide.md`.
- External authoring requires `dashboards:read` and `crews:write` for Crew,
  or `code:run` for an owned Code project. Existing consent and tool availability
  still apply; these instructions grant no additional capabilities.
