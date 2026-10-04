> Current release: PII has been removed as of 2026-10-04. Earlier PII results below are historical. See the removal regression gates at the end of this plan.

# Vault MCP Gateway local test plan

Current behavior: [Vault implementation status](../docs/design/vault-current-state.md). Earlier execution records below are historical and do not certify the current full product.

## Goal and test setup

Prove that a local admin can connect real MCP servers, discover and review their tools, grant one tool to an MCP client, call it through the gateway, resync it, and inspect the audit trail. Exercise the embedded Vault console in the Codex in-app browser as well as the standalone admin API. Use this branch, a loopback gateway, and a local frontend. Never use production credentials or mutate external accounts during the live tests.

Live upstreams: Context7 is the full read-only call target. DeepWiki and Microsoft Learn are discovery/connectivity targets if available; an upstream outage is recorded separately from a gateway failure. A deterministic in-process MCP server covers denials, revocation, PII, deletion, and races.

## Cases and expected results

| ID | Area | Test | Expected result |
| --- | --- | --- | --- |
| A1 | Build | `go test -race ./...`, `go vet ./...`, frontend typecheck and gateway UI tests | All pass. |
| A2 | Gateway OAuth | In-process MCP client completes OAuth and calls approved tool | Client sees only its granted tools; allowed call succeeds. |
| A3 | Live MCP | Context7 discovery, approval, grant, `tools/list`, `resolve-library-id`, audit | Both Context7 tools discovered; only granted tool listed; call succeeds; allow/OK audit event recorded. |
| A4 | Other MCPs | Connect and discover DeepWiki and Microsoft Learn | Valid reachable servers expose tool snapshots; failures include the upstream reason. |
| B1 | Browser startup | Open embedded Vault on this branch | Access, Connected MCPs, Available MCPs, Secrets, People, Audit, Models and Connect sections render in the shared application. |
| B2 | Browser auth | Open console with the existing product account; probe anonymous/non-admin management requests | Local mode uses the local account without a gateway token prompt; only authorized product admins can manage Vault. Service credentials stay on the backend. |
| B3 | Browser connect | Add Context7 from catalog, inspect server and tool list | Server shows connected; initial discovered tools are approved automatically, with no user/group access granted. |
| B4 | Browser review | Resync a connected tool | Initial approval persists through resync if schema is unchanged. |
| B5 | Browser grants | Grant one approved tool to a local user or group | Only that tool becomes visible to that principal. |
| B6 | Browser audit | Make a call and inspect Audit | Decision, outcome, identity, tool and timestamp are shown without raw arguments. |
| C1 | Default deny | Try an unapproved or ungranted tool | Hidden from `tools/list`; direct call denied and audited. |
| C2 | Revocation | Revoke a grant and call again | Next call is denied. |
| C3 | Schema changes | Change upstream tool schema, then resync | Tool returns to pending review; old approval cannot authorize it. |
| C4 | Deletion | Delete a connector and recreate its namespace | Its tools, grants and PII rules do not carry over. |
| C5 | PII | Exercise regex mask, block and input review rules | Policy acts before upstream call; review requires explicit approval; denied calls are audited. |
| C6 | Security | Probe admin without token, unsafe URL/query, private egress, public bind | Each fails closed by default. Catalog OAuth uses the shared sign-in/refresh flow; local fixture egress requires explicit private-upstream opt-in. |
| C7 | Stability | Concurrent resync/remove; bounded audit and review queues | No leaked active connector; queues stay within limits. |
| C8 | Pagination | Upstream returns multiple `tools/list` pages | All pages are discovered before the gateway disables missing tools. |
| D1 | Restart | Restart local gateway | Users, groups, memberships, assignments, approved tool fingerprints, drafts, published policies and revoked-policy denial survive restart from SQLite. Committed audit events survive with SQLite; async events must drain before restart. Off stores no events. Pending PII review queues remain ephemeral. |

