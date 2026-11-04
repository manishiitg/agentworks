package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"text/template"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
	workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type productSurfaceDraft struct {
	recordingRegistrar
	skills []*llmtypes.Skill
	gate   *productToolGate
}

func (d *productSurfaceDraft) RegisterCustomTool(name, description string, parameters map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error), category string) error {
	if d.gate != nil && !d.gate.Admit(name) {
		return nil
	}
	return d.recordingRegistrar.RegisterCustomTool(name, description, parameters, execute, category)
}

func (d *productSurfaceDraft) RegisterCustomToolWithTimeout(name, description string, parameters map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error), _ time.Duration, category string) error {
	return d.RegisterCustomTool(name, description, parameters, execute, category)
}

func (d *productSurfaceDraft) AttachSkill(s *llmtypes.Skill) error {
	d.skills = append(d.skills, s)
	return nil
}
func (d *productSurfaceDraft) AttachedSkills() []*llmtypes.Skill { return d.skills }

func TestAgentWorksProductSurfaceE2E(t *testing.T) {
	server, _ := newFakeWorkspaceServer(t)
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)

	for _, readOnly := range []bool{false, true} {
		mode := "builder"
		workshopMode := "workshop"
		if readOnly {
			mode = "run"
			workshopMode = "run"
		}

		logger := loggerv2.NewNoop()
		session, err := workflow.NewWorkshopChatSession(context.Background(), &workflow.WorkshopConfig{Logger: logger, WorkspacePath: "Workflow/surface-e2e"})
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		env := newCrewFunctionEnv(t)
		api := env.api
		// A workflow Builder has a saved manifest. Seed the actual fixture
		// server (newCrewFunctionEnv installs it) so conditional Watcher
		// registration participates in the full declared surface check.
		fixtureManifest := NewWorkflowManifest("Surface fixture")
		fixtureManifest.CreatedBy = "test-user"
		fixtureRaw, err := json.Marshal(fixtureManifest)
		if err != nil {
			t.Fatal(err)
		}
		env.mock.mu.Lock()
		env.mock.files[manifestPath("Workflow/surface-e2e")] = string(fixtureRaw)
		env.mock.mu.Unlock()
		api.logger = logger
		api.workshopChatSessions.Store("surface-e2e", session)
		api.workshopChatSessions.Store("eval-surface-e2e", session)
		gate := newAgentWorksChatToolGate(mode)
		gate.AllowWorkflowNotifications(true)
		draft := &productSurfaceDraft{gate: gate}
		if err := api.installWorkflowPhaseTools(context.Background(), draft, "surface-e2e", "test-user", "workflow-builder", "Workflow/surface-e2e", "", map[string]string{"WorkshopMode": workshopMode}, nil, nil, nil, nil, nil, QueryRequest{}, readOnly); err != nil {
			t.Fatal(err)
		}
		if err := api.registerMultiAgentLLMTools(draft, nil); err != nil {
			t.Fatal(err)
		}
		if !readOnly {
			if err := api.registerCodingAgentClarificationTool(draft, "surface-e2e", "muse-cli", 0); err != nil {
				t.Fatal(err)
			}
		}
		names := []string{}
		for name := range draft.tools {
			names = append(names, name)
		}
		// Include the real platform pools as well as phase/function registrations.
		// Runtime capabilities can narrow a declaration, never expand it.
		customTools, customExecutors, _ := createCustomTools(true, "test-user", "surface-e2e")
		for _, tool := range customTools {
			if tool.Function == nil || !agentworksproduct.ChatAllowsTool(mode, tool.Function.Name) {
				continue
			}
			if _, ok := customExecutors[tool.Function.Name]; !ok {
				t.Fatalf("declared human tool %q has no executor", tool.Function.Name)
			}
			names = append(names, tool.Function.Name)
		}
		names = uniqueSortedToolNames(names)
		manifest, err := agentworksproduct.AgentWorksManifest()
		if err != nil {
			t.Fatal(err)
		}
		def := manifest.Chat[mode]
		// This fixture has no Brain bindings, Vault admin or workflow model
		// write grant, and does not install the separate workflow-reference
		// reader. Those conditional implementations have their own integration
		// tests; all other declarations, including Watchers with the saved
		// fixture manifest, must match the real registration pools.
		expectedTools := []string{}
		for _, name := range def.Tools {
			if !knowledgebase.IsMCPTool(name) && name != "manage_vault_access" && name != "set_workflow_llm_config" && name != "list_accessible_workflows" {
				expectedTools = append(expectedTools, name)
			}
		}
		if err := compareProductSurface(names, expectedTools); err != nil {
			t.Fatal(err)
		}
		logConfigurationCatalog(t, "goal-"+mode+"-phase", draft.tools)
		logDataCatalog(t, "goal-"+mode+"-phase", draft.tools)
		skillNames := []string{}
		for _, skill := range draft.skills {
			skillNames = append(skillNames, skill.Name)
		}
		if err := compareProductSurface(skillNames, def.Skills); err != nil {
			t.Fatal(err)
		}
		source, err := agentprofiles.LoadChatPrompt(os.DirFS("../../internal/agentworksproduct"), def.Prompt)
		if err != nil {
			t.Fatal(err)
		}
		templateName := "interactiveWorkshopSystem"
		if readOnly {
			templateName = "interactiveRunSystem"
		}
		expected, err := template.New(templateName).Parse(source)
		if err != nil {
			t.Fatal(err)
		}
		actual := workflow.GetTemplate(templateName)
		if actual == nil || len(actual.Templates()) != len(expected.Templates()) {
			t.Fatal("registered prompt templates differ from product.yaml")
		}
		for _, part := range expected.Templates() {
			got := actual.Lookup(part.Name())
			if got == nil || got.Tree.Root.String() != part.Tree.Root.String() {
				t.Fatalf("registered prompt %s is not its product.yaml source", part.Name())
			}
		}
		assertInstructionSectionsDeclared(t, manifest.InstructionSections)
	}
}

