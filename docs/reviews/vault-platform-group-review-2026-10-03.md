# Platform group implementation review — 2026-10-03

Scope: checked-in local installer, gateway bootstrap/storage/policy, product MCP
and secret runtime integration, and RTS/Excellence deployment scripts. Reviewed
the current working tree, including ongoing Vault changes. No remote host was
inspected, no live permissions changed, and no server deployment was performed.

## Findings

### P1 — Vault is not wired into the standard server deployment scripts

`scripts/install-vault.sh` builds/installs the Vault executable and runs
`GATEWAY_BOOTSTRAP_ONLY=1` to create its project database and Platform group.
The installer explicitly asks the operator to start the service separately.
The RTS `deploy/aws-ec2/server/build-and-activate.sh` and shared
`deploy/rootless-linux/build-and-activate.sh` do not build/install/start this
Vault service or configure its product-to-gateway connection. Their existing
authentication gateway is a separate service, not the MCP Vault gateway.

Consequently, ordinary server deployment does not guarantee a usable Platform
group or shared MCP/secret runtime. Gateway startup initializes the group only
once the Vault service is actually launched with the intended persistent paths.
Server integration needs an explicit Vault service, matching project/state/key
locations, lifecycle management and `CAPLAYER_SERVICE_URL`/token-file wiring.

### P1 — External multi-user MCP identity remains a release blocker

Gateway bootstrap still configures one OAuth human (`u1`) and workspace (`w1`).
Product-internal requests have authenticated actor delegation and automatic
Platform membership, but external Claude OAuth does not yet bind each consent
to that person's platform SSO identity. `validateExposure` accordingly refuses
public advertised URLs and non-loopback binds. Platform group initialization
does not resolve this identity limitation or make this alpha internet-ready.

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
  installation/workspace, not across independent RTS/Excellence installations.
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

## Conclusion

The Platform group and shared internal authorization model are implemented and
verified. Server installation integration and individual external MCP OAuth
identity remain unfinished. The current behavior is explicitly shared
availability plus project selection, rather than automatic project attachment.
