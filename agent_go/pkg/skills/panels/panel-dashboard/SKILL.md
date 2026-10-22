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

Use list_dashboards and get_dashboard first. Use create_dashboard/update_dashboard, validate_dashboard and preview_dashboard on the exact revision, then publish_dashboard when requested. Read get_dashboard_catalog for the authoring contract. Use get_dashboard_link to show the result; a link does not grant access. Preserve authored format and content. Cost, file and database access remain the current product’s own checks.

Before authoring a dashboard, Read [the detailed procedure](references/authoring.md).

When playback or a video export is requested, Read [the detailed procedure](references/motion-guide.md).
