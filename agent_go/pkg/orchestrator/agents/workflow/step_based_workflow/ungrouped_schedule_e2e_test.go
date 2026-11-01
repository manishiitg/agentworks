package step_based_workflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// PLAT-746-265: an ungrouped saved schedule must cross the registered tool
// and execute real Python, not just resolve inputs in a unit test.
func TestUngroupedScheduleRunsRegisteredTool(t *testing.T) {
	docs, _ := workflowHTTPTestWorkspace(t)
	const workflow = "Workflow/ungrouped-schedule"
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(docs, workflow, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("workflow.json", `{"code_layout_version":1}`)
	write("planning/plan.json", `{"steps":[{"type":"scripted","id":"save-evidence","title":"Save evidence","description":"Save the requested output.","context_output":"result.json"}]}`)
	write("planning/step_config.json", `{"steps":[{"id":"save-evidence","agent_configs":{"lock_code":true,"learnings_access":"none","knowledgebase_access":"none"}}]}`)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	write("code/save-evidence/main.py", `import json, os
from pathlib import Path
assert os.environ['RUN_FOLDER'] == 'iteration-7-sched'
Path(os.environ['STEP_OUTPUT_DIR'], 'result.json').write_text(json.dumps({'ungrouped_schedule': True}))
`)
	session, err := NewWorkshopChatSession(ctx, &WorkshopConfig{Logger: loggerv2.NewNoop(), WorkspacePath: workflow, ScheduleInvocation: &ScheduleInvocation{RunFolder: "iteration-7-sched", RunID: "scheduled-test", ScheduleID: "saved-schedule"}})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	draft := newWorkshopDefinitionDraft()
	RegisterRunFullWorkflowTool(draft, session, loggerv2.NewNoop())
	out, err := draft.tools["run_full_workflow"].Execute(ctx, map[string]interface{}{})
	if err != nil || !strings.Contains(out, "execution_id:") {
		t.Fatalf("ungrouped schedule rejected by tool: %s %v", out, err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		snapshot, found, _ := session.StepRegistry.LatestSnapshotForStep("full-workflow")
		if found && snapshot.Status != WorkshopStepRunning {
			if snapshot.Status != WorkshopStepDone {
				t.Fatalf("ungrouped schedule failed: %+v", snapshot)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ungrouped schedule did not complete")
		}
		time.Sleep(20 * time.Millisecond)
	}
	raw, err := os.ReadFile(filepath.Join(docs, workflow, "runs/iteration-7-sched/execution/save-evidence/result.json"))
	if err != nil || !strings.Contains(string(raw), `"ungrouped_schedule": true`) {
		t.Fatalf("saved schedule output: %s %v", raw, err)
	}

	RegisterWorkshopChatTools(draft, session, loggerv2.NewNoop())
	out, err = draft.tools["execute_step"].Execute(ctx, map[string]interface{}{"step_id": "save-evidence", "fast_path_only": true})
	if err != nil || !strings.Contains(out, "execution_id") {
		t.Fatalf("ungrouped step rejected by tool: %s %v", out, err)
	}
	deadline = time.Now().Add(15 * time.Second)
	for {
		snapshot, found, _ := session.StepRegistry.LatestSnapshotForStep("save-evidence")
		if found && snapshot.Status != WorkshopStepRunning {
			if snapshot.Status != WorkshopStepDone {
				t.Fatalf("ungrouped step failed: %+v", snapshot)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ungrouped step did not complete")
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, invalid := range []string{`{"groups":[{"name":"disabled","enabled":false}]}`, `{"groups":`} {
		write("variables/variables.json", invalid)
		if result, err := draft.tools["run_full_workflow"].Execute(ctx, map[string]interface{}{}); err == nil {
			t.Fatalf("invalid or disabled inputs started work: %s", result)
		}
	}

}
