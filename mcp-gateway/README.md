# CapLayer MCP Gateway (local alpha)

For the proposed centrally managed Sentry access model, see [SENTRY_ACCESS.md](SENTRY_ACCESS.md). It covers Sentry-scoped MCP connections and CapLayer argument policies.

This module is an opt-in, single-user local alpha. It serves a governed MCP endpoint and a CapLayer admin console. Startup rejects public URLs and non-loopback binds until individual sign-in and durable governance storage are implemented.
Do not put a reverse proxy or tunnel in front of its loopback listener; the process cannot detect a proxy configured outside it.

## Start locally

Run the AgentWorks launcher with the gateway enabled, or start `go run ./cmd/server` from this directory. The gateway generates a fresh admin token on each start in `<GATEWAY_STATE_DIR>/admin-token` (default `./var/admin-token`, mode `0600`). The launcher prints the file path. The token is a backend service credential. Configure the product API with `CAPLAYER_SERVICE_URL` (the gateway origin) and `CAPLAYER_SERVICE_TOKEN_FILE` (that token file). CapLayer uses the existing product login; users never enter this gateway secret in the React console. An explicitly supplied `GATEWAY_HUMAN_TOKEN` must be unique and at least 32 characters; the old `GATEWAY_LOCAL_ADMIN=1` bypass is rejected.

Private-network upstream MCP servers are disabled by default. Set `GATEWAY_ALLOW_PRIVATE_UPSTREAMS=1` only when that access is intentional. OAuth providers reuse the shared product sign-in and token refresh service (configuration below). The gateway does not store their OAuth tokens.
Upstream URLs with query parameters are rejected so credentials cannot be stored in connector URLs or returned by the admin API.

## Reuse upstream MCP OAuth

The server directory uses the same `OAuthStatusBadge`, client registration, OAuth callback, private token store and refresh manager as the other products. An existing platform sign-in is reused when connecting a provider. Providers without dynamic client registration use the existing secure client ID/secret dialog. Reauthorization is available under the connected server's Connection settings.

Configure the gateway with:

```sh
GATEWAY_PRODUCT_URL=http://127.0.0.1:18161
GATEWAY_CATALOG_PATH=/absolute/path/to/agent_go/configs/mcp_servers_clean.json
```

Use the same MCP configuration for the product server (`--mcp-config`) and gateway catalog. This prevents drift between provider names and URLs. `GATEWAY_CATALOG_PATH` is optional for the embedded catalog; it exports only names, URLs and whether OAuth is required. The product still owns all OAuth settings and secrets.

The product's `CAPLAYER_SERVICE_TOKEN[_FILE]` must match the gateway service token. `GATEWAY_PRODUCT_URL` is a fixed HTTPS or loopback origin. Configure `PUBLIC_URL` on the product API for its public OAuth callback (or the frontend origin when `/api` is proxied).

Each upstream HTTP request resolves its current access token through the service-only `POST /internal/caplayer/oauth-token` broker. The broker accepts only the service credential, rejects browser origins, binds credentials to the exact configured provider URL, and returns `Cache-Control: no-store`. The existing OAuth manager refreshes expired tokens. Browser JWTs cannot call this broker, and tokens never appear in gateway inventory or chat. Logout removes the shared token, so subsequent gateway requests fail authorization.

Connecting approves initial tool definitions; users still require separate group grants. Reauthorization and refresh do not grant more tools or change published policies. This flow uses one shared platform identity per provider, as existing products do; separate accounts per connector instance require additional credential identities. The consumer-facing gateway OAuth endpoint remains separate from upstream provider sign-in.

## Shared accounts and roles

The embedded and standalone React consoles use `AuthWrapper`, the existing account menu, and the full product's Users & access editor. Local single-user mode signs in automatically as the local administrator. Multi-user mode uses the deployment's existing login providers and directory.

Management calls go to the product API at `/api/caplayer/api/admin/*`. The product API validates its JWT and current administrator/product permissions before forwarding to the fixed `CAPLAYER_SERVICE_URL`; it supplies the private service credential from `CAPLAYER_SERVICE_TOKEN_FILE` (or `CAPLAYER_SERVICE_TOKEN`). Browser JWTs, cookies and identity headers are not forwarded to the gateway. Disabled users and role changes follow the existing account middleware and directory cache. A gateway service credential failure returns a deployment error, without triggering another product login.

The People → Users tab reuses the existing account/role/product editor. Group membership selectors use active directory IDs; assigning a group binds that central identity to a gateway user record. These bindings have no separate passwords or account roles. The legacy `/admin` token console remains an internal local debugging interface; keep the gateway bound to loopback behind the product API.

