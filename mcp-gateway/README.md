# Vault MCP Gateway (alpha)

For the complete current implementation summary, see [Vault implementation status](../docs/design/vault-current-state.md).

For the proposed centrally managed Sentry access model, see [SENTRY_ACCESS.md](SENTRY_ACCESS.md). It covers Sentry-scoped MCP connections and Vault argument policies.

Vault supports a managed platform deployment and a direct local debugging mode.
Managed installations keep this service private with `GATEWAY_AUTH_MODE=platform`
and publish the product's `/api/vault/mcp` endpoint with individual platform OAuth.
The direct local mode retains static-human consent and must stay on loopback; do
not expose that listener through a tunnel or reverse proxy. Configuration still
supports one service process per database. See
[server installation](../docs/design/vault-server-installation.md).

## Start locally

Run the AgentWorks launcher with the gateway enabled, or start `LOCAL_MODE=true go run ./cmd/server` from this directory. The gateway generates a fresh admin token on each start in `<GATEWAY_STATE_DIR>/admin-token` (default `./var/admin-token`, mode `0600`). The launcher prints the file path. The token is a backend service credential. Configure the product API with `CAPLAYER_SERVICE_URL` (the gateway origin) and `CAPLAYER_SERVICE_TOKEN_FILE` (that token file). Vault uses the existing product login; users never enter this gateway secret in the React console. Set `GATEWAY_AUTH_MODE=platform` and `GATEWAY_PRODUCT_URL` to use the product OAuth endpoint locally; `GATEWAY_HUMAN_TOKEN_FILE` can supply a persistent private service credential. An explicitly supplied `GATEWAY_HUMAN_TOKEN` must be unique and at least 32 characters; the old `GATEWAY_LOCAL_ADMIN=1` bypass is rejected.

Private-network upstream MCP servers are disabled by default. Set `GATEWAY_ALLOW_PRIVATE_UPSTREAMS=1` only when that access is intentional. OAuth providers reuse the shared product sign-in and token refresh service (configuration below). The gateway does not store their OAuth tokens.
Upstream URLs with query parameters are rejected so credentials cannot be stored in connector URLs or returned by the admin API.

## Reuse upstream MCP OAuth

The server directory uses the same `OAuthStatusBadge`, client registration, OAuth callback, private token store and refresh manager as the other products. An existing Vault-scoped platform sign-in is reused when connecting a provider. Normal product connections use the person’s private account; sharing is explicit through Vault. Providers without dynamic client registration use the existing secure client ID/secret dialog. Reauthorization is available under the connected server's Connection settings.

Configure the gateway with:

```sh
GATEWAY_PRODUCT_URL=http://127.0.0.1:18161
GATEWAY_CATALOG_PATH=/absolute/path/to/agent_go/configs/mcp_servers_clean.json
```

Use the same MCP configuration for the product server (`--mcp-config`) and gateway catalog. This prevents drift between provider names and URLs. `GATEWAY_CATALOG_PATH` is optional for the embedded catalog; it exports only names, URLs and whether OAuth is required. The product still owns all OAuth settings and secrets.

The product's `CAPLAYER_SERVICE_TOKEN[_FILE]` must match the gateway service token. `GATEWAY_PRODUCT_URL` is a fixed HTTPS or loopback origin. Configure `PUBLIC_URL` on the product API for its public OAuth callback (or the frontend origin when `/api` is proxied).

Each upstream HTTP request resolves its current access token through the service-only `POST /internal/caplayer/oauth-token` broker. The broker accepts only the service credential, rejects browser origins, binds credentials to the exact configured provider URL, and returns `Cache-Control: no-store`. The existing OAuth manager refreshes expired tokens. Browser JWTs cannot call this broker, and tokens never appear in gateway inventory or chat. Logout removes the shared token, so subsequent gateway requests fail authorization.

Connecting approves initial tool definitions; users still require separate group grants. Reauthorization and refresh do not grant more tools or change published policies. Vault uses one centrally managed platform identity per provider; separate accounts per connector instance require additional credential identities. The consumer-facing gateway OAuth endpoint remains separate from upstream provider sign-in.

## Shared accounts and roles

