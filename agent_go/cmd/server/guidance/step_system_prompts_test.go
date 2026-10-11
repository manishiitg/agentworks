package guidance

import (
	"strings"
	"testing"
	"text/template"
)

func TestStepSystemPromptsShareRuntimeAndBuilderSource(t *testing.T) {
	skill := MaterializeReferenceSkill("workshop")
	const ref = "references/step-system-prompts.md"
	if skill == nil || !strings.Contains(skill.Content, ref) {
		t.Fatal("builder cannot discover the runtime prompt reference")
	}
	projected := materializedFileContent(t, skill, ref)
	if projected != pathDisciplineGuidance+RenderSystemDoc("step-system-prompts") {
		t.Fatal("builder reference drifted from the canonical prompt source")
	}
	// The builder needs to see both branches, not a dummy rendering with all
	// permissions disabled or runtime placeholders silently erased.
	parsed, err := template.New("builder-source").Parse(projected)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"execution", "agent-delegation", "managed-db-read", "managed-db-write"} {
		body := parsed.Lookup(name)
		if body == nil || body.Tree.Root.String() != StepSystemPromptTemplate(name) {
			t.Fatalf("builder and runtime disagree on %s", name)
		}
	}
	for _, marker := range []string{"{{.FolderGuardReadPaths}}", "{{.DBGuidance}}", "{{if and .ValidationSchema", "## Declared Inputs", "{{.StepContextDependencies}}"} {
		if !strings.Contains(projected, marker) {
			t.Fatalf("source reference lost runtime conditional/placeholder %s", marker)
		}
	}
	guide := materializedFileContent(t, skill, "references/step-description.md")
	for _, marker := range []string{ref, "get_step_prompts", "Do not", "table names", "global learnings", "durable system-level charter", "actual user messages"} {
		if !strings.Contains(guide, marker) {
			t.Fatalf("step authoring guidance missing %q", marker)
		}
	}
	sequence := materializedFileContent(t, skill, "references/agent.md")
	for _, marker := range []string{"canonical agent step", "description` is the system-level charter", "items[]` entry is a user turn", "predefined_routes"} {
		if !strings.Contains(sequence, marker) {
			t.Fatalf("agent guidance missing unified-agent contract %q", marker)
		}
	}
	legacy := materializedFileContent(t, skill, "references/agent.md")
	for _, marker := range []string{"canonical agent step", "agent", "predefined_routes"} {
		if !strings.Contains(legacy, marker) {
			t.Fatalf("orchestrator migration guidance missing %q", marker)
		}
	}
	for _, stale := range []string{"description is turn 0", "description is the opening user", "no agentic children"} {
		if strings.Contains(strings.ToLower(guide+sequence+legacy), stale) {
			t.Fatalf("builder reference retains stale prompt model %q", stale)
		}
	}
	// This authoring reference must not add both system prompts to the
	// execution agent's capability-selected reference bundle.
	step := MaterializeStepExecutionReferenceSkill(StepExecutionSignals{ToolNames: []string{"execute_shell_command", "query_workflow_db"}})
	if step != nil && strings.Contains(step.Content, ref) {
		t.Fatal("runtime step received the builder's full prompt reference")
	}
}
