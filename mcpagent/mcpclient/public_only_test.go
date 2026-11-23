package mcpclient

import (
	"context"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"strings"
	"testing"
)

// A user-supplied (public-only) server is remote with a public https URL;
// nothing else connects.
func TestPublicOnlyServersAreRemoteAndPublic(t *testing.T) {
	for name, cfg := range map[string]MCPServerConfig{
		"stdio":      {Command: "/bin/sh", Args: []string{"-c", "true"}, PublicOnly: true},
		"plain http": {URL: "http://example.com/mcp", Protocol: ProtocolHTTP, PublicOnly: true},
		"loopback":   {URL: "https://127.0.0.1/mcp", Protocol: ProtocolHTTP, PublicOnly: true},
		"metadata":   {URL: "https://169.254.169.254/latest", Protocol: ProtocolHTTP, PublicOnly: true},
		"localhost":  {URL: "https://localhost:8443/mcp", Protocol: ProtocolSSE, PublicOnly: true},
	} {
		err := New(cfg, loggerv2.NewNoop()).Connect(context.Background())
		if err == nil || !(strings.Contains(err.Error(), "must be remote") || strings.Contains(err.Error(), "refused")) {
			t.Errorf("%s: connected or wrong error: %v", name, err)
		}
	}
}