## Execution record

Run date: 2026-09-28. Branch: `fix/gateway-review-1`, based on `8657c197c` plus the test changes in this document. Local gateway: `http://127.0.0.1:18746`; local frontend: `http://127.0.0.1:51734`. Frontend uses an isolated runtime config in `/tmp/caplayer-pr228-runtime.js`; the tracked `frontend/public/runtime-config.js` is untouched. The gateway uses `/tmp/caplayer-pr228-local`. These are local test instances, separate from the pre-existing server on port 18745.

Record an upstream outage as blocked, not passed. Repair product failures and rerun their case plus the relevant regression suite.

| ID | Result | Evidence / notes |
| --- | --- | --- |
| A1 | Pass | `go test -race ./...` and `go vet ./...` in `mcp-gateway`; `go test ./...` and `go vet ./...` in `agent_go/pkg/mcpoauth`; frontend `tsc -b` and 52 gateway/switcher UI tests. |
| A2 | Pass | `TestM0GovernedCallPath` completed OAuth, hid ungranted tools, allowed a granted call and denied it immediately after revocation. |
| A3 | Pass | `GATEWAY_LIVE_TEST=1 go test ./e2e -run TestM0LiveContext7 -v -count=1`: discovered two tools, approved and granted one, called `resolve-library-id`, and recorded allow/OK audit. The test previously omitted approval; that test setup is fixed in this branch. |
| A4 | Pass | Added Context7, DeepWiki and Microsoft Learn through the running gateway's catalog API: HTTP 201 for all three, eight quarantined tools discovered (2 + 3 + 3). |
| B1-B6 | Blocked | The Codex in-app browser rejected reopening its crashed tab with a browser URL policy error. No visual browser pass is claimed. The local frontend remains running on port 51734 for manual inspection. API paths behind B2-B6 were exercised below, and frontend component tests passed. |
| C1 | Pass | Local group API key with a grant to a quarantined Context7 tool saw `tools/list=[]`; direct call returned `denied: tool is not active`. |
| C2 | Pass | After exact-version approval and resync, that client listed only `context7__resolve-library-id` and called it successfully. Revoking its group grant made the next list empty and the next call return `denied: no grant for tool`. Three decision/outcome audit events were present. |
| C3 | Pass | `TestSchemaChangeQuarantinesTool` and `TestResyncDisablesRemovedTools` passed under the gateway race suite. |
| C4 | Pass | Running gateway API deletion removed DeepWiki tool grants, group-server attachment and a scoped PII rule. Re-adding the same provider did not restore the grant. Store cascade tests passed. |
| C5 | Pass | Running gateway PII sample endpoint masked `alice@example.com` and blocked a valid test SSN. `TestPIIGuardsCallAndReviewRetry` covered input review, one-use approval, output blocking, and audit redaction. |
| C6 | Pass | Admin without token returned 401. Custom URLs with query credentials or loopback HTTP returned 400; OAuth-only Notion returned 400. Public bind and public URL startup probes exited with explicit errors. Consent page returned `frame-ancestors 'none'` and `X-Frame-Options: DENY`. |
| C7 | Pass | `TestConcurrentResyncAndDeleteCannotRestoreConnector` ran ten races under `-race`; all deleted connectors stayed gone. A slow resync no longer blocked deletion of another connector. Ring-buffer audit and per-caller review-capacity tests passed. |
| C8 | Pass | `TestPaginatedUpstreamKeepsAllApprovedToolsOnResync` used a real in-process MCP server with page size one and three tools. Gateway discovery found all three, and resync kept all three approved. |
| D1 | Observed limit | Before restart: 3 connectors, 8 tools, 1 group, 3 audit events. After restart: all four counts were 0 and the old group key returned 401. Reconnected Context7, DeepWiki and Microsoft Learn for local inspection; eight tools are present and the Context7 resolver is approved. |

