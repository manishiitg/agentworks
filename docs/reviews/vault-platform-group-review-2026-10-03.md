# Platform group implementation review — 2026-10-03

> Updated scope, 2026-10-04: audit storage is SQLite-only for the local/server MVP,
> with collection-off mode. ClickHouse references in earlier review records are
> superseded; ClickHouse is deferred until after the MVP release.


Scope: checked-in local installer, gateway bootstrap/storage/policy, product MCP
and secret runtime integration, and project-a/customer-b deployment scripts. Reviewed
the current working tree, including ongoing Vault changes. No remote host was
inspected, no live permissions changed, and no server deployment was performed.

## Findings and resolution

Both P1 findings below were fixed locally after merging `origin/main` at
`49a1e6761` (merge `9832ca775`). These are source/local verification results,
not confirmation of a deployed server or live IdP/client rollout.

### P1 — Standard server deployment did not install Vault — resolved locally

server A and server B now use `deploy/common/vault.sh` and the shared service
renderer. They build the Linux executable, configure a persistent private state
location and project database, install a systemd service and product credential
drop-in, stop the single writer for bootstrap, initialize Platform and start/
health-check the service. A failed activation runs service recovery through the
deployment exit trap. Frontend/backend allowlists and their preflight checks
include Vault. The separate existing auth gateway forwards public MCP/OAuth
protocol routes while keeping consent/management authenticated.

The private service uses `GATEWAY_AUTH_MODE=platform`. Server audit defaults to
ClickHouse, with explicit SQLite/off overrides. Missing ClickHouse URL fails
before stopping services; the installation does not provision ClickHouse.
Credentials remain outside workspace documents and persist across releases.
See [server installation](../design/vault-server-installation.md) for paths,
settings and rollout checks.

### P1 — External OAuth used one static human — resolved locally

The platform now exposes `/api/vault/mcp` with its own `/vault` OAuth issuer,
`vault:mcp` scope and private token-store namespace. `/oauth/vault` reuses
platform authentication. Consent binds to a verified active directory user,
without a separate Vault password/token-entry screen. Calls and initial/refresh
exchanges recheck account status and Vault entitlement; disconnect revokes the
connection/token family. The private runtime receives service-authenticated
verified actor/client IDs; caller identity headers, cookies and connector
selection cannot override them. Each MCP call rechecks current group policy;
audit attributes calls to the actual account and OAuth client.

Managed mode has no direct `/mcp`, static-user authorization server or `/admin`
console. Legacy group keys/static consent remain private local debugging
mechanisms and are not accepted or advertised by the platform endpoint.

## Verified implementation

- Installer and gateway startup call the same `EnsurePlatformGroup` bootstrap.
  The workspace-specific ID is stable; a second installation does not duplicate
  the group. Existing grants survive restart/reinstallation. The earlier
  Everyone display name migrates to Platform without changing IDs or grants.
- The group starts with no MCP/tool/server or secret grants. Connecting an MCP
  or registering secret metadata does not share it with Platform automatically.
- Existing gateway users in the workspace are backfilled. New users join on
  directory synchronization or a trusted authenticated product runtime request.
  Membership is automatic; HTTP/validated-SQL operations cannot remove a person
  from Platform, rename it or delete it.
- Platform grants apply to all of its users. They are shared access within one
  installation/workspace, not across independent project-a/customer-b installations.
  Resources meant for a subset of people belong in a separate group.
- Ordinary product users consume grants without requiring Vault management
  access. The product API validates active identity and supplies the actor to
  service-only gateway routes; browser-supplied identity/service headers are
  discarded. Disabled/unknown multi-user identities are refused by the host.
- MCP inventory is permission-filtered. Shared agent construction and scoped
  MCP bridge resolution use the governed runtime. The gateway authorizes live
  calls, including calls on retained connections, against current grants.
  A key restricted to a different group does not inherit Platform membership.
