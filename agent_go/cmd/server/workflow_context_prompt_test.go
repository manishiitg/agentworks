package server

import (
	"strings"
	"testing"
)

func TestWorkflowContextPromptUsesCompactReadOnlyReferences(t *testing.T) {
	prompt := buildWorkflowContextPrompt([]string{
		"Workflow/HDFC-Personal-Accounts",
		"Workflow/ICICI-BANK-PARSING-v2/",
		"Chats/Work/projects/company-ca-a1b2c3d4",
	}, "http://workspace.invalid")

	for _, want := range []string{
		"## Workflow Context",
		"work-workflow-files",
		"runtime's effective access grants",
		"(workflow) `Workflow/HDFC-Personal-Accounts/`",
		"(workflow) `Workflow/ICICI-BANK-PARSING-v2/`",
		"(Crew) `Chats/Work/projects/company-ca-a1b2c3d4/`",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("compact workflow context missing %q:\n%s", want, prompt)
		}
	}
	if len(prompt) > 900 {
		t.Fatalf("workflow references expanded to %d bytes; want a compact path-based prompt", len(prompt))
	}
	for _, stale := range []string{"any owner", "builder/conversation/", "call_function", "planning/plan.json"} {
		if strings.Contains(prompt, stale) {
			t.Fatalf("dynamic references duplicate skill policy %q", stale)
		}
	}
}

func TestWorkflowContextPromptUsesTheAttachedProductSkillName(t *testing.T) {
	for _, skillName := range []string{"code-workflow-files", "work-workflow-files", "relays-workflow-files"} {
		prompt := buildWorkflowContextPromptWithLabels([]string{"Workflow/test"}, nil, skillName)
		if !strings.Contains(prompt, "`"+skillName+"`") {
			t.Fatalf("prompt points to a different product's skill: %s", prompt)
		}
	}
}