## Current browser walkthrough

Use the full local app at `/`, with the product API and loopback gateway running.
Do not enter the backend service token in the product UI.

1. Select Vault from the product navigation. Verify the existing local account,
   shared Chat tab/composer, right workspace toolbar and Models panel.
2. Open Connected MCPs and Available MCPs separately. Connect a local reference
   server or the synthetic OAuth Memory fixture. Verify initial tool approval
   without a new group grant; expand the JSON input schema.
3. In Access, choose an existing group, switch Users/Permissions, and assign a
   disposable test tool/secret. Revoke it and verify the next runtime resolution
   denies it. Keep group creation under People.
4. In Audit, verify readable filters, default Logs, filtered Analysis, calling
   app/MCP/user labels, export and storage health. Use synthetic data only.
5. In PII, verify Protection/Test/Reviews, masked email preview and blocked SSN.
   No custom-rule creation controls should appear.
6. Restart the gateway gracefully with the same database/key and audit provider.
   Verify persisted grants, reconnecting servers, committed events and the
   documented loss of pending PII reviews.

The 2026-09-28 browser run was blocked by a browser URL policy error. Subsequent
shared-app browser checks are recorded below; the old token-login walkthrough
and unsupported-OAuth instructions are superseded.

## Release boundary

This plan validates a single-user loopback alpha. Team internet testing still needs individual MCP client sign-in/consent, durable PII review storage and deployment hardening. Governance configuration already persists in SQLite. A separate reverse proxy can expose a loopback listener without the gateway knowing, so none is used in this run.

## Full pre-merge pass (2026-09-28, PR #228 at `9c262770c`)

| Scope | Result | Evidence |
| --- | --- | --- |
| Gateway | Pass | `go test -race ./...` and `go vet ./...` in `mcp-gateway`. Includes OAuth, grants, denial, revocation, PII, audit, connector races, and paginated discovery. |
| Live upstream | Pass | `GATEWAY_LIVE_TEST=1 go test ./e2e -run TestM0LiveContext7 -v -count=1`: two Context7 tools discovered; approved resolver call succeeded. |
| OAuth module | Pass | `go test ./...` and `go vet ./...` in `agent_go/pkg/mcpoauth`. |
| Frontend | Pass | Full Vitest run: 389 files and 2,192 tests passed, one skipped. `npm run build` passed all catalog checks, TypeScript, Vite build, release assets, and enforced bundle budget. Eager JS was 959.29 kB gzip, above the 950 kB warning threshold and below the 1,030 kB limit. |
| Agent Go PR paths | Pass | The exact CI topology matrix and focused OAuth/external MCP server tests passed locally. The nested `mcpbridge` build now uses a temporary workspace with the resolved provider dependency; the corresponding GitHub deterministic-contract job passed after this fix. |
| Agent Go broad suite | Failed outside changed paths | `go test ./... -timeout 5m` failed in unchanged server catalog, provider-key, and prompt-size tests, unchanged browser anonymous-capture test, and unchanged workflow model-default test. The provider checkout returns Claude Opus 5.5 and GPT-6 Sol builder defaults while the test expects Sonnet 5 and GPT-6 Astra. These failures are not counted as gateway passes. |
| Agent Go vet | Failed outside changed paths | `go vet ./...` reports a missing `cancel()` on one path in unchanged `message_sequence_stop_test.go`. |
| Browser visual walkthrough | Blocked | The Codex in-app browser previously refused reopening its crashed local tab under URL policy. B1-B6 remain unverified visually; API and component tests cover their underlying paths. |

The broad-suite failures and browser gap mean this is not a clean full-product test pass. This was the merge recommendation at that historical revision; it is not a statement of the current PR status.


## Shared UI review pass — 2026-09-30