Access is organized by existing group, with Users and Permissions tabs. Choose from visible group rows, with a highlighted selection and search when there are multiple groups. Create, rename and manage membership under People → Groups. Expand an MCP to assign tools, then open a tool's Permissions to inspect its restrictions or prepare a draft through chat. Draft review, simulation, publishing and revocation are scoped to the selected group; publishing checks the draft version reviewed by the administrator. Tool access indicators use runtime authorization, so published or revoked policies take precedence over older tool and whole-server grants.

For standalone hosting, configure the product API's CORS and OAuth return URL for that frontend, or serve both behind the same origin. A separate UI deployment reuses the account service; it does not introduce another identity system. Do not put the service token in frontend runtime configuration.

MCP client consent still uses the local alpha consent implementation. This change integrates console authentication and account roles; enterprise MCP identity/consent and durable governance remain required before team deployment.

## Local development in the shared app

For simple offline integration tests, use the official filesystem and memory reference servers via the [local MCP setup](LOCAL_MCPS.md).

Run the normal frontend (`npm run dev`) with the product API configured as usual. Open the frontend origin at `/` and choose CapLayer in the existing product picker. Set `gatewayUrl` in the public runtime config and configure `CAPLAYER_SERVICE_URL` plus the private service credential on the product server. CapLayer runs inside the same application, login and chat runtime as Goals, Crew and Code; local development does not need `caplayer.html` or a separate frontend process.

## Standalone CapLayer console

Run `npm run build:caplayer` in `frontend/`, then set `CAPLAYER_STATIC_DIR` to the absolute `frontend/dist-caplayer` path and `CAPLAYER_PRODUCT_API_URL` to the existing product account/API origin when starting the gateway. Open the gateway origin in a browser. The standalone entry renders the same `GatewaySurface` as the embedded product, with the shared product picker, product header, conversation transcript, and workspace toolbar used by Goals, Crew, and Code. It only lists CapLayer in the picker because this independent deployment does not host the other products. The embedded AgentWorks entry lists the products available to that user.

CapLayer chat uses the shared product conversation runtime, providers, model picker, streaming, cancellation and durable history. Configure providers through the existing product Providers UI; choose the assistant model under Connect. The server registers the `caplayer` profile when `CAPLAYER_SERVICE_URL` is configured and the `mcp-gateway` product is enabled. Its only governance tool can inspect the environment and schemas or save validated drafts. It cannot publish, revoke, manage membership, retrieve credentials or execute upstream MCP tools. Every call rechecks the user's current administrator role; service credentials remain on the server.

The gateway's legacy `/api/admin/setup/chat` endpoint remains an internal diagnostic API for Chat Completions compatible deployments (`CAPLAYER_AGENT_API_URL`, `CAPLAYER_AGENT_MODEL`, optional `CAPLAYER_AGENT_MODELS` and `CAPLAYER_AGENT_API_KEY`). The React product no longer uses that transport or its model/context limits.

Admins can add a custom MCP server with a centrally managed bearer token and rotate it later. The token is sent to that upstream during MCP requests and is never returned in connector API responses. It currently lives only in process memory, so it must be re-entered after a restart. Upstream OAuth flows still require an implementation before those catalog entries can connect.

An administrator connecting an MCP approves its initial discovered tool definitions. This does not grant access to any user or group. Later syncs retain unchanged approvals and quarantine newly introduced or changed tool definitions for review.

Access packages bind a group to an explicit set of approved tool fingerprints. Each rule may require exact or full-string RE2 matches on explicit string argument paths. Save a draft, test sample arguments, then publish it. A published package governs its tools ahead of older direct, group, and whole-server grants. Revocation leaves a deny tombstone to prevent old grants from silently regaining access. A changed tool fingerprint also denies calls until the package is reviewed and republished. Argument checks run after JSON schema validation and before upstream forwarding, without invoking the setup model.

Argument filters are only appropriate when an audited connector contract proves that the named argument fully identifies the target resource. Query languages, free text, opaque issue IDs, and tools with hidden defaults require upstream scoped credentials or a trusted adapter that resolves and checks resources. A regex alone cannot enforce Grafana namespace or pod boundaries.

## Configuration persistence

