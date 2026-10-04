# Vault server installation and external MCP identity

Status: implemented and verified locally on 2026-10-03. No remote deployment or
live production IdP/client acceptance is claimed.

## Service layout

RTS and Excellence use `deploy/common/vault.sh` and
`deploy/common/install-vault-service.py` from their existing build/activation
scripts. These build the Linux binary, configure persistent paths, bootstrap
Platform, install a user systemd service, start it and check `/healthz`.
Other shared rootless deployments enable this explicitly with `VAULT_ENABLED=true`.

The shared `deploy/common/build-release.sh` bundles `bin/agentworks-vault` and
`bin/install-vault-service.py` before manifest generation. Prebuilt activations
copy these verified assets and load the same lifecycle helpers as source builds;
they never compile Vault on the target server. An enabled Vault rejects a release
missing either executable before initializing persistent state or stopping services.

| Deployment | Private Vault listener | Configuration project | Private credential state |
|---|---|---|---|
| RTS | `127.0.0.1:8083` | `/data/video-studio/docs/Chats/CapLayer` | `/var/lib/video-studio/video-studio/state/vault` |
| Excellence agents | `127.0.0.1:24003` | Deployment `data/docs/Chats/CapLayer` | Deployment `state/vault` |

The configuration database is `<project>/db/gateway.sqlite`. Its encryption key
is in private state, outside agent workspace documents. The generated persistent
`service-token`, `service.env` and `agent.env` have mode 0600; state has mode 0700.
The agent loads `CAPLAYER_SERVICE_URL` and `CAPLAYER_SERVICE_TOKEN_FILE` through a
systemd drop-in. Tokens are never written into public frontend runtime config.

The service is `${product}-vault.service`, ordered before `${product}-agent.service`.
Bootstrap stops its single writer first. A deployment exit trap restarts it if
activation fails after stopping. Repeated bootstrap preserves the Platform group
and existing grants. A fresh managed installation has one Platform group, no
static `u1` user and no resource grants. Runtime/directory synchronization binds
actual platform users and adds automatic Platform membership. Connecting an MCP
or registering a secret does not share it automatically.

## Required deployment settings

Keep the existing product `PUBLIC_URL` set to its HTTPS frontend/API origin.
The public client endpoint is `<PUBLIC_URL>/api/vault/mcp`; clients must not use
the private Vault port. Publish the OAuth discovery routes through the existing
frontdoor; the auth gateway now lets these and the protocol authorization/token
routes reach the product, which performs OAuth authentication itself. Consent
and connection management continue to require platform authentication.

MVP server deployment defaults to SQLite and async audit admission, with the
same 24h retention default as local. No additional database service is required.
SQLite is the only MVP audit storage backend. `VAULT_AUDIT_PROVIDER=off` disables
collection. Other provider choices are rejected before stopping services.
ClickHouse is deferred until after the MVP release. Retention, size limits and
write mode settings are forwarded privately. Async admission can lose queued
events on process/machine failure; choose durable mode when persistence must be
acknowledged before a call completes.

## External Claude/client connection

1. Add the endpoint from Vault's Connect panel to the MCP client.
2. OAuth discovery and registration select the separate `/vault` issuer.
3. PKCE authorization opens `/oauth/vault` and the shared platform login.
4. Consent creates a grant bound to that person's active directory ID and
   `vault:mcp` scope. The opaque access/refresh tokens are isolated from workflow
   and CLI tokens in a dedicated private store beside the platform OAuth store.
5. Each call validates the token, active account and current Vault entitlement.
   The product removes caller identity/cookies and delegates the verified actor
   over a service-authenticated private route. The gateway applies current group,
   tool, schema and argument policy before invoking the upstream.
6. Refresh and initial code exchange also check active entitlement. Disconnect
   revokes the connection and its token family. Audit records identify the user
   and OAuth client. No separate human token-entry login is used.

The public endpoint rejects platform JWTs, service tokens, legacy group keys,
refresh tokens and bearer tokens in URLs. Managed mode does not expose the
private service's static-user OAuth or legacy `/admin` console.

MCP-only users need an allowed platform identity and appropriate groups, without
an execution slot. Vault-only invitations cannot assign other product roles.
Existing root provisioning remains necessary for execution access; broader
account-API/slot enforcement is a separate pending platform task.

## Runtime semantics and operating limits

Platform grants are shared eligibility within one installation/workspace.
Code, Crew, workflows and Relay still explicitly select eligible MCPs/secrets.
A subset of people should receive grants through a separate group. No grant
confers product entry or a Linux execution slot. Secret revocation affects future
resolution; rotate an upstream credential to invalidate copies already given out.

Run one Vault service per configuration database. Back up the database and its
private key using a consistent SQLite backup or after stopping the service.
Keep private state and project storage on persistent volumes. Allow 60 seconds
for graceful audit shutdown. Verify production IdP/Claude flows, SQLite retention,
reboot/redeployment and actual load before rollout. These fixes do
not claim distributed policy storage or multi-worker invalidation.
