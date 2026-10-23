---
name: panel-owners
description: Owners and collaborators panel in Goals and Crews. Inspect or change the project owner where the current
  product supports it.
metadata:
  panel-tools:
    work:
    - crew_owners
    agentworks:
    - manage_project
    relays:
    - manage_project
  external-tools:
  - crew_owners
  - manage_project
  - relay_owners
---

# Owners and collaborators

For Crew use crew_owners list to inspect the main owner and existing collaborators; add/remove requires a Crew owner and an exact person from the server directory. Keep the main owner distinct from the server’s allowed collaborator roles; do not replace Share with an owner list. Goals and Relays use manage_project get_access/set_access. Owners share the whole project and control only their own chat tabs. Keep the execution owner in the owner list. Relay MCP also offers relay_owners against the same owner list. Read current ownership before changing it and read back after success. An account administrator is not automatically the owner of someone else’s private account.
