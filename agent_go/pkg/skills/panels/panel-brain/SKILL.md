---
name: panel-brain
description: Brain connection panel in Goals and Crews. Select or inspect the shared Brain folders used by this
  project.
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

Use brain_browse/brain_read to discover permitted folders and brain_access for its admitted inspect/bind/unbind project actions. Selecting a folder does not grant its content permissions. Preserve unrelated bindings. Use brain_update/brain_skills for authorized content or skill changes, never another product’s files. Read back project bindings to verify; do not infer access from a link alone.
