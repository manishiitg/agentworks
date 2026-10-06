# PLAT-471: Platform group automatically has every shared secret and platform MCP

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | sandbox |
| Area | security |
| Summary | fixed on main, not deployed; GitHub issue #269. |

State: fixed on main; not deployed. GitHub issue: #269.
Priority: P1 (blocks the Vault rollout: every workflow selecting a shared secret is refused).
Owner decision: 2026-10-04.

## Problem

Verified on RTS: Vault had 1 group (Platform, 1 member), 0 registered secrets and 0 grants, while 29 shared secrets exist (23 managed in `_users/_system_global_secrets/secrets.json`, 6 environment-defined `GLOBAL_SECRET_*`). 9 of 17 workflow/Crew manifests select shared secrets and were all refused with `Your groups do not have access to secret "X". Check Vault > Access`, including scheduled production workflows. Environment-defined secrets were never registered in Vault unless an admin opened Vault > Secrets. The selection migration (`migrate-secret-selections`) detaches names that exist nowhere and deliberately never grants.

## Decision

Products automatically get the Platform group's secrets and MCP servers, so an upgrade changes nothing for people who could use them before. Admins can still restrict. See the DECISIONS entry. This grants only secrets and servers that already exist: it never creates, imports or reads a value, so it is not the "broaden grants as a workaround for missing secrets" the acceptance review (docs/reviews/confida-vault-acceptance-2026-10-04.md) rules out.

## Change

mcp-gateway (`internal/store`):
- `EnsurePlatformGroup` (runs at every gateway start and at `GATEWAY_BOOTSTRAP_ONLY` install) now grandfathers: it grants the Platform group every registered secret and attaches every connector (MCP server) of the workspace that is not in `platformRevoked`. Idempotent; logs one line with counts only when it granted something. A server attachment covers every tool of the server, including tools discovered later, so no per-tool grant is written (a per-tool grant would survive detaching the server and defeat a revoke).
- `RegisterSecrets` grants a name that was not registered before; `AddConnector` and `AddConnectorUnique` attach a connector ID that did not exist before. Updating or re-registering an existing item grants nothing.
- Revocations persist: a removal from the Platform group through `SetSecretGrant(s)` (allow=false), `RevokeGroupServerGrant` or `RemoveGroupConnectorAccess` is recorded in `platformRevoked` (keys `secret:NAME`, `server:ID`), which is a new optional field of the encrypted `durableState` blob (omitted when empty, absent in old snapshots, no format bump). An admin re-grant (`SetSecretGrant(s)` allow=true, `AddGroupServerGrant`) clears the entry. Deleting a secret or connector drops its entry; registering the name again is a new item and is granted. Grants of other groups are never touched.
- Every automatic grant appends a policy event (`grant_secret` / `attach_server_to_group`, actor `system:platform-auto-grant`), visible in Vault > Audit / access history.

