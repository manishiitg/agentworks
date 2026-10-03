# CapLayer UI review — 2026-09-30

Current behavior and remaining limits: [Vault implementation status](../design/vault-current-state.md), updated 2026-10-03. Entries below are chronological; later fixes supersede earlier findings.

## Scope

Reviewed CapLayer against Goals, Crew and Code after the repeated reports of inconsistent UI. Main was merged at `03278a7e3`, incorporating `origin/main` at `893aead81`. This report supersedes the earlier primitive-only UI review.

## Root cause

The earlier implementation reused individual presentation primitives while retaining a separate shell, chat state, message submission API and header composition. It also selected a composer variant different from Crew/Code. Fixing individual styles could not provide the shared runtime controls or consistent behavior.

## Implemented correction

| Area | Current implementation |
| --- | --- |
| Application startup and authentication | `caplayer-main.tsx` selects CapLayer and boots the actual shared `main.tsx` application. Existing accounts, theme, product access and routing are used. There is no separate CapLayer login. |
| Header | Actual `ModePresetBar`, product picker, provider/account controls and global page overlays. Gateway sections appear only in the right workspace toolbar. |
| Workspace shell | New `ProductWorkspaceShell` owns the complete pane layout, toolbar strip, Chat tab placement and edge reopening controls. Crew/Code's `WorkSurface` and CapLayer both render it. |
| Chat | Actual `ChatArea`, `ChatInput` and `AgentWorksChatTabItem`. CapLayer uses the same default composer and runtime controls as Crew. The custom setup chat component and its transport are removed from the frontend. |
| Conversation | Registered `caplayer` agent profile with the shared provider catalog and durable singleton conversation API. History restoration and New conversation use the existing product APIs. |
| Models | Right-side Connect panel uses shared provider/model services and `ModelReasoningControl`. No extra model/new-chat buttons are composed into the input. |
| Panels | Gateway-specific content remains on the right, using shared `WorkspaceViewHeader`, toolbar buttons, settings cards and controls. People contains Users/Groups subtabs; Users renders the central product editor. |
| Typing performance | The parent subscribes to session ID and streaming state, rather than the entire chat tab. Draft edits do not rerender all gateway panels. |

## Agent and access boundary

The CapLayer profile embeds its system prompt and `caplayer-access` skill. Its native tool `manage_caplayer_access` can inspect inventory, inspect an exact tool schema, or save a validated draft. The product server rechecks administrator status and product access on every invocation, takes the actor from the authenticated runtime, and calls a fixed gateway service using server credentials.

The tool endpoint rejects unsupported operations, non-object arguments, unknown JSON fields and trailing payloads. Drafts record the trusted actor. Publishing, revocation, group membership, credential handling and upstream execution are excluded from the agent tool. Publishing remains an explicit action in the review UI. Exact schema fingerprints and deterministic argument conditions remain enforced by the gateway.

Generic workspace-memory instructions are omitted for narrow profiles without file tools; other profiles retain their existing memory instructions.

## Verification

### Automated

- `npm run build:caplayer`: TypeScript and production build passed.
- Focused frontend suite: **13 files / 86 tests passed**, covering gateway panels/API, shared shell use, Work toolbar markers/layout, Chat tab, clipboard and account behavior.
- Product server/profile regression tests: passed for CapLayer, profile contracts and the single-user administrator case.
- `go test ./...` in `mcp-gateway`: passed, including the protected draft endpoint and trusted actor attribution.
- `git diff --check`: passed.
- A broader product-server prompt/profile selection still fails `TestAssembledWorkflowPrompt` in two Workshop cases: 24,768 and 24,519 bytes against a 24,000-byte ceiling. That existing workflow context leaves `WorkspaceFilesDisabled` false, so the new applicability condition does not change its output. This is not reported as a clean full-product suite pass.

### Browser

Preview: `http://127.0.0.1:18162/caplayer.html`, with isolated shared product/workspace services and gateway state.

- Compared the actual rendered Crew workspace with CapLayer: same header controls, Chat tab, composer, commands, attachment, microphone, live view and send controls. Code's shared header/product switch was also checked; the isolated instance has no Code workspace, so no Code conversation was replayed.
- Submitted an inventory-only request through the actual shared composer using the local Claude runtime (`claude-sonnet-5-5`). The agent read its skill, discovered its native tool, inspected the gateway and completed its response. Inventory was empty; no draft or policy was created.
- The real transcript showed tool activity, streaming state, stop controls and return to Ready. A subsequent user message also completed through that same runtime.
- The browser remains open for user testing. No external connector permissions were changed.

Earlier navigation, regex PII, endpoint-probe, collapse/reopen, simulation invalidation and clipboard checks remain covered by the focused tests and preceding local review. A new phone walkthrough and voice/file submission end-to-end run were not performed after this architectural replacement.

## Remaining risks and boundaries

1. The standalone entry now imports the full shared application. Its eager main JS is approximately **3.37 MB / 994 kB gzip**. Reusing the application fixes consistency; cold-load bundle optimization remains separate work.
2. Gateway governance state/audit and upstream OAuth persistence retain the documented alpha limitations. A restart of this isolated gateway clears its in-memory inventory.
3. Shared management-console accounts do not complete enterprise MCP-client OAuth consent/SSO. The documented local consent identity limitation remains.
4. No new Sentry/Grafana or enterprise multi-user end-to-end integration was claimed in this pass. The live agent test verified an empty local inventory and a narrow read operation.
5. The shared runtime may expose provider-native capabilities such as web search; governance mutations remain limited by registered tools and server enforcement.

## Latest main merge

