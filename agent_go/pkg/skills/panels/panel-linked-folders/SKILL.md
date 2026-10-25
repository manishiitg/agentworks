---
name: panel-linked-folders
description: Linked projects and folders panel in Goals and Crews. Attach, inspect or remove a project reference
  or an authorized folder.
metadata:
  panel-tools:
    work:
    - list_work_folders
    - attach_work_folder
    - detach_work_folder
    - list_accessible_workflows
    - list_attached_workflows
    - query_linked_workflow_db
    - attach_workflow_reference
    - detach_workflow_reference
    agentworks:
    - list_accessible_workflows
    - request_workflow_folder_access
    relays:
    - request_workflow_folder_access
  external-tools:
  - crew_folders
  - crew_references
---

# Linked projects and folders

Crew uses list_work_folders/attach_work_folder/detach_work_folder for server folders and list_accessible_workflows/list_attached_workflows/attach_workflow_reference/detach_workflow_reference for product references. Goals use request_workflow_folder_access and their admitted reference tools. Discover available targets first, preserve aliases and use the exact stored ID when removing. A reference grants its permitted files, never secret values or private chat history. For a linked workflow you own or edit, query_linked_workflow_db reads its database (read-only, row-bounded; pass the exact workspace_path from list_attached_workflows). Call another product through functions/ask rather than editing its internals.

For configuration details and runtime effects, Read [the detailed procedure](references/configuration.md).

For tagged project files and progressive inspection, Read [the detailed procedure](references/project-references.md).
