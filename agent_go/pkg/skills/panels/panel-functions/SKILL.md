---
name: panel-functions
description: Functions panel in Goals and Crews. Publish or change the actions other people and agents can call.
metadata:
  panel-tools:
    work:
    - list_functions
    - define_function
    - delete_function
    - call_function
    - get_function_call
    agentworks:
    - list_functions
    - define_function
    - delete_function
    - manage_workflow_webhook
    - call_function
    - get_function_call
    relays:
    - manage_workflow_webhook
  external-tools:
  - '*workflow_function*'
  - '*crew_function*'
---

# Functions

List functions before editing. Crew uses define_function/delete_function and list_functions; Goals use manage_workflow_webhook for a typed function route and the registered function tools for calls. Preserve the declared input/result schema and stable entry identity. A real call performs real work. Read the returned execution status/result before claiming success.

For incoming/outgoing function calls, tracked results and attached workflow triggers, Read [the detailed procedure](references/calls.md).