Merged `origin/main` at `b3799ccda` into `fix/gateway-review-1` with merge commit `2b73cd278`. Local CapLayer work was restored after the merge. Resolved dependency checksum, user-directory test and composer conflicts, preserving main's native-terminal commands/attachments and the shared composer components.

Post-merge verification: CapLayer TypeScript/production build passed; 11 focused frontend files / 79 tests passed, including main's terminal-tools tests; targeted CapLayer, profile and directory tests passed. No unresolved Git conflicts remain. Local uncommitted work is preserved with an additional safety stash (`caplayer-local-before-main-2026-09-30`).

## Native runtime compatibility follow-up

Reviewed main's full-CLI/Landlock rollout, native-terminal composer, transcript restoration, own-provider-account default and secret/session fingerprint changes. Crew/Code resolve to hybrid and may upgrade to full CLI under server rollout settings; CapLayer is not a Crew/Code project and keeps MCP-only execution.

CapLayer now explicitly declares MCP-only tool mode, clears inherited API shell transport, and disables project/global secret attachment. This preserves its narrow governance tool surface even if the shared Crew runtime definition expands later. Its provider/model choices and shared terminal/chat controls remain available. A regression test resolves the real CapLayer profile with full-CLI rollout flags and preselected test secrets: secret selections are cleared and shell/file/browser/background/live-policy tools are denied while the governance tool remains admitted.

Targeted Go tests for CapLayer, native-tool defaults, full-CLI rollout, session fingerprint and wrapper upgrades passed. Four frontend files / 28 tests passed, covering terminal commands/attachments, transcript hydration and CapLayer/Work rendering.

The old screenshot showing “Sign in to CapLayer” came from a stale loaded preview. The current source and the Vite-served modules contain no gateway-token login; a fresh in-app browser tab loads the existing Local account and shared chat directly. Both older targets failed browser refresh/inspection. They were preserved, and a fresh current preview was opened for the user.

Live follow-up on the rebuilt product server using main's latest provider dependencies: the existing conversation called `inspect_environment` once and completed successfully in the shared transcript. Inventory remained empty and no draft was created. Runtime evidence showed `coding_agent_tools=mcp_only`, one admitted tool (`manage_caplayer_access`) and the platform shell/file/secret/browser tools filtered out. The current fresh preview remains open for testing.

## Official local reference MCPs

Connected the MCP reference filesystem (`14` tools) and memory (`9` tools) servers, both npm version `2026.8.31`. The development-only `cmd/local-mcps` bridge forwards their original schemas/calls from stdio to loopback Streamable HTTP. It does not run in normal gateway startup. Filesystem is bounded to `/tmp/caplayer-reference-mcps/files`; memory uses `/tmp/caplayer-reference-mcps/memory.jsonl`. The connector helper accepts loopback origins and resyncs without duplicates. Tool definitions remain quarantined for review; no group/policy access was granted.

Verification: live reference-server file write/read and outside-directory denial passed; memory create/search/cleanup passed; the gateway Go suite and diff whitespace check passed. The shared app's MCP server panel shows both connectors and all 23 tools. Reproduction steps and testing examples are in `mcp-gateway/LOCAL_MCPS.md`.


## Server panel readability follow-up

The connected-server table squeezed three columns into the workspace pane. A gateway-only server repeated its name in a monospace chip, showed an unexplained dash for AgentWorks, and wrapped unlabeled action icons underneath. Connector “active” also appeared next to the tool count, obscuring that the tool definitions still required review.

Replaced that table with responsive server cards inside the existing shared SettingsCard. Each server name appears once, followed by an explicit connection state, discovered tool count, separate review/approval counts, endpoint and labeled View tools / Sync tools / Credentials / Remove actions. AgentWorks connection details appear only for servers with that source; multiple gateway instances still have individual controls. Search uses a result count, and the custom connection form appears before the long provider catalog.

Expanded tools now show their upstream name and plain Approved / Needs review / Disabled labels. Exact MCP names, versions, schemas and annotations remain in Review details. Approval controls appear after opening the review, and the panel explains that approving a definition does not itself grant user access. API approval still sends the exact fingerprint/version.

Verification: TypeScript check passed; server-panel and console-utils suites passed (25 tests), including exact-version approval, AgentWorks-only discovery, stale refresh, import/custom URL handling, labeled actions and distinct tool states. In the running shared app at http://127.0.0.1:18162/, both reference servers show the correct counts; all 14 filesystem tools and 9 memory tools expand; memory schema review renders successfully. At a 349 px server-card width, scrollWidth matched clientWidth (347 px), and labeled actions wrapped onto two rows. The shared chat/header/composer remain in use. No tool approval or policy access was granted during browser verification.


## Admin connection approval and simpler summaries

Updated the product contract after clarification: an administrator connecting an MCP approves its initial discovered tool definitions. Assigning selected tools to a group remains a separate action. Gateway initial discovery now snapshots these definitions as active with their approved fingerprint, inside the connector lock. Existing connectors with snapshots, background syncs, credential replacement and explicit resync retain change-review behavior. Changed fingerprints and newly appearing tools after connection remain quarantined; unchanged approvals survive sync. This does not automatically grant any principal access.

Removed the endpoint URL and review/approval counts from each collapsed card. The summary is now simply Connected plus one tool count. Detailed statuses and review controls are inside View tools; the overview omits a zero pending-review count. The custom form remains ahead of the provider catalog.

Verification: full gateway Go suite passed, including a new actual-upstream regression for initial approval, deny before grant, selected read-only group access, unchanged sync, and changed/new tool quarantine. The existing admin API/OAuth integration test now proves default denial and successful group access without an initial approval request. Frontend server-panel tests passed (13); TypeScript and diff checks passed.

