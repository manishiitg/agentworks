package step_based_workflow

import (
	"strings"
	"testing"
)

func TestWorkflowArtifactPurityRejectsSharedPlatformMechanics(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{"bridge env", `POST to $MCP_CUSTOM and authenticate with $MCP_AUTH`, "MCP bridge environment variables"},
		{"folder guard", `If Folder Guard denies the write, retry from the execution directory.`, "Folder Guard implementation"},
		{"managed db tool", `Call mutate_workflow_db with action=execute.`, "managed workflow database tool"},
		{"tool discovery", `Call get_api_spec before invoking the API bridge.`, "tool-discovery workaround"},
		{"session plumbing", `Inspect the Codex tmux session before continuing.`, "coding-agent session plumbing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateWorkflowArtifactInstruction("step.description", tt.text)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want ownership violation %q", err, tt.want)
			}
		})
	}
}

func TestWorkflowArtifactPurityAllowsDomainSpecificImplementation(t *testing.T) {
	allowed := []string{
		"Fetch CloudWatch log events with the AWS API, normalize latency rows, and upsert them transactionally.",
		"Use curl to call the target service's documented health endpoint and retain the response timestamp.",
		"Query the workflow database for unprocessed candidates, score each one, persist decisions, and verify the count.",
		"Use the browser to collect authenticated profile metrics and fail closed when the page is incomplete.",
	}
	for _, text := range allowed {
		if err := validateWorkflowArtifactInstruction("step.description", text); err != nil {
			t.Fatalf("unexpected rejection for %q: %v", text, err)
		}
	}
}

func TestWorkflowArtifactPurityChecksOnlyChangedMutationFields(t *testing.T) {
	if err := validateWorkflowArtifactMutationArgs(map[string]interface{}{
		"existing_step_id": "legacy-step",
		"title":            "Safe title-only edit",
	}); err != nil {
		t.Fatalf("unrelated mutation rejected: %v", err)
	}

	err := validateWorkflowArtifactMutationArgs(map[string]interface{}{
		"existing_step_id": "legacy-step",
		"items": []interface{}{
			map[string]interface{}{
				"id":      "work",
				"type":    "user_message",
				"message": "Use the api-bridge endpoint directly.",
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "step.items[0].message") {
		t.Fatalf("nested message error = %v", err)
	}
}
