---
name: panel-dashboard
description: Project dashboard panel in Goals and Crews. Build, edit, preview, publish or restore the project dashboard.
metadata:
  panel-tools:
    work:
    - list_dashboards
    - get_dashboard
    - create_dashboard
    - update_dashboard
    - validate_dashboard
    - preview_dashboard
    - publish_dashboard
    - get_dashboard_link
    - restore_dashboard
    - get_dashboard_catalog
    - export_dashboard_static
    agentworks:
    - list_dashboards
    - get_dashboard
    - create_dashboard
    - update_dashboard
    - validate_dashboard
    - preview_dashboard
    - publish_dashboard
    - get_dashboard_link
    - restore_dashboard
    - get_dashboard_catalog
    - export_dashboard_static
    relays:
    - list_dashboards
    - get_dashboard
    - create_dashboard
    - update_dashboard
    - validate_dashboard
    - preview_dashboard
    - publish_dashboard
    - get_dashboard_link
    - restore_dashboard
    - get_dashboard_catalog
  external-tools:
  - '*dashboard*'
  - get_report_link
---

# Project dashboard

Use list_dashboards and get_dashboard first. Use create_dashboard/update_dashboard, validate_dashboard and preview_dashboard on the exact revision, then publish_dashboard when requested. Read get_dashboard_catalog for the authoring contract. Use get_dashboard_link to show the result; a link does not grant access. Use managed TSX with shared components from @agentworks/dashboard for dashboards; charts use Recharts. JSON dashboard specifications have been removed. AskButton and ChatPanel appear only for shared Run users, never for Builders. HTML authoring is deprecated; existing HTML remains supported. Preserve authored content and all views/actions when converting. Cost, file and database access remain the current product’s own checks.

Dashboard work stays in the project's dashboard list. “Move this dashboard to React” means the hosted dashboard, not a separate Vite app or localhost server. Read the React section in the detailed procedure; a successful npm build alone is not a completed dashboard. Do not claim it is published until list_dashboards confirms its publication and get_dashboard_link returns its link.

Before authoring a dashboard, Read [the detailed procedure](references/authoring.md).

When playback, a motion dashboard or a video export is requested, Read [the detailed procedure](references/motion-guide.md). Set motion=true through create/update for interactive playback pages and publish to list them under Motion dashboards. The label alone does not animate a page. Use the shared MotionControls toolbar by default, floating within the authored playback area; its placement, appearance and controls stay customizable. MP4 export is optional.

For a list of the current person’s projects, read the “Projects created by the current person” section in references/authoring.md. Use the server-bound `{"$current_user_id":true}` SQL parameter on `created_by_user_id`, across dashboard, chat and MCP; keep existing shared access.
