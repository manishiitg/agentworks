# Vault: implementation status

Updated **2026-10-03** for the local work on `fix/gateway-review-1`. This document
summarizes the implementation and verification completed so far. Dated execution
records in the linked documents describe earlier versions; this summary describes
the current behavior. The product remains a single-user, loopback alpha.

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
  Available MCPs, Secrets, People, Audit, PII, Models and Connect.
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
  explicit string argument paths. Drafts can be inspected and simulated before
  explicit reviewed publication. Version checks reject stale publication.
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
It also records that the standard RTS/Excellence deploy scripts do not yet install
or start Vault; the standalone installer initializes it and gateway startup
repeats the initialization. Existing authentication gateways are separate.

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
covered by those checks too. Individual SSO-to-MCP consent binding also remains
unfinished as described below.

A person may use the Vault MCP endpoint from Claude without using Crew, Code or
workflows, but authenticates through the same platform identity provider. Product
access and administrator status remain separate from grants to MCP tools/secrets.
This is the confirmed identity design, not a claim that the external OAuth consent
bridge is finished: the current alpha still uses its single local consent identity.
The remaining work is to bind each MCP OAuth grant to the verified platform SSO
identity and check disabled/revoked users on subsequent requests. The existing
SSO screen was verified in the browser after withdrawing the separate-directory
changes; the directory identity regression, TypeScript and 24 shared surface/group
tests passed. No new external accounts or grants were created.

## Setup assistant

Vault uses the shared conversation runtime with a dedicated system prompt and
embedded access skill. It inspects actual tool schemas and annotations, preserves
unrelated grants, distinguishes read/write/destructive tools, and tells the user
whether a change is active or only a draft.

| Tool | Implemented authority |
|---|---|
| `manage_caplayer_access` | Inspect inventory/schemas, connect a server without chat credentials, save a validated advanced draft. Connection does not grant group access. |
| `query_workflow_db` | Read bounded governance metadata and schemas from this Vault project's SQLite database. |
| `mutate_workflow_db` | Apply validated, atomic group, membership, simple tool-grant and draft changes to persistent and live state. |
| `manage_vault_secret_access` | Grant or revoke use of an existing secret for a group, without revealing its value. |

Every tool rechecks the current product administrator. The assistant cannot
retrieve credentials, execute upstream MCP tools, change central account roles,
or publish advanced policies. It has no unrestricted shell or direct database
file access. SQL tools use the gateway owner; arbitrary physical SQLite edits
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
group, tool approval, schema, resource conditions and PII policy. Revocation is
checked on the next call even when a connection is warm.

Legacy globally shared connections require migration to Vault grants or private
reauthorization. A catalog entry or project selection is not an authorization
grant. See [MCP ownership](vault-mcp-ownership.md) for the enforcement paths.

## Persistence and credentials

| Data | Location and behavior |
|---|---|
| Configuration, identities, groups, grants, tool approvals, drafts, published policies, history and PII rules | `<WORKSPACE_DOCS_PATH>/_users/default/Chats/CapLayer/db/gateway.sqlite` in the full local launcher. Standalone fallback: `GATEWAY_STATE_DIR/gateway.sqlite`. Automatically committed after mutations. |
| Authoritative configuration | AES-256-GCM encrypted snapshot in SQLite; normalized SQL metadata is readable within the private file. |
| Gateway configuration key | `GATEWAY_STATE_DIR/gateway.sqlite.key`, outside the chat project. Database/key permissions are `0600`. |
| Shared secret values | Existing encrypted host store at `_users/_system_global_secrets/secrets.json`; project SQLite stores names and grants only. |
| Private and shared upstream OAuth credentials | Product-owned sealed stores, including the private user's namespace. Gateway resolves current tokens through a service-only exact-URL broker. |
| Audit events | Separate configured provider/state files, described below. |
| Pending PII reviews | Bounded memory only; not durable yet. |

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

Events contain identity, MCP/tool, decision, outcome, timing and PII metadata.
They contain no raw arguments, outputs or secret values. Requests rejected before
tool dispatch are not tool-call events. Dedicated PII detail columns are not yet
displayed in the log table.

| Setting | Local default | Server default |
|---|---|---|
| `VAULT_AUDIT_PROVIDER` | `sqlite` | `clickhouse` |
| `VAULT_AUDIT_RETENTION` | `24h`; local values cannot exceed 24h | `720h` |
| `VAULT_AUDIT_WRITE_MODE` | `durable` | `durable` |
| `VAULT_AUDIT_MAX_MB` | `256` for SQLite pages | `256` for the local delivery spool |

`VAULT_AUDIT_PROVIDER=off` disables collection/querying without deleting old
files. SQLite uses WAL, batched commits, indexed queries and retention cleanup.
ClickHouse uses a persistent SQLite delivery spool, background batches, retries,
stable event IDs and deduplicated reads. Missing server configuration fails startup.
Server provider defaults do not remove the alpha's public-exposure guard.

