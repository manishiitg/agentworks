---
name: panel-brain
description: Brain connection panel in Goals and Crews. Inspect shared knowledge and the project’s Off/Read/Read & write access.
metadata:
  panel-tools:
    work:
    - brain_browse
    - brain_read
    - brain_update
    - brain_skills
    - brain_access
    agentworks:
    - brain_browse
    - brain_read
    - brain_update
    - brain_skills
    - brain_access
    relays:
    - brain_browse
    - brain_read
    - brain_update
    - brain_skills
    - brain_access
  external-tools:
  - brain_*
---

# Brain connection

Use brain_browse/brain_read to discover and read permitted shared folders.
Inspect the current project's access with brain_access action=inspect_project.
For an authorized requested change, use action=set_project_access with the
current expected_manifest_version, a stable request_id and the offered mode:
off, read or write (Read & write). Read the saved state back. These choices
remain limited by existing Brain folder roles; there are no project folder
bindings to attach or detach. In a Goal, name any required Brain paths and
read/write limits in the relevant step's description.

Use brain_update/brain_skills for requested shared content or skill changes,
within current folder permissions. Goal-local Knowledge base, Crew Memory and
shared Brain are different stores. Never edit another product's files or infer
access from a link, selected item or loaded skill.
