# Chrome extension CDP bridge

Date: 2026-10-05. Tracking: [PLAT-510](../bugs/pulse_platform/browser/plat-510.md),
[PLAT-513](../bugs/pulse_platform/browser/plat-513.md) (current Code-only UI and connection policy).

## Goal

Let a Code agent use explicitly shared tabs in the user's
existing Chrome profile, locally or from a hosted platform. Keep the existing
`agent_browser` tool and agent-browser CLI. Chrome remains on the user's machine;
its login cookies are not exported. Page text, screenshots and action results
do travel to the platform and model.

## Architecture

```
agent_browser → workspace service → agent-browser --cdp <private relay URL>
                                              ↕ CDP WebSocket
                                  agent API's authenticated CDP relay
                                              ↕ outbound authenticated WebSocket
                                  Manifest V3 extension
                                              ↕ chrome.debugger / chrome.tabs
                                  explicitly shared Chrome tabs
```

The extension uses Chrome's debugger transport, not a remote-debugging port.
It does not run the CLI. The server brokers CDP frames over the paired socket. The extension implements
the browser-level CDP operations agent-browser needs, maps targets and flattened
sessions to shared tabs, and forwards page commands and events. The browser
permission boundary is therefore enforced on the user’s machine. Browser/Target methods unavailable through
chrome.debugger are implemented with chrome.tabs or rejected explicitly.

## Pairing and ownership

The authenticated Code Browser settings picker exposes My Chrome or Edge ·
extension. Copy connection returns the same random code for this account and
server-derived Code workspace. Workflows and Crews are deferred and both
management/connection routes enforce this scope. Codes survive server restarts
in a private 0600 credentials file; status/selection state never includes them.
Reset connection code explicitly rotates the code and closes current access.
Every connection and heartbeat rechecks the enabled account and current Code
product/workspace write access. The user pastes the connection into the
extension and explicitly chooses Share this tab. The extension opens an
outbound connection to the same deployment; only HTTPS/WSS is accepted except
loopback development. Authentication happens in the first WebSocket message,
so no app JWT is stored in the extension or placed in its URL.

A binding is private to `(account identity, server-derived browser workspace
identity)`. A collaborator's run never inherits another person's Chrome.
Live bindings last at most eight hours and remain process-local. The selected
browser is recorded separately in private server state, without any credential
or target ID. A restart restores a disconnected selection and requires explicit
human reconnection with the same saved code; it never restores access or silently chooses a server browser. Pairing replaces an older binding only on successful
connection. Only tabs explicitly shared from the extension are exposed. New
tabs may be created within that connected workspace and are included in it.

The connection overrides the workspace browser for that user's agent tool
calls while selected. Product/tool restrictions still apply. The existing
deployment setting that disables operator-host CDP does not disable this
separately authenticated user connection. The browser's server-derived scope
and trusted session identity select the binding; tool arguments cannot supply
an arbitrary endpoint or another user's binding.

## Transport and execution

The server starts a private CDP listener on loopback with an unpredictable
binding capability. The capability is passed only to the trusted workspace
execution path, never included in tool results, prompts or UI status. Split
agent/workspace services can explicitly configure listener address and advertised
host; this requires private-network routing and preserves capability checks.
The extension's public WebSocket route is a narrow exception to ordinary JWT
and gateway auth: its own reusable private credential and subsequent live socket are
the authorization boundary. Adjacent management routes remain authenticated.

Commands for a binding are serialized. The first action claims control for its
trusted root chat/run identity; delegated agents inherit it. Another conversation
is refused until the user explicitly re-pairs. This first release has one
controlling conversation per connection, avoiding shared stale element refs. The selected tab is pinned; closing it fails the next action instead of applying
cached references to another shared tab. The CLI session/socket folder is derived
from that binding and uses the existing workspace folder guard and artifact
broker. A lost connection invalidates the CDP client. It does not restart
Chrome, retry mutations or fall back to a managed browser. The user must pair
again to recover. Disconnect in the app returns future calls to the ordinary
workspace browser; Stop in the extension leaves a disconnected selection so
the next tool call explains that Chrome was stopped.

## Extension experience

Chrome 125 or newer is required for flattened debugger sessions.

