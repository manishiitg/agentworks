# Project dashboard

On a Mac (local mode), functions, external webhooks and dashboard Ask AI run only on the server: "This runs on the server. Add it to the Team to use it online." Do not create or enable them locally. Viewing dashboards/artifacts and their data, schedules, Watchers and non-function internal Crew/workflow calls remain available. Existing local definitions stay readable for Add to Team; the hand-off exports them and their data without local function execution. Server behavior is unchanged.

Use dashboard action=list and dashboard action=get first. Use dashboard action=create/dashboard action=update, dashboard action=validate and dashboard action=preview on the exact revision, then dashboard action=publish when requested. Read dashboard action=catalog for the authoring contract. Use dashboard action=link to show the result; a link does not grant access. Use managed TSX with shared components from @agentworks/dashboard for live dashboards with data, buttons and functions; charts use Recharts. Use HTML (index.html) for a quick shareable page such as a decision, a report or a file view. JSON dashboards are not supported. AskButton and ChatPanel appear only for shared Run users, never for Builders. Preserve authored content and all views/actions when converting. Cost, file and database access remain the current product’s own checks.

Dashboard work stays in the project's dashboard list. “Move this dashboard to React” means the hosted dashboard, not a separate Vite app or localhost server. Read the React section in the detailed procedure; a successful npm build alone is not a completed dashboard. Do not claim it is published until dashboard action=list confirms its publication and dashboard action=link returns its link.

Before authoring a dashboard, Read [the detailed procedure](references/authoring.md).

To inspect a draft, open the exact revision returned by get; do not replace it with the published link or publish it just to preview. Drafts require current edit access. Preview must finish with visible content: a loading result, missing screenshot or remaining visible Loading text is not a successful check.

For a button or link that sends a request into the project's chat, use `report.sendChatMessage(message)` from the explicit click handler. This is separate from `AskButton`/`report.chat.open()`, which only open the composer. Hidden Builder Ask widgets do not disable direct chat actions in the live website dashboard. Read “Buttons that send a message to chat” in the detailed procedure for an example, queued-result wording and preview/export/MCP App differences.

When playback, a motion dashboard or a video export is requested, Read [the detailed procedure](references/motion-guide.md). Set motion=true through create/update for interactive playback pages and publish to list them under Motion dashboards. The label alone does not animate a page. Use the shared MotionControls toolbar by default, floating within the authored playback area; its placement, appearance and controls stay customizable. MP4 export is optional.

For a list of the current person’s projects, read the “Projects created by the current person” section in references/authoring.md. Use the server-bound `{"$current_user_id":true}` SQL parameter on `created_by_user_id`, across dashboard, chat and MCP; keep existing shared access.

An Artifact is a finished page made in a chat to show or share one idea: a proposal, comparison, plan, mock-up or small interactive demo, with no live data or functions. Finished HTML pages belong in the project’s Dashboards by default, including requests phrased as “create an HTML file”; do not ask separately whether to add them. Explicit source-code/download-only requests and HTML inside an existing software project are exceptions; do not import arbitrary repository HTML. For a finished page, make an Artifact, never stop at a loose HTML file or localhost preview: create with artifact=true and an index.html alone, validate, publish, then reply with the `dashboard action=link` URL so the chat shows an Open button. Update the same id for a new version; the link stays the same. An Artifact is shared with the Crew, like its dashboards; to show it to someone without the whole Crew, make it in a Crew they already have.

Adding or updating a Team item requires an account that allows Builder work; Run-only accounts cannot move items. A scoped connection also needs `crews:write` and the existing Crew and file grants.

For **Add to Team**, prepare and publish the item's `online.json` dependency file: `dataset` with whole `tables` and explicit `views`/`triggers`, `functions` IDs, `assets` under `db/assets/`, and updating `automations` as `{id,name}`; use secret names only, never values, and point buttons at the Team agent before the shared `receive_online` preview—never guess rows, supply filesystem paths, retarget a Mac agent automatically, or reseed Team data on **Update on Team**.

## Add a dashboard to the Team

The person's Add to Team request is approval to arrange the move, not to reveal
technical details or ask them to pick tables. The button uses `dashboard action=add_to_team`
for the published item; the same action is available from chat. A static Artifact
copies directly; report its returned plain message, with no agent discussion.
When a live dashboard returns a source-agent task, carry out that task below:
do not repeatedly call add_to_team to request the same task again. For a live dashboard:
- Inspect its current queries, assets, functions and feeding routines; prepare
  its `online.json` dependencies from actual use, never guess IDs or rows.
- After preparing and publishing this item's dependencies, call `dashboard
  action=begin_handoff` in this Crew with its `dashboard_id`, current published
  `revision` and one stable `submission_id`. This explicit request alone may
  create the normal Team home; it copies nothing and starts no AI turn.
  Use its returned admitted `agent:` address for the Team agent; do not construct
  an address, provision on an ordinary ask/preview, or fake a way_add.
- Discover the Team agent's exact `agent:` address through AgentBridge. Use
  `messages` to agree on needed data, feeding schedules/Watchers, online Ask
  targets and available connections. No sessions, substitute messaging or
  impersonation; only names of Vault secrets, never values or personal logins.
- From the original member's authorized source connection, use `receive_online`
  preview first, then apply with the agreed `include_automations` and current
  `item_revision`, retaining one `submission_id`. Use the existing backend:
  it refuses collisions/secrets, copies first-use data once, updates code only
  on republish, and stops Mac routines before enabling their Team copies.
- Ask the person only for a genuinely missing connection or unresolved choice.
  Keep technical preview details between agents, collapsed under the result
  where the chat supports it. Say “preview” and “this Crew”. Confirm acceptance
  from the tool and a real online update before saying “keeps updating online”;
  otherwise plainly say the update is not yet checked. Never invent success.