agent_go (`cmd/server`):
- `startVaultSecretRegistration` (called after `loadManagedGlobalSecrets`) registers every environment-defined and managed shared secret name with Vault at backend start, in the background, retrying (5 s growing to 60 s) until the gateway answers. Names and the `managed` flag only; values are never sent or logged. Saving a managed secret still registers through `syncVaultSecretMetadata`; deleting still calls `revokeVaultSecret`.
- No Vault (`CAPLAYER_SERVICE_URL` unset): `vaultConfigured()` is false and `permittedGlobalSecrets` returns every shared secret, so `visibleGlobalSecrets`, `mergeGlobalSecretsFor`, `validateVaultSecretSelection` and `GET /api/secrets/global` behave as before Vault; `syncVaultSecretMetadata` and `revokeVaultSecret` (hence `saveManagedGlobalSecret` / `deleteManagedGlobalSecret`) are no-ops; `vaultAccessFor` returns an empty inventory without an error. A URL that is set but unusable still fails closed. Judgement call: before Vault, `mergeGlobalSecrets` treated a nil selection as "all globals"; the current code treats it as none and this ticket keeps the current behaviour.
- Not changed: `scopeAgentMCP` still drops global-catalog MCP servers on a server without Vault (the Vault MVP's choice; only a private connection or a Vault grant makes an MCP usable). Restoring the pre-Vault pass-through would widen access to configured global servers and was not part of this decision; raise it separately if Confida needs it.

Frontend: `SecretSelectionSection.tsx` wording fixed to "Assign access in Vault > Access > (pick a group) > Secrets." The empty state "Add shared secrets in Vault first." stays accurate (the Platform group now has every shared secret, so it shows only when none exist).

Docs: `docs/design/vault-current-state.md` no longer says connections and secret creation never grant.

## Members

Users are bound lazily and correctly: every host request to the gateway carries `X-Vault-Platform-User: 1` (`vaultRuntimeRequest`), and the gateway's `EnsurePlatformUser` creates the user and adds it to Platform on the first request; `AddUser` and `EnsurePlatformGroup` also backfill. A user's first run or first MCP listing therefore binds them and, with the auto-grants, gives them access immediately. RTS shows 1 member because only one person has made a Vault request so far; nobody is left without access. Test: `TestUnboundUserFirstRequestJoinsPlatformAndInheritsGrants` and the admin runtime test. No directory-wide pre-binding was added (People in Vault lists users after their first request).

## Deploy notes

- Runs at gateway start (`EnsurePlatformGroup`); no manual step, no flag, nothing to run. The agent_go backend registers its secret names on its own start; the gateway grants them as they register, so start order does not matter (a gateway restart re-checks everything).
- Expected on RTS at the next deploy: the 6 environment-defined and 23 managed secrets (29 names) are registered by the backend and granted to Platform; every existing gateway connector is attached to Platform (count = the connectors shown in Vault > Connected MCPs; each attachment exposes all tools of that server to every Platform member, which is the previous effective access). The 9 refused manifests then pass admission. Expect log lines `Vault: Platform group granted N shared secrets and M MCP servers ...` (gateway; only when it granted something) and `[VAULT] registered 29 shared secret names` (backend).
- Excellence/Confida: Excellence (Vault enabled) behaves like RTS with its own counts. Confida without `VAULT_ENABLED` has no `CAPLAYER_SERVICE_URL`: nothing is registered, granted or refused, and shared secrets are usable by everyone as before Vault.
- Inspect / dry run: there is no `report.json`; the migration is the idempotent start check. To see the effect without deploying, open Vault > Access > Platform after a start (secrets and servers granted), or read the access history events with actor `system:platform-auto-grant` (names only, no values).
- Undo: Vault > Access > Platform > Secrets / Servers: remove the grant; it persists across restarts, syncs and the start check, and re-adding it clears the record. Caveat: a grant an admin removed from Platform before this release has no record, so the first start after the upgrade grants it again; remove it once more afterwards.

## Verification

- mcp-gateway: `go build ./...`, `go vet ./...`, `go test ./...` (the commands of `.github/workflows/mcp-gateway.yml`). New `internal/store/platform_autogrant_test.go`: grandfathering of existing managed and environment-style secrets and servers, idempotent re-run, auto-grant of new items, connector update not restoring a revoke, revoke persistence across ensure, sync and restart, re-grant, other group untouched, delete then re-register, unbound first request. Four older tests that encoded "nothing is granted automatically" were updated.
- agent_go: `TestStartupRegistersEnvironmentAndManagedSecretNamesWithoutValues` (request body and logs contain no value), `TestWithoutVaultSharedSecretsBehaveAsBeforeVault`, `TestWithoutVaultManagedSecretSaveAndDeleteSucceed`, `TestConfiguredButUnreachableVaultStillFailsClosed`.

## Remaining

Not verified against a real gateway/server or in a browser. Deploy is the owner's call. The full `go test ./cmd/server` has the same 18 failures with and without this change (verified against a clean `origin/main` worktree): Relay catalog, Bot dry-run Crew tests, private Code/Crew functions, Sales Crew catalog, delegation tier config, playbook catalog and search, provider accounts, native tmux terminal, MCP management registration and workshop LLM config.

## Register notes

[PLAT-471](plat-471.md), fixed on main, not deployed; GitHub issue #269. The first Vault install granted nothing, so every run selecting a shared secret was refused. The Platform group is now granted every existing and newly registered shared secret and MCP server (gateway start check plus host-side registration of environment-defined and managed secret names); admin removals persist in `platformRevoked`; servers without Vault behave as before Vault.
