package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestRestoreLatestBuilderConversationRanksAfterNativeTranscriptCatchUp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	const workspacePath = "Workflow/sales-outreach"
	workingDir := filepath.Join(home, "workspace-docs", "Workflow", "sales-outreach")
	nativeSessionID := "native-most-recent"
	transcriptDir := filepath.Join(home, ".claude", "projects", claudeNativeTranscriptProjectSlug(workingDir))
	if err := os.MkdirAll(transcriptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTranscriptFixture(t, filepath.Join(transcriptDir, nativeSessionID+".jsonl"), []string{
		`{"type":"user","timestamp":"2026-08-30T08:00:00Z","message":{"role":"user","content":"original question"}}`,
		`{"type":"assistant","timestamp":"2026-08-30T08:01:00Z","message":{"role":"assistant","content":[{"type":"text","text":"original answer"}]}}`,
		`{"type":"user","timestamp":"2026-08-30T10:00:00Z","message":{"role":"user","content":"the actual last request"}}`,
		`{"type":"assistant","timestamp":"2026-08-30T10:01:00Z","message":{"role":"assistant","content":[{"type":"text","text":"the actual last answer"}]}}`,
	})

	workspace := &mockWorkspaceAPI{files: map[string]string{}}
	workspaceServer := httptest.NewServer(workspace)
	defer workspaceServer.Close()
	t.Setenv("WORKSPACE_API_URL", workspaceServer.URL)

	writeConversation := func(sessionID, updatedAt string, history []map[string]interface{}, runtime map[string]interface{}) {
		t.Helper()
		record := map[string]interface{}{
			"session_id":           sessionID,
			"phase_id":             "workflow-builder",
			"updated_at":           updatedAt,
			"conversation_history": history,
		}
		if runtime != nil {
			record["runtime"] = runtime
		}
		content, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		path := workspacePath + "/builder/conversation/2026-08-30/session-" + sessionID + "-conversation.json"
		workspace.files[path] = string(content)
	}

	writeConversation("saved-older-session", "2026-08-30T08:01:00Z", []map[string]interface{}{
		{"Role": "human", "Parts": []map[string]string{{"Text": "original question"}}},
		{"Role": "ai", "Parts": []map[string]string{{"Text": "original answer"}}},
	}, map[string]interface{}{
		"provider": "claude-code", "agent_session_handle": map[string]interface{}{
			"provider": map[string]interface{}{"provider": "claude-code", "native_session_id": nativeSessionID, "working_dir": workingDir},
		},
	})
	writeConversation("newer-saved-session", "2026-08-30T09:00:00Z", []map[string]interface{}{
		{"Role": "human", "Parts": []map[string]string{{"Text": "a different older chat"}}},
		{"Role": "ai", "Parts": []map[string]string{{"Text": "a different older answer"}}},
	}, nil)

	// A bot lookup (run first: a refresh saves its catch-up, which changes the snapshot) refreshes only the most recently updated few (a busy workflow has dozens, and
	// refreshing them all took 70 s): with a limit of 1 the older snapshot is not refreshed, so the
	// most recently saved conversation wins. With room for both it is the caught-up one, as above.
	limited, err := (&StreamingAPI{}).restoreLatestBuilderConversationLimited(context.Background(), "sales-outreach", workspacePath, 1)
	if err != nil || limited == nil || limited.SessionID != "newer-saved-session" {
		t.Fatalf("limited restore = %+v, %v; want the most recently saved conversation", limited, err)
	}
	response, err := (&StreamingAPI{}).restoreLatestBuilderConversation(context.Background(), "sales-outreach", workspacePath)
	if err != nil {
		t.Fatalf("restore latest builder conversation: %v", err)
	}
	if response == nil {
		t.Fatal("expected a restored builder conversation")
	}
	if response.SessionID != "saved-older-session" {
		t.Fatalf("session = %q, want the native-caught-up conversation", response.SessionID)
	}
	if response.UpdatedAt != "2026-08-30T10:01:00Z" {
		t.Fatalf("updated_at = %q, want native transcript recency", response.UpdatedAt)
	}

	both, err := (&StreamingAPI{}).restoreLatestBuilderConversationLimited(context.Background(), "sales-outreach", workspacePath, 2)
	if err != nil || both == nil || both.SessionID != "saved-older-session" {
		t.Fatalf("restore with room for both = %+v, %v; want the caught-up conversation", both, err)
	}
}
