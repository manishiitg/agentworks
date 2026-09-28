package security

import (
	"strings"
	"testing"
)

// Agent shells never inherit the server's bridge credentials: with the
// signing secret a shell could mint a tool-call token for any session.
func TestNativeEnvironmentDropsBridgeCredentials(t *testing.T) {
	for _, name := range []string{"MCP_API_TOKEN", "MCP_SERVER_API_TOKEN", "MCP_BRIDGE_TOKEN_SECRET", "WORKSPACE_API_TOKEN"} {
		t.Setenv(name, "secret-value")
	}
	for _, kv := range buildNativeEnvironment() {
		for _, name := range []string{"MCP_API_TOKEN=", "MCP_SERVER_API_TOKEN=", "MCP_BRIDGE_TOKEN_SECRET=", "WORKSPACE_API_TOKEN="} {
			if strings.HasPrefix(kv, name) {
				t.Errorf("%s leaked into the agent shell environment", strings.TrimSuffix(name, "="))
			}
		}
	}
}
