package step_based_workflow

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// PLAT-746-396: after a move, checking Runtime/planning or Runtime/code
// silently skips real definitions; the skills stamp also cannot find its manifest.
func TestMovedWorkflowStampChecksUseDefinitionsCodeAndRuntimeRoots(t *testing.T) {
	base := t.TempDir()
	docs, state := filepath.Join(base, "docs"), filepath.Join(base, "state")
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	t.Setenv("AGENTWORKS_STATE_ROOT", state)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	write := func(file, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(docs, "Crew/example"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(docs, crewroot.PrivateDirectory, "crew"), 0700); err != nil {
		t.Fatal(err)
	}
	project := crewroot.Record{Product: "work", Folder: "example", ID: "crew", Owner: "owner"}
	raw, _ := json.Marshal(map[string]interface{}{"projects": map[string]crewroot.Record{"work/example": project}})
	write(filepath.Join(state, "ownership/projects.json"), string(raw))
	binding := crewroot.ExecutableBinding{ID: "sequence", Root: "Workflow/sequence", CrewID: "crew", CrewRoot: "Crew/example", Kind: "workflow", RuntimeRoot: "workflows/sequence"}
	raw, _ = json.Marshal(binding)
	write(filepath.Join(state, "ownership/crew-executables", fmt.Sprintf("%x.json", sha256.Sum256([]byte(binding.Root)))), string(raw))
	layout, found, err := crewroot.LookupExecutable(docs, binding.Root)
	if err != nil || !found {
		t.Fatalf("fixture binding: %v", err)
	}
	if err := os.MkdirAll(layout.Runtime, 0700); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(layout.Definitions, "workflow.json"), `{"code_layout_version":1,"workflow_skills_migration":{"version":"1.0.51","files":[]}}`)
	write(filepath.Join(layout.Definitions, "planning/step_config.json"), `{"steps":[]}`)
	if err := ValidateWorkflowSkillsStamp(binding.Root); err != nil {
		t.Fatalf("moved skills manifest not found: %v", err)
	}
	write(filepath.Join(layout.Definitions, "planning/step_config.json"), `{"steps":[{"id":"step","agent_configs":{"learnings_access":"read"}}]}`)
	if err := ValidateWorkflowSkillsStamp(binding.Root); err == nil || !strings.Contains(err.Error(), "learnings_access") {
		t.Fatalf("moved config skipped: %v", err)
	}
	if err := validateStepDescriptionLayoutStamp(StepDescriptionLayoutContractVersion, binding.Root); err == nil {
		t.Fatal("missing moved plan silently passed")
	}
	write(filepath.Join(layout.Definitions, "planning/plan.json"), `{"steps":[{"type":"agent","id":"step","description":"Do the task"}]}`)
	if err := validateStepDescriptionLayoutStamp(StepDescriptionLayoutContractVersion, binding.Root); err == nil {
		t.Fatal("moved description was not checked")
	}
	raw, _ = json.Marshal(map[string]interface{}{"steps": []map[string]string{{"type": "agent", "id": "step", "description": layoutDescription}}})
	write(filepath.Join(layout.Definitions, "planning/plan.json"), string(raw))
	if err := validateStepDescriptionLayoutStamp(StepDescriptionLayoutContractVersion, binding.Root); err != nil {
		t.Fatal(err)
	}
}