See [the complete UI review](../docs/reviews/caplayer-ui-review-2026-09-30.md) for findings, fixes, reference components and remaining boundaries. This pass verified the shared Chat tab, right workspace toolbar, default composer, transcript, model selection, all section navigation, People subtabs, pane resizing/collapse, and a phone breakpoint. The standalone build, 86 focused frontend tests and gateway Go tests passed. Browser checks used the real local gateway for navigation/PII/endpoint probing and a separate explicitly labelled model fixture for chat rendering; no live provider or external connector result is claimed by this pass.

### Shared product account regression

- Vault opens with the existing account; local single-user mode does not prompt for a token.
- People → Users uses the full product editor and includes Vault in the enabled product list.
- Product JWTs reach only the product API proxy; upstream requests use the server's service secret.
- Anonymous and non-admin management requests cannot reach the gateway; central role revocation removes access.
- Group membership accepts only active central IDs and synchronizes the identity binding before granting membership.
- Service authentication failures return deployment errors, without an authentication retry loop.
- Standalone runtime config exposes only public API URLs and requires an existing product account service.

## Complete shared application correction — 2026-09-30

This supersedes the primitive-only chat verification above. Vault now boots the full shared application and renders actual `ModePresetBar`, `ChatArea`, `ChatInput`, and the complete `ProductWorkspaceShell` also used by Crew/Code. Its registered profile uses durable product conversations and the shared provider catalog.

- Production build and 13 frontend files / 86 tests passed.
- Vault/profile/administrator product-server tests and the gateway Go suite passed.
- A real local Claude chat inspected the empty gateway inventory through the registered narrow tool; no draft or policy was published.
- Rendered Crew comparison confirmed the shared composer and runtime controls. Code's header was checked, but this isolated instance has no Code workspace.
- The broad product-server selection retains two workflow prompt-size failures; it is not a full-product pass.
- The current full-app preview is `http://127.0.0.1:18162/`; the earlier standalone URL is superseded. See the updated UI review for architecture, evidence and remaining enterprise/bundle limits.


## Initial connection approval contract — 2026-09-30

An admin connecting a server approves the initial tool list. Group/tool assignment remains the separate access decision. Later syncs quarantine new or changed definitions; unchanged tools keep their approval. Historical results above describe the earlier manual initial-approval behavior. `TestAdminConnectionApprovesInitialToolsAndStillRequiresGroupAccess` covers initial approval, default denial, a read-only group grant, unchanged sync, and later changed/new tool rejection. The existing admin API/OAuth test now verifies deny-before-grant and allow-after-group-grant without an intervening approval request.

## Durable configuration and group access regression

- Save a tool assignment to an empty local test group, restart with the same state directory, and verify both the stored assignment and effective permission inventory. Do not broaden real employee access for this test.
- Remove from group clears that connector's direct group assignments, existing server grant, and draft/published rules atomically. Other groups/connectors remain usable. Retrying stale drafts/publish must not restore removed access. Restart and verify removal remains effective.
- Force a configuration write failure: no unsaved state is visible, the admin API returns 503, and subsequent MCP authorization denies. Repair/restart loads the last committed state.
- Missing/wrong encryption key or malformed database must fail startup; do not silently create an empty permission database. A second store opening the same state directory must fail before serving stale permissions.
- UI: no whole-server assignment control, no per-tool Permissions dropdown, no new-chat header button. Inline Arguments expands the full JSON schema across the tool row. Repeated Ask AI icons are removed; advanced setup remains available in chat. Mobile/Tablet/Laptop controls use the shared split rail.


## Vault project SQL tools