The full local launcher sets `GATEWAY_WORKSPACE_DIR` to the CapLayer chat project and stores configuration at `Chats/CapLayer/db/gateway.sqlite` (physical local path: `<WORKSPACE_DOCS_PATH>/_users/default/Chats/CapLayer/db/gateway.sqlite`). An explicit `GATEWAY_WORKSPACE_DIR` overrides this project location. Standalone launches without a workspace keep `GATEWAY_STATE_DIR/gateway.sqlite` (default `./var/gateway.sqlite`). Existing state-directory configuration migrates using SQLite when the project database does not yet exist; migration refuses to copy an active gateway. Groups, users, membership, direct and group tool assignments, existing server grants, connector configuration and approvals, access drafts, published policies, revocation tombstones, policy history, API key hashes, and PII rules save automatically after each configuration mutation. A separate final JSON file is not required.

The SQLite row contains a versioned, AES-256-GCM encrypted configuration snapshot. The encryption key remains at `GATEWAY_STATE_DIR/gateway.sqlite.key`, outside the chat project; database and key have mode `0600`. Service tokens and the MCP OAuth database also remain in the backend state directory. Back up the database **and key** using a SQLite-consistent backup, or stop the service before copying them (include any remaining WAL files). Keep these files on a persistent volume outside disposable checkout/temp directories. Upstream bearer credentials are encrypted in this snapshot; shared OAuth refresh credentials remain in the product token store.

Configuration writes finish before readers see the new state and before an admin API success response. Failed writes restore the previous committed configuration, return HTTP 503 through the admin API, and deny MCP authorization until storage is repaired and the gateway restarted. A missing/wrong key or unreadable configuration fails startup instead of resetting permissions. A SQLite lock database rejects a second gateway instance using the same database; this release supports one gateway process per database.

Saved active connectors reconnect on startup. Tool discovery preserves unchanged approvals, quarantines changed definitions, and keeps saved access settings when an upstream cannot reconnect. This is a local/single-process persistence implementation, with full snapshots per mutation; a large multi-worker deployment should use a normalized shared database and cross-worker authorization invalidation.

The chat agent exposes the same `query_workflow_db` and `mutate_workflow_db` definitions used by Crew, alongside `manage_caplayer_access`. SQL execution targets this project's database through the gateway storage owner; callers cannot choose another file or workspace. Read queries use SQLite `query_only`, a single-statement guard, a five-second timeout, and bounded results. Mutations accept one statement or an atomic batch of up to 20 statements.

Readable tables are `workspaces`, `users`, `groups`, `group_members`, `connectors`, `tools`, `user_tool_grants`, `group_tool_grants`, `group_server_grants`, `permission_drafts`, `published_permissions`, and `policy_history`. These metadata tables are plaintext within the private SQLite file. Credentials and API key hashes remain in the encrypted snapshot; the agent never receives its decryption key. `tools.input_schema` and draft/live rules contain JSON directly.

Writable SQL tables are `groups`, `group_members`, `user_tool_grants`, `group_tool_grants`, and `permission_drafts`. Account roles stay in the central product directory. Changes validate user/group/tool references, approved fingerprints and argument conditions before committing the SQL rows and encrypted configuration together. The running authorization state updates in the same operation. Draft versions increment automatically; query the current version and use it in WHERE predicates. New grants cannot bypass a governing policy. Publishing remains an explicit reviewed UI action. Removing a group revokes its policies and retains governance tombstones.

The tools recheck the current product administrator on each call. Direct external writes to the database are unsupported: metadata triggers change its revision, causing the gateway to deny authorization until restart restores the last validated snapshot. Use the registered SQL tools so edits reach both persistent storage and live enforcement. Metadata projections currently rebuild per configuration mutation; this local alpha still supports one process, and needs incremental shared storage for a large deployment.

## Current limits

- MCP client OAuth consent still maps to one local human. The API supports groups and users, but this is not individual team sign-in.
- Configuration and policy history persist in SQLite as described above. Public/team deployment still requires per-user MCP identity and consent, durable call audit/review storage, production admin authorization, and operational credential management. The standalone frontend does not remove the local-alpha exposure guard.
- The in-memory audit retains the latest 50,000 events in a ring. The PII review queue holds at most 10,000 entries and limits each caller to 100 active reviews. Full history needs durable storage.
- The bundled MCP SDK's `ListTools` method follows upstream `NextCursor` pages automatically.

## Review fixes in this branch

Connector deletion now removes its tool grants, group-server grants, PII rules, review requests, and version history so a new connector cannot inherit them. The admin console requires a secret on loopback, and the standalone admin cookie contains a short-lived session ID instead of the master token. Connector add, resync, and removal are serialized to prevent stale sessions from returning after deletion.
