# PLAT-503: agents cannot attach or share a Vault MCP connection from chat

**State:** built on main, not deployed, not verified in a live chat. P2. Owner decision 2026-10-05: agents should be able to do it.

**Found:** 2026-10-05, RTS. The owner asked a chat agent to connect a Vault MCP connection; it could use Notion MCP but said "Vault MCP share isn't available from chat tools" and sent the owner to the Integrations UI. Vault itself was running (`video-studio-vault` active).

**What exists today (read from the code, not tried live):** the connection tools in `agent_go/cmd/server/mcp_connection_tools.go` can list servers (including the Vault inventory) and remove or rediscover one; Vault secret access can be granted to a group by `manage_vault_secret_access`, but only from the admin CapLayer chat. Code has a per-place attach tool (`place_mcp_attach.go`, `place_mcp_tool.go`). No chat tool attaches or shares a Vault MCP connection to a Crew or workflow.

**Decision (owner, 2026-10-05):** what an agent may attach or share follows the person it acts for: their account (email) and role, exactly as the Builder MCP connection follows the account (PLAT-487). A read-only account cannot; an account can only share a connection it is allowed to manage. No separate agent-level permission and no global switch.

**Built:** `manage_vault_access` (the Vault chat's tool: connect catalog/custom MCP servers, sign in, status, sync, disconnect, inspect groups and tools, save group permissions) is now offered in every Code, Crew and workflow chat, not only the Vault chat. Scope per the owner: anyone who may manage Vault can do it from anywhere. `registerVaultAccessChatTool` (`agent_go/cmd/server/vault_access_chat_tool.go`) registers it only for an active administrator with the Vault product and `capLayerConnectionAccess` rechecks that on every call, so a role change takes effect at once. Added to the Crew `mcp` feature tools, Code's `product.yaml` allowlist and the Crew read-only deny list. The description and parameters moved to `caplayerproduct.AccessToolDescription` / `AccessToolParameters()` so both chats share one definition. Secret values never pass through it; secret grants stay in the Vault chat.

**Left:** deploy; check live that an administrator's Code, Crew and workflow chats list the tool and that a non-administrator's do not; then an agent doing the RTS case end to end. `TestCodeSkillOptionsStayPrivateAndRefreshWithoutMutatingBuiltins` fails on a clean origin/main as well (the `code-mcp` skill text), unrelated.
