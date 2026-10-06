# Browser: ownership, automation, live control and teaching

This is the single browser design and implementation guide for our products.
It consolidates the former core browser, workflow authoring, live browser and
Chrome/Edge extension designs, including extension installation and deployment.
The extension policy is current through PLAT-530 (2026-10-05); other browser
runtime behavior below retains its implementation and local verification from
2026-10-03. Compact chrome, tab restoration and teaching attachment fixes are
deployed and verified on RTS in `7e2ea79-20261003164344`; other servers require
their own release verification. See [PLAT-393](../bugs/pulse_platform/browser/browser/plat-393.md).
The teaching recorder was qualified against `agent-browser 0.38.2`; older runtimes
must support `get cdp-url`, target IDs in `tab --json`, and native streaming.

## Reading map and connection methods

- [Ownership and product behavior](#ownership-and-product-behavior).
- [Personal Chrome/Edge extension: setup, architecture, tokens, control and deployment](#personal-chrome-extension).
- [Start browser and manual sign-in](#start-browser-and-manual-sign-in), [tabs and reconnection](#tabs-and-reconnection).
- [Teaching](#teach-a-browser-task), [automation and modes](#current-automation-reference).
- [Direct CDP tab lifecycle](#shared-cdp-tab-lifecycle), [state and isolation](#state-and-isolation).
- [Artifacts and diagnostics](#debugging-and-evidence), [workflow authoring](#workflow-authoring).
- [Live browser and manual control](#live-browser-and-manual-control), [persistent profiles](#persistent-managed-profiles).
- [Recording](#diagnostic-recording-existing), [media](#synthetic-microphone-and-camera), [Playwright](#playwright-test-browsers).
- [Deployment, verification and source map](#deployment-and-verification).

| Connection | Browser location and transport | Current scope |
| --- | --- | --- |
| Workspace browser | Managed Chromium in the workspace; existing agent-browser execution and viewer. | Products with browser capability enabled. |
| Chrome · direct connection | Configured debugging endpoint reachable by the runtime; direct CDP. | Where deployment settings permit operator-host CDP. |
| My Chrome or Edge · extension | User's local browser; outbound WebSocket → private server relay → agent-browser CDP. | Owned Code/Crew projects and workflows you can edit; connections stay account-private. |

The extension uses CDP internally but has its own authorization and supported
command boundary. Later direct-CDP sections describing host file URLs, shared
workflow tabs, recording or local upload/download paths do not grant those
features to the extension. Its complete policy is in the extension section.

## Ownership and product behavior

A managed browser belongs to a workflow or product project, not to a person.
Identity authorizes access. Builder chats, runs and delegated agents use the
workflow browser; Crew/Code conversations use the project browser. Physical
project paths include the owner so two owners' same-named projects stay separate.
Deployment prefixes separate installations. Workspace-less chats retain their
session fallback. Browsers start on demand, not when a workflow is created.

| Product/surface | Implemented behavior |
| --- | --- |
| AgentWorks workflows and Relays | Automatic by default. Missing/legacy `none` manifest modes become `auto`; ordinary setup has no Disable browser choice. Manifest remains the canonical settings store. |
| Crew and Code | Browser capability and current product grants remain enforced. `.browser-settings.json` in the physical project is canonical; chat/tab caches cannot override it. |
| Work browser panel | Reads and writes those project settings through the authenticated browser API. |
| Video Studio | Required browser and shared workflow viewer; server uses managed Chrome. |
| SparkQuill | Parent can start and sign in through its browser drawer; teaching is hidden on this surface. Local CDP installation instructions appear only when enabled. Child browser remains explicitly disabled. |
| Dominion | Tool allowlist still excludes `agent_browser`; starting a browser cannot bypass that restriction. |

Code offers Workspace browser, My Chrome or Edge · extension, and Chrome ·
direct connection when enabled. Code has no Automatic choice; missing/legacy
Automatic settings resolve to Workspace browser. Workflow/Crew retain their
existing Automatic/managed/direct-CDP behavior. When
`AGENT_BROWSER_CDP_ENABLED=false`, startup and workflow/project runtime use
managed Chrome even if an old setting requests direct CDP. Ordinary modes are
workspace-scoped; extension selection is private to the account/workspace. Product capability/tool restrictions remain separate from mode
migration. Missing or invalid workflow manifests still fail closed.

Managed workflow/project profiles persist by default beneath the OS configuration
folder, or `AGENT_BROWSER_PROFILE_ROOT`. Docker Compose sets the same profile base
in both services and mounts a persistent `browser_profiles` volume, outside the
document tree. Other split-service deployments must configure the same absolute
profile root and make it available to the workspace service.
Existing `AGENT_BROWSER_SHARED_PROFILE`
configuration still takes precedence. Profiles are separate by scope; default
persistence does not enable the legacy global shared-browser fallback. Containers
must mount profile storage for persistence across container replacement. Explicit
empty `AGENT_BROWSER_SHARED_PROFILE` retains the existing ephemeral override.

`mcpagent` transports tools; `multi-llm-provider-go` does not choose browser scope.
Neither dependency requires a source change for this feature.

## Personal Chrome extension

Tracking: [PLAT-510](../bugs/pulse_platform/browser/browser/plat-510.md),
[PLAT-513](../bugs/pulse_platform/browser/browser/plat-513.md) and
[PLAT-516](../bugs/pulse_platform/browser/browser/plat-516.md) and
[PLAT-524](../bugs/pulse_platform/crew/browser/plat-524.md) and
[PLAT-530](../bugs/pulse_platform/browser/browser/plat-530.md).

Let a Code, Crew or workflow agent use explicitly shared tabs in the user's
existing Chrome profile, locally or from a hosted platform. Keep the existing
`agent_browser` tool and agent-browser CLI. Chrome remains on the user's machine;
its login cookies are not exported. Page text, screenshots and action results
do travel to the platform and model.

In workflows, the Browser icon stays visible in the main toolbar beside Activity,
before Ops. It opens the existing Browser pane, including connection choices,
settings and live status. Selecting Browser highlights that icon.

Workflow, Code and Crew Browser icons also show a green dot while connected,
with a Connected tooltip and accessible description. This remains visible while
another pane is open. A selected extension can be connected with zero shared
tabs; otherwise active live browser sessions count. Disconnects and failed status
requests clear the dot, and completed recordings never count as live connections.
Tracking: [PLAT-542](../bugs/pulse_platform/app/navigation/plat-542.md).

### Install and connect

1. Build/restart the platform with this change. In an owned Code/Crew project or a workflow you can edit,
   open **Browser → Settings** and choose **My Chrome or Edge · extension**.
   Readers cannot pair a browser. Relay rollout remains deferred.
2. Download the extension ZIP from that panel and unzip it.
3. In Chrome, open `chrome://extensions` (in Edge, `edge://extensions`), enable **Developer mode**, choose
   **Load unpacked**, and select the unzipped folder. Pin the extension if useful.
4. Copy the connection from AgentWorks into the extension popup and choose
   **Connect browser**. This immediately connects and shares the current HTTP(S)
   website or about:blank; protected pages and tabs belonging to another workspace
   remain unshared, while the connection still becomes ready for agent-created tabs.
   Automatic connections to other workspaces never adopt your current tab. The token is shared across your Code/Crew projects and workflows until account Reset.
5. Ask the agent to browse. It creates and chooses its own project tabs and
   groups them automatically. No first-tab sharing step is required. To use an
   already-open page, optionally choose the project and **Share this tab** in the extension.
6. In another supported project, the browser picker shows **Connected to your
   account** beside My Chrome or Edge when your account browser is online.
   Choose that option to register and connect this project; no token copy/paste
   is required. Opening the picker alone does not change the project's browser.
   Each agent receives only its own project tabs. The picker is for optional
   manual sharing; automatic connection preserves its current selection.
7. Ask the existing chat to work in Chrome. `agent_browser(command="status")`
   reports extension mode. Use ordinary browser commands without `--cdp`.

Loading unpacked does not require Google login or Chrome Web Store approval.
Keep the extracted folder available while using the extension. Company-managed
browser policies can restrict developer-mode installations. The same package
passed the real local end-to-end check in Chrome 153.0.8010.12 and Microsoft Edge
154.0.4258.53; see PLAT-516 for the qualified coverage.

### Architecture

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
It does not run the CLI. The server brokers CDP frames over the paired socket.
The extension implements the browser-level CDP operations agent-browser needs, maps targets and flattened
sessions to shared tabs, and forwards page commands and events. The browser
permission boundary is therefore enforced on the user’s machine. Browser/Target
methods unavailable through `chrome.debugger` are implemented with `chrome.tabs`
or rejected explicitly.

### Pairing and ownership

The authenticated Code/Crew/workflow Browser settings picker exposes My Chrome or Edge ·
extension and shows account availability separately from this project's connection.
Choosing it explicitly reuses a live account browser through the authenticated
`connect` action; the response contains status, never a pairing credential.
Copy connection registers the server-derived project and returns one
persistent random token for the account plus that project's routing scope. The
full JSON differs by scope even though the token is identical across projects.
Only registered scopes can connect; scope is metadata, not authorization.
Product access, project ownership and write access are checked on management,
every connection and every heartbeat. Workflow pairing requires AgentWorks product
access, an existing workflow root manifest and owner/editor access; Relay manifests
are excluded. Each collaborator pairs their own account browser.

The private credential survives server restarts in the existing 0600 file.
Account Reset rotates it and closes all of that account's live connections;
ordinary disconnect removes only the chosen project selection. An existing
project credential is upgraded to the canonical account token; other legacy
project copies retain their original scope until Reset. Newly copied connections
use the account token and explicit scope. A missing scope is allowed only when
the credential identifies one project unambiguously.

A live binding is private to `(account identity, server-derived browser workspace
identity)`. Each project has its own private CDP capability, target/session maps,
controller, diagnostics and groups. Several Code/Crew projects and workflows can connect from
one extension simultaneously. A local tab can be shared with only one project;
sharing it into a second project is refused. A collaborator's run never inherits
another person's Chrome. Reusing a token in another browser replaces only the
project named by that connection's scope, after successful authorization.

Workflow steps with agent_browser enabled inherit the workflow's scope and the
server-bound account/run identity. Registered child/group tool sessions retain
that run as the browser controller, so later steps can use earlier steps' tabs
without inheriting their filesystem grants. Separate runs/chats cannot take over
a live controller; reconnect to change it. Scheduled or background steps can use
the connection only while the local browser is online; disconnect fails closed.
The account's connection is never borrowed by another workflow owner or reader.

Workshop and full-run browser executors bind authenticated account/run identity
before step tool assembly. Dedicated execution, message-sequence and todo tool
sessions register under that parent run, so fresh script/CLI HTTP calls resolve
the same extension while keeping the child's file grants. Dropping account
identity must never turn a selected extension into a local-CDP fallback.
Tracking: [PLAT-546](../bugs/pulse_platform/browser/browser/plat-546.md).

Live bindings last at most eight hours and remain process-local. Server selection
is recorded without credentials or target IDs. Extension 0.4.0 remembers explicitly
enabled project pairings in browser-local storage, restricted to trusted extension
contexts and never synced to another browser. It opens an outbound WebSocket to
the saved deployment URL and authenticates in its first message. Network loss,
sleep/wake and server/worker/browser restarts trigger reconnect with bounded
backoff and an alarm wake-up. Each successful handshake rechecks account/workspace
access and creates a fresh private relay capability, controller and reference
session; an eight-hour socket expiry renews through the same path. No fallback
browser is selected. The app reports Connected only after the new socket pairs.

Tab grants are separate, in browser-session storage as exact tab IDs. Within the
same browser session, reconnect reattaches only surviving explicitly shared IDs.
It never adopts the foreground page or discovers authority from URLs, titles or
existing groups, and cannot detach a tab belonging to another project. A browser
restart or extension reload clears session grants: the connection returns ready
with zero tabs, and the agent can create its own first tab. Sharing an existing
page again remains optional. Reconnection never brings tabs into focus.

Disconnect in the popup removes that project's remembered pairing before
stopping its debugger/socket; Disconnect all removes all remembered pairings.
App Disconnect removes the durable server selection. Automatic resume requires
that selection to still exist, so an offline browser cannot undo Disconnect.
Account Reset invalidates the credential, and lost access or replacement by
another live browser stops retries when received. Explicit Connect can enable
the project again. Browser-local storage and alarms permissions implement this
lifecycle; the account token itself stays stable until Reset.
HTTPS/WSS is required except loopback development; no app JWT is stored in the
extension or placed in its URL.

The connection overrides the workspace browser for that user's agent tool
calls while selected. Product/tool restrictions still apply. The existing
deployment setting that disables operator-host CDP does not disable this
separately authenticated user connection. The browser's server-derived scope
and trusted session identity select the binding; tool arguments cannot supply
an arbitrary endpoint or another user's binding.

### Transport and execution

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
controlling conversation per connection, avoiding shared stale element refs.
The selected tab is pinned; closing it fails the next action instead of applying
cached references to another shared tab. The CLI session/socket folder is derived
from that binding and uses the existing workspace folder guard and artifact
broker. A lost connection invalidates the CDP client. It does not restart
Chrome, retry mutations or fall back to a managed browser. The user must pair
again to recover. Choosing Workspace browser or direct connection in the app
returns future calls to the ordinary browser; Stop in the extension leaves a disconnected selection so
the next tool call explains that Chrome was stopped.

### Extension experience

Chrome 125 or newer is required for flattened debugger sessions.

The Browser pane exclusively shows the extension connection when chosen; it
never exposes workspace Start browser/teaching controls in that state. Setup
lives inside the ordinary picker, with installation, connection and browsing steps, collapsed installation
instructions and raw JSON hidden behind Copy manually. Waiting ends only after
an actual new connection, not a poll of the prior live browser.
An idle pane displays browser choice cards after successful session discovery.
Opening an existing browser keeps those choices in the header settings. This
avoids flashing setup over a running session while discovery is pending.

The branded popup hides setup after connecting. It shows a Connected badge,
Code/Crew/workflow workspace picker and server identity, empty or populated shared-tab list, Share this tab,
New shared tab, Regroup tabs, Disconnect project and Disconnect all projects.
Sharing or creating the first tab automatically creates a deployment-brand · project group in its window. New tabs
may be created by either the popup or the agent and join that managed group.
Grouping applies only to already shared tabs, separately per window. It does
not share other tabs, including tabs dragged into a group. Stop/unshare removes
our shared tabs from our managed groups without touching unrelated groups.
The copied connection includes the existing runtime appName as display-only
branding. Validate the name with the frontend's branding helper; the extension
also rejects blank, overlong or control-character names and falls back to
AgentWorks for older codes. This metadata never changes the account/project
credential or grants authority.

Extension commands keep the user's active tab by default. The optional
`agent_browser` `active=true` parameter permits Target.activateTarget,
Page.bringToFront and foreground tab creation during one serialized call; its
gate release clears permission. An empty connected project accepts `open` or
`tab new` to create its first authorized tab through the worker before bootstrapping
the CLI. Tab listing returns an empty list without starting a browser; other
page actions explain that a tab must be created. Native labels are preserved by
a temporary bootstrap target when the first new tab requests a label. Popup New shared tab remains a human foreground
action. Inline tab selection reuses the current tab without the CLI's
ref-clearing switch; changed tabs still require a fresh snapshot.

#### Tab diagnostics and artifacts

Extension 0.4.1 retains a bounded ring of 256 protocol metadata records per
project: command start/success/failure, debugger detach reason, explicit unshare
path, and child-session lifecycle. The platform negotiates optional forwarding
in its `paired` response; older servers receive no new message type. The relay
logs validated metadata under `[CHROME_EXTENSION]`, with at most 4096 messages
per connection per minute. It excludes credentials, URLs, page contents and CDP
parameters. Last method is context, not proof that a command caused a detach.
Human revocation still removes the grant; logging adds no automatic reattachment.
Investigation: [PLAT-569](../bugs/pulse_platform/browser/browser/plat-569.md).

Console/errors read bounded per-target relay caches (100 entries per kind,
2048 bytes per text), including child sessions. Removing a shared target or
disconnecting clears its cache; --clear affects only the selected tab.
Status returns screenshot_write_paths from the trusted folder guard. Output
must remain inside those paths; global /tmp/tool_output_folder paths stay denied.

#### Browser documentation

`agent_browser skills list` and `skills get <name> [--full]` read the installed
CLI's version-matched documentation on the server. In extension mode they use
no CDP endpoint, relay lease, tab selection or browser launch, and remain
available with zero tabs or an offline selected extension. They retain the
authenticated account and the calling step's workspace grants. Only those
documentation forms are accepted; connection/launch flags and skill paths are
refused. Documentation success never proves browser connectivity: page actions
still require the selected extension and fail closed when it is offline.

The attached `agent-browser` skill and
`read_skill(skills=[{"name":"builder-reference","path":"references/browser-usage.md"}])`
provide the platform adapter guidance. Upstream examples do not enable extension
network/HAR, recording, trace, profiler, transfer or teaching capabilities.

#### Connection status and lifecycle

Code, Crew and workflow Browser toolbars observe status every 2.5 seconds even
with their Browser pane closed. Connecting, sharing a first tab, disconnecting
or reconnecting updates the browser UI without sending an automatic chat message
or starting an agent turn. Pending browser notices saved by older clients are
discarded before automatic queue delivery. Browser tools continue to resolve
the selected account-private connection on each invocation.
Code has explicit browser choices; missing or legacy Automatic settings select
Workspace browser. Crew retains its ordinary Automatic/managed/direct-CDP
settings alongside the new extension choice. Workflows offer that same choice.
Reusing a code in another browser replaces that project's prior browser on successful
connection; each new binding receives a fresh private relay capability.
Sharing another tab is explicit. Removing a shared tab detaches its debugger;
Disconnect project closes that socket and detaches only its debuggers.
Disconnect all projects closes all sockets and clears remembered credentials.
No automatic reconnect or silent reattachment follows user revocation. Chrome
also supplies its debugger indicator. Heartbeats, command deadlines and bounded
message sizes handle idle periods and failed connections. Transient reconnect
restores only explicit grants from the current browser session; a full browser
restart restores the connection with zero tabs.

### Protocol boundaries

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

### Verification and release

Use a real extension loaded into an isolated Chrome-for-Testing profile, the
real relay and the installed agent-browser version. Prove snapshot/reference
click, fill, screenshot, navigation, tab creation, existing cookie retention,
unshared-tab exclusion and immediate stop. Also cover cross-account lookup,
stable codes, reset revocation and disabled accounts, a second CDP controller,
wrong capability and target revocation through real WebSocket requests. Verify app pairing/control UI,
build the server/frontend and package the unpacked extension as a downloadable
ZIP. Record actual checks and remaining qualifications in the linked platform tickets.
Real Chrome and Edge qualification for the current behavior is recorded in
[PLAT-516](../bugs/pulse_platform/browser/browser/plat-516.md). Browser/server restart,
offline Disconnect and remembered pairing checks are recorded in
[PLAT-532](../bugs/pulse_platform/browser/browser/plat-532.md); deployment remains separate.

References: [agent-browser CDP](https://agent-browser.dev/cdp-mode),
[Chrome debugger API](https://developer.chrome.com/docs/extensions/reference/api/debugger),
[worker lifecycle](https://developer.chrome.com/docs/extensions/develop/concepts/service-workers/lifecycle).

### Extension development and deployment

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

After upgrading to extension 0.4.0, reload the unpacked extension (or load the
new ZIP folder) and connect once to seed its remembered pairing. Subsequent
returns do not require another paste. The upgraded server is also required for
explicit revocation close frames and the automatic-resume selection check.

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

## Start browser and manual sign-in

**Browser → Start browser → Take control → open site → sign in → Return control**
works before an agent runs. Opening the panel discovers browsers; the Start button
launches/reuses the scoped browser through the workspace service.

`GET/POST /api/browser/workspace?workspace_path=...` resolves the browser identity
on the server. POST accepts `start`, `recover` or `save`; fixed-workspace products also pass
`profile_id`. The agent API checks product capability and workspace grants. A
per-scope control gate rejects startup while another controller/action owns it.
Startup preserves existing tabs and enables streaming only when necessary.
Native `tab --json` starts/reuses Chrome; URL-less `open` resets the selected page
in the qualified runtime. Browser commands carry the caller's account identity,
workspace guard and the selected runtime's launch flags. Chrome's private IPC
folder survives command cleanup. Teaching files inherit the scope's group access
so the server account slot can review drafts and consume published skills.

On the server, the user sees streamed server Chrome pixels and inputs travel
through the authenticated live WebSocket. The website is not an application
iframe. Sign-in stays in that scoped browser profile. Local CDP uses the attached
Chrome's own profile and may also be controlled directly in Chrome; the viewer's
control lease holds the same per-port lock as agent CDP commands. Sites may expire
sessions, so saved procedures still require an active sign-in.

## Tabs and reconnection

Connected browser chrome has two 36 px rows: tabs and neutral actions, then
navigation, address and connection/control status. Narrow panels use labeled
icons and scroll the tab strip; actions do not wrap into another header.

Each managed browser remembers up to 50 HTTP(S)/blank tab URLs and the selected
page in `.agentworks-tabs.json` (0600) inside its existing private Chrome profile.
The service snapshots the live daemon once per second while it remains running,
even after a previously opened viewer closes. URLs retain their query/fragment
routing; this is private browser state, distinct from sanitized teaching evidence.
Passwords, field contents and clipboard text are not stored in the tab file.
Startup reopens remembered pages when the new browser is blank, or selects the
remembered page if full Chrome restored the same pages itself. Existing different
pages always win, and repeated Start does not reset a live page. Headless shell
cannot rely on Chrome's `--restore-last-session` flag. Local CDP leaves tab
restoration to the user's Chrome. Unsaved form contents are not restored; changes
within the last snapshot interval may be lost in a sudden crash.

A dropped stream retries with bounded backoff. After failed reconnects, a writable
viewer that previously held control may make one authorized managed-browser
recovery request under the same scope/control gate. Passive watchers and Playwright
never start a browser; local CDP recovery requires the user to reconnect Chrome.
Starting a browser manually also resets exhausted reconnect retries. Teaching
never resumes automatically across an unobserved gap.

## Teach a browser task

**Start browser → sign in → Teach task → describe the result → demonstrate →
Finish → helper prepares the task → Try task → Save skill** is implemented for
ordinary browser forms and controls. Diagnostic video/HAR recording stays a
separate feature; it does not automatically become a learned task.

Local settings show a compact browser choice: Automatic, Workspace browser or
My Chrome. Connection ports, diagnostics and setup commands sit under a closed
Advanced disclosure. Server settings describe the workspace browser without
redundant mode choices. Start browser is in the top header; connected tabs
have close controls and a plus button. Tab mutations require
exclusive manual control; manually entering an address during teaching records
an explicit navigation so replay opens it instead of waiting for a link click; closing the sole remaining tab is refused.

### Capture inside the existing Chrome

`workspace/browserteach` obtains the selected browser's private runtime-provided
CDP URL and attaches to its selected page. Discovery and reviewed replay send
fixed operations to the existing daemon through its private IPC socket. Managed diagnostic capture uses the same IPC rule. They
do not invoke another CLI, which could restart the daemon on a version mismatch. It does not launch another browser,
replace the profile, expose a raw CDP endpoint, or record every Chrome tab.
The private recorder currently requires a loopback WebSocket endpoint. A local
container connecting to host Chrome needs additional endpoint qualification.

An init script and default execution-context listeners cover the current document
and subsequent navigation. Captured actions include genuine clicks, final field
edits (including paste/IME input), select/check/uncheck, and Enter/Escape. The
host assigns action IDs, page/frame identity, timestamps and sanitized URLs.
Targets store semantic role/name, stable selector candidates and row context.
Same-process frames with a stable iframe id/name/test attribute carry a frame
locator chain. Meaningful actions may have bounded JPEG result evidence.
Screenshots are omitted on pages containing sensitive fields or frames, and after
paused fields have been edited, rather than pretending those images are masked.

Teaching holds the viewer's exclusive control lease, so agent actions and another
controller cannot interleave. Other authorized viewers may watch. Recording has
an indicator in the app and Chrome, Pause/Resume/Finish/Cancel, a ten minute limit,
a 1,000 action limit, and a 50 MiB screenshot budget. Disconnect finalizes an
**interrupted** demonstration and releases control; start a new demonstration
instead of stitching an unobserved gap into a tested procedure. Interrupted and
cancelled captures cannot be tested/published directly.

Sign in before teaching. Password/OTP/token/card fields are suppressed before
buffering values, cookies and auth headers are never collected, and URL query and
fragment values are removed. A field edited during Pause stays excluded for the
rest of that demonstration, including a later blur after Resume. New documents
start paused until the host applies the recording state. This is not universal
secret detection: ordinary page text, usernames and non-sensitive example inputs
can be present. Review artifacts before saving them as guidance.

### Draft, review, test and reuse

Evidence lives in `<workspace>/browser-demonstrations/<id>/`:

- `manifest.json`: goal, scope, ordered actions, reviewed guidance, variables,
  outcome check, status and last test time.
- `actions.jsonl`: ordered structured evidence.
- `draft.md`: initial procedure draft.
- `step-*.jpg`: eligible result evidence.

Finish drafts the procedure and, where the panel has a helper chat, sends that
helper a review request. The helper can propose guidance, parameters and an
outcome in the demonstration manifest; it is told to treat site content as
untrusted and to keep the procedure untested. The panel automatically refreshes
the helper’s review. Users see the task goal, reusable inputs, expected result
and readiness, then **Try task** and **Save skill**. Raw actions, selector
warnings, repair controls, file paths and guidance editing stay out of the
normal panel; the helper works with these records behind the scenes.

Test first saves the reviewed draft, acquires the browser control gate, supplies
the chosen example inputs, and runs the same signed-in browser. It resolves a
fresh unique visible target for each step. Stable selectors are candidates;
ambiguous/missing targets stop the run. Generated CSS paths are transient fresh
resolutions, not durable recorded recipes. Observed navigation is checked rather
than repeating a link's navigation. A page text/URL outcome check must pass.
The UI states that trying the task performs it on the website using the chosen
inputs. Changes to the expected result require another successful try before saving.

The workspace service retains a fingerprint receipt for the exact successfully
tested actions, guidance and check. Editing a manifest to say `tested` does not
permit publication. A service restart requires retesting before publication.

**Save skill** creates a scope-owned reusable file:

- Workflows: `learnings/_global/references/browser-<id>.md`, linked from the
  workflow's existing `learnings/_global/SKILL.md`.
- Projects: `skills/browser-<id>/SKILL.md`. Crew/Code select that local skill in
  their canonical `workflow.json`, so subsequent chats attach it.
- `browser-demonstrations/INDEX.md` also indexes tested procedures; enabled product
  runtimes point the helper to that workspace index for future taught tasks.

The saved procedure references its reviewed manifest and expected outcome. It
uses the existing managed browser tool, scope and permissions; it grants no new
website or tool authority. The same procedure can be tested again with new input
values. Site changes or expired sign-in may require another review/test.

### Qualified coverage and remaining work

The local real-Chrome check covers semantic buttons without stored selectors,
parameter replay, sign-in retention across browser restart, actual JPEG evidence,
repeated recorder sessions, password suppression and paused edits across
navigation. Multi-tab qualification covers manual new tabs, switching back,
site-created popups, closing a popup, repeated replay with fresh targets and
privacy for a new tab selected while paused. Control/auth checks and UI review/test behavior have separate tests.

This release does not promise universal browser recording. Canvas, drag actions,
file upload and unlocatable clicks need helper review. Multi-tab procedures
support explicitly opened/selected tabs, opener-owned popups and tab closure.
Each test maps recorded tab identities to fresh Chrome targets before resolving
DOM targets. Missing tabs, undeclared identities, ambiguous popups and unsupported
navigation stop replay. Only explicitly selected tabs and their new popups are
attached; existing unrelated Chrome tabs are excluded. Capture supports up to
twelve simultaneously attached tabs. Cross-process/cross-origin frames, shadow DOM,
native dialogs and protected pages need further qualification. Query/fragment
routing may need a reviewed navigation URL because capture removes those values.
Downloads can be initiated by a demonstrated click, but the current page text/URL
check does not verify the downloaded artifact. Visual replay, download/dialog
verification, native file chooser replay and broad retention/quota administration
remain follow-up work. A video alone does not qualify these cases.

See [workflow learning](../workflow/learning_architecture.md) and
[project instructions](../design/project_instruction_files.md) for the existing
learning conventions. Keep browser guidance here instead of creating parallel
guides for individual products.

### External reference and evidence limits

[Grok Bot's official teaching flow](https://docs.x.ai/grok-bot/skills-routines-and-automations#teach-a-workflow-by-demonstration)
records visible interaction for up to ten minutes without microphone audio,
then produces a draft skill for testing. An
[unofficial reconstruction's recorder](https://github.com/b-nnett/grok-bot-0.18-reconstructed/blob/a9f633e09d49a85829b8236331b9e21f7e612634/source/host/extensions/teach-recording/teach-recording-service.ts#L141)
uses FFmpeg/X11 video and dispatches `learn-from-demonstration`; it does not show
DOM-event recording in that service. Its
[provenance](https://github.com/b-nnett/grok-bot-0.18-reconstructed/blob/a9f633e09d49a85829b8236331b9e21f7e612634/PROVENANCE.md)
is a partial reconstruction, not official source. The learning skill is fetched
separately, so its analysis internals are unverified. Our structured recorder is
our own implementation, not a claim about Grok's latest implementation.

## Current automation reference

All normal automation uses the managed `agent_browser` tool and matching
`agent-browser` skill. Chrome can run headlessly in the workspace, attach through
a configured direct-CDP endpoint where permitted, or use the selected Code
extension relay with ordinary tool commands and no model-supplied `--cdp`.

## Modes

| Mode | Behavior | Typical use |
|---|---|---|
| `none` | Legacy ordinary workflow/project configuration migrates to `auto`. Internal missing-manifest and explicit product capability restrictions remain separate. | Compatibility only; no ordinary UI option. |
| `auto` | Use a reachable configured CDP browser; otherwise use headless. Code normalizes this legacy value to `headless`. | Workflow/Crew default; no Code UI option. |
| `headless` | Use the workflow/project’s managed Chromium. | Background and scheduled runs. |
| `cdp` | Attach to the configured Chrome debugging port. | Existing logins, visual QA, and sites that reject headless browsers. |

The workflow manifest stores the mode under
`capabilities.browser_mode`. Browser steps attach the `agent-browser` skill.
The Code/Crew/workflow extension is a separately stored private account/workspace selection
that overrides ordinary execution while selected; it is not a manifest mode.
A disconnected selected extension fails until explicit reconnection or a browser
choice change, rather than falling back through `auto`.

## Starting a CDP browser

On macOS, install the default launcher on port `9222` with:

```bash
curl -fsSL 'https://raw.githubusercontent.com/manishiitg/coding-agent-loop/main/scripts/install-chrome-cdp-macOS.sh' | bash
```

Install another independent launcher/profile by passing a port:

```bash
curl -fsSL 'https://raw.githubusercontent.com/manishiitg/coding-agent-loop/main/scripts/install-chrome-cdp-macOS.sh' | bash -s -- --port 9333
```

Each CDP profile must use its own port and `--user-data-dir`. The usual port is
`9222`; the port-specific installer creates a separate application and profile.

For a specialized workflow that needs multiple login identities, launch more
profiles on different ports, for example `9222` and `9333`, then configure:

```json
{
  "browser_mode": "cdp",
  "cdp_ports": [9222, 9333]
}
```

The runtime accepts at most four configured ports. Ordinary workflow
concurrency does not require multiple profiles: workflows share one CDP browser
and use labeled tabs plus a per-port select-and-act lock.

## Managed tool

Do not run the `agent-browser` CLI through the shell for browser actions. Call
the managed `agent_browser` tool. The runtime injects and validates the CDP
endpoint, applies session limits, serializes shared-tab actions, and authorizes managed
upload/output paths. `file://` navigation has the separate limitation below.

To check whether CDP is reachable, use the backend status operation:

```text
agent_browser(command="status", args=[], session="default")
```

`status` needs no tab and no `--cdp` argument. `snapshot` is not a connectivity
probe: it reads one specific page, so in shared CDP mode it must name the tab.

Before the first browser action, load the installed CLI's matching command guide:

```text
agent_browser(command="skills", args=["get", "core"])
```

This documentation route also works in extension mode, without shared tabs.
An attached `read_skill` guide is another documentation route; keep the status
check before browsing regardless of how the guide was loaded.

The common flow is:

```text
agent_browser(command="open", args=["https://example.com"])
agent_browser(command="snapshot", args=["-i"])
agent_browser(command="click", args=["@e1"])
agent_browser(command="snapshot", args=["-i"])
```

In CDP mode, list and reuse a suitable tab before asking to create one. Include
the returned real tab ID (`t1`, `t2`, and so on) inline for every page action.
`open` itself remains URL-only. The inline system prompt gives the exact
endpoint and argument form for the active session.

## Shared CDP tab lifecycle

This section describes configured direct CDP. The Code/Crew/workflow extension instead exposes
only authorized targets, has one controlling root chat per binding and keeps tabs
in the background unless a call sets `active=true`.

One visible Chrome is shared safely by verifying and acting under a per-port
lock. A workflow must not assume that the tab selected during its previous tool
call is still active: the user, the website, or another workflow may have
changed Chrome in the meantime. The backend therefore reads the real tab state
immediately before every page action while it holds the shared lock. It keeps
using the requested real `tN` when that tab is already active, and switches only
when another tab is active. This avoids repeatedly bringing Chrome to the
foreground on macOS without allowing one workflow to act in another tab.

The normal flow is:

1. Call `agent_browser(command="tab", args=["--cdp", "<endpoint>"])` once to
   inspect real tab IDs and query-free display URLs.
2. Reuse the workflow's already-owned labeled tab when one exists. It may be
   navigated to the requested URL.
3. Otherwise, reuse a pre-existing tab only when its normalized URL exactly
   matches the requested URL.
4. If neither matches, request a stable labeled tab with
   `agent_browser(command="tab", args=["--cdp", "<endpoint>", "new",
   "--label", "<workflow-label>", "https://target.example"])`.
5. Keep the returned real `tN` and provide it inline on subsequent actions.

The backend repeats the list-and-reuse check atomically before executing
`tab new`. It refuses creation if the real tab list is unavailable or invalid,
rather than risking a duplicate. A label collision with a pre-existing tab at a
different URL is also an error. An arbitrary same-origin tab is deliberately
not reused because navigating it could destroy unrelated user state. URL query
parameters are hidden from model-facing tab lists, but the backend retains the
full normalized URL for exact-match decisions.

`tab new` arguments are parsed and rewritten into the canonical
`new --label <label> <absolute-url>` order before reaching agent-browser. This
prevents a misplaced URL or option from being interpreted as the page to open.

### Model-context behavior

Tab management is intentionally compact:

| Operation | Returned to the agent |
|---|---|
| Explicit `tab` list | At most 20 compact lines; labels, titles, and URLs are individually truncated. |
| Select one tab | A short selected-tab message, not the raw tab list. |
| Automatic active-tab verification before a page action | Nothing extra; the internal tab-state/selection response is discarded. |
| Atomic reuse check before `tab new` | Nothing extra; only the reused/created tab summary is returned. |

Consequently, a large Chrome window does not add every tab to context on every
browser action. Repeated explicit list calls can still accumulate in the
conversation history, so agents should list once, retain the returned `tN`, and
list again only when the tab disappears or the target is genuinely unknown.

### Ownership and cleanup

Only tabs actually created by a workflow are registered for automatic cleanup,
and ownership is recorded against the real `tN` ID returned by agent-browser.
A pre-existing tab reused by exact URL remains user-owned and is never enrolled
in cleanup.

After the final browser-owner lease is released, created tabs remain available
for review for one hour and are then closed by real `tN` ID. Concurrent runs
delay that timer until the final lease ends. Already-missing tabs, including
agent-browser's `No tab with label` response, are retired from the registry
instead of being retried forever. Never call the top-level browser `close` in
CDP mode because it can terminate the user's real Chrome session. Close a
specific workflow-owned tab immediately only when the user requests it or the
workflow must replace it.

## State and isolation

- CDP mode uses the user's real Chrome cookies and login state.
- Managed headless mode uses one browser per workflow or product project. Authorized callers share that scope; unrelated scopes are isolated. Scoped profiles persist by default; deployment services must use the same profile base. Tabs are optional; reuse the current tab or create one when useful.
- Shared CDP concurrency is isolated by real tab IDs plus a per-port
  select-and-act lock; labels are aliases, not durable tab identities.
- Delegated agents inherit the workflow/project browser. Explicit session labels do not create independent browsers. Preserve it at workflow completion. Configured CDP profiles retain their separate specialized login behavior.
- Workflow-created CDP tabs are closed automatically one hour after the final
  run releases its lease; reused user tabs are preserved.

Browser session tracking lives in `agent_go/pkg/browser`. MCP subprocess
connection pooling in `mcpagent` is independent of browser state.

### `file://` URLs are not path-restricted (deliberate, not an oversight)

This applies to configured direct CDP. The extension rejects `file://` URLs.

In CDP mode the browser is the user's **own** Chrome — a host process this app
neither owns nor sandboxes — so `agent_browser` can read any file on the
machine via a `file://` URL, including files the shell tool is explicitly
denied. SparkQuill's standalone server rejected any `file://` URL resolving
outside the workspace or host Downloads (`validateBrowserFileURLs` in its
`browser_tool.go`), verified against a real exploit: the shell tool refused a
decoy file outside the workspace while the browser read the same path's
contents straight back.

That server was deleted on 2026-09-06 and the platform's `agent_browser` has
no equivalent guard: a product's browser access is all-or-nothing
(`runtime.browser` in its `product.yaml`; SparkQuill's child profile sets it
to `disabled`, the parent profile to `preferred`). This gap was raised with
SparkQuill's owner on 2026-09-06 and left unrestored by their explicit choice
— they'd rather the model use its own judgment about which files to read than
have a hard path guard. Not a platform default recommendation for other
products; a single-user deployment's owner deciding what their own AI may
read on their own machine.

**Canonical reference:** [`docs/agent-execution-architecture.html`](../agent-execution-architecture.html)
§4 — the measured before/after, the full list of rejected spellings, and how
this relates to the shell and image-sub-agent boundaries. Keep the detail
there, not here.

## Debugging and evidence

Use agent-browser's managed diagnostic commands so they operate on the same tab
and session as the workflow:

- `network` for requests and HAR capture;
- `console` and `errors` for page diagnostics;
- `screenshot` for visual evidence;
- `record` for video evidence when the user or workflow explicitly requests it;
- `trace` and `profiler` for deeper debugging.

HAR and video artifacts may contain credentials, cookies, page content, or
personal data. Review them before sharing.

### Persistent browser artifact handoff

The agent-browser daemon may outlive the workflow process and therefore cannot
safely rely on that process's current directory or inherited FolderGuard. For a
named screenshot or recording, the managed adapter rewrites the browser output
to a unique file under `/tmp/agentworks-browser-artifacts`. The trusted
workspace server then validates that the staged file is regular, non-empty, of
the expected image/video type, and that the requested destination is covered by
the current request's write paths and is not blocked. It publishes the artifact
atomically into the workflow workspace and removes the staged source.

Screenshots are finalized in the same call. Video recording uses an
owner-and-session-scoped lease: `record start` stores the staged source and
`record stop` finalizes that exact source into the requested workspace path.
This handoff applies to both headless and CDP modes.

In CDP mode, agent-browser recording creates a fresh temporary browser context
and tab. The managed adapter diffs the real tab set, pins the workflow to the
new recording `tN`, rejects interactions until a fresh snapshot succeeds, and
routes stale original-tab arguments to the recorded context. `record stop`
closes the temporary tab and restores the original selection. Abandoned runs
are stopped and cleaned by delayed ownership cleanup so the shared CDP session
cannot remain stuck in an active recording.

## Recent failure findings and fixes

| Finding | User-visible symptom | Current fix |
|---|---|---|
| A tab label was sometimes stored as though it were a real tab ID. | Delayed cleanup called `tab close <label>`, failed, and tabs remained open. | Parse both direct `tab new` and tab-list JSON, persist the returned real `tN`, and treat missing-label errors as already cleaned. |
| Cached backend active-tab state was trusted between calls. | A page action could affect the wrong tab after Chrome changed externally. | Read the real active tab before every page action under the shared lock; select the resolved `tN` only when it is not already active. |
| The resolved `tN` was explicitly selected before every action even when already active. | Visible Chrome repeatedly stole macOS focus while the user typed in another app. | Preserve the current tab when the real state confirms it is already active; tab creation or a genuine tab change may still foreground Chrome once. |
| Agents could request `tab new` without a fresh reuse decision. | Repeated workflows accumulated duplicate tabs. | Perform an atomic owned-tab/exact-URL reuse check; fail closed when listing is unavailable. |
| Flexible or malformed `tab new` argument ordering could reach the CLI. | A new tab sometimes opened an unintended URL. | Validate an absolute URL and canonicalize the command before execution. |
| Raw tab output was suspected of entering context on every selection. | Concern about context growth with many Chrome tabs. | Return tab lists only for explicit list calls, cap them at 20 compact entries, and discard internal selection/reuse responses. |
| A persistent daemon resolved named evidence paths from stale sandbox state. | Named screenshots or recordings failed with path/`getcwd` errors. | Use the guarded staging-and-finalization artifact handoff described above. |
| Upload paths were forwarded unchanged while the command working directory pointed at the run Downloads folder. | Workspace-relative paths could resolve as `Downloads/Workflow/...`, and a daemon launched by an older step could not see a newly granted input folder. | Resolve and authorize upload sources in workspace-api, copy them into short-lived managed staging with the original basename, and remove staging after the command. |
| CSS ID selectors were not shell-quoted. | `upload #file path` was parsed by the shell as a comment and agent-browser reported missing arguments. | Treat `#`, backslashes, and home-prefix characters as shell-sensitive arguments and quote them. |
| Brokered output destinations were joined to the browser working directory. | A requested `Downloads/report.csv` could be published as `Downloads/Downloads/report.csv`. | Resolve brokered screenshot/video/download destinations once from the workspace root. |
| `record start` created a fresh context while selected-tab enforcement returned actions to the original tab. | A valid WebM recorded an idle page while the real reproduction happened outside the video. | Detect the new active `tN`, require a fresh snapshot, pin actions to it until stop, close it afterward, and fail closed if the handoff cannot be identified. |

## Live E2E contract

Run the real managed-browser contract with:

```bash
scripts/run-browser-e2e.sh
```

The test launches a dedicated temporary headless Chrome profile on a random CDP
port. It then exercises the production path from the managed executor, through
the real workspace `/api/execute` handler, into the installed agent-browser CLI
and Chrome. Two simulated workflow owners share that same CDP daemon and issue
overlapping requests. It verifies:

- exact-URL reuse does not create a duplicate or claim a user-owned tab;
- flexible input is canonicalized before a new tab opens;
- newly created tabs are tracked by their real `tN` IDs;
- changing Chrome's active tab externally cannot redirect the next managed
  action;
- selecting one tab returns a compact response rather than the all-tabs JSON;
- parallel page actions verify and, only when needed, switch onto each workflow's own `tN` tab;
- uploads from two newly granted, disjoint workspace trees cross an older daemon
  sandbox while preserving both filename and file content;
- cross-workflow upload reads and artifact writes are rejected by FolderGuard;
- parallel screenshots and explicit downloads are published only into each
  workflow's authorized evidence/Downloads folders and have valid content;
- video recording produces real WebM files, remains exclusive to one workflow
  at a time, rejects another workflow's start/stop calls, requires a fresh
  recording-context snapshot, routes stale original-tab actions to the new
  context, restores the original tab, and decodes the final frame to prove the
  visible test interaction was actually captured;
- shared reset is rejected while another workflow owns the CDP port;
- delayed cleanup closes each workflow's created tab independently and preserves
  both the other live workflow tab and the reused pre-existing user tab.

The test never attaches to the default port or normal Chrome profile. Override
Chrome discovery with `BROWSER_E2E_CHROME_BINARY=/path/to/chrome` when needed.

## File uploads and downloads

Use workspace-relative paths such as `Downloads/report.pdf` or
`Chats/output.csv`. Upload with the `upload` command. Browser downloads for a
workflow run are routed into its execution `Downloads` directory.

Upload paths are not passed directly to the persistent daemon. The workspace
server resolves each source against the workspace root (with a run working-dir
fallback for a bare filename), checks the current FolderGuard read grants,
rejects blocked paths and symlinks, and copies the file into short-lived managed
staging. The daemon receives that staged path with the original basename, and
the staging slot is removed as soon as the command finishes. This allows a
later workflow step to upload from its own authorized folder even if the daemon
was originally launched under a different step's sandbox.

There are two CDP download paths:

- A normal click in visible Chrome may place a file in the user's system
  Downloads folder. That folder is exposed read-only when explicitly granted;
  copy the required file into the run-scoped workspace before processing it.
- `agent_browser(command="download", args=[..., "<selector>",
  "<workspace-path>"])` is an explicit managed download. Its output is written
  to backend staging and atomically published into the requested authorized
  workspace path, like screenshots. It never writes through the persistent
  daemon directly into an arbitrary workspace folder.

## Operational rules

- Use snapshots and current refs for live actions. Re-snapshot after navigation,
  DOM updates, tab changes, or when ref freshness is uncertain.
- Persist durable selectors or parse fresh refs at runtime; never save a literal
  `@e1`-style ref as reusable configuration in a workflow script. Scoped read-only
  `eval` is a discovery fallback when snapshots are insufficient, not a required
  step. Verify locator uniqueness, intended context, and the action's outcome.
- Poll for page state instead of relying on long fixed sleeps.
- Never connect to the CDP WebSocket directly for normal actions; that bypasses
  tab locking and can race other workflows.
- On a local installation, a site rejecting managed headless browsing may
  require `cdp`; record that precondition in its learnings. A server with external
  CDP disabled cannot use that direct-connection workaround. A Code/Crew project or workflow may
  separately pair the user's browser through the extension, subject to its policy.

## Workflow authoring

### Authoring sequence

1. Load the installed command guide with
   `agent_browser(command="skills", args=["get", "core"])`.
2. Open or select the workflow's labeled tab.
3. Take an interactive snapshot.
4. Identify the intended control and act with its current ref or a verified locator.
5. Verify the expected state; re-snapshot after navigation or DOM updates,
   switching tabs, or when ref freshness is uncertain.
6. Save stable site knowledge to the workflow's learnings.

### Persisted scripts

Snapshot refs such as `@e1` are valid only for the current page state. A saved
script may resolve a fresh ref from the current snapshot by role, accessible
name, and surrounding context, or use a verified semantic locator or DOM hook.
Save this locating recipe, never a snapshot's literal ref as reusable config.
Runtime snapshots can remain as evidence. CSS discovery is not required for
every browser step.

Candidates include role plus accessible name, labels, test attributes,
hand-written semantic `id`/`name`, and `aria-label`. None guarantees stability:
a Like button may become Unlike. Scope repeated controls to their intended
row/card and require one intended actionable match; do not choose the first
match when the target is ambiguous. Verify the resulting page/business state
before recording success or retrying.

Avoid generated framework IDs, hashed class names, and `nth-child` chains.
When the accessibility snapshot is insufficient, use a scoped read-only `eval`
to inspect relevant DOM attributes. Do not dump the full page, read credentials,
or click/submit through a discovery probe. Treat any discovered locator as a
candidate to verify. The attached `agent-browser` skill's **Selector Discipline**
section is the shared authoring and learning contract.

## Live browser and manual control

The Browser panel discovers managed browsers after `agent_browser open` runs.
Select a session to watch its active tab; settings are behind the gear button.
Opening the panel currently does not launch a browser. Closed/reaped sessions
disappear. Browser viewing is available through the shared workflow/project
components, subject to product access rules.

Watch mode cannot send input. Take control requires workspace write access and
an exclusive lease. Browser commands wait while manual control is held; unrelated
workflow work can continue. Return control or Escape releases the lease. Closing
the viewer, switching sessions or losing its connection also releases it.

### Sessions and tabs

A workflow/project resolves to one managed browser, which can have multiple
tabs. Discovery may also include isolated Playwright test sessions. The session
selector chooses the tracked browser to watch. The tab strip
shows that session's tabs, with the active tab highlighted. The viewer displays
one active tab at a time; it does not display all tabs simultaneously.

Normal browser commands update the existing session's live view automatically.
There is no separate viewer initialization command for each action. Session
discovery refreshes every five seconds while the panel is mounted. Live frames
arrive over WebSocket, with the proxy requesting a maximum of 10 frames per second.

Watching follows the agent's active tab. Switching tabs changes the actual
browser's active tab, so tab selection is enabled only after taking control.
Users with control can click, type, scroll, and select another tab. Escape returns
control to the agent. Manual control holds browser commands for that session;
it does not pause the entire workflow or unrelated sessions.

### Architecture

```text
Workflow Browser tab
    │ authenticated WebSocket through the app's existing HTTPS endpoint
    ▼
Agent API
    │ checks session owner and workflow; enforces watch/control mode
    │ connects using WORKSPACE_API_URL and WORKSPACE_API_TOKEN
    ▼
Workspace service (same host/container environment as agent-browser)
    │ resolves the session's .stream file to a loopback port
    ▼
agent-browser stream → managed headless Chrome
```

| Endpoint | Purpose |
| --- | --- |
| `GET /api/browser/live/sessions?workspace_path=Workflow/<folder>` | List authorized tracked browser sessions for the selected workspace. |
| `GET /api/browser/live/{session}/stream?workspace_path=Workflow/<folder>` | Authenticated viewer WebSocket on the agent API. |
| `GET /api/browser/live/:session/stream` | Internal workspace-service proxy to the local session stream. |

The frontend sends a heartbeat every 10 seconds. The server releases manual
control after a disconnect or 45 seconds without an incoming message. Only one
viewer connection can hold control of a session at a time. Other connections
can continue watching.

### Viewer controls

The browser header contains control, **Teach task**, expand and overflow actions
with the existing neutral buttons. A separate tab strip contains per-tab close
buttons and **+** for a blank tab. Back, forward and reload sit next to one
address field; Enter opens an HTTP(S) website (bare hostnames use HTTPS).
**Start browser** appears in the idle header and hides once the browser is running.
Selecting a tab can acquire exclusive control; navigation, creating/closing tabs
and clipboard actions require that control. A busy controller is reported.

After taking control, select a field and paste with Cmd+V / Ctrl+V. Select text
and copy with Cmd+C / Ctrl+C; the viewport's right-click menu also offers Copy
and Paste. Mac editing shortcuts map to the remote platform. Clipboard transfer
is plain text, limited to 64 KiB per paste/selection, and never reads the server's
OS clipboard or saves clipboard contents to disk. Multiline Unicode text is
inserted into the existing browser's focused field through private daemon IPC;
no browser launches or endpoint/executable selection are exposed by that route.
Copy reads selection in the active page, open shadow roots and same-origin frames.
Cross-origin frame selection is currently unsupported; paste uses the browser's
focused field and remains available. Clipboard permissions/secure context are
required by the viewer's host browser. Empty selections leave the local clipboard
unchanged. Watch-only users cannot send clipboard or navigation actions.

**Fill width** uses the panel width with vertical scrolling; **Fit page** keeps
the whole viewport visible. Browser is a workspace view (the toolbar group is
currently Pulse), not a Setup page; mode/connection settings remain behind its
gear button. Implementation and qualification: [PLAT-382](../bugs/pulse_platform/browser/browser/plat-382.md).

### Runtime mode changes

The RTS UI check exposed a missing-tool bug when a chat started with **No
browser** and was later changed to **Automatic**. Persistent CLI turns reused
the original tool registration. The follow-up release keeps the workflow
browser tool registered and reads the current manifest on each invocation.
Disabled, missing, or unreadable configuration cannot launch a browser. The
regression test covers enabling and disabling the same tool instance without
creating another chat. Shared fix: `961d22b5a`.

### Builder view switching

Interactive Builder guidance requests `open_workspace_view(view="browser")`
when beginning browser navigation for the user. The tool opens the full browser
panel; the stream updates automatically, without repeated refresh requests.
Scheduled and unattended runs do not manipulate the foreground UI. The Builder
should respect subsequent user view changes.

When a builder workspace-view action changes the visible panel, a small toast
identifies it, for example “Builder opened Browser”. Acknowledged UI actions
notify only after an applied result. Reopening the same visible view or
refreshing it does not create another switch notification.

## Persistent managed profiles

Scoped workflow/project profiles persist by default beneath
`AGENT_BROWSER_PROFILE_ROOT`, or `<OS config>/agentworks/browser-profile` when
unset. Set the existing `AGENT_BROWSER_SHARED_PROFILE` to an absolute dedicated
base outside release folders to retain that deployment layout, for example
`/data/video-studio/browser-profile`. Explicitly empty shared-profile configuration
opts out of persistence. A filesystem root or relative path is rejected.

In persistent headless mode, all agent session names within one workflow map to
that workflow's `workflow-<hash>--browser` identity. Authorized users share its
logged-in accounts; write access is still required for input and recording.
Workflows use directories beneath
`<configured-profile>-workflows/`; projects use
`<configured-profile>-projects/`. Legacy fallback identities may use
`<configured-profile>-users/`. Browser actions share a
per-workflow control lock, and workflow completion preserves the profile. Agents
should inspect existing tabs and avoid reset/close or clearing storage without a
user request.

The shared launch settings are defined once in `workspace/browserconfig` and
used by automation, viewer tab controls, recording, and the browser supervisor.
Shared mode keeps Chrome's native Linux/version user agent, `en-US`, a default
1280x720 viewport, and UTC timezone. It retains the existing AutomationControlled
flag. No third-party stealth plugin is installed; detection avoidance is not
guaranteed. Browser upgrades may change the fingerprint. Existing isolated
profiles are not merged into the new shared profile.

The legacy dedicated/shared-browser deployment can install the user unit
`deploy/aws-ec2/server/video-studio-browser.service` and
run `video-studio-browser` from the release's `bin` directory. It supervises
Chrome separately from the agent/workspace services and gracefully closes it
on service stop. Enable the unit for the user's default target to restart it
on boot. The rootless release build includes the supervisor binary.
The profile directory must remain on persistent storage across deployments.
Cookies and local storage were verified across a real Chrome restart; sites
can still expire sessions or require MFA. This is login-session persistence,
not a separately configured password manager.

Do not use a global shared-browser profile as the default for new workflow/project
launches. A dedicated legacy supervisor is distinct from per-scope browser startup.
Profiles from older per-chat/global browsers are not automatically merged. The
prior global “all users” rollout does not define current ownership.

## Diagnostic recording (existing)

Use **Start recording** / **Stop recording** in the viewer for the same
bundle. Recording requires write access and runs on the server even if the
viewer disconnects. It preserves the existing browser and sign-ins. This is
diagnostic evidence capture; Teach adds structured actions
and learning on top of browser access, without redefining diagnostic recording.

The existing `workspace_browser.agent_browser` tool supports Builder's bundled
`capture` command in managed headless mode (ephemeral or persistent profiles):

```json
{"command":"capture","args":["status"],"session":"main"}
{"command":"capture","args":["start"],"session":"main"}
{"command":"capture","args":["stop"],"session":"main"}
```

Ask the agent, for example: “Record the browser, network and console while you
reproduce this issue, then save the recording.” The browser must already be
running with the intended page selected. The agent uses the same workspace
recording endpoint as the UI, so the Browser view's existing status polling
reflects chat start/stop operations. No new tool or CLI binary is needed.

The managed handler derives the owning workflow from trusted session settings
and sends its current folder permissions to the workspace service. Output normally
lands in `Workflow/<name>/browser-recordings/<capture-id>/`; a step with narrower
write access uses its authorized working directory's `browser-recordings/`.
The response supplies the actual directory and files. Read-only sessions may
inspect authorized recording status but cannot start or stop a capture. Another
workflow cannot stop an active recording or retrieve its paths. A completed
recording does not prevent another workflow from starting a new capture.

Start begins video and HAR capture and clears console/error buffers. Stop exports
`video.webm`, `network.har`, `console.json`, `errors.json`, `manifest.json`, and
`capture.zip`. HAR response bodies are excluded. Console/error output is a
buffer export, not an unlimited log stream. Selected-tab video and recovery
semantics are described below.

Start/stop are idempotent. After a timeout, query status before retrying. A partial
stop returns errors and may leave `recording=true`; inspect the result and retry
stop when necessary. Stop captures started for the task even if reproduction
fails, but do not automatically stop a recording that was already running. Never
mix a bundled capture with separate native `record`/HAR start/stop commands.
Stopping capture preserves the browser and sign-ins.

This extension belongs to Builder's handler and is documented in the tool schema,
Builder browser skill, and browser guidance. Upstream `agent-browser skills` does
not define it. Native `record` remains video-only; external CDP currently uses
its existing separate video/HAR/console commands instead of bundled `capture`.

### Ownership and capture recovery

Workflow/agent cleanup preserves workflow browsers. Idle reaping still applies when no
capture is active. Browser commands and manual control share a per-browser lock;
this serializes individual actions, not an entire snapshot-to-click conversation.
Agents still need fresh snapshots and coordination for concurrent tasks. A capture
started by a tool pins the browser to its owning root run until stopped; viewer-started
capture is workflow-scoped. Manual user control remains available.

Bundled user-browser capture encodes the selected-tab WebSocket stream with ffmpeg,
so tab switches appear in the same video. Native `record start` remains a separate,
single-target command; don't mix it with bundled capture. Background tabs are not
recorded simultaneously. The manifest records browser identity, owner session,
source, timestamps, frame count and validation. `validation=passed` means nonblank
frames and a decodable video, not proof that the workflow achieved its goal; inspect
the actual footage before claiming success. Never substitute an earlier run's video.
HAR/console/error exports retain the upstream CLI's scope and may be partial.

An encoder or browser disconnect marks the capture interrupted on the next status,
stop or start call. A service restart cannot revive a cached active marker. Starting
again creates a fresh directory/file and discards abandoned HAR state. ffmpeg is
required in the workspace service PATH. Deploy both backend services for this change.

Regression coverage: common user/alias/child routing, group-to-builder identity,
profile isolation, viewer authorization, cleanup and capture ownership, real ffmpeg
encoding across a simulated selected-tab change, blank footage, disconnects and stale
capture retries. A live smoke test is required after deployment; the old rollout
status is not a current deployment inventory.

## Synthetic microphone and camera

Managed headless browser launches (including the shared supervisor, tool commands,
UI tab controls and recording) include these Chrome flags through the central
`workspace/browserconfig.HeadlessArgs()` helper:

- `--use-fake-device-for-media-stream`
- `--use-fake-ui-for-media-stream`

These provide synthetic media devices and automatic media permission handling,
so server-side flows that require a microphone can obtain a stream. They do not
supply the user's voice or meaningful spoken dialogue. External CDP Chrome keeps
its own launch configuration. An already running Chrome needs one graceful
restart to apply the flags; the persistent profile is retained, while in-memory
page state and ongoing calls may need to be resumed.

The Builder agent-browser skill explains how to verify getUserMedia and the
application outcome without switching to an unrelated Playwright harness. A
microphone permission success alone is not evidence that an RTS simulation started.

References: [agent-browser launch options](https://agent-browser.dev/configuration),
[Chromium media switches](https://chromium.googlesource.com/chromium/src/+/main/media/base/media_switches.cc).

## Playwright test browsers

JavaScript/TypeScript tests can import `test` and `expect` from the local
`@agentworks/playwright` package instead of `@playwright/test`. See
[fixture installation and usage](../../packages/playwright/README.md). Existing
custom fixtures can call `attachLiveBrowser(context)` explicitly. Ordinary
Playwright tests do not register themselves. Python sync/async and pytest suites use
[the Python fixture/helper](../../packages/playwright-python/README.md). Both packages
are distributed from the release through authenticated session package endpoints.

The fixture connects to `/s/{session_id}/tools/browser/live` using the runner's
existing MCP bearer credentials. The agent API derives user/workflow ownership
from that server-owned active session and allocates a separate `pw-` session for
each test context. The producer sends Chromium JPEG frames and page metadata,
with no browser command channel and no public CDP port. Producer disconnect or
45 seconds without heartbeat removes its discovery entry and closes viewers.

The existing authenticated session-list/viewer endpoints include these sessions
alongside agent-browser, including deployments using shared Chrome. Viewers see
only their own workflow's test sessions. Control and agent-browser recording
routes are rejected for these sessions on the server; the frontend also hides
those controls. Playwright owns isolated contexts, teardown, and video artifacts
in its test report. No automatic upload into a custom dashboard is performed.

Validation: `AGENTWORKS_PLAYWRIGHT_LIVE_TEST=1 go -C agent_go test ./cmd/server
-run '^TestPlaywrightFixtureLive$' -count=1` runs a real Chromium test against an
isolated local publisher/viewer server.

## Deployment and verification

This uses the shared agent API and workspace service, so the same implementation
works in native/rootless and Docker deployments. Deploy both backend
binaries and the frontend together. Install a streaming-capable agent-browser in
the workspace service's environment; tested with 0.37.0 and headless Chrome.
Existing native installations may need an agent-browser upgrade; presence-only
install checks do not upgrade an older binary. New images install the current
agent-browser package as before.

No dashboard daemon, public browser port, configured CDP endpoint, or local
Chrome is required. `AGENT_BROWSER_CDP_ENABLED=false` remains supported. The
headless browser uses its own internal browser protocol; that is independent of
the app's shared-CDP mode.

The browser connects to `/api/browser/live/{session}/stream` on the normal app
API with the existing login. The agent API checks the workflow and session owner,
then connects to `/api/browser/live/:session/stream` on `WORKSPACE_API_URL`,
carrying `WORKSPACE_API_TOKEN`. The workspace service resolves the session's local
`.stream` metadata and proxies to loopback. This keeps working when the agent API
and workspace service are in separate containers. Reverse proxies must forward
WebSocket upgrades on `/api/` (as for the existing live terminals); ingress must
allow an idle timeout longer than 45 seconds. No session port is published.

The generic workspace proxy rejects this internal stream route. The live viewer
only forwards viewport, tab, URL and connection status messages, and accepts
input only while its connection holds control. Shared desktop/CDP sessions are
not listed; they can contain tabs belonging to other workflows. Discovery exposes only browsers in workspaces the caller
can access.

### Troubleshooting

| Symptom | Check |
| --- | --- |
| No browser sessions appear | Use Start browser or let an enabled agent open it. Confirm that the caller can access the selected workspace and its browser identity. A closed session is not a replayable recording; use the saved artifacts. |
| Session appears but live view cannot connect | Check the agent-browser version, its session `.stream` metadata, and whether streaming is enabled in the workspace service's runtime environment. |
| WebSocket connection fails | Check the existing HTTPS proxy's upgrade forwarding, `WORKSPACE_API_URL`, and matching workspace service tokens. Use Reconnect after correcting the issue. |
| Take control reports busy | Let the current browser action finish, or have the existing controller return control, then retry. |
| Cannot switch tabs while watching | Take control first; switching tabs affects the browser the agent is using. |
| Take control is unavailable or denied | Confirm workflow write access. Session visibility alone does not grant control. |

### Implementation files

- [Extension worker](../../extensions/agentworks-chrome/background.js): shared target authorization, debugger transport, groups and foreground permission.
- [Extension popup](../../extensions/agentworks-chrome/popup.html): connection and explicit sharing controls.
- [Extension management API](../../agent_go/cmd/server/browser_extension.go): pairing, stable codes and authenticated selection.
- [Private relay](../../agent_go/pkg/browserrelay/relay.go) and [diagnostics](../../agent_go/pkg/browserrelay/diagnostics.go): capability transport, serialized controller ownership and per-target logs.
- [Extension executor](../../agent_go/pkg/browser/extension_executor.go): managed tool routing and guarded CLI execution.
- [Browser workspace panel](../../frontend/src/components/workflow/BrowserWorkspacePanel.tsx) and [connection UI](../../frontend/src/components/workflow/ChromeExtensionConnection.tsx): explicit methods and selected extension experience.
- [Browser toolbar status](../../frontend/src/hooks/useBrowserToolbarConnection.ts): read-only connection health without chat messages.
- [WorkflowLiveBrowser.tsx](../../frontend/src/components/workflow/WorkflowLiveBrowser.tsx): session discovery, viewport, tab strip, input, and connection lifecycle.
- [WorkflowCapabilitiesPanel.tsx](../../frontend/src/components/workflow/WorkflowCapabilitiesPanel.tsx): embeds the viewer above browser settings.
- [BrowserTeachingPanel.tsx](../../frontend/src/components/workflow/BrowserTeachingPanel.tsx): demonstration controls, draft review, test and publication.
- [browser_workspace.go](../../agent_go/cmd/server/browser_workspace.go): authorized startup, canonical settings and teaching proxy.
- [browser_teaching.go](../../workspace/handlers/browser_teaching.go): scoped artifacts, managed replay and tested publication.
- [recorder.go](../../workspace/browserteach/recorder.go): private CDP attachment and structured capture.
- [Agent API browser_live.go](../../agent_go/cmd/server/browser_live.go): workflow-scoped discovery, authenticated stream relay, and manual control.
- [live_control.go](../../agent_go/pkg/browser/live_control.go): exclusive control gate shared with managed browser commands.
- [executor.go](../../agent_go/pkg/browser/executor.go): waits on the control gate before running headless browser commands.
- [Workspace browser_live.go](../../workspace/handlers/browser_live.go): resolves local stream metadata and proxies within the workspace environment.
- [workspace_proxy.go](../../agent_go/cmd/server/workspace_proxy.go): prevents bypassing viewer access checks through the generic workspace proxy.

### Existing verification commands

- `cd workspace && go test ./handlers -run TestBrowserLive`
- `cd agent_go && go test ./pkg/browser -run TestBrowserControl`
- `cd agent_go && go test ./cmd/server -run TestLiveBrowser`
- `cd agent_go && RUN_LIVE_BROWSER_E2E=1 go test ./cmd/server -run TestLiveBrowserRealHeadless`

The opt-in integration test launches and closes its own headless browser and
checks live frames, tabs, mouse focus and typing through the workspace proxy.

Frontend validation: `cd frontend && npm run build`.

Historical validation (2026-09-09, agent-browser 0.37.0): real headless
frames, tab discovery, mouse focus, typing, workflow isolation, watch-mode input
blocking, exclusive control, and disconnect recovery. Backend race checks and
desktop/mobile UI checks also passed. Container topology support comes from
routing through the workspace service; it has not been verified by a live
rollout to each deployment.

## Open ownership and cleanup review

The following mechanisms were verified by source review on 2026-10-05; no live
exploit reproduction or runtime fix is claimed. The account token/extension
rollout does not resolve these direct-CDP/managed-runtime issues.

| Issue | Scope and remaining work |
| --- | --- |
| [PLAT-520](../bugs/pulse_platform/browser/browser/plat-520.md) | Direct-CDP listing, known-tab selection and exact-URL reuse currently expose the configured browser; ownership is bookkeeping, not target authorization. Define/enforce the complete boundary and redact label conflicts. |
| [PLAT-521](../bugs/pulse_platform/browser/browser/plat-521.md) | Global managed-browser eviction can stop an unrelated idle session; reject or reclaim only an authorized victim. |
| [PLAT-522](../bugs/pulse_platform/browser/browser/plat-522.md) | Force cleanup trusts persisted PIDs; validate process identity and reject special/system PIDs before signaling. |
| [PLAT-523](../bugs/pulse_platform/browser/browser/plat-523.md) | Text-based dead-session classification can reset a healthy runtime; corroborate transport failures with execution-host health. |

The extension uses a separate account/project relay and shares only that project's
authorized tabs. Direct-CDP locks prevent simultaneous commands from racing;
they do not prevent a caller from selecting another listed tab on the same port.

## Source map and historical rollout notes

- [Conversation ownership resolver](../../agent_go/cmd/server/browser_conversation_isolation.go).
- [Workflow browser runtime](../../agent_go/cmd/server/workflow_browser_runtime.go).
- [Product capability runtime](../../agent_go/cmd/server/agent_profile_runtime.go).
- [Launch/profile configuration](../../workspace/browserconfig/launch.go).
- [Work browser settings](../../frontend/src/products/work/WorkWorkspacePane.tsx).
- [Workflow learning](../workflow/learning_architecture.md).

The former live guide contained RTS releases and rollout observations from
2026-09-09 (`0b9593dd0`, `504c35a5e`, `34a9b1d17`, `66c9a7e07`), including a
legacy global shared-profile deployment and then-pending Dominion/Confida work.
Those are historical evidence in Git history, not statements of current server
status. Deployment work must inspect the actual host and verify the current
per-workspace identity, streaming/control and recording before reporting success.
