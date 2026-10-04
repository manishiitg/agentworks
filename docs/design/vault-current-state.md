# Vault: implementation status

Updated **2026-10-04** for the local work on `fix/gateway-review-1`. This document
summarizes the implementation and verification completed so far. Dated execution
records in the linked documents describe earlier versions; this summary describes
the current behavior. The product remains a single-user, loopback alpha.

## MVP scope — 2026-10-04

- Local and server installations use the same Vault functionality.
- Crew, Code, workflows and Relays retain their own private MCP connections and project secrets.
- Shared MCPs and secrets are managed in Vault and granted through the built-in Platform group. Projects explicitly select shared resources; connections and secret creation do not automatically grant use.
- MCP installation supports named accounts, OAuth and custom servers. Tool grants and argument conditions (exact equality or full-string regex) remain deterministic gateway checks on each call.
- Regex only constrains schema-visible string arguments; opaque IDs and query languages need upstream scope enforcement or an adapter.
- Audit logs use SQLite by default for both local and MVP server installs. SQLite is the only MVP audit storage backend; collection can be disabled. Asynchronous logging, bounded storage and local 24-hour retention remain available.
- PII detection, scanning, masking and custom PII rules are removed.

## PR #228 review follow-up — 2026-10-04

- Six Vault secret source/test files were present locally but excluded by the
  broad `*secret*` ignore rule. Explicit source-file exceptions restore the
  gateway metadata/grant API and host-side secret permission integration to
  clean checkouts. Stored credential files remain ignored.
- Audit capture replaces an oversized `json.Number` with a JSON string marker;
  truncation no longer causes serialization of the entire payload to fail.
- Policy clones prepare immutable regexes when saved and after SQLite restore.
  Tool calls reuse compiled expressions, retaining full-string matching and
  denial for invalid conditions. Edits cannot reuse a stale expression.

## Product and shared interface

- The visible name is **Vault**. Internal `caplayer` profile IDs, routes,
  environment variables and `Chats/CapLayer` paths remain compatible with saved data.
- Vault uses the full AgentWorks application: existing login, account roles,
  conversation history, streaming, cancellation, Chat tab, composer, split panes,
  workspace toolbar and Mobile/Tablet/Laptop layout controls.
- The shared navigation uses a thin left rail with the existing product icons.
  Global pages have return controls; runtime health is under the account menu.
  Relay is included through the main integration.
- Chat is on the left. The right workspace has Access, Connected MCPs,
  Available MCPs, Secrets, People, Audit, Models and Connect.
- The model selector is in the shared right-side Models panel. The obsolete
  composer model selector and extra new-chat button were removed.
- Shared `hideHeader` and unboxed settings options reduce repeated headings,
  subtitles and nested borders in Vault. Shared titles become larger when no
  subtitle is present. The blue, black and white theme stays consistent across
  products. Other callers retain their defaults.
- Repeated Ask AI icons and explanatory text were reduced. Detailed schemas,
  conditions, failure messages and credential-revocation guidance remain available.
- Normal local development opens `/` in the full app. A standalone build reuses
  the same surface and product account service; it has no separate Vault login.

## MCP connections and tools

Connected MCPs and Available MCPs are separate right-side sections, with search
and connection actions. Custom server setup starts through chat from the bottom
of Available MCPs. OAuth reuses the existing registration, sign-in, callback,
sealed credential store and refresh implementation. Credentials are entered in
secure UI controls rather than chat.

UI OAuth connections bind the callback to the initiating Vault chat. The backend
accepts only a chat owned by the signed-in user, including a restored product
conversation from that user's registry. Every sign-in attempt gets a unique
notification ID, so reconnects produce a fresh notice. Success/failure is recorded
as a compact chat event even if no resident agent is available after a restart;
the shared browser chat queue starts the follow-up through `sendWorkspacePaneMessageToChat()` and the normal product query contract. No retained backend agent is required.
Requests without a chat ID still connect but do not inject a chat turn. Connecting
a server does not assign group permissions.

The shared private MCP flow now also carries the initiating chat for Crew, Code,
workflow and Relay UI sign-ins and their agent setup tools. Private callbacks
record success/failure notices with a unique ID per attempt. The shared chat
consumer delivers them through the same browser queue as right-pane actions. HTTP routes verify chat ownership before starting OAuth. Workflow
and Relay panes select an interactive chat for the matching workflow; view-only,
scheduled and bot tabs are excluded. Missing chat IDs remain compatible for
callers without a conversation. Private sign-in does not grant Vault access.
Browser focus still refreshes connection state, but does not send a second chat
request for OAuth connections already handled by the callback (including custom
servers and unavailable catalogs). Non-OAuth and older callers retain the focus
notification fallback.

OAuth notices are fetched on chat mount and browser focus, with bounded polling
while a UI sign-in is pending. Returning from an agent-provided sign-in link also
watches for the delayed callback. Queue receipts are persisted with the tab config
so focus, remounts and reconnect polling cannot submit the same callback again.
A busy chat queues the notice behind its current turn. Older callbacks already
continued by the backend are detected and not replayed. The browser performs the
model continuation; a closed browser receives the stored notice when its chat is
opened, subject to normal event retention. Notices contain provider/status data,
not credentials or raw callback errors.

Live check on 2026-10-04: Notion sign-in at 10:06 succeeded and discovered 44 tools,
but after a backend restart its notification had no resident agent and was not
fetched by the idle chat. The shared consumer recovered that actual callback;
`sendWorkspacePaneMessageToChat()` submitted an automatic turn at 10:12 and the
assistant confirmed “Notion is connected to Vault. No group access assigned.”
This is an observed browser completion, not only an event-rendering test.

