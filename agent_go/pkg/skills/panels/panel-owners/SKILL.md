---
name: panel-owners
description: Owners and collaborators panel in Goals and Crews. Inspect or change the project owner where the current
  product supports it.
metadata:
  panel-tools:
    work:
    - crew_owners
  external-tools:
  - crew_owners
---

# Owners and collaborators

For Crew use crew_owners list to inspect the main owner and existing collaborators; add/remove requires the main owner and an exact person from the server directory. Keep the main owner distinct from the server’s allowed collaborator roles; do not replace Share with an owner list. Goals use manage_project get_access/set_access under their existing owner rules. Read current ownership before changing it and read back after success. An account administrator is not automatically the owner of someone else’s private account.
