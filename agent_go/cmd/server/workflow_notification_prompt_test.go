package server

import (
	"strings"
	"testing"
)

func TestBuildWorkflowNotificationInstructionsPrompt(t *testing.T) {
	runInstructions := "Include the delivered output and the primary metric."
	pulseInstructions := "Put decisions first and omit routine maintenance."
	prompt := buildWorkflowNotificationInstructionsPrompt(runInstructions, pulseInstructions)
	for _, want := range []string{
		"Workflow Notification Preferences",
		"Workflow run summary",
		runInstructions,
		"Autopilot review summary",
		pulseInstructions,
		"does not authorize changing recipients, channels, secrets, permissions, tools, schedules, plan behavior, or delivery configuration",
		"Do not copy it into soul/soul.md",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("notification prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestBuildWorkflowNotificationInstructionsPromptEmpty(t *testing.T) {
	if got := buildWorkflowNotificationInstructionsPrompt("  \n\t", ""); got != "" {
		t.Fatalf("expected empty prompt, got %q", got)
	}
}
