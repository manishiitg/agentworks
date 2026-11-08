package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// Extend the existing real Python entrance fixture, which starts the actual
// session/runtime with no model executor. MCP must use that direct path too.
func checkSavedScriptMCPStart(t *testing.T, api *StreamingAPI, binding crewExecutableBinding, docs string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	server := serveExternalMCP(t, api, &UserClaims{UserID: "owner", Username: "owner"})
	cli := dialExternalMCP(t, ctx, server.URL+externalMCPPath)
	initializeExternalMCP(t, ctx, cli)
	call := func(args map[string]any) map[string]any {
		t.Helper()
		args["workflow_id"] = binding.ID
		reply := callRemoteTool(t, ctx, cli, "call_tool", map[string]any{"name": "runs", "arguments": args})
		requireRemoteSuccess(t, reply, "script run")
		var result map[string]any
		if err := json.Unmarshal([]byte(marshalStructured(t, reply)), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	labelOnly := callRemoteTool(t, ctx, cli, "call_tool", map[string]any{"name": "runs", "arguments": map[string]any{"action": "step", "workflow_id": binding.ID, "step_id": "Prepare report"}})
	requireRemoteError(t, labelOnly, "Python labels cannot start independent jobs", "invalid_arguments")
	bad := callRemoteTool(t, ctx, cli, "call_tool", map[string]any{"name": "runs", "arguments": map[string]any{"action": "start", "workflow_id": binding.ID, "inputs": map[string]any{"count": "invalid"}}})
	requireRemoteError(t, bad, "typed input rejected before launch", "invalid_arguments")
	accepted := call(map[string]any{"action": "start", "input_preset": "Saved", "inputs": map[string]any{"count": 8}})
	session, _ := accepted["session_id"].(string)
	if session == "" {
		t.Fatalf("missing normal session receipt: %v", accepted)
	}
	layout, found, err := crewroot.LookupExecutable(docs, binding.Root)
	if err != nil || !found {
		t.Fatal(err)
	}
	for {
		// Poll the same status endpoint as a real caller, and prove completion
		// from the resulting Python artifact and saved run record.
		call(map[string]any{"action": "status", "session_id": session})
		folders, err := os.ReadDir(filepath.Join(layout.Runtime, "runs"))
		if err != nil {
			t.Fatal(err)
		}
		for _, folder := range folders {
			raw, err := os.ReadFile(filepath.Join(layout.Runtime, "runs", folder.Name(), "execution/saved/result.json"))
			if err != nil || !strings.Contains(string(raw), `"count": 8`) {
				continue
			}
			var artifact struct {
				PythonRan bool   `json:"python_ran"`
				Preset    string `json:"preset"`
			}
			if err := json.Unmarshal(raw, &artifact); err != nil || !artifact.PythonRan || artifact.Preset != "Saved" {
				t.Fatalf("MCP preset/typed values did not reach real Python: %s %v", raw, err)
			}
			record := call(map[string]any{"action": "get", "run_folder": folder.Name()})
			metadata, _ := record["metadata"].(map[string]any)
			if metadata["status"] != "completed" {
				continue
			}
			if metadata["python_steps"] == nil {
				t.Fatal("completed MCP run has no recorded Python steps")
			}
			t.Logf("MCP direct script session=%s run=%s real_python=true preset=Saved count=8", session, folder.Name())
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}
