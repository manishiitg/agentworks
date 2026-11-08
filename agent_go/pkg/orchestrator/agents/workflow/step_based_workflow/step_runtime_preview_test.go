package step_based_workflow

import (
	"strings"
	"testing"
)

func TestStepPromptsRespectExplicitPermissionsAndScriptedDBHelpers(t *testing.T) {
	renderer := &WorkflowExecutionOnlyAgent{runtimeSupplements: []string{"Standing runtime scope for this step."}}
	for _, access := range []string{DBAccessNone, DBAccessRead, DBAccessReadWrite} {
		vars := map[string]string{"DBAccess": access, "KbAccess": KBAccessNone, "IsScriptedMode": "true", "DBDirectAccess": "true"}
		system := renderer.executionOnlySystemPromptProcessor(vars)
		// An ordinary step is not a decision handler: the saved-decision lifecycle and Builder/optimizer
		// process text belong to the agents that hold those tools, not to every step's system prompt.
		for _, leak := range []string{"mark_human_input_consumed", "outcome_evidence", "optimizer", "Unmigrated", "Autopilot"} {
			if strings.Contains(system, leak) {
				t.Fatalf("ordinary step prompt carries platform process text %q", leak)
			}
		}
		if strings.Count(system, "Standing runtime scope for this step.") != 1 || strings.Count(renderer.executionOnlySystemPromptProcessor(vars), "Standing runtime scope for this step.") != 1 {
			t.Fatal("runtime supplements disappeared or grew across prompt renders")
		}
		user := renderer.executionOnlyUserMessageProcessor(vars)
		if !strings.Contains(system, "never try a forbidden action to test it") || strings.Contains(system, `Never conclude "read-only" or "not allowed" from the instructions`) {
			t.Fatalf("access %s conflicts with explicit restrictions", access)
		}
		if strings.Contains(system+user, "persist through the absolute `$DB_PATH`") || strings.Contains(user, "persist scripted results through `$DB_PATH`") || strings.Contains(user, "6. Create the output file.") {
			t.Fatalf("access %s retained conflicting persistence/output instructions", access)
		}
		if access == DBAccessReadWrite && !strings.Contains(system, "Use `agentworks_db` in saved scripts") {
			t.Fatal("scripted persistence must consistently use the helper")
		}
		if access == DBAccessNone && strings.Contains(user, "persist results with `mutate_workflow_db`") {
			t.Fatal("no-DB step told to mutate database")
		}
	}
}
