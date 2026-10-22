---
name: panel-team
description: Team panel in Goals and Crews. Inspect or change the Goal’s team, Leader or route assignments.
metadata:
  panel-tools:
    agentworks:
    - workflow_team
    - get_workflow_team
  external-tools:
  - workflow_team
---

# Team

Use workflow_team get first; update uses the current expected_revision and complete intended policy. Use only eligible_members returned by the server. Preserve unrelated members, grants, route assignments and read-only steps. The server’s existing confirmation/owner rules still apply. reauthorize_schedule reviews a saved schedule separately with its exact revision; changing the team must not silently broaden a schedule. Verify the returned saved policy.
