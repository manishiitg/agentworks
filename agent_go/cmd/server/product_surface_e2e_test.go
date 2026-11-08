package server

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
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
