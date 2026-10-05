# PLAT-503: agents cannot attach or share a Vault MCP connection from chat

**State:** open, not built. P2. Owner decision 2026-10-05: agents should be able to do it.

**Found:** 2026-10-05, RTS. The owner asked a chat agent to connect a Vault MCP connection; it could use Notion MCP but said "Vault MCP share isn't available from chat tools" and sent the owner to the Integrations UI. Vault itself was running (`video-studio-vault` active).

**What exists today (read from the code, not tried live):** the connection tools in `agent_go/cmd/server/mcp_connection_tools.go` can list servers (including the Vault inventory) and remove or rediscover one; Vault secret access can be granted to a group by `manage_vault_secret_access`, but only from the admin CapLayer chat. Code has a per-place attach tool (`place_mcp_attach.go`, `place_mcp_tool.go`). No chat tool attaches or shares a Vault MCP connection to a Crew or workflow.

**Left:** design the tool (who may share, to which group/Crew/workflow, audited, never shows secret values), build it, and check it live with an agent doing the RTS case. Needs the owner's call on who may use it (admin only, or any account allowed to manage that connection).