An administrator connecting a server approves its **initial tool definitions**.
It grants no user or group access. Later syncs preserve unchanged approvals and
quarantine new or changed definitions. Removing a connector clears its related
grants and governance state; recreating its namespace does not restore access.

One MCP connections UI is shared across products. Tool rows share names,
descriptions and a caret that opens the full-width JSON input schema. Vault's
connected-server and group views show tools; Crew, Code and workflow integration
views hide tool details. Open Vault shortcuts respect current product/admin access.

## Groups and deterministic permissions

- Access selects an existing group using visible group rows, with Users and
  Permissions subtabs. Group permissions stay inside the selected group's area.
  Creating groups and editing membership belong under People, not Access.
- Groups have optional persisted descriptions. Individual tool assignments and
  secret grants are explicit. Removing an MCP from a group clears that group's
  connector grants and related policies atomically without deleting the server.
- Installation and startup idempotently create **Platform**, a built-in group
  containing active platform users. It starts with no grants. Its description
  and resource grants are editable; its name and automatic membership are fixed.
- Advanced permissions support exact values or full-string RE2 conditions on
  explicit string argument paths. Saving validates and applies them immediately. Version checks reject stale edits.
- Published policies take precedence over older grants. Revocation retains deny
  tombstones; changed tool fingerprints require review. Schema validation and
  authorization run on every call, independently of the setup assistant.

Regex conditions only constrain resources when the actual tool contract exposes
and uses the complete resource identity. Free-form queries, opaque IDs and hidden
defaults need scoped upstream credentials or a trusted resource-checking adapter.
Regex alone does not enforce Grafana dashboard, namespace or pod boundaries.
The two Sentry approaches are documented in [SENTRY_ACCESS.md](../../mcp-gateway/SENTRY_ACCESS.md).

## Identity decision: platform SSO

The [Platform group implementation review](../reviews/vault-platform-group-review-2026-10-03.md)
verifies bootstrap, persistence, automatic membership and shared runtime grants.
RTS and Excellence now build and bootstrap a private Vault service and configure
the product proxy through the shared deployment helper. Startup repeats the
idempotent Platform initialization. Their existing authentication gateways
remain separate. See [server installation](vault-server-installation.md).

Vault uses the platform's SSO/account directory for both administrators and MCP
consumers. People > Users reuses the existing Users & access editor; there is no
separate external-user directory or email-only account creation flow. Group
membership binds active central account IDs to gateway authorization records.
Vault uses the shared editor's `vaultOnly` mode: new accounts always submit
`role=viewer`, `admin=false`, `can_create=false`, `can_edit=false` and
`products=["mcp-gateway"]`. The form shows email and a Vault-only badge, with no
platform role picker or choices for Goals, Relays, Crew or Code. Existing accounts'
roles/products are read-only in Vault, and the Code reviewer toggle is absent.
MCP read/write permissions and secret grants are configured through groups.
Global and workflow user pages retain the shared platform role/product editor.
Relay is included in
the server’s product inventory and shared product selector when hosted/enabled;
its workflow permissions continue to use the existing runtime.

### User accounts versus provisioned execution slots

The shared add-user HTTP endpoint writes an account to the platform directory;
it does not provision a Linux execution slot. The root-run
`deploy/common/provision-slots.sh adduser` explicitly performs both steps:
the platform `server add-user` command, followed by slot assignment, filesystem
ownership and isolation configuration. Excellence's deployment sets
`AGENTWORKS_SLOTS=on`; unassigned identities fail slot-aware execution. RTS's
build sets `optin` when slots are installed, retaining legacy execution for
unassigned users. These statements describe checked deployment source, not a
fresh inspection of the running remote hosts.

MCP-only consumers need a platform SSO identity and Vault group grants, not a
personal Linux execution slot. Vault-only invitations now prevent execution
product grants and global Admin creation through the Vault form. This is a UI and
submission restriction, not a new server-wide authorization gate: global/workflow
editors and the existing account API still do not check slot provisioning.
Slot deployments need read-only provisioning status and backend checks on
product/role changes that confer execution access. Root provisioning remains
outside the web process. The Admin role's implicit all-products access must be
covered by those checks too. Vault MCP consent itself uses the platform identity
as described below.

A person may use the Vault MCP endpoint from Claude without using Crew, Code or
workflows, but authenticates through the same platform identity provider. Product
access and administrator status remain separate from grants to MCP tools/secrets.
The product now publishes `/api/vault/mcp`. OAuth consent at `/oauth/vault`
uses the platform login and binds each grant to its verified account ID.
The grant uses a dedicated `vault:mcp` scope and token/store namespace; it cannot
access workflow APIs or the management console. Calls, initial token exchanges
and refreshes recheck the active directory account and Vault entitlement.
The gateway rechecks current groups and tool/argument policies on every call.
Disconnecting a client revokes its grant and token family. Audit records contain
the actual user and OAuth client IDs.

Managed services use `GATEWAY_AUTH_MODE=platform`. Their listener exposes only
private administration/runtime routes and health; it has no static `u1` OAuth
endpoint or token-entry console. The product replaces incoming identity headers
and forwards its private service credential plus the verified actor. Direct
local debugging mode retains its legacy static-user OAuth and group keys, which
are not accepted by the public platform endpoint or advertised in the shared UI.

Focused regressions exercise two different users, account disablement, consent,
PKCE, refresh, revocation, spoofed headers, group removal and audit attribution.
These tests and local preview checks do not establish a deployed Linux/IdP
end-to-end result or production capacity.

## Setup assistant

Vault uses the shared conversation runtime with a dedicated system prompt and
embedded access skill. It inspects actual tool schemas and annotations, preserves
unrelated grants, distinguishes read/write/destructive tools, and tells the user
whether a change was saved successfully.

