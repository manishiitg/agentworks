package step_based_workflow

import (
	"context"
	"encoding/json"
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

	if out, err := draft.tools["run_full_workflow"].Execute(ctx, map[string]interface{}{"group_name": "old"}); err == nil || !strings.Contains(err.Error(), "input_preset") {
		t.Fatalf("retired group selector was not explained: %s %v", out, err)
	}
	write("variables/variables.json", `{"variables":[{"name":"target","value":"default"}],"groups":[{"name":"Production","values":{"target":"A"}},{"name":"production","values":{"target":"B"}}]}`)
	group, err := session.controller.resolveWorkshopInputGroup(ctx, "production", nil)
	if err != nil || group != "production" {
		t.Fatalf("exact preset name changed: %s %v", group, err)
	}
	if _, err := session.controller.resolveWorkshopInputGroup(ctx, "PRODUCTION", nil); err == nil {
		t.Fatal("a folder alias was accepted as a saved input name")
	}
	write("variables/variables.json", `{"groups":`)
	if _, err := draft.tools["run_full_workflow"].Execute(ctx, map[string]interface{}{}); err == nil {
		t.Fatal("malformed input definitions launched work")
	}
}

// PLAT-746-265: saved values cannot fan out or create group-named folders.
// Three real registered-tool/Python runs retain independent resolved inputs.
func TestSavedInputsRunOnceWithIndependentSnapshots(t *testing.T) {
	docs, _ := workflowHTTPTestWorkspace(t)
	const workflow = "Workflow/saved-inputs"
	write := func(rel, body string) {
		t.Helper()
		file := filepath.Join(docs, workflow, rel)
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("workflow.json", `{"code_layout_version":1}`)
	write("planning/plan.json", `{"steps":[{"type":"scripted","id":"save","title":"Save inputs","description":"Save the invocation inputs.","context_output":"result.json"}]}`)
	write("planning/step_config.json", `{"steps":[{"id":"save","agent_configs":{"lock_code":true,"learnings_access":"none","knowledgebase_access":"none"}}]}`)
	write("variables/variables.json", `{"variables":[{"name":"TARGET","value":"default"},{"name":"CAMPAIGN_ID","value":""},{"name":"COUNT","type":"integer","required":true,"value":"7"},{"name":"ENABLED","type":"boolean","value":"false"},{"name":"DETAILS","type":"object","value":"{\"tag\":\"ok\"}"}],"groups":[{"name":"India / ecommerce","values":{"TARGET":"saved","CAMPAIGN_ID":"campaign-one"}},{"name":"Another preset","values":{"TARGET":"other"}}]}`)
	write("code/save/main.py", `import json, os
from pathlib import Path
assert os.environ['RUN_FOLDER'].startswith('run-')
assert int(os.environ['VAR_COUNT']) == 7
assert json.loads(os.environ['VAR_ENABLED']) is False
assert json.loads(os.environ['VAR_DETAILS']) == {'tag':'ok'}
Path(os.environ['STEP_OUTPUT_DIR'], 'result.json').write_text(json.dumps({'target':os.environ['VAR_TARGET'], 'campaign_id':os.environ['VAR_CAMPAIGN_ID'], 'run':os.environ['RUN_FOLDER']}))
`)
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	sessions := []*WorkshopChatSession{}
	for _, args := range []map[string]interface{}{
		{},
		{"input_preset": "India / ecommerce"},
		{"input_preset": "India / ecommerce", "inputs": map[string]interface{}{"TARGET": "explicit"}},
	} {
		session, err := NewWorkshopChatSession(ctx, &WorkshopConfig{Logger: loggerv2.NewNoop(), WorkspacePath: workflow})
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		tools := newWorkshopDefinitionDraft()
		RegisterRunFullWorkflowTool(tools, session, loggerv2.NewNoop())
		out, err := tools.tools["run_full_workflow"].Execute(ctx, args)
		if err != nil || !strings.Contains(out, "execution_id:") {
			t.Fatalf("admission: %s %v", out, err)
		}
		sessions = append(sessions, session)
	}
	// Editing saved values after admission cannot change a running invocation.
	write("variables/variables.json", `{"variables":[{"name":"TARGET","value":"changed"},{"name":"CAMPAIGN_ID","value":""}],"groups":[]}`)
	for _, session := range sessions {
		for {
			snapshot, found, _ := session.StepRegistry.LatestSnapshotForStep("full-workflow")
			if found && snapshot.Status != WorkshopStepRunning {
				if snapshot.Status != WorkshopStepDone {
					t.Fatalf("execution: %+v", snapshot)
				}
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("runs did not complete")
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
	folders, err := os.ReadDir(filepath.Join(docs, workflow, "runs"))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, folder := range folders {
		if !folder.IsDir() || !strings.HasPrefix(folder.Name(), "run-") {
			continue
		}
		root := filepath.Join(docs, workflow, "runs", folder.Name())
		raw, err := os.ReadFile(filepath.Join(root, "execution/save/result.json"))
		if err != nil {
			t.Fatal(err)
		}
		var result map[string]string
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		seen[result["target"]] = folder.Name()
		snapshot, err := os.ReadFile(filepath.Join(root, "inputs.json"))
		if err != nil {
			t.Fatal(err)
		}
		var recorded workflowRunInputs
		if err := json.Unmarshal(snapshot, &recorded); err != nil {
			t.Fatal(err)
		}
		if recorded.Values["TARGET"] != result["target"] {
			t.Fatalf("recorded inputs differ from execution: %s %s", snapshot, raw)
		}
		if _, err := os.Stat(filepath.Join(root, "India / ecommerce")); !os.IsNotExist(err) {
			t.Fatal("saved input label became a folder")
		}
		// A new controller simulates loading the same run after a restart.
		restored, err := sessions[0].controller.resolveRunInputs(ctx, folder.Name(), "deleted preset", map[string]string{"TARGET": "wrong"})
		if err != nil || restored["TARGET"] != result["target"] {
			t.Fatalf("run inputs did not survive reload: %v %v", restored, err)
		}
	}
	if len(seen) != 3 || seen["default"] == "" || seen["saved"] == "" || seen["explicit"] == "" {
		t.Fatalf("expected one run per invocation: %v", seen)
	}
	// PLAT-746-265: Run people and functions cannot select an existing run.
	// Builder continuation reads the original snapshot even after the preset is deleted.
	write("variables/variables.json", `{"variables":[{"name":"TARGET","value":"changed"},{"name":"CAMPAIGN_ID","value":""}]}`)
	builder, err := NewWorkshopChatSession(ctx, &WorkshopConfig{Logger: loggerv2.NewNoop(), WorkspacePath: workflow})
	if err != nil {
		t.Fatal(err)
	}
	defer builder.Close()
	draft := newWorkshopDefinitionDraft()
	RegisterWorkshopChatTools(draft, builder, loggerv2.NewNoop())
	step := draft.tools["execute_step"]
	for _, folder := range []string{"../outside", "/absolute", "run-a/child", `run-a\child`, ".hidden", "run-missing"} {
		if out, err := step.Execute(ctx, map[string]interface{}{"step_id": "save", "run_folder": folder}); err == nil {
			t.Fatalf("invalid continuation accepted: %q %s", folder, out)
		}
	}
	builder.SetWorkshopModeOverride("run")
	RegisterWorkshopChatTools(draft, builder, loggerv2.NewNoop())
	step = draft.tools["execute_step"]
	if _, err := step.Execute(ctx, map[string]interface{}{"step_id": "save", "run_folder": seen["saved"]}); err == nil || !strings.Contains(err.Error(), "Builder") {
		t.Fatalf("Run caller continued an existing run: %v", err)
	}
	builder.SetWorkshopModeOverride("workshop")
	RegisterWorkshopChatTools(draft, builder, loggerv2.NewNoop())
	step = draft.tools["execute_step"]
	builder.config.WebhookInvocation = &WebhookInvocation{}
	if _, err := step.Execute(ctx, map[string]interface{}{"step_id": "save", "run_folder": seen["saved"]}); err == nil || !strings.Contains(err.Error(), "Builder") {
		t.Fatalf("function continued a run: %v", err)
	}
	builder.config.WebhookInvocation = nil
	out, err := step.Execute(ctx, map[string]interface{}{"step_id": "save", "run_folder": seen["saved"], "input_preset": "deleted", "fast_path_only": true})
	if err != nil || !strings.Contains(out, "execution_id:") {
		t.Fatalf("Builder continuation rejected: %s %v", out, err)
	}
	for {
		snapshot, found, _ := builder.StepRegistry.LatestSnapshotForStep("save")
		if found && snapshot.Status != WorkshopStepRunning {
			if snapshot.Status != WorkshopStepDone {
				t.Fatalf("continuation: %+v", snapshot)
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("continuation did not complete")
		case <-time.After(20 * time.Millisecond):
		}
	}
	raw, err := os.ReadFile(filepath.Join(docs, workflow, "runs", seen["saved"], "execution/save/result.json"))
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]string
	if err := json.Unmarshal(raw, &result); err != nil || result["target"] != "saved" {
		t.Fatalf("continuation re-resolved deleted preset: %s %v", raw, err)
	}

}
