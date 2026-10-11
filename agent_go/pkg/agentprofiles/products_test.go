package agentprofiles

import (
	"reflect"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productpolicy"
)

func TestProfileProductProjectionDoesNotMutateRegistry(t *testing.T) {
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("AGENTWORKS_LOCAL_SERVER_PRODUCTS", "0")
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "")
	t.Setenv("AGENT_PRODUCTS", "")
	p := Profile{ID: "work", Name: "Crew", Version: 1, BuiltIn: true,
		SystemPromptTemplate: "project <!-- product:knowledgebase -->Brain<!-- /product -->",
		Skills:               []string{"brain", "work-mcp"},
		Features:             []FeatureBinding{{ID: "knowledgebase"}, {ID: "mcp"}, {ID: "secrets"}},
		Tools:                []ToolBinding{{ID: "knowledgebase.manage-access"}, {ID: "caplayer.access"}, {ID: "work.create-project"}},
		ToolPolicy:           ToolPolicy{Mode: "allowlist", Enabled: []string{"brain_read", "manage_vault_access", "list_mcp_servers", "set_workflow_secret"}},
		Runtime:              RuntimePolicy{Transport: "structured", BridgeTools: []string{"brain_read", "list_mcp_servers"}},
		ResolvedFeatures:     []ResolvedFeature{{ID: "knowledgebase", Tools: []string{"brain_read"}}, {ID: "mcp", Tools: []string{"manage_vault_access", "list_mcp_servers"}}},
	}
	registry := NewRegistry()
	if err := registry.RegisterProfile(p); err != nil {
		t.Fatal(err)
	}
	raw, err := registry.Resolve("work", 1, "default")
	if err != nil {
		t.Fatal(err)
	}
	local := raw.ForProducts(productpolicy.Selection{})
	if strings.Contains(local.SystemPromptTemplate, "Brain") || len(local.Skills) != len(raw.Skills)-1 || len(local.Features) != 2 || len(local.Tools) != 1 || len(local.ResolvedFeatures) != 2 {
		t.Fatalf("local manifest retained server capabilities: %+v", local)
	}
	for _, name := range append(local.ToolPolicy.Enabled, local.Runtime.BridgeTools...) {
		if productpolicy.ToolProduct(name) != "" {
			t.Fatalf("server tool retained in metadata: %s", name)
		}
	}
	if !strings.Contains(strings.Join(local.ToolPolicy.Enabled, ","), "set_workflow_secret") || !strings.Contains(strings.Join(local.ToolPolicy.Enabled, ","), "list_mcp_servers") {
		t.Fatal("local project resources were removed")
	}
	t.Setenv("AGENTWORKS_LOCAL_SERVER_PRODUCTS", "1")
	again, err := registry.Resolve("work", 1, "default")
	if err != nil {
		t.Fatal(err)
	}
	server := again.ForProducts(productpolicy.Selection{})
	if !strings.Contains(server.SystemPromptTemplate, "Brain") || len(server.Skills) != len(raw.Skills) || len(server.Tools) != 3 || len(server.ToolPolicy.Enabled) != len(raw.ToolPolicy.Enabled) || !reflect.DeepEqual(again, raw) {
		t.Fatal("local projection mutated the shared server manifest")
	}
}
