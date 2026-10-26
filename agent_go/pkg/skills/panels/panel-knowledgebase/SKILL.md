---
name: panel-knowledgebase
description: Knowledge base panel in Goals and Crews. Inspect or maintain a Goal’s local knowledge base.
metadata:
  panel-tools:
    agentworks:
    - files
    - execute_shell_command
  external-tools:
  - files
---

# Knowledge base

Inspect the Goal’s knowledge files through its scoped file tools, then use files action=consolidate_project_knowledge/reorganize_project_knowledge for the requested maintenance. External MCP also offers files action=knowledge_list/knowledge_read. Read builder-reference/references/stores.md for the managed store contract. Local Goal knowledge and shared Brain are separate stores; Brain project access is governed by its current mode and folder roles, not folder bindings. Preserve source attribution and verify the affected records through their readers.
