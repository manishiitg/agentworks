// session.go
//
// This file provides public API for session-scoped connection management.
// When agents are created with SessionID, their MCP connections are stored
// in a shared session registry and persist until explicitly closed.

package mcpagent

import (
	"os"

	"github.com/manishiitg/mcpagent/mcpclient"
)

func CloseSession(sessionID string) {
	closeTurnSession(sessionID)
	registry := mcpclient.GetSessionRegistry()
	registry.CloseSession(sessionID)
	RemoveIsolatedSessionWorkspace(sessionID)
}

func RemoveIsolatedSessionWorkspace(sessionID string) {
	if dir := isolatedWorkspaceDirForSession(sessionID); dir != "" {
		_ = os.RemoveAll(dir)
	}
}

func CloseSessionServer(sessionID, serverName string) {
	registry := mcpclient.GetSessionRegistry()
	registry.CloseSessionServer(sessionID, serverName)
}

func GetSessionStats(sessionID string) *mcpclient.SessionStats {
	registry := mcpclient.GetSessionRegistry()
	return registry.GetSessionStats(sessionID)
}

func GetAllSessionStats() map[string]*mcpclient.SessionStats {
	registry := mcpclient.GetSessionRegistry()
	sessions := registry.ListSessions()

	result := make(map[string]*mcpclient.SessionStats)
	for _, sessionID := range sessions {
		if stats := registry.GetSessionStats(sessionID); stats != nil {
			result[sessionID] = stats
		}
	}
	return result
}

func GetSessionConnections(sessionID string) []string {
	registry := mcpclient.GetSessionRegistry()
	connections := registry.GetSessionConnections(sessionID)
	if connections == nil {
		return nil
	}

	servers := make([]string, 0, len(connections))
	for serverName := range connections {
		servers = append(servers, serverName)
	}
	return servers
}

func ListSessions() []string {
	registry := mcpclient.GetSessionRegistry()
	return registry.ListSessions()
}

func CloseAllSessions() {
	registry := mcpclient.GetSessionRegistry()
	registry.CloseAllSessions()
}

func HasSession(sessionID string) bool {
	registry := mcpclient.GetSessionRegistry()
	return registry.HasSession(sessionID)
}

func RegisterHTTPSession(httpSessionID, mcpSessionID string) {
	registry := mcpclient.GetSessionRegistry()
	registry.RegisterHTTPSession(httpSessionID, mcpSessionID)
}

func ResolveConnectionSessionID(sessionID, serverName string) string {
	registry := mcpclient.GetSessionRegistry()
	return registry.ResolveConnectionSessionID(sessionID, serverName)
}

func MarkSessionsStopped(sessionIDs []string) {
	registry := mcpclient.GetSessionRegistry()
	registry.MarkSessionsStopped(sessionIDs)
}

func ClearSessionsStopped(sessionIDs []string) {
	registry := mcpclient.GetSessionRegistry()
	registry.ClearSessionsStopped(sessionIDs)
}

func CloseHTTPSession(httpSessionID string) {
	registry := mcpclient.GetSessionRegistry()
	registry.CloseHTTPSession(httpSessionID)
}

func ClearHTTPSessionStopped(httpSessionID string) {
	registry := mcpclient.GetSessionRegistry()
	registry.ClearHTTPSessionStopped(httpSessionID)
}

func GetSessionRegistry() *mcpclient.SessionConnectionRegistry {
	return mcpclient.GetSessionRegistry()
}
