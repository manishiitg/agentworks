package step_based_workflow

import (
	"strings"
	"testing"
)

func executeInteractiveWorkshopPromptForMode(t *testing.T, mode string) string {
	t.Helper()
	name := "interactiveWorkshopSystem"
	if mode != "workshop" {
		name = "interactiveRunSystem"
	}
	prompt, err := ExecuteTemplate(name, map[string]string{
		"AbsDocsRoot":                       "/app/workspace-docs",
		"AbsWorkspacePath":                  "/app/workspace-docs/Workflow/example",
		"AvailableGroups":                   "group-1",
		"BrowserPrompt":                     "",
		"Focus":                             "",
		"GroupName":                         "",
		"Instruction":                       "",
		"IsCodeExecutionMode":               "false",
		"MainPyAuthoringRules":              "",
		"Mode":                              "",
		"PlanJSON":                          "{}",
		"ProgressSummary":                   "",
		"RunFolder":                         "",
		"SecretPrompt":                      "",
		"SkillPrompt":                       "",
		"SpecialWorkspaceToolsInstructions": "",
		"StepConfigSummary":                 "",
		"StepID":                            "",
		"StepSummary":                       "",
		"StepsToReview":                     "",
		"TargetRunFolder":                   "",
		"UseKnowledgebase":                  "false",
		"UseProjectedReferenceSkills":       "true",
		"UserRequest":                       "",
		"WorkflowObjective":                 "Build a reliable workflow.",
		"WorkflowSuccessCriteria":           "It runs end to end.",
		"WorkshopMode":                      mode,
		"WorkspacePath":                     "Workflow/example",
	})
	if err != nil {
		t.Fatalf("ExecuteTemplate returned error: %v", err)
	}
	return prompt
}

func TestInteractiveWorkshopPromptDoesNotBanAuthorizedSourceReview(t *testing.T) {
	prompt := executeInteractiveWorkshopPromptForMode(t, "workshop")

	for _, forbidden := range []string{
		"Never read application source code",
		"Do NOT search or read *.go, *.ts, or *.json files outside the workspace",
	} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("workshop prompt still contains blanket source-review ban %q", forbidden)
		}
	}
}

// After the agent migration, the full pattern catalog lives in
// templates/system/agent.md (loaded via read_skill). The
// inline workshop prompt now carries only the reference pointer; the detailed pattern descriptions are
// asserted against the rendered .md content.

// TestPulseReviewResultPathAcceptsEveryCanonicalModule reproduces a real
// launch blocker introduced by the stores_health merge: validatePulseReviewIdentity's
// whitelist still listed only the three retired store modules, so every
// current stores_health reviewer failed to persist its result file with
// "module \"stores_health\" is not a valid Pulse review module". The whitelist
// is hand-maintained separately from pulseModuleOrder, so nothing else catches
// a canonical module missing from it.