| Tool | Implemented authority |
|---|---|
| `manage_vault_access` | Inspect inventory/schemas, connect a server without chat credentials, save and immediately apply validated advanced permissions. Connection does not grant group access. |
| `query_workflow_db` | Read bounded governance metadata and schemas from this Vault project's SQLite database. |
| `mutate_workflow_db` | Apply validated, atomic group, membership, simple tool-grant changes to persistent and live state. |
| `manage_vault_secret_access` | Grant or revoke use of an existing secret for a group, without revealing its value. |

Governance tools recheck the current product administrator. The assistant can
execute active approved Vault MCP tools through its administrative setup route,
but cannot retrieve secret values through governance tools, change central
account roles, or retrieve raw credentials. Full native CLI tools follow the
same platform confinement as other product builders; governance database edits
must use the governance APIs. SQL tools use the gateway owner; arbitrary physical SQLite edits
invalidate the live metadata revision instead of silently changing authorization.

## Private MCPs and shared Vault runtime

Normal connections default to **My MCPs**, private to the authenticated person.
Sharing a project does not share that person's external login. Explicit Vault
connections are centrally managed and require group grants.

```text
Crew / Code / workflow agent
  → AgentWorks MCP proxy and trusted user identity
  → Vault tools/list filtering and per-call permission checks
  → connected upstream MCP server
```

Native agents receive restricted, expiring delegation credentials rather than
upstream tokens or the deployment's service secret. Connection pools isolate
private users and Vault delegations. Each shared call checks current account,
group, tool approval, schema and resource conditions. Revocation is
checked on the next call even when a connection is warm.

Legacy globally shared connections require migration to Vault grants or private
reauthorization. A catalog entry or project selection is not an authorization
grant. See [MCP ownership](vault-mcp-ownership.md) for the enforcement paths.

## Persistence and credentials

| Data | Location and behavior |
|---|---|
| Configuration, identities, groups, grants, tool approvals, drafts, published policies, history | `<WORKSPACE_DOCS_PATH>/_users/default/Chats/CapLayer/db/gateway.sqlite` in the full local launcher. Standalone fallback: `GATEWAY_STATE_DIR/gateway.sqlite`. Automatically committed after mutations. |
| Authoritative configuration | AES-256-GCM encrypted snapshot in SQLite; normalized SQL metadata is readable within the private file. |
| Gateway configuration key | `GATEWAY_STATE_DIR/gateway.sqlite.key`, outside the chat project. Database/key permissions are `0600`. |
| Shared secret values | Existing encrypted host store at `_users/_system_global_secrets/secrets.json`; project SQLite stores names and grants only. |
| Private and shared upstream OAuth credentials | Product-owned sealed stores, including the private user's namespace. Gateway resolves current tokens through a service-only exact-URL broker. |
| Audit events | Separate configured provider/state files, described below. |

Configuration writes update committed storage and live enforcement together.
Storage failures restore the prior committed state and deny authorization until
repair/restart. Missing keys or unreadable state fail startup. Saved connectors
reconnect on startup while preserving access settings. One gateway process owns
each configuration database. Backups need the consistent database and its key.

## Secrets across products

Secrets use the shared Integrations UI. Vault manages platform values; Access
assigns **use** to groups. Runtime access is the intersection of current group
grants and explicit project selection. Missing selection grants nothing; lookup
failure cannot fall back to all globals. Project secrets retain precedence when
names collide. Authorized values are injected as `SECRET_<NAME>` after checks.

The UI has compact value rows, authorized copy and rotation icons, a prominent
Add secret button below the list, and group selection that displays assigned
secrets without replacing the whole panel during checkbox updates. Copy performs
a fresh authorized reveal. Group selectors and normal lists contain metadata only.

Rotation saves a replacement value; it does not rotate credentials at the
provider. Removing permission stops future resolution but cannot recall values
already copied or supplied to running processes. Revoke/rotate the credential at
its provider, save the replacement in Vault, and restart affected consumers.

There is **no general `get_secret` MCP tool or Vault CLI** in this implementation.
External MCP calls use server-held connection credentials. Writing secret values
to an external client's local file remains a proposed feature, not completed work.
See [Vault secrets](vault-secrets.md) for runtime coverage and migration details.

## Audit logs and async writes

Audit defaults to **Logs**; **Analysis** is a separate subtab. Filters use readable
user, MCP and result selections, with group, tool, calling app and permission
decision under More. Filters carry between subtabs; analysis loads on demand.
CSV/JSON export and refresh use the same filters. Calling app identifies the MCP
client, such as AgentWorks; it is separate from the upstream MCP server and user.
Group API keys attribute a call to a group/key rather than an individual human.

Events contain identity, MCP/tool, decision, outcome and timing metadata.
New events also contain bounded input arguments and output content, structured data and error indicators. Tool payloads may contain sensitive values. Transport headers, protocol metadata and configured credentials are excluded. Audit access and exports stay administrator-only and workspace-scoped. Historical events may lack payloads. Requests rejected before
tool dispatch are not tool-call events.

| Setting | Local default | Server default |
|---|---|---|
| `VAULT_AUDIT_PROVIDER` | `sqlite` | `sqlite` (MVP) |
| `VAULT_AUDIT_RETENTION` | `24h`; local values cannot exceed 24h | `24h` |
| `VAULT_AUDIT_WRITE_MODE` | `durable` | `durable` |
| `VAULT_AUDIT_MAX_MB` | `256` for SQLite pages | `256` for SQLite pages |

`VAULT_AUDIT_PROVIDER=off` disables collection/querying without deleting old
files. SQLite uses WAL, batched commits, indexed queries and retention cleanup.
Unsupported storage providers fail startup. ClickHouse is deferred until after
the MVP release.
Server provider defaults do not remove the alpha's public-exposure guard.

