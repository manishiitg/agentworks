package step_based_workflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// PLAT-556 decision 1: naming a guide in Inputs/Guides delivers it. Pins the
// security rule (a named path becomes readable even with learnings and KB
// access off, never writable) and what the step's system prompt carries.
func TestDescriptionNamedGuidesAreReadableAndAttached(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	const workflow = "Workflow/upwork"
	write := func(rel, content string) {
		path := filepath.Join(docs, workflow, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("learnings/_global/references/job-selection.md", "Skip jobs under $500.\n")
	write("knowledgebase/notes/positioning.md", strings.Repeat("x", 5000))
	write("learnings/_global/SKILL.md", "not named, never attached")

	previous := BrainNoteReader
	BrainNoteReader = func(_ context.Context, _, note string) (string, error) {
		return "", fmt.Errorf("Brain access is off for this project")
	}
	t.Cleanup(func() { BrainNoteReader = previous })

	description := "## Goal\nPick one job. Mentions learnings/_global/SKILL.md outside the sections.\n\n" +
		"## Inputs\n- `Workflow/upwork/knowledgebase/notes/positioning.md`\n- knowledgebase/notes/missing.md\n\n" +
		"## Rules\n- Never bid twice.\n\n" +
		"## Guides\n- learnings/_global/references/job-selection.md\n- brain:people/manish\n"

	base, err := orchestrator.NewBaseOrchestrator(loggerv2.NewNoop(), nil, orchestrator.OrchestratorTypeWorkflow, "", 0, "", nil, nil, false, &orchestrator.LLMConfig{}, 1, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	base.SetWorkspacePath(workflow)
	hcpo := &StepBasedWorkflowOrchestrator{BaseOrchestrator: base, selectedRunFolder: "iteration-0/default"}

	// Folder Guard: learnings and KB access are off, naming still grants read.
	cfg := &AgentConfigs{LearningsAccess: LearningsAccessNone, KnowledgebaseAccess: KBAccessNone}
	readPaths, writePaths := hcpo.setupMessageSequenceFolderGuard("step-1", "pick", cfg, MessageSequenceWriteAccess{})
	readPaths = appendDescriptionReferenceReadPaths(readPaths, workflow, description)
	for _, want := range []string{workflow + "/learnings/_global/references/job-selection.md", workflow + "/knowledgebase/notes/positioning.md"} {
		if !slices.Contains(readPaths, want) {
			t.Fatalf("named path %s not readable: %v", want, readPaths)
		}
		if slices.Contains(writePaths, want) {
			t.Fatalf("named path %s became writable", want)
		}
	}
	for _, path := range readPaths {
		if strings.HasSuffix(path, "SKILL.md") || strings.HasSuffix(path, "missing.md") {
			t.Fatalf("unnamed or missing path granted: %s", path)
		}
	}

	// System prompt: small guide attached, large one listed, missing and
	// unreadable Brain note reported, in the order named.
	prompt := (&WorkflowExecutionOnlyAgent{}).executionOnlySystemPromptProcessor(map[string]string{
		"StepTitle":        "Pick",
		"StepDescription":  description,
		"ReferencedGuides": hcpo.referencedGuidesForStep(context.Background(), "pick", description),
	})
	section := prompt[strings.Index(prompt, "## Referenced guides"):]
	for _, want := range []string{
		"`knowledgebase/notes/positioning.md` (5000 characters): too large to attach; read it: " + filepath.Join(docs, workflow, "knowledgebase/notes/positioning.md"),
		"`knowledgebase/notes/missing.md`: does not exist in this workflow.",
		"`brain:people/manish.md`: this project cannot read this Brain note (Brain access is off for this project)",
		"### learnings/_global/references/job-selection.md\n\nSkip jobs under $500.",
	} {
		if !strings.Contains(section, want) {
			t.Fatalf("referenced guides missing %q:\n%s", want, section)
		}
	}
	if strings.Contains(section, "not named, never attached") {
		t.Fatalf("a path outside Inputs/Guides was attached:\n%s", section)
	}

	// The plan edit names the missing path; a description without the
	// sections adds nothing.
	if notice := descriptionReferencesEditNotice(workflow, description); !strings.Contains(notice, "knowledgebase/notes/missing.md") || strings.Contains(notice, "job-selection") {
		t.Fatalf("edit notice = %q", notice)
	}
	if got := hcpo.referencedGuidesForStep(context.Background(), "pick", "Pick one job using learnings/_global/references/job-selection.md."); got != "" {
		t.Fatalf("description without Inputs/Guides changed the prompt: %q", got)
	}
}
