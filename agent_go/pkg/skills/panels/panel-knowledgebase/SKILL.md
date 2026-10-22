---
name: panel-knowledgebase
description: Knowledge base panel in Goals and Crews. Inspect or maintain a Goal’s local knowledge base.
metadata:
  panel-tools:
    agentworks:
    - consolidate_knowledgebase
    - reorganize_knowledgebase
    - execute_shell_command
  external-tools:
  - list_workflow_knowledge
  - read_workflow_knowledge
---

# Knowledge base

Inspect the Goal’s knowledge files through its scoped file tools, then use consolidate_knowledgebase/reorganize_knowledgebase for the requested maintenance. External MCP also offers list_workflow_knowledge/read_workflow_knowledge. Read builder-reference/references/stores.md for the managed store contract. Local Goal knowledge and company Brain bindings are separate stores. Preserve source attribution and verify the affected records through their readers.
