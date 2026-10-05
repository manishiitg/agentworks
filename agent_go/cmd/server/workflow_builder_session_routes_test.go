package server

import (
	"strings"
	"testing"
	"time"
)

func TestSummarizeStepExecutionConversationExtractsLatestImageGenError(t *testing.T) {
	content := `{
		"conversation_history": [],
		"tool_calls": [
			{
				"tool_name": "execute_shell_command",
				"step_id": "step-generate-illustrations",
				"timestamp": "2026-04-30T16:26:32.73268+05:30",
				"completed_at": "2026-04-30T16:26:32.797426+05:30",
				"result": "{\"stdout\":\"ERROR: Custom tool execution failed: output_path must stay inside the current session's writable folder, but no active session/workflow write guard was found\",\"stderr\":\"\",\"exit_code\":0,\"execution_time_ms\":58}"
			}
		]
	}`

	summary := summarizeStepExecutionConversation(
		"Workflow/instagram/runs/iteration-0/test-run/logs/step-generate-illustrations/execution/execution-attempt-1-iteration-0-conversation.json",
		content,
	)
	if summary == nil {
		t.Fatal("expected step summary")
	}
	if summary.StepID != "step-generate-illustrations" {
		t.Fatalf("unexpected step id: %q", summary.StepID)
	}
	if summary.Status != "error" {
		t.Fatalf("unexpected status: %q", summary.Status)
	}
	if !strings.Contains(summary.Message, "image_gen") && !strings.Contains(summary.Message, "output_path must stay inside") {
		t.Fatalf("summary did not include image_gen guard error: %q", summary.Message)
	}
	if summary.UpdatedAt.IsZero() {
		t.Fatal("expected timestamp from tool call")
	}
}

func TestWorkflowBuilderConversationLogPathUsesDateFolder(t *testing.T) {
	got := workflowBuilderConversationLogPath("Workflow/instagram", "abc-123", time.Date(2026, 4, 30, 10, 11, 12, 0, time.UTC))
	want := "Workflow/instagram/builder/conversation/2026-04-30/session-abc-123-conversation.json"
	if got != want {
		t.Fatalf("unexpected path:\n got: %s\nwant: %s", got, want)
	}
}

func TestWorkflowBuilderOwnedConversationLogPathUsesPrivateUserFolder(t *testing.T) {
	got := workflowBuilderOwnedConversationLogPath("Workflow/instagram", "user-123", "abc-123", time.Date(2026, 4, 30, 10, 11, 12, 0, time.UTC))
	want := "Workflow/instagram/builder/conversation/users/user-123/2026-04-30/session-abc-123-conversation.json"
	if got != want {
		t.Fatalf("unexpected owned path:\n got: %s\nwant: %s", got, want)
	}
}

func TestEffectiveBuilderConversationOwnerNeverDowngradesExistingOwner(t *testing.T) {
	if got := effectiveBuilderConversationOwner("default", "real-user"); got != "real-user" {
		t.Fatalf("background owner downgrade: got %q", got)
	}
	if got := effectiveBuilderConversationOwner("request-user", "old-user"); got != "request-user" {
		t.Fatalf("request owner should win: got %q", got)
	}
}

func TestIsWorkflowBuilderConversationLogPathAcceptsOwnedAndLegacyLayouts(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"Workflow/instagram/builder/conversation/2026-04-30/session-abc-conversation.json", true},
		{"Workflow/instagram/builder/conversation/users/user-123/2026-04-30/session-abc-conversation.json", true},
		{"Workflow/instagram/builder/conversation/system/2026-04-30/session-abc-conversation.json", true},
		{"Workflow/instagram/builder/conversation/users/user-123/session-abc-conversation.json", false},
		{"Workflow/instagram/builder/session-abc-conversation.json", false},
		{"Workflow/instagram/conversation/2026-04-30/session-abc-conversation.json", false},
		{"Workflow/instagram/builder/conversation/session-abc-conversation.json", false},
		{"Workflow/instagram/builder/conversation/2026-04-30/note-conversation.json", false},
		{"Workflow/instagram/runs/iteration-0/logs/step/execution/session-abc-conversation.json", false},
	}

	for _, tt := range cases {
		if got := isWorkflowBuilderConversationLogPath("Workflow/instagram", tt.path); got != tt.want {
			t.Fatalf("isWorkflowBuilderConversationLogPath(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}
