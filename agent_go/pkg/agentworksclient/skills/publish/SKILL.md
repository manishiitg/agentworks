---
name: publish
description: Publish this local project's files and current chat to an online Crew when the person asks to publish to AgentWorks.
---

# Publish to Crew

Publish only when the person asks. Save this project's files and this exact
chat; hosted work continues from a private summary on the disclosed company
account while the computer is offline. Native chat resume comes later.

Use the current AgentWorks connection. Discover destinations with the existing
workspace list tool if the person wants an existing Crew. Preserve their choice:
New Crew starts Only me; adding a named agent to an existing Crew keeps that
Crew's Main, instructions and Share. Imported raw chat remains private to
Builders. Login files and .env are excluded.

From this native Claude Code or Codex chat, run `agentworks publish --name NAME`
in its own project folder. Add `--crew WORKSPACE_ID` for an existing Crew.
The CLI identifies its invoking chat itself. Do not supply a session override,
copy provider login files, or choose another chat's transcript. If disconnected,
connect the project with the normal `agentworks start` flow first.

In the website's Local chat use its `publish_project` tool, or the Files
panel's **Publish to Crew** button. Apps using MCP can call workspace
`publish_preview`, `publish` and `publish_status` for an exact own Local
website conversation. They cannot select a native transcript on the computer.

Keep the operation receipt. A lost response is not a failed publication:
resume the same receipt (`agentworks publish --resume OPERATION_ID`) or check
status. Report success only when state is `live`; show the Crew link and name,
its current audience and company account. If preparation fails, explain the
returned next step. Republishing follows the saved project/agent link and
preserves online working data and conversations.
