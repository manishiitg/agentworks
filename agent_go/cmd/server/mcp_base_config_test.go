package server

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/manishiitg/mcpagent/mcpclient"
)

// Adding a server changed the shared base config map while listings and
// discovery ranged over it: a fatal concurrent map access that stopped the
// server (run with -race). Readers get copies, edits happen under the lock.
func TestBaseMCPConfigEditsAndReadsDoNotRace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp_servers.json")
	api := &StreamingAPI{mcpConfig: &mcpclient.MCPConfig{MCPServers: map[string]mcpclient.MCPServerConfig{}}, mcpConfigPath: path}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			name := fmt.Sprintf("server-%d", i)
			if err := api.changeBaseMCPConfig(func(cfg *mcpclient.MCPConfig) error {
				return cfg.AddServer(name, mcpclient.MCPServerConfig{URL: "https://example.com/mcp"}, path)
			}); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	overlay := &mcpclient.MCPConfig{MCPServers: map[string]mcpclient.MCPServerConfig{"custom": {URL: "https://example.com/custom"}}}
	for i := 0; i < 200; i++ {
		copied := api.baseMCPConfig()
		for name := range copied.MCPServers {
			copied.MCPServers[name+"-reader"] = mcpclient.MCPServerConfig{}
		}
		_ = api.customServers(overlay)
	}
	<-done
	if got := len(api.baseMCPConfig().MCPServers); got != 200 {
		t.Fatalf("base config has %d servers, want 200 (a reader's copy leaked into it?)", got)
	}
}
