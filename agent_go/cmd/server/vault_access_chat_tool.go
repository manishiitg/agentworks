package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/caplayerproduct"
)

// registerVaultAccessChatTool offers manage_vault_access (the Vault chat's tool: connect MCP servers, sign them in,
// grant groups use of them, inspect groups) in the other chats an administrator works in: Crew, Builder and Code
// (PLAT-503). It follows the person, not the chat: the tool is registered only for an active administrator with the
// Vault product, and capLayerConnectionAccess rechecks that on every call, so an account whose role changes mid-chat
// loses it at once. Secret values never pass through it; secret grants stay in the Vault chat.
func (api *StreamingAPI) registerVaultAccessChatTool(reg definitionToolRegistrar, userID string) error {
	if !vaultAdminActive(userID) {
		return nil
	}
	err := reg.RegisterCustomTool("manage_vault_access", caplayerproduct.AccessToolDescription+vaultPromoteNote, vaultAccessChatParameters(),
		func(ctx context.Context, args map[string]interface{}) (string, error) {
			operation, _ := args["operation"].(string)
			if operation == "" {
				return "", fmt.Errorf("operation is required")
			}
			if operation == promoteOperation {
				in, _ := args["arguments"].(map[string]interface{})
				confirm, _ := in["confirm"].(bool)
				name, _ := in["name"].(string)
				return api.promoteConnectionToVault(ctx, userID, name, "", confirm)
			}
			payload, err := json.Marshal(args["arguments"])
			if err != nil {
				return "", err
			}
			return api.capLayerConnectionAccess(ctx, userID, operation, payload)
		}, "vault")
	// A chat can reach this from two registration points (the query handler and the Builder phase tools); the second is
	// identical, so a duplicate is not an error.
	if err != nil && (strings.Contains(err.Error(), "already") || strings.Contains(err.Error(), "duplicate")) {
		return nil
	}
	return err
}

const promoteOperation = "promote_place_connection"

const vaultPromoteNote = " promote_place_connection: {name, confirm}: make a sign-in connection that already works in this Crew, Code or workflow (or in the user's own store; use its exact name from list_mcp_servers) a shared Vault connection with its existing sign-in, no second sign-in. Call it once without confirm to get the explanation, then with confirm=true. No group gets access by this; grant a group afterwards with save_permissions. Only the person who signed the connection in can promote it, and only an administrator. There is no other way to move a connection into Vault."

// vaultAccessChatParameters is the Vault chat's tool schema plus the promote operation, which needs this chat's own
// Crew, Code or workflow and so does not exist in the Vault chat.
func vaultAccessChatParameters() map[string]interface{} {
	params := caplayerproduct.AccessToolParameters()
	props, _ := params["properties"].(map[string]interface{})
	if op, ok := props["operation"].(map[string]interface{}); ok {
		if enum, ok := op["enum"].([]string); ok {
			op["enum"] = append(append([]string{}, enum...), promoteOperation)
		}
	}
	return params
}