Opt-in `VAULT_AUDIT_WRITE_MODE=async` uses this path:

```text
MCP request → permission/schema/argument checks → upstream/result
            → copy metadata into bounded queue → return
background worker → batch SQLite commit
```

No worker is created per call. The queue holds 1,024 waiting events plus at most
256 in the current batch. Full queues return explicit errors. Failed batches
remain queued and retry with 50ms–5s backoff; new admissions are rejected while
the writer is unhealthy. Settings expose pending writes, write failures and health.
An already accepted asynchronous event cannot retroactively fail its caller.

SIGINT/SIGTERM stop requests, wait up to 45 seconds for active handlers, then
drain accepted events. A flush error is reported. Crash/SIGKILL/power loss can
lose uncommitted in-memory events. Durable mode waits for persistent admission;
Async reduces the request's storage wait, not total CPU/disk work. Full details: [gateway README](../../mcp-gateway/README.md#audit-storage).

## Verification completed

Local reference fixtures include the pinned MCP filesystem and memory servers
plus a synthetic OAuth Memory provider for registration, PKCE and refresh.
Filesystem access is limited to the test folder. See [local MCP setup](../../mcp-gateway/LOCAL_MCPS.md).
Real Notion/Asana authorization has not been demonstrated by these fixture tests.

Audit verification before the feature removal on **2026-10-03**:

- `go test -race ./internal/store ./internal/mcpserver ./internal/admin ./cmd/server` passed in `mcp-gateway`; frontend `npx tsc -b` and the gateway build passed.
- Tests cover nonblocking async admission during a blocked database write,
  metadata copying, bounded overload, retained/retried failed batches, rejection
  while unhealthy, recovery without duplicate events, flush errors, and restart.
- A granted local OAuth Memory empty-observations call succeeded. A synthetic
  payload check was exercised by the former scanner (since removed). Graceful
  shutdown exited with code 0; all five observed
  events remained after restart, with no pending writes or write failures.
- In-app browser showed **SQLite · Async · 24h retention** and the persisted
  events. The current preview explicitly enables async; installation defaults
  remain durable.
- Earlier provider checks exercised real disposable ClickHouse 25.8 for filters,
  aggregation, TTL and replay deduplication, and confirmed Off creates no audit DB.
- Earlier browser checks verified shared layout, server/group JSON schemas,
  audit filters/analysis. Focused frontend and backend regressions
  are recorded in the [test plan](../../mcp-gateway/TEST_PLAN.md) and [review](../reviews/caplayer-ui-review-2026-09-30.md).

Measured on Apple M3, 200 iterations per isolated stage:

| Stage | Mean | p95 | p99 |
|---|---:|---:|---:|
| Async audit admission | 0.199 µs | 0.208 µs | 0.292 µs |
| Durable audit admission | 5.01 ms | 6.23 ms | 6.58 ms |

These are audit admission benchmarks, not end-to-end latency or a supported RPS. No complete current platform suite or
running Linux deployment pass is claimed; earlier broad host tests had unrelated
catalog/Pulse failures and timeouts.

## Remaining work and release boundary

- The managed SSO endpoint and server installation hooks are implemented locally.
  Real Linux deployment and production IdP/client acceptance
  testing remain required. Direct service listeners must stay private; the public
  endpoint belongs to the platform product.
- Configuration uses full snapshots and rebuilt metadata projections with one
  process per database. Large deployments need shared incremental governance
  storage, cross-worker invalidation and workload-specific load tests.
- AUTH_SECRET rotation does not yet rekey all OAuth credential stores. Legacy
  plaintext migration failures/custom token-path coverage remain open findings.
- Async audit crash loss and secret-copy revocation limits are inherent to the
  currently selected mechanisms and must inform deployment choices.
- Private skills/company knowledgebase remain **v2**, not shipped features.

## Documentation map

- [Gateway setup, storage, limits and audit configuration](../../mcp-gateway/README.md)
- [Test plan and dated execution records](../../mcp-gateway/TEST_PLAN.md)
- [Local filesystem, memory and OAuth fixtures](../../mcp-gateway/LOCAL_MCPS.md)
- [Sentry resource-scoping approaches](../../mcp-gateway/SENTRY_ACCESS.md)
- [Private MCP ownership and Vault enforcement](vault-mcp-ownership.md)
- [Shared secrets, installation and revocation](vault-secrets.md)
- [Chronological UI and security review](../reviews/caplayer-ui-review-2026-09-30.md)

## Platform deployment and individual MCP OAuth follow-up — 2026-10-03

Merged `origin/main` at `49a1e6761` before applying these fixes. Shared deployment
helpers build the Linux Vault binary, render persistent private credentials and
systemd units, initialize Platform before activation, health-check startup and
recover service startup after an interrupted deployment. No grants are seeded.
MVP server audit defaults to SQLite, as local installations do. SQLite is the only MVP audit storage backend; `off` disables collection. These helpers select
async audit mode. The general local launcher still defaults to durable unless
explicitly configured otherwise.

The local preview now runs fresh product and managed gateway binaries.
Verification details and boundaries are recorded in the updated
[Platform review](../reviews/vault-platform-group-review-2026-10-03.md).

## Multiple accounts for one MCP provider (2026-10-04)

Vault now supports separately named catalog connections to the same provider and MCP URL, through both the shared UI and its chat builder.

Named private accounts are also supported by the shared Crew, Code, workflow and
Relay MCP panel and setup tools. Providers stay available after connection, each
account shows its label, and sign-in/removal target the exact connection name.
Vault connections remain shared through group permissions; private accounts use
the caller's private store. Both use the shared named-connection form and MCP
browser components.

