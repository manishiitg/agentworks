package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/mcpagent/executor"
	"github.com/manishiitg/mcpagent/mcpclient"
)

// CLI bridge requests have a session bearer, not browser JWT claims. Resolve
// their identity from server-owned session data; never authorize OAuth as the
// generic default user just because the HTTP context has no browser claims.
func (api *StreamingAPI) mcpToolUserID(ctx context.Context) (string, error) {
	userID := ""
	if claims := GetUserFromContext(ctx); claims != nil {
		userID = strings.TrimSpace(claims.UserID)
	}
	if directID, _ := ctx.Value(common.UserIDKey).(string); directID != "" {
		if userID != "" && userID != directID {
			return "", fmt.Errorf("MCP tool user identity mismatch")
		}
		userID = directID
	}
	sessionID := executor.SessionIDFromContext(ctx)
	if sessionID == "" {
		sessionID, _ = ctx.Value(common.ChatSessionIDKey).(string)
	}
	if sessionID != "" {
		owner := ""
		if api.eventStore != nil {
			owner = api.mcpSessionPerson(sessionID)
		}
		if owner == "" {
			return "", fmt.Errorf("MCP tool session owner is unavailable")
		}
		if userID != "" && userID != owner {
			return "", fmt.Errorf("MCP tool user does not own this session")
		}
		userID = owner
	}
	if userID == "" {
		return "", fmt.Errorf("MCP tool requires an authenticated user")
	}
	return userID, nil
}

// Connection status comes from the current Vault inventory, never a private store.
func (api *StreamingAPI) mcpToolStatusForUser(name, person string, _ mcpclient.MCPServerConfig) ToolStatus {
	rows, err := vaultServersFor(context.Background(), person)
	if err == nil {
		for _, row := range rows {
			if vaultServerName(row.ID) == name {
				return ToolStatus{Name: name, Server: name, Status: "not_loaded", Connection: connectionConnected}
			}
		}
	}
	return ToolStatus{Name: name, Server: name, Status: "not_connected", Connection: connectionAvailable}
}
