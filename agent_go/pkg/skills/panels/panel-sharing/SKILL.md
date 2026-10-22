---
name: panel-sharing
description: People and access panel in Goals and Crews. Inspect or change who may use this project.
metadata:
  panel-tools:
    work:
    - get_dashboard_share
    - share_dashboard
    agentworks:
    - get_dashboard_share
    - share_dashboard
    - manage_project
    relays:
    - get_dashboard_share
    - share_dashboard
    - manage_project
  external-tools:
  - get_dashboard_share
  - share_dashboard
---

# People and access

Use get_dashboard_share/share_dashboard for this product’s Share selection (Only me, Chosen people, Everyone). Goal owner/editor lists use manage_project get_access/set_access under existing owner checks. Share only the requested product with the requested people; a URL itself grants nothing. Crew has one main owner; using a Crew is through its shared dashboard. Read back the saved audience/access before reporting completion.