### UI

1. Open **Available MCPs** and choose **Add connection**. Providers remain available after their first connection.
2. Name the connection, for example **Notion · Engineering** or **Notion · Sales**.
3. The new row appears in **Connected MCPs** with **Sign-in required**. Click its **Sign in** button and choose the intended provider account/workspace.
4. OAuth completion discovers that connection’s tools. Assign its tools to groups in **Access**; connection/sign-in never grants group access automatically.

Connection settings reauthorize only that row. Disconnect removes that connection and its permissions. Other connections to the same provider retain their own credentials, sessions and grants. Labels describe accounts chosen by the administrator; they are not verified account identities returned by providers. The provider controls account selection during authorization; use its account chooser or a separate browser session if it automatically selects an existing login.

### Chat builder

`manage_vault_access` supports:

| Operation | Arguments | Result |
| --- | --- | --- |
| `inspect_environment` | `{}` | Actual catalog providers, connectors, groups and tools |
| `connect_server` | `{provider, label, instance?}` | Create a named catalog connection; OAuth starts pending |
| `connect_server` | `{name, url, instance?}` | Existing custom-server flow; credentials stay outside chat |
| `sign_in_connection` | `{connection_id}` | Same OAuth flow as the UI; returns an authorization link or directs client-app setup to the secure form |
| `connection_status` | `{connection_id}` | Authentication/connection metadata, never tokens |
| `sync_connection` | `{connection_id}` | Rediscover this connection’s tools |
| `disconnect_connection` | `{connection_id}` | Remove the explicitly requested connection and its permissions |

The embedded system prompt and `vault-access` skill describe this lifecycle, independent account naming, provider account selection, completion verification and separate group assignments. Chat sign-in uses `PUBLIC_URL` for its callback and reports completion/failure back to its initiating chat. Administrator access is checked on every tool call and again before OAuth token exchange. Passwords, API keys and OAuth client secrets are never requested through chat.

### Storage and enforcement

- New OAuth connectors persist `OAuthCredentialID = connector.ID`. Their provider and instance form a distinct, stable tool namespace. An omitted instance gets a unique generated identifier for OAuth connections.
- Tokens, dynamic client registrations and OAuth metadata are encrypted by the existing platform credential sealer, in separate `_platform/vault_<connection-id>` files under the configured MCP token root. The token-root helpers use the same local/Linux deployment paths as the platform’s other encrypted MCP credentials.
- The service-only token broker receives the connection ID and checks the live connector, exact provider and configured upstream URL. Unknown/deleted/disabled IDs cannot reuse another account’s token. Refresh is serialized per connection.
- Sign-in suspends that connection’s previous MCP session; completion opens a fresh session for its new account. Initial definitions are approved only after successful discovery. Later changed/new definitions retain the normal review behavior. Group access remains separate.
- OAuth completion polling is bound to the connection ID **and** the newly started flow state, so an older valid token cannot complete a replacement login.
- Logout/removal cancels pending sign-in generations. The product removes that connection’s credential files; direct private-service removal also makes remaining orphaned credentials unusable because the broker requires a live connector.
- Existing connectors without `OAuthCredentialID` retain their legacy provider login and namespaces. No credentials or grants are silently migrated. Add a new named connection for a separately authenticated account.

### Verification

- Product race tests cover separate encrypted OAuth tokens and dynamic client registrations, refresh isolation, chat status/sign-in, cancelled-login protection, deleted/forged IDs and current administrator authorization. Existing provider OAuth tests remain green.
- Gateway race tests cover pending connection creation, distinct namespaces, independent discovery, actual MCP `tools/list`/`tools/call`, group isolation and continued operation of the other account after suspension/deletion.
- Frontend tests cover the shared naming form, providers remaining available, connection-specific OAuth start/polling and the existing Vault panels.
- Local in-app browser created two disposable Notion rows with separate IDs, namespaces and sign-in controls. Only those test rows were removed afterward; existing MCPs and permissions were preserved.
- Actual sign-in to two real Notion accounts has not been performed. OAuth isolation is exercised with a synthetic issuer and real MCP protocol test server.


## PII feature deferred — 2026-10-04

Removed the PII section, scanner, rule/review APIs and standalone admin pages,
review queues, persisted rule fields, and audit action/type fields. MCP payloads
pass through without PII masking, blocking or review. Authentication, tool grants,
approved schemas, resource argument conditions and audit logging
remain active. Audit now captures bounded tool inputs and outputs without reintroducing PII scanning.

Existing encrypted SQLite snapshots still load: legacy PII fields are ignored
and omitted on the next saved configuration mutation. Historical audit rows are
retained; obsolete fields are ignored when read. No configuration reset is needed.
PII can be designed again in a future release.

Removal verification: all gateway race tests, frontend typecheck and 42 focused
frontend tests passed. The local gateway was rebuilt and restarted with the
existing configuration; 3 groups and 4 connectors loaded. Audit stayed healthy
and the browser retained its 5 existing events. Removed endpoints return 404.

## Vault chat tool recovery — 2026-10-04

The api-bridge contained all governance tools, but Muse 1.4.2's interactive MCP
configuration loader failed when launched directly in the protected Vault project.
The agent therefore saw no bridge tools and tried its blocked native read_skill.
Vault now uses the same private CLI projection as Crew/workflows, linked to the
authoritative project. Native tools were disabled at that stage (superseded by profile v4 below); the governance tools, SQL
checks, OAuth isolation and tool fingerprints are unchanged. The prompt identifies
the bridge read_skill tool explicitly and instructs inventory requests to inspect
live tools instead of treating old assistant failures as current availability.

