---
name: background-work
description: Run a bounded research, analysis, writing, coding, or file task asynchronously and rely on {{product}}'s automatic completion notification instead of polling.
---

# Background work

Use `run_in_background` when a self-contained task can continue independently while this chat remains available. Give it a short name, complete instructions, the smallest useful reasoning level, and any extra skill names it needs. The child already inherits this project's workspace access and attached skills.

After the tool returns, tell the user the task is running and end the turn. Do not poll with `query_agent` or sleep loops. {{product}} automatically sends an `[AUTO-NOTIFICATION]` into this same chat when the child completes or fails; then summarize the result and continue only if more work is required.

Use `query_agent` only when the user explicitly asks for live status, `list_agents` to identify running work, and `terminate_agent` when the user asks to stop it.
