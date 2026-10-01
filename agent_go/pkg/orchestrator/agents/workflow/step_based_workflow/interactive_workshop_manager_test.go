package step_based_workflow

import (
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/guidance"
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

// After the message-sequence migration, the full pattern catalog lives in
// templates/system/message-sequence.md (loaded via read_skill). The
// inline workshop prompt now carries only the reference pointer; the detailed pattern descriptions are
// asserted against the rendered .md content.

func TestInteractiveWorkshopPromptDocumentsMessageSequenceRouteReuse(t *testing.T) {
	prompt := executeInteractiveWorkshopPromptForMode(t, "workshop")

	// The dedicated "Message sequence routes" inline section was folded
	// into the consolidated "Planning steps" section that lists
	// message-sequence as one of several per-step-type deep-dive skills.
	// The agent reaches the full pattern catalog by loading the skill.
	inlineMustContain := []string{
		"references/workflow-chat.md",
	}
	for _, snippet := range inlineMustContain {
		if !strings.Contains(prompt, snippet) {
			t.Errorf("expected workshop prompt (builder) to contain inline snippet %q", snippet)
		}
	}

	operations, err := guidance.RenderReferenceKindForTest("workflow-chat", "workshop")
	if err != nil || !strings.Contains(operations, "references/plan-design.md") || !strings.Contains(operations, "message-sequence") {
		t.Fatal("workflow operations must route to plan design and message sequences")
	}

	// Detailed pattern content lives in the .md doc.
	doc := guidance.RenderSystemDoc("message-sequence")
	docMustContain := []string{
		"Conversational route sub-agents use `message_sequence`, including stateless one-turn work",
		"Normal repeated calls reuse the route conversation",
		"re-entry user message",
		"The sequence's own conversation remains responsible for reasoning, verification",
		"restart only when the prior conversation is stale, wrong, or contaminated",
		"## MESSAGE SEQUENCE ROUTE PATTERNS",
	}
	for _, snippet := range docMustContain {
		if !strings.Contains(doc, snippet) {
			t.Errorf("expected message-sequence.md to contain snippet %q", snippet)
		}
	}
}

func TestOptimizerPromptDocumentsMessageSequenceRoutePatterns(t *testing.T) {
	prompt := executeInteractiveWorkshopPromptForMode(t, "workshop")

	// The pattern catalog now lives entirely in message-sequence.md.
	// The inline workshop prompt only carries the consolidated
	// "Planning steps" section that names message-sequence (and the
	// other per-step-type skills) as deep-dive entry points.
	inlineMustContain := []string{
		"references/workflow-chat.md",
	}
	for _, snippet := range inlineMustContain {
		if !strings.Contains(prompt, snippet) {
			t.Errorf("expected workshop prompt (optimizer) to contain inline snippet %q", snippet)
		}
	}

	doc := guidance.RenderSystemDoc("message-sequence")
	docMustContain := []string{
		"## MESSAGE SEQUENCE ROUTE PATTERNS",
		"Use these patterns when designing or repairing an agent's `predefined_routes`",
		"Use a `message_sequence` route when the parent agent should preserve specialist memory",
		"restart only when the prior conversation is stale, wrong, or contaminated",
	}
	for _, snippet := range docMustContain {
		if !strings.Contains(doc, snippet) {
			t.Errorf("expected message-sequence.md to contain snippet %q", snippet)
		}
	}
}

// TestPulseReviewResultPathAcceptsEveryCanonicalModule reproduces a real
// launch blocker introduced by the stores_health merge: validatePulseReviewIdentity's
// whitelist still listed only the three retired store modules, so every
// current stores_health reviewer failed to persist its result file with
// "module \"stores_health\" is not a valid Pulse review module". The whitelist
// is hand-maintained separately from pulseModuleOrder, so nothing else catches
// a canonical module missing from it.
