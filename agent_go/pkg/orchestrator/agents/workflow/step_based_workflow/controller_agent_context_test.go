package step_based_workflow

import "testing"

func TestAgentContinuationUsesOnlyNewDelegationInstruction(t *testing.T) {
	opts := agentSequenceCallOptions{
		ReentryMessage:      "durable route description\n\n## Orchestrator Instructions\n\ninspect the latest run",
		ContinuationMessage: "inspect the latest run",
	}

	if got := agentSequenceContinuationMessage(opts); got != "inspect the latest run" {
		t.Fatalf("continuation message = %q; want only the new parent instruction", got)
	}
}

func TestAgentContinuationFallsBackForLegacyCallers(t *testing.T) {
	opts := agentSequenceCallOptions{ReentryMessage: " continue the route "}

	if got := agentSequenceContinuationMessage(opts); got != "continue the route" {
		t.Fatalf("continuation message = %q; want trimmed legacy re-entry message", got)
	}
}