The Browser pane exclusively shows the extension connection when chosen; it
never exposes workspace Start browser/teaching controls in that state. Setup
lives inside the ordinary picker, with three short steps, collapsed installation
instructions and raw JSON hidden behind Copy manually. Waiting ends only after
an actual new connection, not a poll of the prior live browser.
An idle pane displays browser choice cards after successful session discovery.
Opening an existing browser keeps those choices in the header settings. This
avoids flashing setup over a running session while discovery is pending.

The branded popup hides setup after connecting. It shows a Connected badge,
Code project/server identity, empty or populated shared-tab list, Share this tab,
New shared tab, Regroup tabs and Disconnect browser. Sharing the first tab
automatically creates a deployment-brand · project group in its window. New tabs may
be created by either the popup or the agent and join that managed group.
Grouping applies only to already shared tabs, separately per window. It does
not share other tabs, including tabs dragged into a group. Stop/unshare removes
our shared tabs from our managed groups without touching unrelated groups.
The copied connection includes the existing runtime appName as display-only
branding. Validate the name with the frontend's branding helper; the extension
also rejects blank, overlong or control-character names and falls back to
AgentWorks for older codes. This metadata never changes the account/project
credential or grants authority.

Extension commands keep the user's active tab by default. The optional
agent_browser active=true parameter permits Target.activateTarget,
Page.bringToFront and foreground tab creation during one serialized call; its
gate release clears permission. Popup New shared tab remains a human foreground
action. Inline tab selection reuses the current tab without the CLI's
ref-clearing switch; changed tabs still require a fresh snapshot.
Console/errors read bounded per-target relay caches (100 entries per kind,
2048 bytes per text), including child sessions. Removing a shared target or
disconnecting clears its cache; --clear affects only the selected tab.
Status returns screenshot_write_paths from the trusted folder guard. Output
must remain inside those paths; global /tmp/tool_output_folder paths stay denied.
The active interactive Code chat observes status every 2.5 seconds even with
its Browser pane closed. Connection, first-share and disconnection notices use
the existing global durable chat queue, preserve drafts and wait behind running
turns. Per-connection receipts prevent repeats; failed status requests do not
mean disconnection. Notices tell the agent to verify status, discard old refs
and use ordinary agent_browser commands through the backend-owned connection.
Code has explicit browser choices; missing or legacy Automatic settings select
Workspace browser. Workflow/Crew settings are unchanged.
Reusing a code in another browser replaces the prior browser on successful
connection; each new binding receives a fresh private relay capability.
Sharing another tab is explicit. Removing a shared tab detaches its debugger;
Stop closes the socket, detaches every debugger and clears the pairing secret.
No automatic reconnect or silent reattachment follows user revocation. Chrome
also supplies its debugger indicator. Heartbeats, command deadlines and bounded
message sizes handle idle periods and failed connections. State is reset rather
than restored with authority after an extension worker/browser restart.

## Protocol boundaries

HTTP(S) pages and about:blank are supported. Internal Chrome pages, extension
pages, file URLs and debugger access to unrelated targets are refused. Target
IDs are opaque to the backend and only the extension maps them to local tabs.
CDP calls that expose cookies, global browser state, native file paths, network
interception or browser shutdown are not part of the first release. They fail
with an explicit protocol error. Ordinary snapshots, click/fill/keyboard,
navigation, tab operations and screenshots are the qualification target.
Downloads stay on the laptop; server paths cannot name local upload files.
Teaching, HAR/video capture, native dialogs, store publication, unattended
operation with a sleeping laptop and complete CDP parity are outside this
release. Their availability must not be implied by the ordinary tool help.

## Verification and release

Use a real extension loaded into an isolated Chrome-for-Testing profile, the
real relay and the installed agent-browser version. Prove snapshot/reference
click, fill, screenshot, navigation, tab creation, existing cookie retention,
unshared-tab exclusion and immediate stop. Also cover cross-account lookup,
stable codes, reset revocation and disabled accounts, a second CDP controller, wrong capability and target
revocation through real WebSocket requests. Verify app pairing/control UI,
build the server/frontend and package the unpacked extension as a downloadable
ZIP. Record actual checks and remaining qualifications in PLAT-513.

References: [agent-browser CDP](https://agent-browser.dev/cdp-mode),
[Chrome debugger API](https://developer.chrome.com/docs/extensions/reference/api/debugger),
[worker lifecycle](https://developer.chrome.com/docs/extensions/develop/concepts/service-workers/lifecycle).