The embedded and standalone React consoles use `AuthWrapper`, the existing account menu, and the full product's Users & access editor. Local single-user mode signs in automatically as the local administrator. Multi-user mode uses the deployment's existing login providers and directory.

Management calls go to the product API at `/api/caplayer/api/admin/*`. The product API validates its JWT and current administrator/product permissions before forwarding to the fixed `CAPLAYER_SERVICE_URL`; it supplies the private service credential from `CAPLAYER_SERVICE_TOKEN_FILE` (or `CAPLAYER_SERVICE_TOKEN`). Browser JWTs, cookies and identity headers are not forwarded to the gateway. Disabled users and role changes follow the existing account middleware and directory cache. A gateway service credential failure returns a deployment error, without triggering another product login.

The People → Users tab reuses the shared account editor in Vault-only mode. New users receive only `mcp-gateway` access with the Viewer platform role; the form has no other product or platform-role choices. Existing account roles/products are read-only in Vault. Configure MCP and secret permissions through groups. Group membership selectors use active directory IDs; assigning a group binds that central identity to a gateway user record. These bindings have no separate passwords or account roles. MCP consumers use the same platform SSO identity even when they only use Vault from Claude; each public MCP OAuth grant now binds to that verified account. Account creation does not provision Linux execution slots; RTS/Excellence execution access still requires the root provisioning scripts, and server-wide slot checks on account grants remain pending. The legacy `/admin` token console remains an internal local debugging interface; keep the gateway bound to loopback behind the product API.

Access is organized by existing group, with Users and Permissions tabs. Choose a visible group row; create, rename and manage membership under People → Groups. Expand an MCP to assign tools and see saved conditions under each tool. Regex/equality permissions save and apply immediately through chat, with schema, fingerprint and version validation. There is no draft or publish step. Tool indicators use effective runtime authorization.

For standalone hosting, configure the product API's CORS and OAuth return URL for that frontend, or serve both behind the same origin. A separate UI deployment reuses the account service; it does not introduce another identity system. Do not put the service token in frontend runtime configuration.

Managed MCP client consent uses the shared platform login at `/oauth/vault`. The public endpoint accepts only its dedicated Vault OAuth access tokens. Account disablement, removed Vault entitlement and disconnected clients deny subsequent calls and refresh. Current group policy is evaluated for each call; the actual account and client IDs appear in audit. Group keys are legacy local-only credentials and are hidden in the shared UI. Configuration persists in SQLite. Real Linux/IdP acceptance testing and operational/load verification remain deployment gates.

## Local development in the shared app

For simple offline integration tests, use the official filesystem and memory reference servers via the [local MCP setup](LOCAL_MCPS.md).

Run the normal frontend (`npm run dev`) with the product API configured as usual. Open the frontend origin at `/` and choose Vault in the existing product picker. Set `gatewayUrl` in the public runtime config and configure `CAPLAYER_SERVICE_URL` plus the private service credential on the product server. Vault runs inside the same application, login and chat runtime as Goals, Crew and Code; local development does not need `caplayer.html` or a separate frontend process.

## Standalone Vault console

Run `npm run build:caplayer` in `frontend/`, then set `CAPLAYER_STATIC_DIR` to the absolute `frontend/dist-caplayer` path and `CAPLAYER_PRODUCT_API_URL` to the existing product account/API origin when starting the gateway. Open the gateway origin in a browser. The standalone entry renders the same `GatewaySurface` as the embedded product, with the shared product picker, product header, conversation transcript, and workspace toolbar used by Goals, Crew, and Code. It only lists Vault in the picker because this independent deployment does not host the other products. The embedded AgentWorks entry lists the products available to that user.

Vault chat uses the shared product conversation runtime, providers, model picker, streaming, cancellation and durable history. Configure providers through the existing product Providers UI; choose the assistant model in the right-side Models panel. The server registers the `caplayer` profile when `CAPLAYER_SERVICE_URL` is configured and the `mcp-gateway` product is enabled. Its four governance tools inspect inventory/schemas, connect servers, query project metadata, apply validated group/membership/simple-grant mutations, save advanced drafts, and grant/revoke secret use. The administrator builder can call connected MCPs for setup lookups through its dedicated shared bridge authority; it cannot retrieve credential values. See the current implementation summary for each tool’s authority. Every call rechecks the user's current administrator role; service credentials remain on the server.

