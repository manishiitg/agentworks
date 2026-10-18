# Workspace navigation and return

Returning people open their last accessible project, chat and panel layout. There is no Home dashboard or Home navigation item. `/home` remains an alias for `/`. Explicit project links, panel links and browser Back/Forward take precedence over saved preferences.

The first-use welcome appears only when the account has no existing work. The server chooses an available project or product destination; the browser does not fetch the attention feed to make that choice.

## Shared navigation

Goals, Crew (including the former Code storage origin) and Flows use the same existing header and product switcher. Branding, project name, search and account controls keep one navigation owner. Setup stays at the bottom of the chat sidebar. Product visibility follows installation capabilities and the person's current access.

The left sidebar groups open, running and recent chats beneath their projects. Product icons identify project groups; chat, clock and branch icons distinguish conversations, scheduled runs and steps. Clicking does not reorder work. Running chats have a spinner; questions waiting for a person have an indicator. Useful source labels such as Schedule, Slack and WhatsApp distinguish external conversations; the default conversation has no repeated Main chat subtitle. Unnamed conversations use Chat or Chat N. Custom titles remain intact.

Closing an execution tab closes its display without stopping the run. Permanent project chats remain protected, and active side chats must finish or stop before closing. Account, server and session identity are rechecked after asynchronous navigation or cleanup.

Goal step inspections appear beneath their exact owning chat or run. Server receipts require current project access and exact parent-session ownership; child-session receipts are excluded. Opening the inspector independently checks authorization again. Internal agents do not become unrelated top-level chats.

Goal activity is available through the Goals workspace menu and search. A compact Needs you signal in the existing Goals header opens pending/unapplied decisions. It is not an Activity entry in the global chat sidebar.

## Right context card

The card stays available for everyday work and shortcuts. Expanded panels use the existing header's back icon to return to it. Product settings stay in the three-dot menu.

| Area | Goals | Crew | Flows |
| --- | --- | --- | --- |
| Results | Dashboard and report documents | Dashboard | Dashboard and recorded runs |
| Work | Plan, schedules and triggers | Existing schedules, triggers and functions | Flow, API triggers and condition triggers |
| Review | Pulse, decisions and activity | Existing suggestions and history | Run logs and costs |
| Tools | Existing knowledge, browser and files | Existing memory, browser, files and admitted development tools | Existing files and history |

The table describes navigation, not new permissions. Product-specific features and current resource roles determine which entries appear. Crew does not gain development tools merely because the shared component supports them; former Code projects retain their existing features. More tools and search keep admitted destinations discoverable.

## Connections and Use & share

**Connections** contains resources an agent consumes: MCPs, Secrets, Skills, admitted Vault/Brain references, folders/workflow references, Google accounts and the personal Slack sending account. Its landing view shows connected resources. Add connection exposes the admitted choices, with Brain and Vault first when available. Selecting a resource opens its original management panel.

**Use & share** contains the existing Share control, dashboard access and supported incoming channels. Share uses **Only me / Chosen people / Everyone**, with server authorization. People who can use a shared project can see the files/results that its existing rules permit; secret values and private owner conversations stay protected.

Goals and Crew retain supported Slack app and WhatsApp routes. Former Code projects retain their own-bot, one-to-one Slack restriction. Flows expose API/MCP access without gaining unsupported incoming bot controls. Project-only MCP setup requires the recipient's own sign-in and existing project access; see [project MCP connections](project-mcp-connections.md).

Existing section IDs and search/deep links remain valid. Grouping a destination does not widen its permission or replace its native implementation.

## Personal shortcuts

`GET/PUT /api/workspace-shortcuts/{product}` stores ordered preferences per signed-in person and product. Valid product keys are `agentworks`, `relays`, `work` and `code`. The request cannot select another account. Preferences are presentation only; product, feature and resource checks still decide whether a panel may open.

A revision conflict returns 409. Unknown/unavailable entries and an intentionally empty layout are retained. Explicit inheritance supports the Crew/Code consolidation without inferring a merge from a disabled product or rewriting preferences on page load. Older MCP shortcut aliases continue to resolve through Connections.

Corrupt, oversized or unsupported shortcut records return defaults instead of permanently blocking the editor. A subsequent valid save replaces the damaged preference. Storage transport errors remain errors. Writes serialize per person/product rather than blocking unrelated accounts.

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
