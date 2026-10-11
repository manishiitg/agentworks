package step_based_workflow

import (
	"strings"
	"testing"
)

func TestMaintenanceSpecialistSystemPromptsAreReadOnly(t *testing.T) {
	kbReorganize := renderKBReorganizeSystemPrompt(map[string]string{
		"NotesFolderPath": "/workspace/knowledgebase/notes",
		"NotesIndexPath":  "/workspace/knowledgebase/notes/_index.json",
	})
	kbConsolidate := renderKBConsolidateSystemPrompt(map[string]string{
		"NotesFolderPath": "/workspace/knowledgebase/notes",
		"NotesIndexPath":  "/workspace/knowledgebase/notes/_index.json",
	})

	cases := map[string]string{
		"kb_reorganize":  kbReorganize,
		"kb_consolidate": kbConsolidate,
	}
	for name, prompt := range cases {
		for _, want := range []string{"READ-ONLY REVIEW OVERRIDE", "Pulse Fixer", "Do not"} {
			if !strings.Contains(prompt, want) {
				t.Fatalf("%s reviewer prompt missing %q:\n%s", name, want, prompt)
			}
		}
	}
}

func TestReviewPlanPromptPrefersCoherentAgenticSteps(t *testing.T) {
	prompt, err := ExecuteTemplate("reviewPlanAgentSystem", map[string]string{
		"AbsWorkspacePath":        "/app/workspace-docs/Workflow/example",
		"PlanJSON":                `SHOULD_NOT_BE_INJECTED`,
		"StepConfigSummary":       "SHOULD_NOT_BE_SUMMARIZED_BY_GO",
		"TargetRunFolder":         "",
		"WorkflowObjective":       "Complete the workflow outcome.",
		"WorkflowSelectedSkills":  "",
		"WorkflowSuccessCriteria": "The outcome is verified.",
		"WorkspacePath":           "Workflow/example",
		"Focus":                   "",
		// The real caller, WorkflowPlanReviewAgent.Execute, always sets this
		// before rendering the same template; the test's hand-built map had
		// drifted out of sync with it.
		"DBGuidance": BuildManagedWorkflowDBGuidance(DBAccessRead),
	})
	if err != nil {
		t.Fatalf("render review plan prompt: %v", err)
	}
	for _, forbidden := range []string{"SHOULD_NOT_BE_INJECTED", "SHOULD_NOT_BE_SUMMARIZED_BY_GO", "## CURRENT PLAN", "## STEP CONFIG SUMMARY"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("review plan prompt still injects controller-generated plan context %q", forbidden)
		}
	}

	for _, want := range []string{
		"Read the workflow's source artifacts yourself",
		"targeted `jq` queries",
		"do not dump the full plan",
		"workflow.json",
		"planning/step_config.json",
		"one large `agent` per coherent shared-context span",
		"fewest durable steps",
		"substantial end-to-end outcome",
		"agent",
		"Give the first work item the whole outcome",
		"tiny sequence item per routine action",
		"Separate deterministic acquisition from agentic processing",
		"scripted regular fetcher steps",
		"one-scripted-step-per-endpoint fragmentation",
		"run-specific proof/provenance",
		"evidence-based double-check and repair turns",
		"Multiple large sequences are correct when their contexts should not be shared",
		"builder should decide this intelligently from workflow semantics",
		"10+ representative-run threshold applies only",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("review plan prompt missing coherent-agentic-step guidance %q", want)
		}
	}
}