Inspection now returns stored tool annotations alongside the schema, description
and approved fingerprint. The existing chat inspected all 44 Notion tools and
returned 26 read tools and 18 write tools, with destructive hints called out.
No upstream action or group permission change was executed.

Named OAuth reconnect previously happened before the gateway listener started;
the product credential broker's live-connector check could not call back into it.
Reconnect now runs after the authenticated connector routes are serving. The same
Notion account rediscovered all 44 tools after a graceful gateway restart, without
another provider login. Focused server/admin race tests and six installation tests
passed; the private CLI projection test covers all five CLI providers and durable
project linkage without shared provider configuration.

## Shared runtime maintenance — 2026-10-04

Vault, Crew and workflow/Relay chats now call the single
linkedProjectCLIWorkingDir implementation for private provider configuration,
project linkage and stable runtime identity. The separate Vault setup helper was
removed. Existing Crew Run/Builder selection and workflow isolation settings stay
in their product policy adapters; the file preparation and provider checks are
shared. Code uses its coding workspace as its provider cwd; its MCP bridge,
provider adapters, tool admission and conversation machinery are the same shared
platform components. This layout difference is explicit workspace policy, not a
separate MCP integration.

All ChatArea consumers use the shared OAuth notification hook and
sendWorkspacePaneMessageToChat queue. Product-specific system prompts, skills,
governance tools and permission boundaries describe each product's job; transport,
OAuth storage/refresh, schema discovery and tool fingerprint validation are shared.

## SQLite-only MVP audit storage — 2026-10-04

ClickHouse audit storage, delivery spool helpers, installer settings and UI provider
labels have been removed. Local and server installations use SQLite by default;
`off` remains available to disable collection. Unsupported providers fail explicitly
rather than switching storage silently. Existing audit files are not deleted.
Async batching, durable write mode, retention and storage bounds remain supported.
Earlier ClickHouse test records in this document describe a superseded implementation.

## Named MCP accounts across products — 2026-10-04

- The shared private MCP adapter uses `McpNamedConnectionForm`, already used by
  Vault. Each catalog row offers Add connection; label submission uses the normal
  panel-to-chat function in Crew/Code, or the private API in workflow/Relay panes.
  Grouped legacy provider setup remains available for one shared provider login.
- Private accounts persist a display `label` and a generated stable `name`.
  Tokens/client registrations, cached sessions and tool namespaces stay distinct
  per user and connection. Named accounts cannot reuse the legacy provider-group
  OAuth token, including for grouped Google providers.
- `ensurePrivateMCP` is shared by project HTTP creation, Code setup and
  Crew/workflow/Relay builder setup. `install_mcp_server` and `add_mcp_server`
  accept `catalog` and `label`; Code's `manage_my_mcp_servers(connect)` accepts
  the same fields. Exact-name reuse preserves existing credentials. Labelled
  creation adds an account without replacing another.
- Inventory returns labels and exact names. Sign-in again, discovery, removal and
  project selection target exact names. Ambiguous provider aliases are rejected;
  runtime construction cannot collapse two accounts into one provider alias.
  Project selection of one account does not authorize another.
- Existing IDs, sealed credential paths and unlabelled provider-group logins
  remain unchanged. Provider account selection still occurs in its OAuth screen;
  a display label is not verified upstream identity.
- Verified: shared frontend tests cover all four private product names, multiple
  rows, reauthorization/removal targeting and OAuth chat notifications. Backend
  regressions cover two persisted accounts, separate encrypted client/token paths,
  independent runtime namespaces, project selection, foreign-user rejection,
  reuse and removing one account without breaking another. Live Crew UI form
  verified locally; real two-account provider OAuth requires user sign-in.


## Plugins and caller-scoped Vault inventory — 2026-10-04

Crew, Code, workflows and Relay reuse `ProjectPluginsPanel`. Integrations now has a Plugins tab. A shared **Integrations → Plugins** breadcrumb returns to the integration section picker. The only tab row is in the shared header and contains **Connected**, **Available**, **Secrets**, **Skills** and **Vault**. There is no third Connected/Available navigation level. Private connection setup and multiple named accounts use the same connection browser; project secrets and skills retain the existing shared editors.

Vault displays the authenticated user's groups, their permitted shared connections and secret names, including the built-in Platform group. The host's `/api/me/mcp/vault` calls the service-only gateway inventory with a server-owned actor identity. It does not accept a user/email override. Group rows include only the caller's memberships and currently visible tools; upstream URLs, credentials and secret values are excluded. Direct grants outside groups remain visible as Other access. Revoked project selections can be removed.

The shared builder `list_mcp_servers` and Code `manage_my_mcp_servers(action="list")` expose this same metadata in `vault`, `vault_groups` and `vault_secrets`. Product prompts and the integration reference instruct agents to inspect these fields before setup, use exact connection names, and never request secret values. Availability appears automatically based on live user grants; project MCP and secret selection remains explicit and durable. Selection never grants additional access: execution still enforces current tool grants, schema fingerprints and argument/regex rules. The secret runtime rechecks current grants before injecting selected values. Authentication remains the existing platform identity on local and server deployments.

Verification covers caller and group isolation, metadata-only secret responses, live revocation, both builder inventories, exact resource selection, viewer controls and the shared tab hierarchy.


### Vault builder chat: live MCP resource lookup

Vault profile v5 exposes the shared `list_mcp_servers` and `call_mcp_tool` through api-bridge alongside its governance/SQLite tools. The administrator builder discovers all active connected Vault MCPs, all groups and all secret metadata. It can search/fetch resources before drafting equality or regex restrictions without joining the target group or changing grants. Other users' private MCP connections remain private.

