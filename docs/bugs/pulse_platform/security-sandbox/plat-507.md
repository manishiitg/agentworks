# PLAT-507: people create their own vaults and share secrets and MCPs from them (and promote existing ones)

**State:** open, design, nothing built. P2. Owner direction 2026-10-05: Vault is how people share MCPs and secrets; people should be able to create their own vaults and share from them, and agents should be able to promote an existing MCP sign-in or secret from a Crew, Code or workflow into Vault (see PLAT-503 for the agent tool that exists today).

**Today:** one platform Vault (the service threads a workspace id through everything, 493 places, but the server only ever uses `w1`), managed by administrators. `docs/design/vault-mcp-ownership.md` states the opposite principle for private logins: they stay private, sharing a Crew/Code/workflow does not share the person's external account, and nothing copies private credentials into shared stores. A promote is therefore a deliberate change to that rule, not an extension of it. The agent tool `manage_vault_access` can create a new Vault connection (new sign-in) but cannot move an existing login or secret in.

**Decided (owner, 2026-10-05):** the platform Vault stays administered by the platform owner. In addition, any person can create their own vaults and share secrets and MCPs from them. So there are two kinds: the platform Vault (owner-administered, company-wide) and personal vaults (the creator administers and shares). Promote into the platform Vault stays administrator-only; promote into a person's own vault is theirs to do.

**Still open (answers change the design):**
1. A personal vault is its owner's own space: the owner is its administrator, adds MCPs and secrets, and grants named people or groups. Can those members re-share? (Suggest no.)
2. Does a platform administrator see and control every personal vault (audit, takedown)? (Suggest yes, read and revoke, not use.)
3. (Decided above.)
4. Limits: vaults per person, secrets and connections per vault.

**Sketch:** one Vault workspace per person (`w-<user>`) on the existing gateway, the owner as its admin; the same tools (`manage_vault_access`, secret access) scoped to the caller's own vault; promote for MCP sign-ins and secrets (server-side re-seal, the agent never sees a value, confirm step, audit log, no group access until granted); UI for My vaults. Secrets and OAuth client secrets are still never entered in chat.

**Left:** owner answers above, then design review, then build in steps (own vault, then promote, then UI).