The gateway's legacy `/api/admin/setup/chat` endpoint remains an internal diagnostic API for Chat Completions compatible deployments (`CAPLAYER_AGENT_API_URL`, `CAPLAYER_AGENT_MODEL`, optional `CAPLAYER_AGENT_MODELS` and `CAPLAYER_AGENT_API_KEY`). The React product no longer uses that transport or its model/context limits.

Admins can add a custom MCP server with a centrally managed bearer token and rotate it later. The token is sent to that upstream during MCP requests and is never returned in connector API responses. Bearer credentials persist in the encrypted configuration snapshot. Upstream OAuth connections reuse the product token service described above.

An administrator connecting an MCP approves its initial discovered tool definitions. This does not grant access to any user or group. Later syncs retain unchanged approvals and quarantine newly introduced or changed tool definitions for review.

Saved permissions bind a group to approved tool fingerprints. Each rule may require exact equality or full-string RE2 matches on explicit string argument paths. Saving applies validated conditions immediately. Existing governing policies take precedence over older grants; revocation retains denial tombstones. Changed fingerprints deny calls until permissions are updated. Schema and argument checks run before upstream forwarding without invoking a model.

Argument filters are only appropriate when an audited connector contract proves that the named argument fully identifies the target resource. Query languages, free text, opaque issue IDs, and tools with hidden defaults require upstream scoped credentials or a trusted adapter that resolves and checks resources. A regex alone cannot enforce Grafana namespace or pod boundaries.

## Configuration persistence

The full local launcher sets `GATEWAY_WORKSPACE_DIR` to the Vault chat project and stores configuration at `Chats/CapLayer/db/gateway.sqlite` (physical local path: `<WORKSPACE_DOCS_PATH>/_users/default/Chats/CapLayer/db/gateway.sqlite`). An explicit `GATEWAY_WORKSPACE_DIR` overrides this project location. Standalone launches without a workspace keep `GATEWAY_STATE_DIR/gateway.sqlite` (default `./var/gateway.sqlite`). Existing state-directory configuration migrates using SQLite when the project database does not yet exist; migration refuses to copy an active gateway. Groups, users, membership, direct and group tool assignments, existing server grants, connector configuration and approvals, access drafts, published policies, revocation tombstones, policy history, and API key hashes save automatically after each configuration mutation. A separate final JSON file is not required.

The SQLite row contains a versioned, AES-256-GCM encrypted configuration snapshot. The encryption key remains at `GATEWAY_STATE_DIR/gateway.sqlite.key`, outside the chat project; database and key have mode `0600`. Service tokens and the MCP OAuth database also remain in the backend state directory. Back up the database **and key** using a SQLite-consistent backup, or stop the service before copying them (include any remaining WAL files). Keep these files on a persistent volume outside disposable checkout/temp directories. Upstream bearer credentials are encrypted in this snapshot; shared OAuth refresh credentials remain in the product token store.

Configuration writes finish before readers see the new state and before an admin API success response. Failed writes restore the previous committed configuration, return HTTP 503 through the admin API, and deny MCP authorization until storage is repaired and the gateway restarted. A missing/wrong key or unreadable configuration fails startup instead of resetting permissions. A SQLite lock database rejects a second gateway instance using the same database; this release supports one gateway process per database.

Saved active connectors reconnect on startup. Tool discovery preserves unchanged approvals, quarantines changed definitions, and keeps saved access settings when an upstream cannot reconnect. This is a local/single-process persistence implementation, with full snapshots per mutation; a large multi-worker deployment should use a normalized shared database and cross-worker authorization invalidation.

The chat agent exposes the same `query_workflow_db` and `mutate_workflow_db` definitions used by Crew, alongside `manage_vault_access` and `manage_vault_secret_access`. SQL execution targets this project's database through the gateway storage owner; callers cannot choose another file or workspace. Read queries use SQLite `query_only`, a single-statement guard, a five-second timeout, and bounded results. Mutations accept one statement or an atomic batch of up to 20 statements.

