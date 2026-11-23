package picli

import "testing"

// Two sessions launching the same bridge config differ only in their
// per-session values (session ID, scoped URL, bridge token); they are the
// same config for the working-directory lease.
func TestPiMCPConfigFingerprintIgnoresPerSessionBridgeValues(t *testing.T) {
	config := func(session, token string) string {
		return `{"mcpServers":{"api-bridge":{"command":"mcpbridge","env":{"MCP_API_URL":"http://h/s/` + session + `","MCP_SESSION_ID":"` + session + `","MCP_API_TOKEN":"` + token + `","MCP_AUTH":"Authorization: Bearer ` + token + `","MCP_TOOLS":"[]"}}}}`
	}
	if piMCPConfigFingerprint(config("a", "mcps1.a.x")) != piMCPConfigFingerprint(config("b", "mcps1.b.y")) {
		t.Fatal("per-session bridge values must not change the config fingerprint")
	}
	other := `{"mcpServers":{"api-bridge":{"command":"mcpbridge","env":{"MCP_API_URL":"http://h/s/a","MCP_TOOLS":"[\"x\"]"}}}}`
	if piMCPConfigFingerprint(config("a", "t")) == piMCPConfigFingerprint(other) {
		t.Fatal("a different tool set must still change the fingerprint")
	}
}