Rebuilt the local gateway and reconnected the two reference servers. Before restart, the isolated gateway had no groups, packages, PII rules/reviews, grants or audit events; its read-only state snapshot was retained privately under /tmp/caplayer-ui-review/before-initial-approval.json. The updated preview has 23 approved initial definitions and zero group access. Browser verification shows Connected / 14 tools and Connected / 9 tools, no endpoint URLs or duplicated review counts. Memory's nine definitions show Approved with no approval buttons. The normal shared application remains open at http://127.0.0.1:18162/.


## Server actions and compact layout follow-up

The card now presents one compact row: server identity, Connected / tool count, View tools, and an actions popover. Removed the permanent Sync / Credentials / Remove button strip. Maintenance uses the existing shared IconPopover: Refresh tool list, Connection settings, and a separated destructive Disconnect server action. The shared popover now accepts a close callback for actions and exposes aria-expanded; its existing children API remains compatible. The existing shared confirmation dialog explains that disconnecting removes group permissions and reconnecting requires reassigning them. The token editor is hidden until Connection settings is selected and explains the difference between the upstream server token and user/group access.

Verification: TypeScript passed and the server-panel suite passed (14 tests), including menu closure after selection, settings visibility, disabled empty-token submission, disconnect confirmation/cancel, stale refresh and existing discovery/approval behavior. Live browser refresh retained the filesystem's 14 tools; opening/closing settings worked without entering credentials; disconnect confirmation was inspected and cancelled. The compact row was checked at a 349 px card width with no horizontal overflow, and its controls wrap below the identity on narrow panels. Preview remains open at the normal shared application URL.

## Custom server setup through the shared agent

Removed the permanent custom name/URL/token form and JSON import form from Servers. A single Add custom server action sits after the provider catalog. It uses the existing right-pane-to-chat draft helper, reopens the shared chat, preserves an existing unsent draft, and focuses the shared composer. Clicking it does not immediately submit a message or create a connector. The agent collects the administrator's server name and URL.

Added `connect_server` as an operation of the existing `manage_caplayer_access` tool. The product server still rechecks the central administrator role and product access on every call. Gateway connection setup uses the same URL/private-network checks, uniqueness handling, discovery, initial definition approval and rollback as the admin connector API. The operation accepts a name, URL and optional instance; unknown fields, embedded credentials and unsafe URLs are rejected. No bearer token is accepted through the model tool. Token-authenticated upstreams still require a secure credential setup flow; secrets must not be pasted into chat. Publishing, revocation, membership and upstream tool execution remain excluded.

The Servers panel refetches when a chat turn completes, so a successful agent connection appears without reloading the page. The first live chat test exposed ambiguity between an operation and a standalone tool, plus reliance on an older schema in the native conversation. Updated the system prompt to specify the exact management call and recheck the current schema when history describes older capabilities.

Verification: TypeScript passed; two frontend suites passed (25 tests); targeted CapLayer product tests and the full gateway Go suite passed. A real-upstream endpoint regression proves unauthorized setup denial, rejection of credentials/unsafe URLs, discovery and initial approval, duplicate prevention, and default denial of user access. In the live shared app, Add custom server focused the correct composer. The actual Claude runtime then connected a temporary local Memory instance and discovered nine tools; inventory refreshed automatically. Zero groups or packages were created. The temporary connector was removed after verification; the original two reference connectors remain with 23 approved definitions and no group grants. The local browser remains open.

## Proposed overall navigation direction

Keep the actual shared header, chat tab, composer and workspace toolbar. Within the right workspace, simplify navigation around domain entities:

- Servers opens a quiet directory containing only connected server names, connection status and one tool count. Selecting a server opens a full-width detail view with Tools, Group access and Settings tabs.
- Browse available providers in a separate add flow instead of keeping a long catalog mixed with connected inventory. Custom server setup remains a bottom action that starts chat, without an inline form.
- People retains Users and Groups. Each group detail shows its allowed tools and members, with a readable review step before applying access.
- Access presents human-readable draft summaries, selected tools and resource conditions before the final publish action. URLs, credential controls, fingerprints, raw schemas and IDs belong in details/settings.

Two interactive conversation previews explore the server directory and group workspace. These are proposals; the corresponding full directory/detail and group-editor restructuring has not been applied to the live product in this pass.

## Upstream OAuth reuse — implemented and verified

This supersedes the earlier “Requires OAuth · coming later” limitation for configured catalog providers.

### Implementation

- The server directory reuses the actual shared `OAuthStatusBadge` and existing product OAuth endpoints, dynamic client registration, PKCE callback, client ID/secret dialog, private token store, and refresh manager. Clicking Connect with OAuth first checks for an existing shared sign-in.
- OAuth connection settings offer shared reauthorization instead of a raw bearer-token editor. The backend also rejects bearer replacement for an OAuth connector. OAuth credentials remain associated with the existing platform identity per provider; separate connector accounts are not implemented.
- The gateway requests the current access token from a service-only product broker for each upstream HTTP request. It never copies refresh tokens into the gateway or browser. Service authentication, browser Origin rejection, exact configured name/URL binding, fixed broker origin, disabled redirects, bounded responses and no-store responses protect this handoff.
- Shared product sign-in, status refresh, broker refresh and logout serialize token operations per provider. Existing token-prefix logging and frontend authorization-response logging were removed. The shared UI cleans up completion polling on unmount and prevents overlapping status polls.
- `GATEWAY_PRODUCT_URL` configures the product credential broker. `GATEWAY_CATALOG_PATH` lets the gateway reuse the product MCP catalog, exporting only provider names, URLs and the OAuth requirement. The product and gateway must use matching configuration and service credentials.
- Initial tool approval remains part of the admin's connection action. Groups receive no permissions until explicitly assigned. OAuth refresh and reauthorization do not broaden tool access or change policies.