Readable tables are `workspaces`, `users`, `groups`, `group_members`, `connectors`, `tools`, `user_tool_grants`, `group_tool_grants`, `group_server_grants`, `published_permissions`, `policy_history`, `vault_secrets`, and `group_secret_grants`. These metadata tables are plaintext within the private SQLite file. Credentials and API key hashes remain in the encrypted snapshot; the agent never receives its decryption key. `tools.input_schema` and saved rules contain JSON directly.

Writable SQL tables are `groups`, `group_members`, `user_tool_grants` and `group_tool_grants`. Account roles remain in the central directory. SQL changes validate references and update persisted configuration and runtime in one transaction. Advanced rules use `save_permissions`, which checks approved fingerprints, schemas and current versions before immediate activation. New grants cannot bypass governing policies. Removing a group revokes its saved policies and retains denial tombstones.

The tools recheck the current product administrator on each call. Direct external writes to the database are unsupported: metadata triggers change its revision, causing the gateway to deny authorization until restart restores the last validated snapshot. Use the registered SQL tools so edits reach both persistent storage and live enforcement. Metadata projections currently rebuild per configuration mutation; this local alpha still supports one process, and needs incremental shared storage for a large deployment.

## Current limits

- Direct local debugging OAuth still maps to one local human. Managed installations use individual platform OAuth through `/api/vault/mcp`.
- Configuration and policy history persist in SQLite with one writer. Production rollout still needs real IdP/client acceptance, operational credential management and capacity tests. The private service exposure guard remains in both modes.
- Runtime call audits use the configured provider and durable/async write mode described below.
- The bundled MCP SDK's `ListTools` method follows upstream `NextCursor` pages automatically.

## Review fixes in this branch

Connector deletion now removes its tool grants, group-server grants and version history so a new connector cannot inherit them. The admin console requires a secret on loopback, and the standalone admin cookie contains a short-lived session ID instead of the master token. Connector add, resync, and removal are serialized to prevent stale sessions from returning after deletion.

## Audit storage

Call metadata (user/group/client, MCP server and tool, decision, outcome and timing) uses a separate provider from permission configuration. New events also record bounded input arguments and output content, structured data and error indicators. Tool payloads may contain sensitive values; audit viewing and exports remain administrator-only and workspace-scoped. Transport headers, protocol metadata and configured connector credentials are excluded. Historical events may lack payloads. Every audit/usage/export endpoint uses
the configured provider. Query failures return 503; they never appear as empty history.

| Setting | Local (`LOCAL_MODE=true`) | Server (otherwise) |
|---|---|---|
| Default provider | SQLite | SQLite (MVP default) |
| Default retention | Rolling 24 hours | Rolling 24 hours |
| Default write mode | Durable | Durable |
| Disable collection | `VAULT_AUDIT_PROVIDER=off` | Same explicit setting |

SQLite is the only audit storage backend in the MVP. Set
`VAULT_AUDIT_PROVIDER=sqlite` (default), or `off` to disable collection.
The local launcher sets `LOCAL_MODE=true`, including gateway-only launches.
Standalone local launches must set it explicitly. Unsupported provider choices
fail startup. ClickHouse is deferred until after the MVP release.
Turning auditing off stops collection and querying, but does **not** delete earlier
stored data. Retention cleanup resumes when that provider is enabled again.

SQLite lives at `GATEWAY_STATE_DIR/audit.sqlite`, outside the agent-editable chat
folder. It uses WAL and FULL synchronous commits, a single writer that groups
concurrent calls into transactions (up to 256 events or 5ms), and time/user/MCP/tool
indexes. In the default durable mode, a write is acknowledged only after its transaction commits. Cleanup runs
at startup and every five minutes; every query and usage aggregate also applies
its retention cutoff, hiding expired rows immediately. Cleanup uses bounded deletes,
WAL checkpoints and incremental vacuum to reuse/reclaim space. A stopped process
cannot clean files; expired rows are removed on the next startup.

`VAULT_AUDIT_RETENTION=24h` changes retention (minimum 1m, maximum 8760h).
Local mode rejects values above 24h. `VAULT_AUDIT_MAX_MB=256` bounds SQLite database
pages; indexes count toward this limit. WAL
and lock files add bounded operational overhead. A full disk/queue produces an
explicit tool error rather than dropping accepted events. This cap is a protection,
not a promise that a 24-hour history at any request rate fits on disk.

### Async audit writes

