package step_based_workflow

import (
	"context"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"
	"path/filepath"
	"testing"
)

func TestWorkflowMCPPreflightUsesRunOwnerScopeInsteadOfGlobalCatalog(t *testing.T) {
	previous := common.ScopeAgentMCP
	t.Cleanup(func() { common.ScopeAgentMCP = previous })
	path := filepath.Join(t.TempDir(), "mcp.json")
	if err := mcpclient.SaveConfig(path, &mcpclient.MCPConfig{MCPServers: map[string]mcpclient.MCPServerConfig{"LegacyShared": {URL: "https://example.com/mcp"}}}); err != nil {
		t.Fatal(err)
	}
	common.ScopeAgentMCP = func(ctx context.Context, session string, names []string, overrides mcpclient.RuntimeOverrides) ([]string, mcpclient.RuntimeOverrides, map[string]string, error) {
		if session != "owner-chat" {
			t.Fatal("preflight lost its main session identity")
		}
		return []string{"u_private", "vault_scoped"}, nil, map[string]string{"myprivate": "u_private", "vault_connection": "vault_scoped"}, nil
	}
	available, err := workflowMCPAvailability(context.Background(), "owner-chat", []string{"MyPrivate", "vault_connection", "LegacyShared"}, path, loggerv2.NewNoop())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"myprivate", "vault_connection"} {
		if _, found := available[name]; !found {
			t.Fatalf("authorized connection %s was rejected because absent from global catalog", name)
		}
	}
	if _, found := available["legacyshared"]; found {
		t.Fatal("catalog definition was treated as a global access grant")
	}
}
