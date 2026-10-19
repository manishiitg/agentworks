# Workspace navigation and return

Returning people open their last accessible project, chat and panel layout. There is no Home dashboard or Home navigation item. `/home` remains an alias for `/`. Explicit project links, panel links and browser Back/Forward take precedence over saved preferences.

The first-use welcome appears only when the account has no existing work. The server chooses an available project or product destination; the browser does not fetch the attention feed to make that choice.

## Shared navigation

Goals, Crew (including the former Code storage origin) and Flows use the same existing header and product switcher. Branding, project name, search and account controls keep one navigation owner. Setup stays at the bottom of the chat sidebar. Product visibility follows installation capabilities and the person's current access.

The left sidebar groups open, running and recent chats beneath their projects. Product icons identify project groups; chat, clock and branch icons distinguish conversations, scheduled runs and steps. Clicking does not reorder work. Running chats have a spinner; questions waiting for a person have an indicator. Useful source labels such as Schedule, Slack and WhatsApp distinguish external conversations; the default conversation has no repeated Main chat subtitle. Unnamed conversations use Chat or Chat N. Custom titles remain intact.

Closing an execution tab closes its display without stopping the run. Permanent project chats remain protected, and active side chats must finish or stop before closing. Account, server and session identity are rechecked after asynchronous navigation or cleanup.

Goal step inspections appear beneath their exact owning chat or run. Server receipts require current project access and exact parent-session ownership; child-session receipts are excluded. Opening the inspector independently checks authorization again. Internal agents do not become unrelated top-level chats.

Goal activity is available through the Goals workspace menu and search. A compact Needs you signal in the existing Goals header opens pending/unapplied decisions. It is not an Activity entry in the global chat sidebar.

## Right project menu

Crew, Goals and Relays share one menu: **Use & share**, **Connections**, then
**Ops**, collapsed by default. Expanded panels use the header's back icon to
return. Only the menu contents scroll; search stays visible. There is no
More tools menu, duplicate toolbar switcher or personal shortcut editor/API.

Use & share holds Dashboard and every report document, Share, Browser where
supported, Automations (Schedules, Watchers, Webhooks, Functions) and channels
(Slack app, WhatsApp, MCP access). Relays retain their existing Webhooks and
Watchers inventory and do not gain bot channels.

Connections holds admitted Brain, Vault & secrets, MCPs, Google apps, Slack
account, folders/workflow references and Crew's agents from your computer.
Skills and chat history live in Ops. Crew's Memory & skills contains both
project-authored skills and the existing install/selection manager. Ops also
holds the existing plan, files, usage and Settings destinations, with each
product's original availability. Goals keep Publish and Notify there. Goal
Pulse appears above the groups only when ON; when OFF, Ops links to its
existing settings screen so the owner can turn it on. Opening settings does
not activate Pulse or send it a message.

Share remains **Only me / Chosen people / Everyone**, controlled by the server.
Run people have chat and dashboard only. All links retain their original
server feature, account and resource checks; the browser adds no new roles.
The UI-control binding returns the contract for that authorized chat's product,
so Crew-only views are not mixed into the Goal contract.

## Server-owned workspace restore

- `GET /api/workspace-state` returns validated last/recent preferences, a server-resolved `destination`, and `first_visit`. Removed access clears the saved destination and supplies a plain explanation.
- `PUT /api/workspace-state` accepts a version, revision and last selection. It validates the selected project without scanning attention or old transcripts. Stored chat preferences contain only session selection, display name and transcript display mode.
- On GET, current project access, exact chat ownership and workspace binding are checked. Conversation mode, schedule/bot origin and execution flags come from the active server session or saved history. Crew conversation keys come from the server registry. Browser-supplied authority flags are ignored.
- `GET /api/workspace-access` provides the current project capability used for connection editing controls. Storage path prefixes and the account role alone cannot decide project editing access.

The state file contains no transcript, prompt or credential. It stores project/view selection, session identity, personal display preferences, pane visibility, split ratio, preview device, focus and navigation mode, plus bounded recent selections and a revision. The workspace storage API provides atomic replacement. Corrupt state recovers to an available destination; unavailable storage does not masquerade as an empty account.

Writes debounce settled layout changes, flush when the document is hidden, and use keepalive on page exit. Abrupt failure before an acknowledged save can lose the latest movement. The last completed workspace save wins across browser tabs; the server advances the revision and rechecks project access for every save. Locks are per person; only restore loads the selected transcript, and recent entries are project-checked without reopening every old chat. An empty Crew chat can restore from its current server-owned conversation slot before its first transcript exists. The frontend cache is scoped to account and connected server and cannot authorize a destination.

## One attention projection

MCP `needs_you action=list` combines live questions, pending/unapplied decisions and the latest failed/blocked run to review. A later successful run summary clears an earlier failure. Questions and decisions retain their existing answer/apply permissions. Run-review entries point to execution logs and cannot be answered as decisions. The duplicate Home MCP tool is removed.

## Dashboards and service products

Empty dashboards offer a labelled local sample of live charts, forms/actions and stories/media. Samples do not query business data, call models or execute functions. Creation actions prepare an unsent draft and preserve existing draft text. Check again remains available, and Share remains subject to its existing server gate even before an authored dashboard exists.

When a shared Crew dashboard is read, the server can provide the existing platform default from the project's declared purpose and published functions. The browser does not issue a separate create-default command. Current role checks still determine authoring and sharing.

Brain opens its Library and Vault opens connected MCP management. Their existing controls stay in the main panel; the right card holds shortcuts. Assistant chat opens only when requested, preserving history and drafts when closed. Failed setup offers retry; account or permission changes discard late responses. Viewing access never exposes mutation controls or secret values.

## Verification

Regression checks cover user/server isolation, stale revisions, corrupt preference recovery, revoked project access, foreign sessions, forged restore flags, server registry bindings, product capabilities, scoped inspection receipts, attention visibility, channel restrictions, shortcut migration and draft retention. Browser checks exercise the real shared header, sidebar and context panels without requiring model calls or business workflow execution.