Set `VAULT_AUDIT_WRITE_MODE=async` to return from logging immediately after admission
to a bounded in-memory queue (1,024 waiting events plus at most 256 being written).
One background worker writes batches to SQLite. No goroutine is created per call.
MCP calls do not wait for the batch timer or SQLite commit in this mode.

A full queue returns an explicit tool error. A failed SQLite batch remains in memory
and retries with backoff from 50ms to 5s; new admissions are rejected while the writer
is unhealthy. A failed event already accepted asynchronously cannot retroactively
change its caller's successful response. `/api/admin/audit/settings` exposes
`write_mode`, `pending_writes`, `write_failures`, and `write_healthy` for monitoring.

SIGINT/SIGTERM stop incoming requests, wait up to 45 seconds for active handlers,
then drain accepted audit events. Give deployments at least 60 seconds of termination
grace. A shutdown flush error is reported rather than claiming success. A crash,
SIGKILL, or power loss can lose events still in memory. Set
`VAULT_AUDIT_WRITE_MODE=durable` when calls must wait for persistent audit admission.
Standalone starts default to durable mode. The shared server installer selects
async mode unless explicitly configured otherwise.

Run bounded caller-latency benchmarks with:

```sh
go test ./internal/store -run '^$' -bench 'BenchmarkAuditAdmission$' -benchtime=200x -count=1
```

These report p95/p99 for isolated audit admission, not
end-to-end MCP latency or a supported request-rate limit.

Local and server installations use the same SQLite backend:

```sh
VAULT_AUDIT_PROVIDER=sqlite
VAULT_AUDIT_RETENTION=24h
VAULT_AUDIT_MAX_MB=256
```

Audit events persist at `GATEWAY_STATE_DIR/audit.sqlite`. Give each Vault instance
private persistent storage. No additional database service is required.

Audit errors are surfaced to callers. A persistence error after upstream execution
cannot undo that execution; the error explicitly warns against automatic retry.
This records completed attempts, not a distributed transaction with the upstream.
Unknown tool names and requests rejected before tool dispatch are transport errors,
not tool-call audit events. Group API keys identify the group/key, not an individual.
Use personal OAuth credentials when individual attribution is required.

These are throughput-oriented implementations, not a claim of a measured RPS limit.
Load-test the intended rate, retention, event sizes and storage hardware before rollout.


### PII deferred

PII scanning, masking/blocking, custom rules, reviews and UI/API surfaces were
removed on 2026-10-04. Tool payloads pass through unchanged after normal permission,
schema and argument-condition checks. Audit captures bounded input/output snapshots without scanning or modifying forwarded payloads. Older configuration snapshots load without their removed
PII fields; the next mutation saves only the current configuration. Historical audit
rows remain available.

### Vault navigation

Audit logs and the Vault MCP endpoint open as full-width pages from the bottom of the shared left navigation. Each has Back to Vault; the chat remains mounted. Audit defaults to Logs, with Analysis as a subtab. Global Monitor stays at the top. Group MCP lists put allowed tools first and show saved equality/regex conditions below each tool.

## Access groups and readable rules

Access opens an **Access groups** list without a search field. Opening a group replaces the list with a Groups back button and separate Users, MCPs and Secrets tabs. Group names and descriptions are directly editable, with Save/Cancel for changes; Platform retains its built-in name. The shared MCP header uses provider branding, an allowed/total Tools badge and saved regex-condition count. Allowed tools sort first. Restrictions show plain-language explanations before expandable technical expressions.

Every new or updated `matches` condition requires `description`, a nonblank human-readable rule of at most 500 characters. API and builder save validation enforce it. The builder prompt, skill and fallback tool schema instruct the builder to generate it from verified rules. Existing conditions without descriptions continue matching, but updates require explanations. Descriptions are display text; enforcement uses the validated path/operator/value.

Audit details and CSV/JSON exports include Input/Output and truncation flags. Detached capture bounds string copies, nodes and nesting and caps each payload at 64 KiB; captures may truncate earlier. Async mode performs SQLite I/O in the existing queue worker, while capture uses bounded CPU at admission. Durable mode still waits for commit. Audit off bypasses capture. Existing retention, size limits, access isolation and shutdown flushing remain. No PII scanning is added.