### Verified

- Full gateway Go suite passed, including an OAuth catalog integration test proving initial approval, default denial, preservation of explicit grants after refresh, and rejection of raw bearer replacement.
- Product CapLayer and existing OAuth persistence/registration tests passed. Broker tests cover browser JWT rejection, Origin rejection, URL mismatch, concurrent refresh producing one token exchange, token deletion/revocation and responses containing only the access token for the service caller.
- TypeScript checks and 28 tests across the shared OAuth badge, server directory and CapLayer surface passed. Tests verify existing sign-in reuse, the normal authorization flow, poll cleanup and OAuth-specific connection settings.
- In the live in-app browser at `http://127.0.0.1:18162/`, Local OAuth Memory completed actual DCR and PKCE through the existing product callback. CapLayer discovered 9 tools. After expiry, refresh succeeded without another sign-in. After rebuilding/restarting the gateway, Connect with OAuth reused the same platform sign-in without another authorization prompt. The fixture reported one authorization, two refreshes and twelve authenticated MCP requests. No groups or policies were created.
- The filesystem and memory reference servers remain available locally. Browser proof is saved at `/tmp/caplayer-shared-oauth.png`; the app tab remains open.

External provider accounts were not used in this verification. Provider-specific registration requirements still follow the existing OAuth flow. Arbitrary custom OAuth providers need an OAuth entry in the shared product MCP configuration. Durable governance storage, enterprise client identity/consent and production rollout requirements remain as described above.

## Access organized by existing group — implemented

The user chose Access as the location for group permissions and explicitly excluded group creation there. This supersedes the earlier global Access package presentation and the proposed placement of tool permissions in People.

- Access selects an existing group and reuses the shared tabs for Users and MCPs & tools. Users are read-only in this view. There are no group creation, rename, membership editing or API key controls here, including when there are no groups.
- People → Users retains shared account/role/product administration. People → Groups handles group creation, rename, membership and optional group API keys.
- Expand an MCP to assign its tools; expand a tool's Permissions to inspect allowed/denied state, published conditions, draft conditions and stale tool fingerprints. Configure access in chat prepares a group-specific draft prompt in the shared composer without sending it, preserving existing unsent text.
- Draft review, simulation, publish, revoke and policy history are filtered to the selected group. Group changes remount permission panels to prevent displaying prior group data. Membership loading fetches only the selected group instead of issuing a request for every group.
- The authenticated group permission inventory uses the actual runtime authorization function. Published policy ownership and revoked-policy denial are reflected in the UI; legacy grants cannot override these rules. Governed tool checkboxes are disabled and direct the administrator to policy configuration.
- Publishing includes the reviewed draft version; a stale review returns HTTP 409 before publishing. Existing clients that omit the version remain compatible.

Verification: the real application TypeScript check (`tsc -p tsconfig.app.json --noEmit`), 37 tests across five relevant frontend suites, and the full gateway Go suite passed. Tests cover group scoping, per-tool condition display, creation controls only in the directory, selected-group membership loading, sample result invalidation, publishing the reviewed version, effective authorization after publish/revoke, and cross-workspace/authentication rejection. `git diff --check` passed.

In the local in-app browser, Access initially showed only the empty-group explanation. Created an empty Local test · Readers group under People → Groups, returned to Access, verified the existing-group selector and both tabs, expanded Memory and one tool's permissions, and verified the Users tab has no membership editing. No members or permissions were granted and no policy was published during this browser check. The three local MCPs remain connected. Preview is saved at `/tmp/caplayer-group-access.png`; a fresh app tab is left open because the original tab's browser connection became unresponsive.

### Group content containment correction

Moved the MCPs & tools tab panel, including per-tool permissions and policy review, inside the selected group's SettingsCard below its tabs. The permissions section no longer renders as a separate outer card. The local browser confirms one group boundary contains the selector, tabs and MCP permissions; group creation remains in People. Preview: `/tmp/caplayer-group-access-contained.png`.

### Group selector and naming follow-up

Replaced the native group dropdown with visible group buttons using the shared Button and Input components. The selected group has a blue tint and checkmark, names wrap, and multiple groups offer search with a bounded scrolling list. Search does not silently switch the selected group. The group tab is now named Permissions with a key icon. Updated the group-switch regression to select a filtered group using the new controls; permissions and review remain inside the group boundary.

### Shared Ask AI control

Replaced Configure access in chat and Configure restrictions in chat with the actual shared AskAIButton. The group action uses the standard icon control in the permissions header; tool details use the same component with its label. Both retain the shared deliberate confirmation, feedback and error behavior. Confirmed requests use sendWorkspacePaneMessageToChat with the CapLayer profile/conversation identity, preserving unsent composer text and queuing behind active turns. The standard Ask AI message format keeps internal group/tool IDs out of the displayed request while including them in the assistant context. Publishing remains a separate administrator action.

Live verification confirmed the Permissions tab, selected group row, contained content and Ask AI arming/Escape cancellation without sending a model request. The current preview is `/tmp/caplayer-group-access-contained.png` and the app remains open.

## Group controls and durable configuration follow-up — implemented

These changes supersede the earlier memory-only governance limitation and per-tool Permissions dropdown described above.

