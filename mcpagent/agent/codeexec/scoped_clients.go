package codeexec

import (
	"crypto/sha256"
	"encoding/hex"
	"log"
	"strings"
	"sync"

	"github.com/manishiitg/mcpagent/mcpclient"
)

// Scoped names are authorization identities. Keep their identity unchanged;
// ownership only bounds their lifetime. Ordinary global clients remain pooled.
var scopedClients = struct {
	sync.Mutex
	users map[string]int
}{users: make(map[string]int)}

// RetainScopedMCPServer must run before lazy registration or eager connection.
// The returned release is idempotent and closes only the last user's exact key.
func RetainScopedMCPServer(server string) func() {
	public, scope, ok := strings.Cut(server, "__scope_")
	if !ok || (!strings.HasPrefix(public, "vault_") && !strings.HasPrefix(public, "srv_")) || len(scope) != 32 {
		return func() {}
	}
	if _, err := hex.DecodeString(scope); err != nil {
		return func() {}
	}
	scopedClients.Lock()
	scopedClients.users[server]++
	scopedClients.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			scopedClients.Lock()
			defer scopedClients.Unlock()
			scopedClients.users[server]--
			hash := sha256.Sum256([]byte(server))
			log.Printf("[MCP_SCOPED] released key=%x remaining=%d", hash[:16], scopedClients.users[server])
			if scopedClients.users[server] > 0 {
				return
			}
			delete(scopedClients.users, server)
			// Serialize deletion with acquiring a new owner, so old delayed cleanup
			// cannot remove a replacement connection for the same delegation.
			registryMu.Lock()
			if globalRegistry != nil {
				globalRegistry.mu.Lock()
				delete(globalRegistry.mcpClients, server)
				for tool, target := range globalRegistry.toolToServer {
					if target == server {
						delete(globalRegistry.toolToServer, tool)
					}
				}
				for key := range globalRegistry.reportedToolMappingConflicts {
					parts := strings.Split(key, "\x00")
					if len(parts) == 3 && (parts[1] == server || parts[2] == server) {
						delete(globalRegistry.reportedToolMappingConflicts, key)
					}
				}
				globalRegistry.mu.Unlock()
			}
			registryMu.Unlock()
			mcpclient.GetSessionRegistry().CloseServerConnections(server)
		})
	}
}

// ScopedMCPCounts reports owned delegation keys and their client entries in the
// connection pool. Pending lazy configs are keys, not connected clients.
func ScopedMCPCounts() (keys, clients int) {
	scopedClients.Lock()
	defer scopedClients.Unlock()
	live := mcpclient.GetSessionRegistry().GetSessionConnections("global")
	for key := range scopedClients.users {
		keys++
		if live[key] != nil {
			clients++
		}
	}
	return
}

// Used only for log deduplication, never for selecting a connection or access.
func mappingLogServer(server string) string {
	public, scope, ok := strings.Cut(server, "__scope_")
	if ok && (strings.HasPrefix(public, "vault_") || strings.HasPrefix(public, "srv_")) && len(scope) == 32 {
		if _, err := hex.DecodeString(scope); err == nil {
			return public
		}
	}
	return server
}
