package server

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/caplayerproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productpolicy"
)

func assertNoDisabledProductGuidance(t *testing.T, label, body string) {
	t.Helper()
	pattern := regexp.MustCompile(`\b(Vault|Brain|Flows?|Relays?)\b|\bCode (projects?|workspaces?|dashboards?|scripts?|owners?|is|remains|stays|may|can use)\b|\bCode's|Crew(?:/Code| or Code| and Code)|\bCodes\b|[Ii]n a Code\b`)
	for _, line := range strings.Split(body, "\n") {
		// Saved Resources fields and the quoted conditional runtime prompt are
		// technical contracts, not advertisements for the current product set.
		candidate := strings.ReplaceAll(line, "`Brain:`", "")
		if strings.HasPrefix(candidate, `{{if eq .KbAccess "shared"}}`) {
			candidate = strings.ReplaceAll(candidate, "**Shared Brain:**", "**Shared reference knowledge:**")
		}
		if pattern.MatchString(candidate) {
			t.Errorf("%s mentions disabled product: %s", label, line)
		}
	}
}

func TestBuilderProductsGateCachedAndExternalTools(t *testing.T) {
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("AGENTWORKS_LOCAL_SERVER_PRODUCTS", "0")
	t.Setenv("AGENT_PRODUCTS", "")
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "")
	blocked := append(knowledgebase.ToolNames(), caplayerproduct.ExternalTools()...)
	blocked = append(blocked, "brain_schedule", "brain_secrets", "run_relay", "test_relay", "publish_relay")
	gate := newProductToolGate(nil)
	claims := &UserClaims{UserID: "default"}
	resolve := (&StreamingAPI{}).bindToolExecutionContext(context.WithValue(context.Background(), UserContextKey, claims), "product-selection-test", QueryRequest{}, false)
	for _, name := range blocked {
		if productpolicy.ToolProduct(name) == "" {
			t.Fatalf("server tool missing ownership: %s", name)
		}
		gate.Declare(name)
		if gate.Allows(name) || externalTokenAllows(claims, externalTool{Name: name}) {
			t.Fatalf("disabled server tool advertised: %s", name)
		}
		if _, err := resolve(context.Background(), name); err == nil || !strings.Contains(err.Error(), "product unavailable") {
			t.Fatalf("cached call allowed: %s", name)
		}
	}
	for _, name := range []string{"set_workflow_secret", "list_mcp_servers", "call_mcp_tool", "private_vault_tool"} {
		if !gate.Allows(name) {
			t.Fatalf("local project capability removed: %s", name)
		}
		if _, err := resolve(context.Background(), name); err != nil {
			t.Fatalf("local project execution denied: %s: %v", name, err)
		}
	}
}

func TestBuilderProductsLocalToolGuidance(t *testing.T) {
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("AGENTWORKS_LOCAL_SERVER_PRODUCTS", "0")
	t.Setenv("AGENT_PRODUCTS", "")
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "")
	selection := builderProductSelection(nil)
	reg := &recordingRegistrar{}
	api := &StreamingAPI{}
	if err := api.registerMultiAgentMCPServerTools(reg, nil); err != nil {
		t.Fatal(err)
	}
	if err := api.registerMultiAgentSkillToolsIn(reg, nil, "work", ""); err != nil {
		t.Fatal(err)
	}
	for name, tool := range reg.tools {
		if !selection.AllowsTool(name) {
			continue
		}
		// Vault is the only MCP store (2026-10-22), so these tools name it by design.
		if strings.Contains(name, "mcp_") {
			continue
		}
		assertNoDisabledProductGuidance(t, name, selection.Text(tool.desc))
		data, err := json.Marshal(selection.Schema(tool.params))
		if err != nil {
			t.Fatal(err)
		}
		assertNoDisabledProductGuidance(t, name+" schema", string(data))
	}
	tools, _, _ := createCustomTools(false)
	for _, tool := range tools {
		if tool.Function != nil {
			assertNoDisabledProductGuidance(t, tool.Function.Name, selection.Text(tool.Function.Description))
		}
	}
}

func TestLocalProductSelectionExcludesRelayGuidance(t *testing.T) {
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("AGENTWORKS_LOCAL_SERVER_PRODUCTS", "0")
	for _, topic := range externalGuidanceTopics {
		if topic.Source != "relay" {
			continue
		}
		if externalTopicVisible(nil, topic) {
			t.Fatalf("local catalog exposes %s", topic.Name)
		}
		if _, _, err := externalGuidanceContent(topic.Name); err == nil {
			t.Fatalf("local guidance serves %s", topic.Name)
		}
	}
}
