package server

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/guidance"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/caplayerproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productpolicy"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func assertNoDisabledProductGuidance(t *testing.T, label, body string) {
	t.Helper()
	pattern := regexp.MustCompile(`\b(Vault|Brain|Relays?)\b`)
	for _, line := range strings.Split(body, "\n") {
		if pattern.MatchString(line) {
			t.Errorf("%s mentions disabled product: %s", label, line)
		}
	}
}

func TestBuilderProductsCanonicalPromptsAndSkills(t *testing.T) {
	t.Setenv("AGENT_PRODUCTS", "")
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "")
	if err := workproduct.RegisterProductSkills(); err != nil {
		t.Fatal(err)
	}
	if err := workproduct.RegisterFeatureSkills("code", "Code"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, mode, opt string
		enabled         bool
	}{{"local", "local", "0", false}, {"local opt-in", "local", "1", true}, {"server", "server", "0", true}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", tc.mode)
			t.Setenv("AGENTWORKS_LOCAL_SERVER_PRODUCTS", tc.opt)
			selection := builderProductSelection(nil)
			for _, p := range []agentprofiles.Profile{workproduct.BuiltinAgentProfile(), codeproduct.BuiltinAgentProfile()} {
				text := p.ForProducts(selection).SystemPromptTemplate
				if !tc.enabled {
					assertNoDisabledProductGuidance(t, p.ID+" prompt", text)
				}
				if strings.Contains(text, "Vault") != tc.enabled || strings.Contains(text, "<!-- product:") {
					t.Fatalf("%s prompt differs from deployment: %s", p.ID, text)
				}
			}
			if text := selection.Text(agentworksproduct.ChatPromptTemplate("builder")); strings.Contains(text, "brain_browse") != tc.enabled {
				t.Fatal("workflow builder Brain guidance differs from deployment")
			}
			for _, ref := range []*llmtypes.Skill{guidance.MaterializeReferenceSkill("workshop"), guidance.MaterializeReferenceSkill("run"), guidance.MaterializeGuidanceSkill("workshop")} {
				if ref == nil {
					continue
				}
				projected := selection.Skill(ref)
				for _, file := range projected.SupportingFiles {
					body := string(file.Content)
					if !tc.enabled {
						assertNoDisabledProductGuidance(t, ref.Name+"/"+file.RelPath, body)
					}
					if strings.Contains(body, "<!-- product:") || !tc.enabled && (strings.Contains(body, "brain_browse") || strings.Contains(body, "Vault")) {
						t.Fatalf("%s leaked product guidance: %s", file.RelPath, body)
					}
				}
			}
			loaded := skills.LoadAttachableIn("", "", []string{"work-mcp", "code-mcp", "work-integrations", "code-integrations", "work-dashboard", "code-dashboard"})
			if len(loaded) != 6 {
				t.Fatalf("canonical skill fixture incomplete: %d", len(loaded))
			}
			for _, skill := range loaded {
				body := selection.Skill(skill).Content
				if !tc.enabled {
					assertNoDisabledProductGuidance(t, skill.Name, body)
				}
				if !tc.enabled && strings.Contains(body, "Vault") {
					t.Fatalf("%s leaked Vault: %s", skill.Name, body)
				}
			}
		})
	}
}

func TestBuilderProductsGateCachedAndExternalTools(t *testing.T) {
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("AGENTWORKS_LOCAL_SERVER_PRODUCTS", "0")
	t.Setenv("AGENT_PRODUCTS", "")
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "")
	blocked := append(knowledgebase.ToolNames(), caplayerproduct.ExternalTools()...)
	blocked = append(blocked, "brain_schedule", "brain_secrets", "read_knowledgebase", "run_relay", "test_relay", "publish_relay")
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

func TestBuilderProductsExplicitServerSurfaceSelection(t *testing.T) {
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("AGENTWORKS_LOCAL_SERVER_PRODUCTS", "0")
	t.Setenv("AGENT_PRODUCTS", "")
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "code")
	claims := &UserClaims{UserID: "default"}
	for _, name := range []string{"list_crews", "brain_read", "manage_vault_access", "run_relay"} {
		if externalTokenAllows(claims, externalTool{Name: name}) {
			t.Fatalf("disabled product advertised by MCP: %s", name)
		}
	}
	text := (productpolicy.Selection{}).Text(externalMCPInstructions)
	if strings.Contains(text, "list_crews") || strings.Contains(text, "list_workflows") || strings.Contains(text, "<!-- product:") || !strings.Contains(text, "list_code_workspaces") {
		t.Fatal(text)
	}
	js := runtimeFrontendConfigJS(1, "")
	if !strings.Contains(js, `enabledProductSurfaces: ["code"]`) {
		t.Fatal(js)
	}
}

func TestBuilderProductsExternalCodeRequiresInstalledProduct(t *testing.T) {
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("AGENT_PRODUCTS", "")
	t.Setenv("AGENTWORKS_ENABLED_PRODUCT_SURFACES", "work")
	catalog, err := externalTools()
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, tool := range catalog {
		if isExternalCodeRunTool(tool.Name) || isExternalCodeReviewTool(tool.Name) {
			checked++
			if externalTokenAllows(&UserClaims{UserID: "default"}, tool) {
				t.Fatalf("disabled Code tool advertised: %s", tool.Name)
			}
		}
	}
	if checked == 0 {
		t.Fatal("Code coverage did not exercise any tools")
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
