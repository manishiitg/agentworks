package step_based_workflow

import (
	"strings"
	"testing"
)

// PLAT-062: contradictory user instructions previously sent script authors to
// a learnings path the folder guard would deny. Keep one runtime write target
// for both canonical code and legacy run code; user turns point to that target.
func TestScriptedInstructionsAgreeOnTheWriteTarget(t *testing.T) {
	renderer := &WorkflowExecutionOnlyAgent{}
	for _, workingDir := range []string{"/ws/runs/iteration-0/default/execution/task/code", "/ws/Workflow/example/code/task"} {
		vars := map[string]string{"StepDescription": "Write the report.", "IsScriptedMode": "true", "IsCodeExecutionMode": "true", "ScriptedWorkingDir": workingDir, "StepExecutionPath": "/ws/runs/iteration-0/default/execution/task", "DBAccess": DBAccessNone, "KbAccess": KBAccessNone}
		if strings.Contains(workingDir, "/Workflow/") {
			vars["DirectCodeSource"] = "true"
		}
		system := renderer.executionOnlySystemPromptProcessor(vars)
		user := renderer.executionOnlyUserMessageProcessor(vars)
		if !strings.Contains(system, workingDir+"/main.py") {
			t.Fatalf("runtime did not supply the actual script path: %s", system)
		}
		if !strings.Contains(user, "exact main.py path supplied in Code Execution Mode") || strings.Contains(user, "learnings/") {
			t.Fatalf("user turn supplied a competing write target: %s", user)
		}
	}
}
