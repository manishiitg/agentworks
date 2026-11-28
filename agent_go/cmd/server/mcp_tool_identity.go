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

func normalizeVaultCatalogKey(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Connection status comes from the current Vault inventory, never a private store.
func vaultServerSetFor(ctx context.Context, person string) map[string]struct{} {
	rows, err := vaultServersFor(ctx, person)
	if err != nil || len(rows) == 0 {
		return nil
	}
	connected := make(map[string]struct{}, len(rows)*4)
	for _, row := range rows {
		name := vaultServerName(row.ID)
		connected[name] = struct{}{}
		connected["id:"+strings.ToLower(name)] = struct{}{}
		if key := normalizeVaultCatalogKey(row.Provider); key != "" {
			connected["provider:"+key] = struct{}{}
		}
		if label := strings.ToLower(strings.TrimSpace(row.Label)); label != "" {
			connected["label:"+label] = struct{}{}
		}
	}
	return connected
}

func mcpServerInVaultSet(name string, connected map[string]struct{}) bool {
	if len(connected) == 0 {
		return false
	}
	if _, ok := connected[name]; ok {
		return true
	}
	if _, ok := connected["id:"+strings.ToLower(vaultSelectionName(name))]; ok {
		return true
	}
	if key := normalizeVaultCatalogKey(name); key != "" {
		if _, ok := connected["provider:"+key]; ok {
			return true
		}
	}
	if label := strings.ToLower(strings.TrimSpace(name)); label != "" {
		if _, ok := connected["label:"+label]; ok {
			return true
		}
	}
	return false
}

func mcpToolStatusFromSet(name string, connected map[string]struct{}) ToolStatus {
	if mcpServerInVaultSet(name, connected) {
		return ToolStatus{Name: name, Server: name, Status: "not_loaded", Connection: connectionConnected}
	}
	return ToolStatus{Name: name, Server: name, Status: "not_connected", Connection: connectionAvailable}
}

func (api *StreamingAPI) mcpToolStatusForUser(name, person string, _ mcpclient.MCPServerConfig) ToolStatus {
	return mcpToolStatusFromSet(name, vaultServerSetFor(context.Background(), person))
}
