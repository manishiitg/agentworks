# PLAT-516 — Code browser setup, notices and background control

State: fixed on main, not deployed. Date: 2026-10-05. Priority: P2. Requested by owner.

## Problem and decision

Connecting or stopping the browser extension changes tool routing without
telling the Code chat. Automatic browser selection also obscures the explicit
choice, and grouping shared tabs requires a separate popup action. Follow-up
requests add opt-in foreground activation, deployment-branded groups and
visible choices in an idle pane. A user review identified dead inline refs and
console output from the wrong shared tab.

## Implemented

- Observe the active interactive Code chat's extension status every 2.5 seconds,
  including when its Browser pane is closed. Send connection, first-shared-tab
  and disconnection notices with the existing global workspace-pane sender and
  durable queue used for MCP notices. Preserve drafts, wait behind running turns,
  deduplicate per connection and ignore failed requests or stale chat responses.
- Notices tell the agent to verify agent_browser status, use ordinary commands
  through the backend-owned connection and discard cached refs. A stopped
  selected extension pauses browser actions instead of allowing fallback.
- Remove Automatic from the Code picker. Missing/legacy automatic settings
  use Workspace browser in both the UI and server runtime. Workflow/Crew choices
  and their deferred extension rollout are unchanged.
- An idle Browser pane shows browser choice cards after session discovery;
  existing sessions/connected extensions keep the controls in their header.
  Cards open the existing settings/setup for the chosen method.
- Sharing creates a brand · project group in that window automatically.
  Popup-created and agent-created tabs join it. Regroup tabs restores grouping
  after a move. Group membership never authorizes an unshared tab, and
  disconnect/unshare still revoke debugger access and remove our group membership.
- Group names use the deployment's validated runtime appName from the copied
  connection, with AgentWorks as the fallback. This display-only metadata does
  not rotate the stable token or change authority. Older codes remain usable.
- Extension actions and new tabs remain in the background. Optional tool
  parameter active=true allows foreground activation for that serialized call
  only, covering Target.activateTarget and Page.bringToFront. Explicit human
  popup New shared tab still selects its new tab.
- Inline tab selection skips the native CLI's ref-clearing switch when that
  tab is already selected. A switch to another tab still requires a fresh snapshot.
- Console/errors use bounded per-shared-target relay caches, including child
  sessions, cleared on target removal/disconnection. --clear affects only that
  tab. Status/guidance expose screenshot write paths and explain that output
  must be inside the authorized project; /tmp/global tool_output_folder denial
  preserves the existing folder guard.

## Verification

- Real Chrome 153.0.8010.12 and Microsoft Edge 154.0.4258.53: actual unpacked
  extension, private relay, agent-browser and guarded workspace shell passed
  automatic grouping, new-tab membership, snapshot/fill/click, screenshots,
  cookie retention, scoped/protected targets, pinned closed-tab refusal,
  immediate Stop and same-code reconnect without restored tab authority.
  A private tab dragged into the group remains excluded from agent targets.
  Background snapshot/fill/click/navigation/new tabs preserve the user's active
  tab. Explicit active=true selects the intended tab and does not persist.
  Inline ref fill/click, isolated console/error output and tab-local clear pass.
  Custom brand groups and older-code AgentWorks fallback pass.
- Browser-rendered UI integration passed connection, first share, disconnect,
  browser change, reconnect and repeat-poll deduplication through the real
  global queue, with controlled HTTP status responses. The Code picker has no
  Automatic option; Workflow/Crew do not offer/query extension access. Existing
  dark/light and narrow/wide layouts passed and screenshots were inspected.
  Idle choice cards open setup and disappear when an existing session or a
  connected extension is present. Copied metadata follows runtime branding.
- Nineteen focused frontend checks passed, including real global-queue delivery
  behind a running turn, stale response rejection and existing MCP notifications.
- Focused server browser checks passed, including a regression for missing and
  legacy Code automatic settings while retaining Crew automatic behavior.
- Production frontend build and packaged ZIP/source equality passed.
  Relay tests and real Chrome tool/extension E2E passed the Go race detector.
  Qualification used an owned worktree; no running checkout was built or tested.

## Remaining

Deploy/rebuild the running application and reload the unpacked extension.
Notifications depend on the active Code chat being open; transient changes
between polls may not be observed. Qualification did not call an LLM: actual
model submission uses the existing chat queue processor. Per-tab diagnostics
retain the latest 100 entries per kind, with each text limited to 2048 bytes.
Reload/reconnect using a newly copied code to receive the current deployment
brand; the account/project token remains unchanged.
Workflow/Crew extension access and store publication remain deferred.

[Design](../../../design/chrome_extension_cdp_bridge.md).
