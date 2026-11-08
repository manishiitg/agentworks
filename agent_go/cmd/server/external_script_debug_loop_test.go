package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// PLAT-746-468: use the existing actual-runtime fixture to fail real Python,
// diagnose through MCP, edit nested source and rerun recorded values to success.
func checkSavedScriptMCPDebugLoop(t *testing.T, api *StreamingAPI, binding crewExecutableBinding, docs string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	owner := &UserClaims{UserID: "owner", Username: "owner"}
	server := serveExternalMCP(t, api, owner)
	cli := dialExternalMCP(t, ctx, server.URL+externalMCPPath)
	initializeExternalMCP(t, ctx, cli)
	call := func(name string, args map[string]any) map[string]any {
		t.Helper()
		reply := callRemoteTool(t, ctx, cli, "call_tool", map[string]any{"name": name, "arguments": args})
		requireRemoteSuccess(t, reply, name)
		var value map[string]any
		if err := json.Unmarshal([]byte(marshalStructured(t, reply)), &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	write := func(file, source string) {
		t.Helper()
		call("workspace", map[string]any{"action": "automation_write", "crew_id": binding.CrewID, "workspace_path": binding.Root, "file": file, "content": source})
	}
	write("nested/__init__.py", "")
	write("nested/repair.py", "from agentworks import check, step\n@step('Check report')\ndef produce(count):\n    print('x' * (2 * 1024 * 1024))\n    print('full-log-only-marker')\n    check('Report valid', False, 'Intentional repair fixture')\n    raise ValueError('repair-fixture failure')\n")
	write("main.py", "import json, os\nfrom pathlib import Path\nfrom agentworks import inputs\nfrom nested.repair import produce\nresult = produce(inputs['count'])\n(Path(os.environ['STEP_OUTPUT_DIR']) / 'result.json').write_text(json.dumps({'count': result}))\n")
	layout, found, err := crewroot.LookupExecutable(docs, binding.Root)
	if err != nil || !found {
		t.Fatal(err)
	}
	folders := func() map[string]bool {
		t.Helper()
		entries, err := os.ReadDir(filepath.Join(layout.Runtime, "runs"))
		if err != nil {
			t.Fatal(err)
		}
		values := map[string]bool{}
		for _, entry := range entries {
			values[entry.Name()] = true
		}
		return values
	}
	waitRun := func(before map[string]bool, session, status string) string {
		t.Helper()
		for {
			call("runs", map[string]any{"action": "status", "workflow_id": binding.ID, "session_id": session})
			for folder := range folders() {
				if before[folder] {
					continue
				}
				raw, err := os.ReadFile(filepath.Join(layout.Runtime, "runs", folder, "run_metadata.json"))
				if err != nil {
					continue
				}
				var meta struct {
					Status string `json:"status"`
				}
				if json.Unmarshal(raw, &meta) == nil && meta.Status == status {
					return folder
				}
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	before := folders()
	accepted := call("runs", map[string]any{"action": "start", "workflow_id": binding.ID, "input_preset": "Saved", "inputs": map[string]any{"count": 9}})
	failed := waitRun(before, accepted["session_id"].(string), "failed")
	diagnosis := call("runs", map[string]any{"action": "logs", "workflow_id": binding.ID, "run_folder": failed})
	failure, _ := diagnosis["failure"].(map[string]any)
	values, _ := diagnosis["inputs"].(map[string]any)
	steps, _ := failure["steps"].([]any)
	trace, _ := failure["traceback"].(string)
	if values["count"] != float64(9) || diagnosis["input_preset"] != "Saved" || len(steps) == 0 || !strings.Contains(trace, "repair-fixture failure") || !strings.Contains(trace, "repair.py") {
		t.Fatalf("one-call diagnosis missing real failure/typed inputs: %v", diagnosis)
	}
	logs, _ := diagnosis["script_logs"].([]any)
	if len(logs) == 0 {
		t.Fatal("no script log tail")
	}
	log := logs[0].(map[string]any)
	if log["truncated"] != true || len(log["tail"].(string)) > externalScriptLogTailBytes || !strings.Contains(log["tail"].(string), "full-log-only-marker") {
		t.Fatalf("log preview was not bounded with failure tail: %v", log)
	}
	// A token may still read this run's public metadata while its explicit
	// file exclusion hides the program log content and its path in diagnostics.
	guarded := &UserClaims{UserID: "owner", Username: "owner", AccessToken: &accesstokens.Token{ID: "run-log-test", Scopes: []string{"workflows:read", "files:read"}, AllWorkflows: true, FileGuard: &wf.FolderGuard{ReadPaths: []string{"runs/" + failed}, BlockedPaths: []string{log["path"].(string)}}}}
	guardServer := serveExternalMCP(t, api, guarded)
	guardClient := dialExternalMCP(t, ctx, guardServer.URL+externalMCPPath)
	initializeExternalMCP(t, ctx, guardClient)
	guardReply := callRemoteTool(t, ctx, guardClient, "call_tool", map[string]any{"name": "runs", "arguments": map[string]any{"action": "logs", "workflow_id": binding.ID, "run_folder": failed}})
	requireRemoteSuccess(t, guardReply, "bounded run log access")
	if strings.Contains(marshalStructured(t, guardReply), "full-log-only-marker") {
		t.Fatal("excluded log escaped its folder grant")
	}

	write("nested/repair.py", "from agentworks import check, step\n@step('Check report')\ndef produce(count):\n    check('Report valid', True, 'Repair fixture passed')\n    return count\n")
	before = folders()
	rerun := call("runs", map[string]any{"action": "rerun", "workflow_id": binding.ID, "run_folder": failed})
	changes, _ := rerun["source_changes"].([]any)
	changedNested := false
	for _, changed := range changes {
		if changed.(map[string]any)["file"] == "nested/repair.py" {
			changedNested = true
		}
	}
	if rerun["rerun_of"] != failed || !changedNested || rerun["inputs_reused"] != true || rerun["input_preset"] != "Saved" {
		t.Fatalf("rerun lost saved inputs/change summary: %v", rerun)
	}
	passed := waitRun(before, rerun["session_id"].(string), "completed")
	good := call("runs", map[string]any{"action": "logs", "workflow_id": binding.ID, "run_folder": passed})
	newValues, _ := good["inputs"].(map[string]any)
	checks, _ := good["evidence"].([]any)
	if newValues["count"] != float64(9) || good["input_preset"] != "Saved" || len(checks) == 0 || checks[0].(map[string]any)["ok"] != true {
		t.Fatalf("edited real script did not pass with recorded values: %v", good)
	}
	t.Logf("MCP script debug loop failed=%s passed=%s edited=nested/repair.py preset=Saved count=9 real_python=true", failed, passed)
}