- Secrets use current group grants intersected with explicit project selection.
  Same-named project secrets retain precedence. Selected inaccessible secrets
  fail validation; a gateway outage does not grant secret access. Revoking a
  grant affects subsequent resolution but cannot remove values already handed
  to a running process or copied by a user.
- Sharing through Platform makes resources available for selection in the
  common runtime used by Code, Crew, workflows and Relay. It does not attach
  every resource to every project, grant product entry, or provision Linux slots.

## Verification evidence

- `go test -race ./internal/store ./internal/policy ./internal/admin
  ./internal/mcpserver ./cmd/server -count=1 -timeout 2m` passed in mcp-gateway.
  Includes Platform bootstrap/membership/restart, grant revocation, trusted
  runtime binding and group-key isolation regressions.
- Product backend tests matching Vault, Governed and CapLayerDirectory passed.
  Additional Crew selected-secret, native secret injection, retained profile
  MCP scope and report runtime tests passed.
- Built a fresh gateway executable and ran `scripts/install-vault.sh --binary`
  twice into one disposable workspace/state/install directory. Both exited 0.
  A read-only SQLite check found exactly one Platform record, with the expected
  stable ID and initial synthetic bootstrap membership.
- These runs were on macOS. This is not a Linux deployment or full live
  end-to-end test of every product. Existing regression fixtures use dummy
  secrets; no real stored secret values were read or printed.

## Follow-up verification evidence

- Product Vault/governed/CapLayer and provider-switch regressions passed with
  `go test -race ./cmd/server` and focused names. The individual OAuth test
  covers two users sharing a client, PKCE failure/code replay, wrong resource,
  spoofed headers, disabled users, refresh and connection revocation. MCP tokens
  cannot approve consent or access platform management APIs.
- Gateway store/policy/admin/MCP/server race tests and the shared OAuth module's
  full race suite passed. The new external runtime test verifies permission-
  filtered inventory, live grant removal, user/client audit attribution and
  rejection of unauthenticated actor binding.
- Six deployment helper tests passed: persistent credentials and file modes,
  ClickHouse configuration, symlink/path checks, both deployment integrations,
  allowlist/preflight agreement and recovery after a synthetic bootstrap failure.
  Shell syntax checks passed. The hosted auth-gateway challenge/discovery test
  passed with its password gate both enabled and disabled.
- TypeScript build passed. Thirty-one tests passed across Connect, groups, consent,
  OAuth safe returns and shared integration layout. Earlier focused shared UI
  suites also passed following the main merge.
- A Linux amd64 gateway binary built successfully. Fresh native managed
  bootstrap ran twice in disposable state; read-only synthetic SQL found one
  Platform group and zero users. No MCP/tool/secret grants were seeded. Local
  managed service `/mcp`, `/admin` and direct OAuth metadata return 404.
- Fresh native product and managed gateway restarted for the local preview.
  Both health endpoints returned 200. The in-app browser showed the configured
  `/api/vault/mcp` URL and a successful sign-in-required reachability check.
  Live PKCE/consent/exchange and MCP initialize/list passed through both actual
  services; revocation then returned 401. This used the local platform identity,
  not a real IdP. No upstream tools were called or group grants changed.
- The local smoke account received an empty tool list. Its only granted fixture,
  Local OAuth Memory, needs upstream reauthorization (also recorded in pre-change
  startup logs). The two other reference servers have no Platform grants. The
  gateway regression exercises successful calls on disposable granted fixtures;
  the live preview run is not evidence of a working external-provider call.
- The broader rootless deployment suite has a pre-existing stale assertion for
  `PERSIST_MCP_STATE` in the shared builder; `origin/main` already lacks that
  marker. It is not counted as passing. No complete platform suite, remote
  Linux activation, configured production ClickHouse or real IdP/Claude flow
  was run in this task.

## Conclusion

Platform bootstrap and shared internal authorization remain verified. The two
reviewed gaps now have implemented, locally tested fixes. Real server/client
acceptance remains a rollout gate. Shared availability continues to require
explicit project selection; installation adds no resource permissions or slots.