- Describe the public schema with `query_workflow_db`; read groups and raw tool schemas. Attempt a write through the query tool; require rejection by the SQL guard and SQLite query-only mode.
- Through `mutate_workflow_db`, atomically edit a group, add a valid member and tool grant; verify live authorization and persistence after restart. Remove the grant and verify immediate denial.
- Reject foreign workspaces, arbitrary file paths, DDL, ATTACH, stacked SQL, writes to credentials/tool approvals/published policies, and quoted-CTE attempts to disguise a protected target.
- Reject an invalid member or fingerprint and verify that the entire batch rolls back with no memory change. Validation failures must not latch storage errors.
- Edit a draft and verify its automatic version increment. Reject stale publication and attempts to bypass a governed policy using direct grants.
- Delete a group and verify membership/grant cleanup, revoked policy history, and retained denial tombstones.
- Reject non-admin/currently-disabled callers and tools invoked outside the Vault chat project. Verify trusted actor/service identity and exact shared workflow tool names.
- Modify a metadata row outside the gateway owner in a test database; verify revision invalidation and denial until restart.
- In the local browser, ask the agent to query, rename an empty test group, verify the name, restore it, and query again. Verify actual SQL tool events and refreshed UI without permission changes.


## Console recovery and shared composer

- After a healthy Access load, make group/member/permission reads fail in the test backend. Require one pane-level warning and one Retry action, retained old data with a stale warning, and recovery of all sections after service restoration. Duplicate failures/callbacks must be deduplicated.
- Verify no group or per-tool Ask AI icons remain. Advanced permissions can still be described in chat.
- In the right-side Models panel, select a different provider/model and verify the next generation uses it in server runtime logs, then restore the original. The composer has no legacy model selector. Model availability uses product profile identity.


## Audit, PII and identity documentation update — 2026-10-03

| Scope | Result | Evidence |
|---|---|---|
| Async storage and PII enforcement | Pass | `go test -race ./internal/store ./internal/pii ./internal/mcpserver ./internal/admin ./cmd/server` in the gateway. Bounded admission, copied metadata, failed-batch retention/retry, unhealthy rejection, recovery without duplicates and shutdown flush errors are covered. |
| Latest builds | Pass | Gateway binary build and frontend `npx tsc -b`. |
| Live governed calls | Pass | Local OAuth Memory empty observations allowed; synthetic SSN blocked before execution. Audit contains PII action/type but no raw value. Five observed events survived a graceful gateway exit (code 0) and restart. Settings reported async, 24h, zero pending/failures and healthy writer. |
| Browser audit UI | Pass | SQLite / Async / 24h storage label and persisted events; readable dropdown filters, clear/reset and filtered Analysis verified in earlier browser checks. |
| Browser PII UI | Pass | Protection/Test/Reviews, synthetic email masking and no rule-creation controls. Existing focused PII test and typecheck passed. |
| Provider alternatives | Pass in earlier provider run | Real disposable ClickHouse 25.8 exercised filters, usage, TTL and replay deduplication; Off created no audit SQLite file. The ClickHouse container was removed after testing. |
| Retained SSO directory | Pass | Existing active-product-identity backend regression, TypeScript and 24 Surface/Groups tests passed after withdrawing the separate external-directory changes. In-app browser shows the shared SSO Users screen; no external account or grant was created. |
| Entire current product / Linux / external user sign-in | Not claimed | Historical broad host fixture failures/timeouts remain separately recorded. Platform SSO is the chosen identity source; individual MCP OAuth consent binding is unfinished. |

### Added regression cases

- Confirm durable is the installation default and async must be selected explicitly.
- Block the database writer: async admission returns without waiting for commit;
  queue overload returns an explicit error and never grows beyond its bound.
- Force a failed batch: accepted events remain queued, new writes are rejected
  while unhealthy, repair recovers without duplicates, failed shutdown reports error.
- Confirm graceful shutdown drains; document that crash/SIGKILL can lose memory events.
- Check provider settings, Off behavior, local retention <=24h and unsupported
  storage provider rejection. Query failures must not appear as empty logs.
- Verify raw values stay out of audit storage and that input/output PII enforcement
  remains synchronous. Rule management is absent from the UI, not the backend API.