// Compare declarations independently of productToolGate.Declare: a new Go
// registration cannot silently widen its own expected surface.
func compareProductSurface(actual, declared []string) error {
	actualSet, declaredSet := map[string]bool{}, map[string]bool{}
	for _, name := range actual {
		actualSet[name] = true
	}
	for _, name := range declared {
		declaredSet[name] = true
	}
	var undeclared, missing []string
	for name := range actualSet {
		if !declaredSet[name] {
			undeclared = append(undeclared, name)
		}
	}
	for name := range declaredSet {
		if !actualSet[name] {
			missing = append(missing, name)
		}
	}
	if len(undeclared) > 0 || len(missing) > 0 {
		return fmt.Errorf("product.yaml surface differs: undeclared=%v missing=%v", uniqueSortedToolNames(undeclared), uniqueSortedToolNames(missing))
	}
	return nil
}

func comparePromptSource(actual string, expected string) error {
	if actual != expected {
		return fmt.Errorf("system prompt differs from product.yaml source")
	}
	return nil
}

func TestCrewProductSurfaceE2E(t *testing.T) {
	manifest, err := workproduct.WorkManifest()
	if err != nil {
		t.Fatal(err)
	}
	registry := agentprofiles.NewRegistry()
	if err := workproduct.RegisterAgentProfileRuntime(registry, "http://127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	profile := workproduct.BuiltinAgentProfile()
	allowed := append([]string(nil), manifest.Profile.ToolPolicy.Enabled...)
	for _, binding := range manifest.Profile.Tools {
		tool, err := registry.BuildTool(binding, agentprofiles.ToolRuntimeContext{UserID: "test-user", SessionID: "surface-e2e", WorkspacePath: "Chats/Work/projects/surface-e2e"})
		if err != nil {
			t.Fatal(err)
		}
		allowed = append(allowed, tool.Name)
	}
	allowedSet := map[string]bool{}
	for _, name := range allowed {
		allowedSet[name] = true
	}
	draft := &productSurfaceDraft{}
	api := &StreamingAPI{agentProfiles: registry}
	resolved := &resolvedAgentProfile{Definition: profile}
	if err := api.registerAgentProfileTools(draft, newProductToolGate(resolved), resolved, "test-user", "surface-e2e", "Chats/Work/projects/surface-e2e", false, QueryRequest{}); err != nil {
		t.Fatal(err)
	}
	for name := range draft.tools {
		if !allowedSet[name] {
			t.Errorf("Crew registered undeclared tool %q; declare its feature/binding in product.yaml", name)
		}
	}
	if len(draft.tools) == 0 {
		t.Fatal("no production registration was exercised")
	}
	if err := compareProductSurface(profile.Skills, manifest.Profile.Skills); err != nil {
		t.Fatal(err)
	}
	prompt, err := manifest.RenderPrompt(os.DirFS("../../internal/workproduct"), manifest.Profile, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := comparePromptSource(profile.SystemPromptTemplate, prompt); err != nil {
		t.Fatal(err)
	}
	runPrompt, err := agentprofiles.LoadChatPrompt(os.DirFS("../../internal/workproduct"), manifest.Chat["run"].Prompt)
	if err != nil || runPrompt != workproduct.RunPromptTemplate() {
		t.Fatalf("Crew Run prompt is not its product.yaml source: %v", err)
	}
	assertInstructionSectionsDeclared(t, manifest.InstructionSections)
}

func assertInstructionSectionsDeclared(t *testing.T, declared []string) {
	t.Helper()
	actual := []string{}
	for _, section := range promptSections {
		actual = append(actual, section.Name)
	}
	if err := compareProductSurface(actual, declared); err != nil {
		t.Fatalf("shared prompt sections: %v", err)
	}
}

func TestProductSurfaceGuardRejectsUndeclaredAdditions(t *testing.T) {
	for _, kind := range []string{"tool", "skill", "prompt section"} {
		if err := compareProductSurface([]string{"declared", "rogue-" + kind}, []string{"declared"}); err == nil {
			t.Fatalf("guard accepted undeclared %s", kind)
		}
	}
	if err := comparePromptSource("declared prompt\nrogue instruction", "declared prompt"); err == nil {
		t.Fatal("guard accepted extra system instructions")
	}
}
