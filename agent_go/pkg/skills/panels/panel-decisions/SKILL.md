---
name: panel-decisions
description: Decisions panel in Goals and Crews. Inspect, answer or withdraw a Goal’s pending human decision.
metadata:
  panel-tools:
    agentworks:
    - get_human_input_request
    - answer_human_input_request
    - create_human_input_request
    - withdraw_human_input_request
  external-tools:
  - get_human_input_request
  - create_human_input_request
  - answer_human_input_request
---

# Decisions

Use get_human_input_request to read the actual question/options and answer_human_input_request for the person’s answer. create_human_input_request raises a needed decision; withdraw_human_input_request closes an obsolete request. Marking consumed or dismissing duplicates is not an answer. Use stable request IDs and inspect the saved outcome before continuing dependent work.