- Each MCP in a group has an explicit Remove from group action and the shared confirmation dialog. The backend removes that group's server grant, individual tool grants, and connector rules from both draft and live policies under one store lock. Empty live policies remain revoked; governance tombstones remain. Versions advance to prevent an old review from restoring access. Other groups, connectors and unrelated rules remain intact.
- Removed the Whole server assignment option and the header's extra new-conversation button. Existing server grants still display their inherited state until removed.
- Tool Arguments is an inline caret under the name/description. Expanding it immediately shows formatted JSON schema across the tool card's available width, including nested properties and required fields. There is no second schema expander or per-tool Permissions dropdown. Published/draft conditions remain visible when present. Per-tool advanced setup uses the actual shared AskAIButton next to the tool heading.
- Mobile, Tablet and Laptop use the shared split rail and existing preference storage. Mobile pins the right pane to the shared phone width; other modes restore resizable layouts. Fixed CapLayer conversation restoration to explicitly omit an unrelated active session ID, eliminating the observed 422 continuity conflict without changing other products' default behavior.
- The gateway now opens `GATEWAY_STATE_DIR/gateway.sqlite`. A typed, versioned configuration snapshot is encrypted with AES-256-GCM using a separate private key. Groups, users, membership, grants, connectors, approved snapshots, draft/live policies, tombstones, policy history, key hashes and PII rules persist on each configuration mutation. No standalone JSON export is needed.
- Writes complete before readers see changes. Failed writes restore committed state and latch a storage fault; authenticated admin mutations return 503 and MCP authorization denies until repair/restart. Missing/unreadable key or state fails startup. A separate SQLite exclusive lease rejects concurrent gateway instances before they can serve stale permission snapshots.
- Existing local group and all three connector IDs/approvals were migrated. Verified a local Memory read_graph assignment survived a real gateway restart and remained allowed in the effective permission inventory. Removed that synthetic assignment through the browser's full server removal action; no members were added. All three servers remain connected.

Verification: application TypeScript check and four frontend suites (35 tests) passed. Full gateway Go suite passed; store/admin tests also passed with the race detector. Tests cover restart recovery, encrypted credentials and hashed API keys, removal surviving restart, missing keys, concurrent instance rejection, unexpected revision changes, write rollback, admin 503 and runtime denial. Browser verified the inline JSON schema, absent Permissions dropdown, shared preview controls, restored chat and full removal confirmation. Schema occupied the full tool card content width (693px within a 719px padded card in the observed layout).

Remaining limits: tool-call audit/usage events and PII review queues are still memory-only. Persistence currently rewrites a full configuration snapshot per mutation and supports one gateway process; enterprise multi-worker rollout needs normalized shared storage, cross-worker invalidation and individual MCP OAuth identity/consent. The local-alpha exposure guard remains enabled. Backups need the SQLite database and private key; use a persistent deployment volume. The running local gateway now uses `/Users/mipl/.local/share/agentworks/caplayer-local/gateway.sqlite`, migrated from the temporary test directory with SQLite backup. The configured directory must be a persistent deployment volume in production.

Browser proof: `/tmp/caplayer-groups-final.png`. The local app remains open at `http://127.0.0.1:18162/`.

## Chat project database placement — follow-up

The user requested that configuration belong to the CapLayer chat project, like Crew workflow data. The local launcher now sets GATEWAY_WORKSPACE_DIR to the actual local owner project. The database is Chats/CapLayer/db/gateway.sqlite; service credentials, OAuth state and its encryption key stay in backend state. Existing database contents relocate using SQLite (including committed WAL data), preserving groups, grants and policy history; an active old gateway must stop first. Standalone runs can configure an explicit project directory, with the previous state-directory path retained as fallback.

The current agent has authenticated query/setup operations through manage_caplayer_access (inspect_environment, inspect_tool, connect_server, save_draft). Draft mutations persist to this database. It does not have generic SQLite query/mutation tools, decryption keys, publishing or membership authority. The folder move does not change these capabilities.

Verified the running project database at `/tmp/caplayer-product-runtime/docs/_users/default/Chats/CapLayer/db/gateway.sqlite`: configuration revision 40 and SQLite integrity OK. The existing group, three connectors, 32 tool definitions and policy history remain intact. The agent inspection service returns this restored inventory. The key is absent from the chat folder. Full gateway Go suite and launcher shell syntax check passed, including active-instance migration rejection and preservation of configuration with an external key.


## Project SQL tools follow-up

Supersedes the earlier statement that the agent has no generic SQL tools. The user explicitly requested Crew-style SQLite query and mutation. CapLayer now registers the actual shared `query_workflow_db` and `mutate_workflow_db` definitions. The workflow SQL validator was extracted into a shared package without changing the workflow tool surface.

The chat project's SQLite file contains normalized governance metadata in addition to its encrypted authoritative snapshot. SQL mutations execute through its gateway owner, validate references/approvals/draft conditions, and atomically update persisted rows, snapshot and live memory. Editable tables are groups, membership, direct/group tool assignments and permission drafts. Central accounts/roles, credentials, approval state and published policies are outside this writable surface. Publication still requires reviewed UI action. Draft edits increment versions, group deletion revokes live policies while retaining denial tombstones, and new direct grants cannot bypass governed restrictions.

Both tools enforce the current central product admin role per call and are bound to the CapLayer chat project. The key stays outside chat. SQL reads have engine-level query-only protection and bounded duration/results; mutation batches are atomic. External physical database edits invalidate the running owner's revision and require restart. The public metadata tables are plaintext within a private file; secrets remain encrypted. This is still a single-process alpha with full metadata projections per configuration write, not enterprise incremental shared storage.


