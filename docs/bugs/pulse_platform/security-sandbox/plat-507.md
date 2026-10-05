# PLAT-507: people create their own vaults and share secrets and MCPs from them (and promote existing ones)

**State:** connections built on main (not deployed, not verified live); secrets and UI not built. P2. Owner direction 2026-10-05: Vault is how people share MCPs and secrets; people should be able to create their own vaults and share from them, and agents should be able to promote an existing MCP sign-in or secret from a Crew, Code or workflow into Vault (see PLAT-503 for the agent tool that exists today).

**Today:** one platform Vault (the service threads a workspace id through everything, 493 places, but the server only ever uses `w1`), managed by administrators. `docs/design/vault-mcp-ownership.md` states the opposite principle for private logins: they stay private, sharing a Crew/Code/workflow does not share the person's external account, and nothing copies private credentials into shared stores. A promote is therefore a deliberate change to that rule, not an extension of it. The agent tool `manage_vault_access` can create a new Vault connection (new sign-in) but cannot move an existing login or secret in.

**Definition (owner, 2026-10-05):** a vault is a bundle of MCP connections and secrets that any user can create and share with other users. It maps onto the existing Vault group (members plus grants to connections and secrets), owned by a person instead of only by the platform administrator.

**Ownership (owner, 2026-10-05):** a vault has one or more owners. Only an owner can add, remove or update the MCPs and secrets in it and decide who may use it; other members can use what it contains. So members cannot re-share: only owners change membership and contents, and an owner can add another owner. The platform Vault is the same shape with the platform owner as its owner.

**Decided (owner, 2026-10-05):** the platform Vault stays administered by the platform owner. In addition, any person can create their own vaults and share secrets and MCPs from them. So there are two kinds: the platform Vault (owner-administered, company-wide) and personal vaults (the creator administers and shares). Promote into the platform Vault stays administrator-only; promote into a person's own vault is theirs to do.

**Still open (answers change the design):**
1. (Decided above: owners only manage; members use.)
2. Does a platform administrator see and control every personal vault (audit, takedown)? (Suggest yes, read and revoke, not use.)
3. (Decided above.)
4. Limits: a person can own at most 5 vaults (owner decision 2026-10-05). Still open: secrets and connections per vault.

**Sketch:** one Vault workspace per person (`w-<user>`) on the existing gateway, the owner as its admin; the same tools (`manage_vault_access`, secret access) scoped to the caller's own vault; promote for MCP sign-ins and secrets (server-side re-seal, the agent never sees a value, confirm step, audit log, no group access until granted); UI for My vaults. Secrets and OAuth client secrets are still never entered in chat.

**Left:** owner answers above, then design review, then build in steps (own vault, then promote, then UI).

## 2026-10-05: built (connections)

- Vault service (`mcp-gateway`): `store/vaults.go` (a vault is a group with `Kind: vault` and `Owners`; connections carry `VaultID`; 5 owned per person; only owners change members, owners and connections; a vault keeps at least one owner; a vault is deleted only when empty; an owner cannot take over an existing connection) and `admin/vaults.go` (`/api/vaults/...`, service-only, a validated person required, owner checks in the store). One test pins those rules (`store/vaults_test.go`).
- Server: `manage_my_vaults` (`vault_person.go`) for any enabled, non-read-only account in Code, Crew and workflow chats: list, create, inspect, add/remove member and owner by email, connect (catalog or custom URL, with the owner's sign-in link), sign_in, promote (an existing Crew/Code/workflow connection into the owner's own vault, no second sign-in), sync, remove_connection, delete. The Vault sign-in flow now accepts a vault's owner as well as the platform administrator (`vaultActorCanManage`, rechecked on every step). Members get a connection's tools as soon as it is in the vault, through the existing group grants.
- Not built: secrets in a personal vault (names must be namespaced per vault and run-time secret resolution must honour vault membership); the "My vaults" UI; limits on connections per vault.

**Left:** deploy and check live on RTS (create a vault, promote the Notion connection into it, add a second account, see the tools from that account); then secrets; then the UI.

**Verified live (RTS, 2026-10-05):** create vault and promote an existing Notion sign-in into it worked (see PLAT-503). Still to check live: second account as a member sees the tools; member cannot add people; sixth-vault refusal. Secrets and the UI are not built.

## 2026-10-05: security flaw found in my first build, fixed; secrets built

**Flaw (live on RTS from 4a25761d4 until the next deploy):** the Vault service grants every newly created connection to the Platform group (everyone) unless the grant is revoked, and the vault connection was bound to its vault only after it was created. A vault's connection (the promoted Notion one) was therefore visible to the whole company, not only the vault's members. Found by reading `autoGrantServerLocked`, not by exploitation. Fix: the connection row is created already carrying its `VaultID`; `autoGrantServerLocked` and `autoGrantSecretLocked` skip anything vault-owned (including the startup backfill); and a repair at every Vault start (`revokeVaultOwnedFromPlatformLocked`) removes any company-wide grant from vault-owned connections and secrets and remembers the removal. One store test pins all of it (create-time, startup backfill, repair of an earlier wrongly granted connection).

**Secrets built (not deployed, not verified live):** a vault secret's value is stored encrypted in the host's managed global store as `VLT_<vault>__<NAME>`; the Vault service records only that name and grants it to the vault's group, never to the Platform group; the platform's own name sync skips `VLT_` names. Owner-only set (replace needs an explicit flag), remove, and `promote_secret` (copies a Crew/Code/workflow secret server-side, the agent never sees the value, confirm step, original stays). Values are typed only into the vault screen endpoint `POST /api/my-vaults/{id}/secrets` or promoted; never through a tool or chat. Admins cannot create `VLT_` names. Vault connections are created from the Vault catalog only (no custom URL), per the owner's no-fallbacks rule.

**Left:** deploy and verify live (connection no longer visible to a non-member; secret usable by a member, not by a stranger); the "My vaults" screen (endpoints exist: `/api/my-vaults/op`, `/api/my-vaults/{id}/secrets`); a limit on connections and secrets per vault.
