package mcpagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"
)

// A server outside the catalog (a person's own server) connects from the
// complete configuration its override carries, never from a catalog lookup.
func TestExtraServerOverrideIsUsedInsteadOfTheCatalog(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(configPath, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	overrides := mcpclient.RuntimeOverrides{"u1a2b3c4d__svc": {Server: &mcpclient.MCPServerConfig{
		URL: "https://127.0.0.1/mcp", Protocol: mcpclient.ProtocolHTTP, PublicOnly: true,
	}}}
	_, _, _, _, tools, _, _, err := NewAgentConnectionWithSession(context.Background(), nil, "u1a2b3c4d__svc", configPath,
		"", "", nil, loggerv2.NewNoop(), true, overrides, "")
	if len(tools) != 0 {
		t.Fatalf("connected to a blocked address: %d tools", len(tools))
	}
	if err != nil && strings.Contains(err.Error(), "not found in config") {
		t.Fatalf("the extra server was looked up in the catalog: %v", err)
	}
	if got, ok := overrides.ExtraServerConfig("u1a2b3c4d__svc"); !ok || !got.PublicOnly {
		t.Fatalf("ExtraServerConfig = %+v %v", got, ok)
	}
	if _, ok := overrides.ExtraServerConfig("linear"); ok {
		t.Fatal("a catalog server reported as extra")
	}
}
