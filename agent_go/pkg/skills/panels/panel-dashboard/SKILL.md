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

Use list_dashboards and get_dashboard first. Use create_dashboard/update_dashboard, validate_dashboard and preview_dashboard on the exact revision, then publish_dashboard when requested. Read get_dashboard_catalog for the authoring contract. Use get_dashboard_link to show the result; a link does not grant access. Use managed TSX with shared components from @agentworks/dashboard for new dashboards; JSON is optional. AskButton and ChatPanel appear only for shared Run users, never for Builders. HTML authoring is deprecated; existing HTML and JSON remain supported. Preserve authored content and format unless conversion is requested. Cost, file and database access remain the current product’s own checks.

Dashboard work stays in the project's dashboard list. “Move this dashboard to React” means the hosted dashboard, not a separate Vite app or localhost server. Read the React section in the detailed procedure; a successful npm build alone is not a completed dashboard. Do not claim it is published until list_dashboards confirms its publication and get_dashboard_link returns its link.

Before authoring a dashboard, Read [the detailed procedure](references/authoring.md).

When playback or a video export is requested, Read [the detailed procedure](references/motion-guide.md).
