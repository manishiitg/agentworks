package server

import "testing"

// TestBackgroundAgentSetMetadataMergesRatherThanReplaces pins the PLAT-084
// follow-up fix: OnExecutionComplete calls SetMetadata with only its own
// completion-time keys (iteration, group_name, ...), which never include
// registration-time fields like execution_type or suppress_auto_notification.
// A replace-all implementation silently wiped those the moment an execution
// completed — for execution_type specifically, that would have undermined
// scheduledWorkflowExecutionProducedEvidence's Metadata-based fallback for any
// caller relying on it instead of (or in addition to) Kind.
func TestBackgroundAgentSetMetadataMergesRatherThanReplaces(t *testing.T) {
	agent := &BackgroundAgent{
		Metadata: map[string]string{
			"execution_type":             "workflow-step",
			"workflow_path":              "Workflow/latency-report",
			"suppress_auto_notification": "true",
		},
	}

	agent.SetMetadata(map[string]string{
		"iteration":  "3",
		"group_name": "dev",
	})

	got := agent.GetSnapshot().Metadata
	if got["execution_type"] != "workflow-step" {
		t.Fatalf("execution_type was wiped by a completion-time SetMetadata call: %+v", got)
	}
	if got["workflow_path"] != "Workflow/latency-report" {
		t.Fatalf("workflow_path was wiped by a completion-time SetMetadata call: %+v", got)
	}
	if got["suppress_auto_notification"] != "true" {
		t.Fatalf("suppress_auto_notification was wiped by a completion-time SetMetadata call: %+v", got)
	}
	if got["iteration"] != "3" || got["group_name"] != "dev" {
		t.Fatalf("new completion-time keys were not applied: %+v", got)
	}
}

// TestBackgroundAgentSetMetadataOverwritesExplicitKeys guards the other
// direction: a caller that explicitly passes a key already present must
// still win for that key — this is a merge, not a "never overwrite" union.
func TestBackgroundAgentSetMetadataOverwritesExplicitKeys(t *testing.T) {
	agent := &BackgroundAgent{
		Metadata: map[string]string{"workshop_mode": "run"},
	}
	agent.SetMetadata(map[string]string{"workshop_mode": "review"})

	if got := agent.GetSnapshot().Metadata["workshop_mode"]; got != "review" {
		t.Fatalf("workshop_mode = %q, want the explicitly-passed value \"review\"", got)
	}
}

// TestBackgroundAgentSetMetadataHandlesNilInitialMetadata guards the case
// where an agent was registered without any metadata at all.
func TestBackgroundAgentSetMetadataHandlesNilInitialMetadata(t *testing.T) {
	agent := &BackgroundAgent{}
	agent.SetMetadata(map[string]string{"iteration": "1"})

	if got := agent.GetSnapshot().Metadata["iteration"]; got != "1" {
		t.Fatalf("iteration = %q, want \"1\"", got)
	}
}
