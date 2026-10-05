# AgentWorks Browser Bridge

The source is in `agentworks-chrome/`. The design and implementation tracking are
in [the design document](../docs/design/chrome_extension_cdp_bridge.md) and
[PLAT-513](../docs/bugs/pulse_platform/browser/plat-513.md).

## Install and connect

Requires Chrome 125 or newer for flattened debugger sessions.
The same unpacked package also passed the complete local end-to-end check in
desktop Microsoft Edge 154.0.4258.53.

1. Build/restart the platform with this change. In a writable Code project,
   open **Browser → Settings** and choose **My Chrome or Edge · extension**.
   Workflows and Crews do not offer the extension yet.
2. Download the extension ZIP from that panel and unzip it.
3. In Chrome, open `chrome://extensions` (in Edge, `edge://extensions`), enable **Developer mode**, choose
   **Load unpacked**, and select the unzipped folder. Pin the extension if useful.
4. Copy the connection from AgentWorks into the extension popup and choose
   **Connect browser**. The same code works until you explicitly reset it.
5. Open the tab to use and select **Share this tab** in the extension.
6. Ask the existing chat to work in Chrome. `agent_browser(command="status")`
   reports extension mode. Use ordinary browser commands without `--cdp`.

Only explicitly shared tabs and tabs created by the connected agent are exposed.
The pairing is private to your account/workspace. One chat/run controls each
connection; its delegates inherit access. Reconnect to switch controlling chats. Chrome remains on your laptop,
with its existing login session. Shared page contents and screenshots are sent
back to the platform/model. Keep Chrome and the laptop running and connected.

**Disconnect browser** in the popup revokes access immediately. The platform
keeps the disconnected selection so subsequent actions fail instead of switching
browsers. Choosing **Workspace browser** (or direct connection) in Browser
settings explicitly disconnects the extension and restores ordinary controls. Reconnect with the same saved code after browser/server restart or disconnection;
there is no automatic reconnect. Connections expire after eight hours.

Shared tabs automatically join a **brand · project** group in their window,
using the deployment's app name (AgentWorks by default). Copy a fresh connection
to include the current branding; your private token stays the same.
**New shared tab** creates a tab for the connected project; agent-created tabs
join the group too. **Regroup tabs** restores the grouping after moving tabs.
Dragging an unshared tab into the group does not share it.
The popup clearly shows Connected even before the first tab is shared. Its
connection form disappears after connection.

The active Code chat receives connection, first-shared-tab and disconnection
notices through the same durable message queue as MCP notifications. Notices
queue behind a running turn and preserve unsent drafts. Status is checked every
2.5 seconds while the chat is open, including with its Browser pane closed.
Code offers explicit browser choices; older Automatic settings use Workspace browser.
An idle Browser pane shows those choices directly; a running browser keeps them
in its header settings.

Agent operations and new tabs stay in the background by default. An explicit
`active: true` tool parameter allows that call to bring its shared tab forward,
for example `command="tab", args=["t1"], active=true`. Popup **New shared tab**
is a human action and selects its new tab. Inline `tab tN` actions preserve refs
when the selected tab is unchanged; switch to another tab and take a fresh
snapshot before acting. Console/errors are scoped to that shared tab.
For screenshots use an explicit path inside the project's granted writable
workspace (shown by status), such as `<project>/evidence/screenshot.png`.
Global `/tmp` and `tool_output_folder` destinations are outside that grant.

Copying the code repeatedly returns the same private account/project code.
Connecting another browser with it replaces the prior connection. **Reset
connection code** invalidates saved copies and disconnects the current browser.
Server access is checked again on each connection/heartbeat; saved codes do not
bypass revoked access. The extension retains no code or automatic authority
across worker/browser restart.

Downloads remain on your laptop. Upload transfer, teaching, recording, HAR,
internal Chrome pages and complete CDP compatibility are not supported in this
release. The extension is loaded unpacked; Chrome Web Store publication is not
part of the implementation.

Loading unpacked does not require Google login or Chrome Web Store approval.
Keep the extracted folder available while using the extension. Company-managed
browser policies can restrict developer-mode installations.

## Develop and verify

Run from an owned repository worktree:

```sh
python3 scripts/package-chrome-extension.py
npm ci --prefix packages/playwright
RUN_CHROME_EXTENSION_E2E=1 go -C agent_go test ./pkg/browser \
  -run '^TestChromeExtensionToolRealE2E$' -count=1 -v
go -C agent_go test -race ./pkg/browserrelay
```

Install Playwright's full Chromium browser if needed, or set
`CHROME_EXTENSION_E2E_CHROME` to a Chrome-for-Testing executable. The live test
uses its own temporary profile; it does not control the user's existing Chrome.

The packager embeds the extension ZIP in the Go server, which serves it at
`/api/downloads/chrome-extension.zip`. Repackage after every extension source edit.
The test runs the actual guarded workspace shell, the installed agent-browser,
the relay and the unpacked extension, including screenshot artifact transfer.

The CDP listener defaults to a random loopback port on the agent API host.
Native workspace services on the same machine need no extra configuration.
For split services, set `AGENT_BROWSER_EXTENSION_RELAY_BIND` to an explicitly
chosen private address/port (for example `0.0.0.0:9334`) and
`AGENT_BROWSER_EXTENSION_RELAY_HOST` to the hostname/IP reachable from the
workspace service. Restrict that port to the private service network. Each CDP
connection still requires its opaque capability. Only the extension's outbound
WebSocket `/api/browser/extension/connect` traverses the public gateway.

To check the app controls visually, run the frontend dev server from this
worktree, then run `node scripts/test-chrome-extension-ui.mjs` with
`CHROME_EXTENSION_UI_URL` set to that server's URL. The fixture is served only
by the development server; it is not a production build entry point. Screenshots
are saved beneath `/tmp/agentworks-chrome-extension-ui` by default.
