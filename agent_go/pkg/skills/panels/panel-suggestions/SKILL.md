---
name: panel-suggestions
description: Suggestions panel in Goals and Crews. Read, answer or dismiss pending suggestions for the project.
metadata:
  panel-tools:
    work:
    - list_needs_you
    - answer_needs_you
    - dismiss_needs_you
  external-tools:
  - list_needs_you
  - answer_needs_you
  - dismiss_needs_you
---

# Suggestions

Crew uses list_needs_you, answer_needs_you and dismiss_needs_you. List first and use the returned item ID. Answering records the person’s choice; implement any requested change explicitly and verify it separately. Dismiss only the selected item with a reason. Goal human decisions use the Decisions tools, not another Crew’s inbox.
