---
name: panel-dashboard
description: Project dashboard panel in Goals and Crews. Build, edit, preview, publish or restore the project dashboard.
metadata:
  panel-tools:
    work:
    - dashboard
    agentworks:
    - dashboard
    relays:
    - dashboard
  external-tools:
  - dashboard
---

# Project dashboard

Use dashboard action=list and dashboard action=get first. Use dashboard action=create/dashboard action=update, dashboard action=validate and dashboard action=preview on the exact revision, then dashboard action=publish when requested. Read dashboard action=catalog for the authoring contract. Use dashboard action=link to show the result; a link does not grant access. Use managed TSX with shared components from @agentworks/dashboard for dashboards; charts use Recharts. JSON dashboard specifications have been removed. AskButton and ChatPanel appear only for shared Run users, never for Builders. HTML authoring is deprecated; existing HTML remains supported. Preserve authored content and all views/actions when converting. Cost, file and database access remain the current product’s own checks.

Dashboard work stays in the project's dashboard list. “Move this dashboard to React” means the hosted dashboard, not a separate Vite app or localhost server. Read the React section in the detailed procedure; a successful npm build alone is not a completed dashboard. Do not claim it is published until dashboard action=list confirms its publication and dashboard action=link returns its link.

Before authoring a dashboard, Read [the detailed procedure](references/authoring.md).

For a button or link that sends a request into the project's chat, use `report.sendChatMessage(message)` from the explicit click handler. This is separate from `AskButton`/`report.chat.open()`, which only open the composer. Hidden Builder Ask widgets do not disable direct chat actions in the live website dashboard. Read “Buttons that send a message to chat” in the detailed procedure for an example, queued-result wording and preview/export/MCP App differences.

When playback, a motion dashboard or a video export is requested, Read [the detailed procedure](references/motion-guide.md). Set motion=true through create/update for interactive playback pages and publish to list them under Motion dashboards. The label alone does not animate a page. Use the shared MotionControls toolbar by default, floating within the authored playback area; its placement, appearance and controls stay customizable. MP4 export is optional.

For a list of the current person’s projects, read the “Projects created by the current person” section in references/authoring.md. Use the server-bound `{"$current_user_id":true}` SQL parameter on `created_by_user_id`, across dashboard, chat and MCP; keep existing shared access.