Setup authority is bound to the authenticated administrator's interactive Vault profile, fixed workspace and owned chat session. The bound tool context cannot be supplied by the model or HTTP caller. The host signs a connector/session/purpose delegation, separates pooled connections by that signed authority, and rechecks current administrator role, product access and session ownership on every proxied request. A separate service-only gateway builder route bypasses group grants and published argument restrictions for setup calls while retaining active-connector checks, approved fingerprints, schema validation and audit events with the real user and `agentworks-vault-builder` client.

Normal product and external MCP routes never acquire this authority. Their inventories and execution remain caller/group scoped, including when an administrator uses Crew, Code, Goals, workflows or Relays. Group grants are not changed by a setup lookup. Upstream mutations still require an explicit user request. Secret values remain in the encrypted host store: the Vault builder receives names and permission metadata, can manage assignments, and cannot retrieve values into chat through the Vault governance tools. Native CLI skill/file/search/shell tools are enabled through the shared full-tools runtime and its confinement policy.


### Vault native CLI tools — 2026-10-04

Vault requests `runtime.agent_tools.mode: full`, like the other product builders. Muse receives its full native toolset rather than the `mcp_only` PreToolUse hook that blocks `read_skill`, files and shell. The shared provider skill projection exposes `vault-access` to Muse's native `read_skill` as a project skill; the bridge reader remains an alternative with its own input schema. Native tools use the existing Seatbelt/Landlock policy, with the normal bridge-only fallback when confinement is unavailable. No Vault-specific hook bypass or provider fork is introduced. The profile version and definition fingerprint change relaunch the retained CLI while preserving the application's saved conversation. Connected MCP execution, SQL changes and secret assignment retain their existing gateway paths.

### Saved MCP tools after gateway restart — 2026-10-04

The live Vault builder test exposed a startup ordering failure: the gateway's one-time OAuth reconnect could run before the product credential broker was ready. Persisted Notion schemas remained visible in inventory, but their MCP handlers were absent, producing `tool not found` instead of a useful connection error.

The gateway now registers saved tool definitions before serving. An authorized call initializes and rediscovers a missing upstream under the same per-connector lock used by sync and reauthorization. It retries connection/discovery only, never a tool invocation. Temporary broker failures leave the connector eligible for recovery on the next call. Failed discovery drops the newly opened session. Current approval, fingerprint, identity, group and argument restrictions are checked before reconnect and again before invoking the tool; changed definitions stay quarantined. A connection failure records an upstream-error audit event and returns `upstream connection unavailable`. This shared path applies to Vault builder, product and external MCP callers without adding group grants.

Regression tests reproduce persisted inventory with a failed startup OAuth broker, successful recovery on a later call, denied callers making no reconnect attempt, changed-schema quarantine, unchanged memberships and audit coverage.

Live verification: submitted a read-only Notion fetch in the existing open Vault builder chat. Before the fix it returned the exact protocol `tool not found` error. After rebuilding/restarting the gateway, the same chat's `call_mcp_tool` completed successfully and returned the Task List database's canonical ID and data-source reference. No Notion content, group memberships, grants or secrets were changed. Gateway MCP, admin, policy and server tests passed.

### Vault tool branding and nullable policy arrays — 2026-10-04

The builder's exposed management tool is now `manage_vault_access`, its category is `vault`, and its projected setup skill is `vault-access`. Prompt references, tool policy and bridge allowlists use the new names. Profile v5 changes the definition fingerprint so a retained CLI refreshes its tool surface on the next turn. Internal profile/factory IDs, service routes and the existing chat/database folder remain compatible with saved installations.

Unrestricted policy rules previously serialized empty conditions as `null`, which crashed the review panel on `.length`. The shared frontend access API now normalizes absent/null package lists, rules and conditions into arrays for both the group tools and draft-review views. The backend clones saved policies with empty arrays, retaining the existing meaning of no argument restriction. Tests cover legacy null/missing conditions, null rules, unchanged restricted rules and no automatic publishing. The existing Task List draft was opened successfully in the local browser without altering or publishing it.

Live rename verification: after restarting the idle product and gateway, the existing builder chat successfully loaded `vault-access` with Muse's native skill reader. A focused `inspect_environment` call to `manage_vault_access` through the configured shared API bridge returned `success:true`. The existing draft review rendered its regex, equality and unrestricted rules without crashing. Verification made no permission or connection changes. Frontend regression tests (16), the TypeScript build, focused product tests and gateway access/admin/policy/MCP tests passed.

### Per-server group access count — 2026-10-04

Each MCP row in group permissions shows `allowed/total tools` using the backend's effective group-permission decisions. Published conditional access counts as allowed; unpublished draft rules do not add to the count. A tooltip explains the count. Ordinary checkboxes save live grants immediately, while builder-created argument/regex policies are saved as drafts for simulation/review and become active only when explicitly published. Edits to a published policy remain drafts until publishing replaces its live version.


## Immediate regex permissions (2026-10-04)

Scoped equality and regex permissions now save and apply in one gateway configuration transaction through `manage_vault_access` → `save_permissions` or POST `/api/admin/access/packages`. There is no draft, simulation or publish UI/API. This supersedes the historical draft workflow described above. Existing pending drafts are retained only as inactive compatibility data and are not automatically applied.

Saving validates the group, active connector, approved tool fingerprint and explicit string schema paths before activating the conditions. Current versions prevent concurrent edits from overwriting one another. Configuration and the `save_permissions` actor event persist together in the project SQLite database. Runtime calls immediately enforce full-string regex/equality matches; missing or invalid arguments fail closed. Saved restrictions appear under the group’s tool, and the server keeps its allowed/total count.

SQL remains available for groups, memberships and simple grants. Advanced rules are edited through the validated save operation; `permission_drafts` is no longer exposed as a mutable SQL table. Group access enforcement for other products and the dedicated Vault administrator setup authority are unchanged.