Opt-in `VAULT_AUDIT_WRITE_MODE=async` uses this path:

```text
MCP request → permission/PII checks → upstream/result checks
            → copy metadata into bounded queue → return
background worker → batch SQLite commit → ClickHouse delivery if configured
```

No worker is created per call. The queue holds 1,024 waiting events plus at most
256 in the current batch. Full queues return explicit errors. Failed batches
remain queued and retry with 50ms–5s backoff; new admissions are rejected while
the writer is unhealthy. Settings expose pending writes, write failures and health.
An already accepted asynchronous event cannot retroactively fail its caller.

SIGINT/SIGTERM stop requests, wait up to 45 seconds for active handlers, then
drain accepted events. A flush error is reported. Crash/SIGKILL/power loss can
lose uncommitted in-memory events. Durable mode waits for persistent admission;
ClickHouse delivery remains asynchronous after spool commit. Async reduces the
request's storage wait, not total CPU/disk work. Full details: [gateway README](../../mcp-gateway/README.md#audit-storage).

## Deterministic PII protection

Protection, Test and Reviews are separate subtabs. The default rules are compact;
test results show the action and masked preview. Custom rule creation/editing
controls were removed as requested. Existing custom rules are read-only in the
UI; backend custom-rule APIs remain available.

Detection uses regex and deterministic validation, not a model. Default email
and US phone patterns mask; valid US SSNs, Luhn-valid card numbers and supported
API-key patterns block. Rules can scope group, connector, tool and direction.
Patterns compile once; JSON scanning is limited to 256 KiB and scans string
values, not numeric values or keys. Opaque binary/image payloads fail closed.

Input enforcement must finish before upstream execution; output enforcement
must finish before returning data. Moving these checks behind the response would
allow leakage. Output blocking cannot undo an upstream side effect. Audit stores
PII metadata only, so logging does not repeat a full-payload cleaning pass.
Input review approval permits one matching retry. Review state expires after
24 hours, is bounded, stores hashes/metadata, and is lost on restart.

## Verification completed

Local reference fixtures include the pinned MCP filesystem and memory servers
plus a synthetic OAuth Memory provider for registration, PKCE and refresh.
Filesystem access is limited to the test folder. See [local MCP setup](../../mcp-gateway/LOCAL_MCPS.md).
Real Notion/Asana authorization has not been demonstrated by these fixture tests.

Latest audit/PII verification on **2026-10-03**:

- `go test -race ./internal/store ./internal/pii ./internal/mcpserver ./internal/admin ./cmd/server` passed in `mcp-gateway`; frontend `npx tsc -b` and the gateway build passed.
- Tests cover nonblocking async admission during a blocked database write,
  metadata copying, bounded overload, retained/retried failed batches, rejection
  while unhealthy, recovery without duplicate events, flush errors, and restart.
- A granted local OAuth Memory empty-observations call succeeded. A synthetic
  SSN input was blocked before execution; audit recorded its action/type without
  retaining the value. Graceful shutdown exited with code 0; all five observed
  events remained after restart, with no pending writes or write failures.
- In-app browser showed **SQLite · Async · 24h retention** and the persisted
  events. The current preview explicitly enables async; installation defaults
  remain durable.
- Earlier provider checks exercised real disposable ClickHouse 25.8 for filters,
  aggregation, TTL and replay deduplication, and confirmed Off creates no audit DB.
- Earlier browser checks verified shared layout, server/group JSON schemas,
  audit filters/analysis, and PII masking. Focused frontend and backend regressions
  are recorded in the [test plan](../../mcp-gateway/TEST_PLAN.md) and [review](../reviews/caplayer-ui-review-2026-09-30.md).

Measured on Apple M3, 200 iterations per isolated stage:

| Stage | Mean | p95 | p99 |
|---|---:|---:|---:|
| Async audit admission | 0.199 µs | 0.208 µs | 0.292 µs |
| Durable audit admission | 5.01 ms | 6.23 ms | 6.58 ms |
| PII JSON scan, approximately 1 KiB | 0.105 ms | 0.111 ms | 0.121 ms |
| PII JSON scan, approximately 4 KiB | 0.427 ms | 0.441 ms | 0.464 ms |
| PII JSON scan, approximately 64 KiB | 6.98 ms | 7.27 ms | 7.36 ms |

These are admission/scan benchmarks, not end-to-end latency or a supported RPS.
Input and output can each require a scan. No complete current platform suite or
running Linux deployment pass is claimed; earlier broad host tests had unrelated
catalog/Pulse failures and timeouts.

## Remaining work and release boundary

- External MCP OAuth consent still maps to the local human. Individual enterprise
  MCP identity/consent and public/team deployment remain unfinished; public binds
  and URLs remain guarded. A standalone frontend does not change that boundary.
- Configuration uses full snapshots and rebuilt metadata projections with one
  process per database. Large deployments need shared incremental governance
  storage, cross-worker invalidation, durable reviews and workload-specific load tests.
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