- Verify People uses the shared platform directory, separate account creation is
  rejected, and unknown/disabled identities cannot be assigned to groups. Future
  MCP OAuth consent must resolve a verified platform SSO subject per person.

The isolated Apple M3 audit/PII benchmark results are in
[the current-state summary](../docs/design/vault-current-state.md#verification-completed).
They are not an end-to-end latency or RPS guarantee.


### Shared user editor follow-up (2026-10-03)

Historical result: Vault's product/role choices below were superseded by the
Vault-only user form described in the next section.

- Vault’s add-user form keeps Vault checked/locked and sends its product ID for
  Viewer/Editor/Creator users, including after successive successful additions.
  Admins retain implicit access to all products. Opening the page does not modify
  existing users.
- Global and workflow user pages reuse the same component and expose all four
  account roles. Relay appears only when hosted and enabled. Its product ID is
  returned by the server for workflow/Relay deployments.
- TypeScript and 21 tests across UsersAdminPanel, selectableProducts and
  GatewaySurface passed. Focused backend product registration, role mapping/writes
  and active-identity binding regressions passed. No live account was added.
- Rebuilt/restarted the product backend (`/api/health` 200). Verified in the
  in-app browser: four invitation roles, checked/disabled Vault, optional Relays
  checkbox and Relays navigation. Kept the preview open.

### Vault-only user creation (2026-10-03)

- Vault has no new-user platform role picker or product selectors. Existing
  users have no role/product editing or Code reviewer toggle in this view.
- Successive submissions send only `products=["mcp-gateway"]`, Viewer role and
  false admin/create/edit flags. Tests use mocked creation, with no live user
  or grant changes. The global editor retains its existing role/product behavior.
- TypeScript and 15 tests in UsersAdminPanel/GatewaySurface passed. Browser
  verification shows email, Vault-only badge and Add user, with no execution
  product controls. The browser remains open.
- This does not verify SSO-to-MCP OAuth consent or server-wide slot enforcement.
  Account APIs/global editors still need provisioning checks. Slot assignment
  remains a root-run deployment operation.

### Platform installation and individual Vault MCP OAuth — 2026-10-03

- Merged main `49a1e6761`, preserving the shared left product strip and removing
  the chat-input model selector. Provider-switch race regressions passed.
- Product OAuth regressions: two identities per client; valid/invalid PKCE;
  one-use codes; disabled account and removed entitlement; resource isolation;
  refresh; connection revocation; service/group/JWT/refresh/query-token rejection;
  management/consent isolation; stripped caller identity/cookies. Passed.
- Gateway runtime regressions: filtered inventory, live grant removal, unknown
  service rejection and actual user/client attribution. Gateway and shared OAuth
  race suites passed.
- Hosted auth-gateway discovery/challenge/token forwarding passed with and
  without its shared password gate. Six deployment helper tests passed, including
  repeat credentials, failed-bootstrap recovery and matching product allowlists.
- TypeScript passed; 31 focused frontend tests passed across Connect, groups,
  shared consent, safe login returns and integration layout. Legacy group keys
  are hidden by default in favor of individual platform sign-in.
- Linux amd64 gateway built. Disposable native managed bootstrap repeated twice
  produced one Platform group, zero static users and no resource grants.
- Real remote installation/SSO/Claude acceptance is still pending.
  The broader rootless suite retains its existing stale `PERSIST_MCP_STATE`
  shared-builder assertion, also absent in origin/main. It is not a passing gate.

- Browser showed the configured `http://127.0.0.1:18162/api/vault/mcp` endpoint
  and “Endpoint reachable — sign-in required.” Discovery metadata also returned
  the correct resource/issuer through Vite. Kept the browser open.
- Live protocol smoke traversed the actual frontend/product/private gateway:
  registration, PKCE, local-platform consent, token exchange, initialize/list
  and revoked-token rejection passed. Test grant was disconnected afterward.
  No upstream calls or group permission changes were made. The live inventory
  was empty: the granted OAuth Memory fixture needs upstream reauthorization,
  already recorded before these changes. Positive MCP call tests use disposable
  granted fixtures. Real provider/IdP acceptance is still pending.

## Multiple account connections

- Add two named catalog connections to the same OAuth MCP URL; verify independent connection/credential IDs and public namespaces, pending state and no group grants.
- Complete each sign-in with different accounts. Verify separately encrypted token/config/client-registration files and separate MCP sessions.
- Grant one account’s tools to a group. Confirm `tools/list` hides the other account and a granted `tools/call` reaches the correct upstream identity.
- Refresh or reauthorize one account; verify the other still works. A replacement login must wait for its own OAuth flow, not an old valid token.
- Suspend/delete one connection and verify calls fail for it while the other remains callable. A pending callback after logout/deletion must not restore usable credentials.
- Through chat: inspect providers, create named catalog connections, obtain sign-in links, inspect connection status, sync and explicitly disconnect one connection. Reject credential/path arguments and revoked administrators.
- Preserve a legacy provider-scoped connector through restart; adding a new account must not overwrite its credential or grant namespace.
- Verify UI catalog remains available after adding the first connection, both rows offer separate sign-in/settings, and pending rows do not imply tool discovery or group access.

Automated: `TestVaultConnectionOAuthAccountsAreIsolated`, `TestVaultConnectionChatRechecksAdministratorAccess`, `TestSetupToolCreatesSeparateNamedCatalogAccounts`, `TestSharedOAuthTwoConnectionsKeepAccountsAndGrantsSeparate`, `GatewayServersPanel.test.tsx`, `OAuthStatusBadge.test.tsx`. Real-provider acceptance still requires two administrator-selected provider accounts.


## PII removal — 2026-10-04

The PII scenarios and results above are historical; their scanner, UI, rule APIs,
review queue and benchmarks are removed from this release. Current regression gates:

- Open Vault and verify there is no PII navigation/section.
- Former PII REST and standalone-admin routes are absent.
- Granted text/image/structured MCP results pass through unchanged; ungranted
  calls still deny before upstream execution. Audit retains caller/MCP metadata,
  never raw payloads.
- Load a pre-removal SQLite snapshot, verify membership, credentials and grants
  survive, then mutate configuration and verify obsolete fields are omitted.
- Run gateway tests (including audit, OAuth and argument-policy suites), frontend
  typecheck and focused Vault tests.

Verification: gateway `go test -race ./...` passed; frontend typecheck and 42
focused group/audit/server/feedback tests passed. Restarted the local gateway
from the existing database: 3 groups and 4 connectors loaded, SQLite async audit
was healthy with zero pending writes, and former rule/review/admin-page endpoints
returned 404. In-app browser showed the existing 5 audit events and no PII section.


## Vault UI OAuth chat notification — 2026-10-04

- Start sign-in from a connected/pending named account row and include its Vault
  chat session ID in `/api/oauth/start`.
- Deliver a local fixture callback through the UI handler; verify a completed
  notification is stored in the target chat. Reconnect the same account and verify
  a new notification ID prevents event deduplication.
- Reject another user's session before starting the connector flow. Accept the
  caller's active, retained or restored product chat. Preserve callers without a
  chat ID.
- Render completed/failed notices without exposing internal agent instructions.
  Preserve hidden synthetic events for unrelated background agents.

Verification: focused server tests passed under `go test -race`, including the
UI-handler fixture callback, reconnect IDs and chat ownership tests. Frontend
OAuth badge/server/event rendering suites passed (18 tests); TypeScript passed.
Real Notion reauthorization was not performed for this regression test.


## Private MCP OAuth chat notification — 2026-10-04

- Crew/Code pass the product tab's session to the shared MCP panel. Workflow and
  Relay panels select their matching interactive workflow chat.
- Both private HTTP sign-in routes reject another user's notification session
  before creating a flow. Existing callers may omit the session.
- Code's `manage_my_mcp_servers` and builder `install_mcp_server` use the chat
  context when starting private OAuth.
- Local fixture callback errors reach that chat; repeated sign-ins get distinct
  IDs. Success notices render even when no resident agent can continue. Browser
  focus updates status without duplicating a callback notification, including
  custom OAuth servers or an unavailable catalog.

Verification: private callback tests exercise both HTTP routes and both agent
setup tools without an external provider. Focused server suites passed with the
race detector. Shared private MCP, event, badge and product/workflow layout suites
passed (25 tests), plus frontend TypeScript. Real-provider sign-in was not repeated.


## OAuth delivery through the shared chat queue — 2026-10-04

The earlier callback/event tests missed the idle-chat consumer and a missing
resident backend agent. Continuation now uses `sendWorkspacePaneMessageToChat()`
from a shared ChatArea consumer. The backend records the outcome and does not
start a competing model turn for private/named Vault connections.

- Restore an idle chat after restarting the backend; complete OAuth and verify
  an actual assistant reply, not just a stored `synthetic_turn_ready` event.
- Verify mount/focus and sign-in polling deliver the event to the exact session.
- Queue behind an active turn; persist callback receipts with queued messages;
  repeat fetch/remount and verify only one request. A new reconnect ID still runs.
- Ignore a late response after a tab changes sessions, and avoid replaying an
  outcome already injected by an older backend.

Verification: 33 focused frontend tests passed, including the actual shared queue
in the idle/restored and busy cases; TypeScript and focused server race tests
passed. In-app browser recovered the user's real Notion callback (44 discovered
tools) and displayed the assistant's connection confirmation without another
provider login. Group permissions stayed unchanged.