Allowed tools appear first within each MCP. Saved regex and equality conditions appear directly under their tool without a separate policy review box.

Audit and Vault MCP endpoint are product-specific entries in the shared left navigation. They open full-width pages with Back to Vault; Audit defaults to Logs and keeps Analysis as a subtab. They are removed from the right workspace toolbar. The builder chat remains mounted while these pages are open.

Navigation placement: Global Monitor stays at the top below the product switcher. Audit and MCP endpoint sit in the bottom action stack with Providers, MCP clients and Users; one divider separates Vault pages from platform actions.

The MCP endpoint page uses the same single-row page header and shared `CliMcpSetupPanel` as platform MCP connection setup. Local-client commands/configurations use the Vault endpoint and a separate `vault` server name. OAuth clients and revocation use `/api/oauth/vault/connections`. Hosted app instructions explain public URL requirements. Workflow skill/plugin downloads are omitted for Vault. The Send test request action is removed.

All users use the same Vault MCP endpoint URL and authenticate separately with their own platform account. Each call applies that user’s current user/group permissions; the endpoint is not a shared credential. This is explained in the setup UI.

Audit Logs now shows readable MCP/tool names and one result badge (Success, Blocked, Failed). User and duration remain in the main row. Expand a row to see groups, calling app, permission decision, outcome, full tool ID and error text. Group-key calls explicitly identify the group credential rather than inventing a user. Filters, refresh and export share one toolbar; storage details stay in a tooltip and the visible note explains retention. Analysis remains separate.

## Access groups and payload audit (2026-10-04)

- **Access groups** opens a list without search. Selecting a group replaces the list with a back button and independent **Users**, **MCPs**, **Secrets** tabs. Name/description fields are always editable; Save/Cancel appear for changes. Platform keeps its immutable name.
- The shared MCP header shows original provider branding (including Notion), **Tools 3/44**, and a regex-condition count where present. Allowed tools appear first. Human-readable restrictions appear beneath each tool; the actual expression is in a disclosure.
- New/edited regex conditions require nonblank `description` of at most 500 characters. Both API and builder save validate it. The prompt, projected skill and fallback schema instruct the builder to supply it. Existing rules without descriptions keep working; editing requires explanations. Description text never controls authorization.
- Audit stores detached bounded **Input** and **Output** JSON. Output includes content, structured output and error indicator, excluding protocol metadata/transport headers/configured credentials. Payloads themselves may contain sensitive values. Denied calls capture attempted arguments but no upstream output. Transport failures retain safe error metadata.
- Capture limits string copies, nodes and nesting with a 64 KiB ceiling per payload and explicit truncation flags. Bounded capture consumes CPU at admission; async mode performs SQLite I/O in the queue worker. Off skips capture. Retention, database size, shutdown flushing and administrator/workspace isolation remain intact. No PII scanning is added.
- Expand a call to open **Input arguments** and **Tool output** JSON. Earlier calls show **Not recorded for this call**; denied output says **Tool was not called**. CSV and JSON exports include payloads and truncation indicators.

## Crew live regex verification (2026-10-04)

The local account (`default`) was added to **local platform test** and, at the user's request, remains a member permanently. The existing Crew builder selected **notion - manish 1**; **notion - manish 2** remained unselected. No grants, regex conditions, secrets or Notion content were changed.

The generated per-tool API paths normalize hyphens to underscores. This exposed a routing mismatch before requests could reach Vault. The shared product bridge now resolves forward-normalized connection/tool names against the caller's live authorized inventory, then uses the original names. Project selection still checks the exact connection ID. Ambiguous aliases, absent selection and revoked inventory fail closed; names are not reverse-guessed by replacing underscores.

The actual Crew builder made three read-only calls through the ordinary `agentworks` runtime, with user `default` and connection `c-f31bdfa1707d322e26a77298f6e868cf`:

| Call | Gateway result | Audit time (UTC) |
| --- | --- | --- |
| `notion-fetch` with Task List ID `487025c3-8b29-494a-a48f-f03da00fac90` | Allow / OK | 09:12:23.223257 |
| `notion-fetch` with ID `00000000-0000-4000-8000-000000000001` | Deny: arguments outside published access package | 09:12:24.558403 |
| `notion-get-users` with `{}` | Deny: no grant for tool | 09:12:24.750175 |

The allowed call recorded input and output; both denied calls recorded attempted input without upstream output. HTTP 200 is the tool transport envelope and does not imply permission was granted. These results used group permissions, not Vault administrator setup authority. Focused product regression tests and the backend build passed.

This test used the connected Codex CLI temporarily because Muse did not load its MCP bridge tools. The original Muse model and Max reasoning were restored afterward. Muse MCP loading remains unresolved. This verifies the fetch-ID regex and tool grant for **manish 1** only; it does not verify the search equality rule or restrict **manish 2**, which has separate grants without this regex.

### Integration with current main for deployment testing (2026-10-04)

The merge retains main's ownership registry, explicit CLI run-as identity and Crew migration to `Crew/<id>`, including native session continuity. Product manifests declare main's existing `native-subagents` guidance. Workflow MCP registration now obeys its manifest tool admission so the Vault call wrapper does not silently widen the workflow surface. Gateway end-to-end fixtures explicitly include approved tool fingerprints. Main's pinned provider version and its module checksums are retained.

Verification after integration: the gateway Go suite, focused Vault/Crew/migration/product-surface tests, linked runtime tests, 35 Vault UI tests, TypeScript checking and 16 installer/deployment transport tests passed. Confida deployment acceptance remains a separate server test; this merge does not verify or resolve the local Muse MCP-loading issue.