Verification for SQL: full gateway Go suite and store/admin race checks passed; shared workspace SQL guard tests passed; product CapLayer/workflow tests passed. The running product and gateway were rebuilt. A live chat invoked the real SQL tools six times to describe/read, rename an empty test group, verify, restore, and verify again. Both name changes committed and the final name was restored. The test made no member, grant, connector or policy changes.

## Retry warnings and composer cleanup

Nested panel loaders previously rendered independent error/stale cards, each with Retry, even when the underlying failure was the same unavailable service. Added a pane-level feedback boundary that combines their messages, deduplicates identical failures and refresh callbacks, and provides one Retry action. Previously loaded data stays visible with an explicit stale warning; failed sections recover together when the service returns. This does not replay mutations or upstream tool calls. Browser inspection found no active retry errors across Access, Servers, People, Audit, PII and Connect; it did not establish the cause of any earlier provider retries in chat.

The user then requested removal of repeated Ask AI icons. Removed the group-level and per-tool icons and their unused chat-draft callbacks. Advanced policy setup remains available through chat, with explanatory text in the permissions section.

The composer already had the shared ModelReasoningControl, but its visibility incorrectly depended on the product visual variant. CapLayer uses the common AgentWorks-style composer, so that condition hid the picker. It now depends on the product profile, reusing existing model/provider catalogs and selection transport. CapLayer may change engines in its retained conversation, as supported by the shared runner. In the browser, opened the picker with provider/model/reasoning choices, selected Haiku, verified the changed picker label, and restored Sonnet. Preview: /tmp/caplayer-model-picker-final.png. The browser and services remain running.

Frontend verification: application TypeScript check passed. Four focused suites passed with 24 tests; the final real-loader outage integration case also passed (25 focused tests in total). It simulates a failed refresh of group/membership/permission loaders and verifies that one Retry recovers every section, without losing the displayed group data.


## Follow-up: main integration, runtime model switch and panel clarity

- Merged `origin/main` at `41a74f76d` into `fix/gateway-review-1` (merge `26478e716`). Restored local work and resolved the prompt, API session-context, checksum and shared-header conflicts. Removed an auto-merge duplicate preset declaration. Recovery stash retained.
- Removed CapLayer’s composer model picker and disabled its legacy models dialog. The right-side Models section now renders the same `WorkModelsPanel` used by Crew and Code, with CapLayer’s profile options. It includes shared provider/account setup and the model selector.
- Runtime switches now bypass retained live delivery and close the old durable MCP-agent session, in addition to closing its provider CLI. A stale retained provider/model is detected even if an earlier attempt already saved the new registry selection. Added regressions for provider switches, stale retry, unchanged selections and same-provider model changes.
- Live verification: Muse `muse-spark-1.3-contributor` returned `CAPLAYER_MODEL_OK`; selection then changed to Claude Opus `claude-opus-5-5`, and the user’s next chat ran on that provider/model with the same conversation ID. Verified through actual server agent-construction and generation logs, not the picker label. No test changed grants or membership.
- The obsolete token-login screen survived in ignored production bundles. Rebuilt the complete frontend output and added a release check rejecting bundles containing the old standalone token-login prompt. A refreshed in-app browser loads the shared Local account and chat directly.
- Removed repeated panel subtitles and Access instructions, shortened connection/audit/PII/account copy, and moved default PII details into a disclosure. Exact schema and permission conditions remain available with each tool.
- Shared `WorkspaceViewHeader` automatically uses an 18px, vertically centered title when the subtitle is absent or blank; it retains the existing 14px title when a subtitle is present. CapLayer uses this shared behavior.
- Verification: TypeScript build; 48 focused frontend tests (shared headers, gateway panels and release validation); focused product-runtime and CapLayer Go tests; product binary build. Frontend release assets and bundle budget passed, with the existing eager-bundle size warning. Browser left open on the running local app.


### Setup assistant instructions

Expanded the actual shared-chat profile prompt and embedded `caplayer-access` skill with common MCP/group assignments, read/write classification using real schema/descriptions/annotations, preservation of unrelated grants, destructive-tool ambiguity, current-tool-set semantics, cross-group access paths and resource-rule coverage. Corrected the obsolete assertion that no tools can edit membership and the missing leading slash in the organization condition example. Added concise inventory/assignment/draft reply formats and explicit active-versus-draft status. Kept advanced publishing and caller authorization outside model discretion. Rebuilt and restarted the local product with the updated embedded prompt.

### Follow-up: separate MCP panels and shared tool cards (2026-10-01)

- Split Connected MCPs and Available MCPs into separate sections of the existing workspace toolbar. Each has its own list/search; successful catalog connections return to Connected MCPs. Custom server setup remains at the bottom of Available MCPs through chat.
- Added one shared `GatewayToolCard` for connected gateway tools, platform tools and group permissions. Name, description and expandable Arguments use identical markup and styling. Arguments show the full JSON schema. Group checkboxes and server review actions remain contextual controls.
- Verification: TypeScript build and 38 tests across four focused suites passed. Browser verification confirmed catalog/connected separation, matching group/server cards and expandable JSON schema. No grants, membership or servers were modified. Screenshot: `/tmp/caplayer-mcp-panels-20261001.jpg`; local browser and services remain open.

- After previewing Connected MCPs without its title bar, enabled shared `WorkspaceViewHeader hideHeader` across all CapLayer sections. The option defaults to false for other callers, hides title/icon/subtitle/context and automatic walkthrough, and retains explicit actions, tabs and below content. Headers with no remaining controls render nothing. People keeps its Users/Groups tabs. TypeScript and 22 focused shared-header/surface tests passed; verified People, Available MCPs and Access locally. Screenshot: `/tmp/caplayer-all-hidden-headers-20261001.jpg`.

