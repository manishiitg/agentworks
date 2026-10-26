---
name: panel-linked-folders
description: Linked projects and folders panel in Goals and Crews. Attach, inspect or remove a project reference
  or an authorized folder.
metadata:
  panel-tools:
    work:
    - files
    - list_accessible_workflows
    - list_attached_workflows
    - database
    - attach_workflow_reference
    - detach_workflow_reference
    agentworks:
    - list_accessible_workflows
    - files
    relays:
    - files
  external-tools:
  - files
  - crew_references
---

# Linked projects and folders

Crew uses files action=folders/folder_attach/folder_detach for server folders and list_accessible_workflows/list_attached_workflows/attach_workflow_reference/detach_workflow_reference for product references. Goals use files action=request_folder and their admitted reference tools. Discover available targets first, preserve aliases and use the exact stored ID when removing. A reference grants its permitted files, never secret values or private chat history. For a linked workflow you own or edit, database action=query_linked reads its database without changing it (row-bounded; pass the exact workspace_path from list_attached_workflows). Call another product through functions/ask rather than editing its internals.

For configuration details and runtime effects, Read [the detailed procedure](references/configuration.md).

For tagged project files and progressive inspection, Read [the detailed procedure](references/project-references.md).