### Vault bridge recovery and MVP defaults — 2026-10-04

- Reproduced Muse interactive MCP loader failure in the protected project, while headless discovery listed seven governance tools.
- Reused the Crew/workflow private CLI runtime for Vault. Regression checks stable runtime identity, user/provider separation, authoritative project linkage and refusal of runtime state inside workspace data.
- Live browser chat called bridge read_skill, inspect_environment and inspect_tool for all 44 Notion tools; result: 26 read, 18 write, destructive annotations identified. No provider action or grant change.
- Inspection responses retain the actual schema and readOnlyHint/destructiveHint annotations.
- Listener-before-reconnect regression lets startup validation read a live authenticated connector route; named Notion OAuth recovery was verified across a graceful gateway restart without reauthorization.
- Installer uses SQLite by default for MVP local/server audit logs and supports explicitly disabling collection. Six installer tests and server/admin race tests passed.

### SQLite-only audit MVP — 2026-10-04

This supersedes earlier ClickHouse provider execution records. The MVP includes
only SQLite audit storage and collection-off mode. Regressions cover unsupported
provider rejection in runtime and installer, installer preserving existing files
on rejected settings, restart persistence, filters, retention, bounded async
admission, concurrent writes and graceful drain. ClickHouse delivery code and its
tests have been removed; it is deferred until after the MVP release.

### Platform-wide named private MCP connections — 2026-10-04

- Add two accounts of one catalog provider in the shared private MCP panel.
  Verify both display labels, that the provider remains available, and that the
  same named form is used in Crew, Code, workflow and Relay.
- Reauthorize/remove the intended row by stable connection name; preserve the
  other row and its login. Reuse an exact ID across projects without erasing tokens.
- Inspect builder schemas for catalog/label support and create accounts through
  both the general builder and Code setup tool.
- Verify independent sealed OAuth/client files, runtime names, cache identities,
  provider-group exclusion and exact project selection. Reject ambiguous provider
  aliases and foreign-user runtime IDs. Existing legacy accounts keep their paths.
- Provider sign-in chooses the actual account. A label is not verified identity;
  two-account real OAuth acceptance still requires user authorization.