### Headerless settings layout

Removed the outer SettingsCard border and padding when the surrounding CapLayer pane has `hideHeader=true`. The shared `SettingsCardLayout` supplies this option without changing other product defaults. Group selection, server and tool cards retain their individual boundaries. TypeScript and 21 focused group/surface tests passed; browser verification confirmed the Groups section has no outer box.

## OAuth credential security audit (2026-10-01)

Committed the current implementation as `378b0202b`, then merged `origin/main` at `6802950ef` as `46af2ce3d`. The only conflict was in `agent_go/go.sum`; retained both sets of dependency checksums and the newer upstream MCP-agent version. The pre-commit secret scan passed on both commits.

### Verified protections

- Platform token and OAuth-client files under the configured MCP tokens root are sealed with AES-256-GCM and path-bound additional authenticated data. Client secrets move out of the JSON config overlay into the sealed client file. Personal MCP credentials use a separate path-bound namespace.
- Checked local files without exposing credential values: `Notion.client.json` and `Local OAuth Memory.json` were not plaintext JSON and both had mode `0600`; the token root and `_platform` directory had mode `0700`. No Notion token existed at inspection time, so its authorization was incomplete.
- The latest main caches `AUTH_SECRET` in server memory and clears its environment variable at startup. Child-process helpers pass an explicit minimal environment; the child-environment audit records names, not values.
- The CapLayer OAuth broker requires the server service credential, rejects browser Origin headers and requires the exact configured upstream URL. It returns only the short-lived access token, uses `Cache-Control: no-store`, serializes refreshes and observes token deletion. The browser/admin inventory does not receive the upstream credential.
- Post-merge TypeScript and 44 focused frontend tests passed. Focused backend sealing, auth-cache, child-environment, rotation and broker tests passed.

### Remaining findings

1. **Shared connection ownership bypasses CapLayer groups — addressed by the later private/Vault ownership work below.** Connecting from CapLayer uses the platform OAuth flow and publishes a shared AgentWorks connection. A user able to select that shared connector elsewhere can use it through the normal product path without CapLayer group restrictions. Encryption protects stored credentials but does not enforce gateway-only use. Reuse the authorization implementation with separate CapLayer ownership before treating group policies as the organization's access boundary.
2. **AUTH_SECRET rotation does not cover OAuth credentials.** OAuth token/client encryption derives its key from AUTH_SECRET, but `rotate-auth-secret` currently rotates provider keys and workflow secret documents only. It does not enumerate platform/personal OAuth files. Changing the secret through this command can leave those credentials unreadable and require reauthorization. Add complete OAuth rekeying or refuse rotation when uncovered stores exist.
3. **Legacy plaintext migration is not fail closed.** Startup logs an error from `sealPlainPlatformCredentials` and continues; the platform sealer also accepts legacy plaintext JSON. A failed migration can leave usable plaintext credentials on disk. Custom token-file paths outside the claimed roots are likewise not covered by this sealer. Enforce an approved credential root and refuse insecure legacy state, or disable affected connections until migration succeeds.

This audit covers the merged source, automated tests and local file metadata. It does not verify a running Linux deployment. The already-running local backend has not been rebuilt/restarted with the newly merged environment-hardening changes.

## Product rename: Vault (2026-10-01)

Renamed the visible product to Vault, with the picker description "Tools, skills, and access". Updated the shared product picker, page title/favicon, account product labels, workspace accessibility labels, onboarding/errors, assistant profile/prompt/skill, standalone entry, gateway admin templates and current product documentation. Existing profile IDs, API routes, environment variables and `Chats/CapLayer` storage remain compatible so saved chats and permissions retain their location.

TypeScript and 35 focused frontend tests passed, along with the focused CapLayer backend tests. Rebuilt and restarted the local product backend with this rename and the latest main security changes; its health endpoint is healthy. Startup revealed an unquoted colon in Google AI's embedded skill description under the newly strict frontmatter parser. Quoted that YAML scalar and verified the existing complete generation-skill registration test passes. Browser verification shows Vault and its new description in the shared picker; screenshot: `/tmp/vault-product-renamed-20261001.jpg`.


## Platform MCP ownership: private connections and Vault (2026-10-01)

Crew, Code and workflow integrations now share **My MCPs** and **Vault** sections.
Ordinary chat MCP details also expose Vault. My MCPs uses the authenticated
person's sealed store; project sharing never transfers their external login.
Vault lists only the current group's granted tools, shows their raw argument
schemas, and loads shared connections through the gateway. Global catalog
entries remain templates. Removed the deployment-wide account-sharing prompts
and normal connection admin gate; explicit Vault OAuth setup still requires a
central administrator. Custom keys are entered in a password field, stored
privately, and attached using a secret reference.

Runtime constructors, native coding-agent execution bridges, Crew creation and
workflow preflight resolve the same ownership boundary. Pool names isolate
users and Vault delegations; every shared call checks current gateway policy.
Account switching clears private MCP UI metadata and rejects late responses.

[Architecture, migration notes and test results](../design/vault-mcp-ownership.md)
record the enforcement and rollout behavior. Focused backend checks, 47 frontend
tests and the production build passed. The full host suite has catalog/Pulse
fixture failures and timed out; live browser QA was blocked by CDP timeouts.
Old globally shared accounts require Vault connections plus group grants, or
private reauthorization. AUTH_SECRET OAuth key rotation remains open.


## Shared navigation, integrations and secrets follow-up (2026-10-03)

The shared application retains the thin left navigation rail and original product
icons, including Relay after main integration. Global pages have return controls;
runtime health moved to the account menu. The obsolete composer model control
was removed across products; runtime model selection belongs in the shared Models
panel. Connected/available MCP views use the shared integrations component and
common tool rows. Crew/Code/workflow views hide tool details; Vault group and
server views retain expandable full-width JSON schemas.

Secrets now live under Integrations across products. Vault manages platform
values, groups grant use, and each project explicitly selects permitted names.
The common secret UI has compact rows, copy/rotation icons, a larger Add secret
button below, assigned group selections and stable checkbox updates. Platform
bootstrap and group descriptions persist. Provider rotation, copied-value
revocation, external MCP secret export and current runtime selection behavior are
specified in [the secrets design](../design/vault-secrets.md).

## Audit and PII follow-up (2026-10-03)

Audit uses readable identity/MCP filters, More options and CSV/JSON export; Logs
is the default subtab and Analysis loads only on selection. Calling app denotes
the client, separately from user and upstream MCP. Audit stores metadata only.
Local SQLite, server ClickHouse with a durable delivery spool, and Off are
configurable. Local retention cannot exceed 24 hours.

The new async mode admits copied metadata to one bounded writer queue (1,024
waiting plus at most 256 in flight). Failed batches retry and reject admissions
while unhealthy; queue overflow and flush failures are explicit. SIGINT/SIGTERM
drains accepted events after stopping requests. Durable remains the installation
default; the current preview explicitly enables async. Crash loss before commit
and errors after upstream execution remain documented limits.

PII now has Protection/Test/Reviews with reduced text. Rule creation/edit/delete
controls were removed; existing custom rules are read-only in this UI. Regex and
checksum enforcement remains synchronous before forwarding/returning payloads.
Logging neither stores nor rescans raw payloads. PII review state is still bounded
memory, not durable. No model performs detection.

Verification: the focused gateway store/PII/MCP/admin/server race suite, frontend
TypeScript and gateway build passed. Local allowed/blocked calls were recorded
without the synthetic SSN value; all five events survived graceful restart with
zero pending writes/failures. Browser showed SQLite / Async / 24h and the events.
Isolated mean admission was 0.199 µs async versus 5.01 ms durable; PII scan means
were 0.105/0.427/6.98 ms for approximately 1/4/64 KiB. These are not full request
latencies or a supported RPS. Details and p95/p99 are in
[the implementation summary](../design/vault-current-state.md).

## Confirmed identity design (2026-10-03)

The user confirmed platform SSO as the identity source for Vault consumers,
including people who use its MCP endpoint from Claude without using other
products. People > Users continues to reuse the shared account/role editor.
The attempted separate external-user directory changes were withdrawn before
backend deployment; no external accounts or grants were created.

A platform identity is separate from product access and tool/secret grants.
Individual MCP OAuth consent must still be wired to verified platform SSO users;
the local alpha's static consent identity does not implement that team flow.
This remains a release boundary, documented in the current-state summary.

The restored shared Users screen was verified in the in-app browser. TypeScript,
24 Surface/Groups frontend tests and the existing active-product-identity backend
regression passed after withdrawal. The live backend was not replaced, no new
external account/grant was created, and the browser remains open.

## Shared user editor and Relay product list (2026-10-03)

Vault’s Users adapter now sets a required invitation product on the existing
UsersAdminPanel. Vault is checked/locked for new users and included in submitted
products after each form reset. Other products remain optional; admin accounts
retain the existing implicit all-products behavior. Existing account grants are
not changed merely by opening the panel. Global and workflow user pages retain
the same shared Viewer/Editor/Creator/Admin creation and editing controls.

Relay was missing from both the backend product inventory and the frontend main
product filter. Added its registered surface for workflow/Relay deployments and
used the shared product-label map. Dedicated unrelated deployments do not acquire
it. The isolated preview configuration also now includes Relays. Existing Relay
workflow access rules remain authoritative. TypeScript, 21 tests in three frontend
suites and focused backend registration/role/identity checks passed.

Rebuilt the product executable, restarted the local backend and confirmed
`/api/health` returns 200. In-app browser verification shows all four invitation
roles, Vault checked/disabled, optional Relays access and Relays in product
navigation. No live account was created. The preview tab remains open.

### User form styling and slot provisioning boundary (2026-10-03)

The shared role editor now uses the existing Radix Select with concise role
descriptions, labeled fields, product selection chips and a separate submit row.
Creation and account-role editing use the same picker. Keyboard selection and
submitted role/product behavior are covered in the component regressions.

The user clarified that "slots" means the root-provisioned Linux execution
accounts used by RTS and Excellence. The attempted relocation to global-only
account creation and local navigation visibility change were withdrawn. The UI
still does not provision slots or check them when granting execution products;
the current-state document records this unresolved boundary. No account, role,
grant or remote provisioning was changed during inspection.

### Vault-only user form correction (2026-10-03)

Supersedes the earlier checked-Vault-plus-optional-products UI. The shared
editor now accepts `vaultOnly`; the Vault adapter enables it. Invitations expose
only email and submit a Viewer account with only `mcp-gateway` product access.
Platform role changes, product toggles and Code reviewer controls are absent
from the Vault account list. Group permissions determine MCP and secret access.
Existing accounts' grants are not rewritten. The global editor is unchanged.

TypeScript and 15 focused tests passed, including successive Vault-only payloads
and absence of platform permission controls. Verified the form in the in-app
browser. No live account was created. This UI restriction does not replace the
pending backend slot checks for global/account-API execution grants.
