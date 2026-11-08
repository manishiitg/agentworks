package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// PLAT-746-468: scripts must be discoverable and inspectable over the real MCP
// transport without executing authored Python or bypassing connection grants.
func TestExternalScriptMCPReadsKeepSourceAndRunBoundaries(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	f, _ := newExternalCrewCommandFixture(t)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("LOCAL_MODE", "true")
	t.Setenv("NATIVE_WORKSPACE", "true")
	t.Setenv("AGENT_PRODUCTS", "work,agentworks")
	f.api.projectActions = NewProjectActionDispatcher(f.api, f.api.agentProfiles)
	owner := crewWriter("owner")
	owner.AccessToken.AllWorkflows = true
	owner.AccessToken.Scopes = append(owner.AccessToken.Scopes, "workflows:read", "files:read")
	srv := serveExternalMCP(t, f.api, owner)
	cli := dialExternalMCP(t, t.Context(), srv.URL+externalMCPPath)
	initializeExternalMCP(t, t.Context(), cli)
	call := func(name string, args map[string]any) map[string]any {
		t.Helper()
		reply := callRemoteTool(t, t.Context(), cli, "call_tool", map[string]any{"name": name, "arguments": args})
		requireRemoteSuccess(t, reply, name)
		var result map[string]any
		if err := json.Unmarshal([]byte(marshalStructured(t, reply)), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	call("workspace", map[string]any{"action": "automation_create", "crew_id": "team", "kind": "script", "name": "Weekly report"})
	bindings, err := readCrewExecutables()
	if err != nil || len(bindings) != 1 {
		t.Fatalf("saved binding: %v %v", bindings, err)
	}
	var script crewExecutableBinding
	for _, binding := range bindings {
		script = binding
	}
	definition := map[string]any{"secrets": []string{"EXAMPLE_SERVICE_TOKEN"}, "id": script.ID, "name": "Weekly report", "inputs": []any{map[string]any{"name": "limit", "value": "3", "type": "integer"}}, "input_presets": []any{map[string]any{"name": "weekly", "values": map[string]string{"limit": "7"}}}}
	raw, _ := json.Marshal(definition)
	call("workspace", map[string]any{"action": "automation_write", "crew_id": "team", "workspace_path": script.Root, "file": "script.json", "content": string(raw)})
	call("workspace", map[string]any{"action": "automation_write", "crew_id": "team", "workspace_path": script.Root, "file": "main.py", "content": "from agentworks import step\nraise RuntimeError('source must never execute')\n@step('Prepare report')\ndef prepare():\n    return 3\nprepare()\n"})
	assertDefinition := func(row map[string]any) {
		t.Helper()
		inputs, _ := row["inputs"].([]any)
		presets, _ := row["input_presets"].([]any)
		names, _ := row["secrets"].([]any)
		if len(names) != 1 || names[0] != "EXAMPLE_SERVICE_TOKEN" {
			t.Fatalf("required secret names missing: %v", row)
		}
		if row["kind"] != "script" || len(inputs) != 1 || inputs[0].(map[string]any)["type"] != "integer" || len(presets) != 1 || presets[0].(map[string]any)["name"] != "weekly" {
			t.Fatalf("missing typed script discovery: %v", row)
		}
	}
	for _, compact := range []bool{true, false} {
		listed := call("workflow", map[string]any{"action": "list", "compact": compact})
		rows, _ := listed["workflows"].([]any)
		if len(rows) != 1 {
			t.Fatalf("script not listed: %v", listed)
		}
		assertDefinition(rows[0].(map[string]any))
	}
	assertDefinition(call("workflow", map[string]any{"action": "get", "workflow_id": script.ID}))
	graph := call("workflow", map[string]any{"action": "plan", "workflow_id": script.ID, "view": "outline"})
	graphJSON, _ := json.Marshal(graph)
	if !strings.Contains(string(graphJSON), "Prepare report") {
		t.Fatalf("saved AST labels missing: %s", graphJSON)
	}
	// Source stays with Builder even when a token can read workflow files.
	readOnly := crewReader("owner")
	readOnly.AccessToken.AllWorkflows = true
	readOnly.AccessToken.Scopes = append(readOnly.AccessToken.Scopes, "workflows:read", "files:read")
	body, _ := json.Marshal(map[string]any{"name": "workflow", "arguments": map[string]any{"action": "plan", "workflow_id": script.ID}})
	w := httptest.NewRecorder()
	f.api.dispatchExternalCall(w, adminRequest(http.MethodPost, "/api/external/call", string(body), readOnly, nil), false)
	externalTestBody(t, w, http.StatusForbidden)

	writeRunFile := func(relative, content string) {
		t.Helper()
		physical, _, err := crewroot.Resolve(f.docs, script.Root+"/"+relative)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(physical), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(physical, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	const folder = "run-20261108-120000"
	writeRunFile("runs/"+folder+"/run_metadata.json", `{"execution_id":"exec-one","status":"completed","python_steps":{"step-one":{"id":"step-one","name":"Prepare report","status":"completed","source_call":"main.py:4:0"}}}`)
	writeRunFile("runs/"+folder+"/helpers/evidence/0001.json", `{"id":"check-one","kind":"check","at":"2026-11-08T12:00:00Z","name":"Totals agree","ok":true,"detail":"Checked three rows"}`)
	writeRunFile("runs/"+folder+"/helpers/requests/private.json", `{"note":"helper-request-fixture-marker"}`)
	writeRunFile("runs/"+folder+"/helpers/answers/answer.txt", "Public final answer")
	answer := call("files", map[string]any{"action": "read", "workflow_id": script.ID, "path": "runs/" + folder + "/helpers/answers/answer.txt"})
	if answer["content"] != "Public final answer" {
		t.Fatalf("public answer artifact unavailable: %v", answer)
	}
	privateBody, _ := json.Marshal(map[string]any{"name": "files", "arguments": map[string]any{"action": "read", "workflow_id": script.ID, "path": "runs/" + folder + "/helpers/requests/private.json"}})
	w = httptest.NewRecorder()
	f.api.dispatchExternalCall(w, adminRequest(http.MethodPost, "/api/external/call", string(privateBody), owner, nil), false)
	helperFile := externalTestBody(t, w, http.StatusOK)
	if !strings.Contains(helperFile["content"].(string), "helper-request-fixture-marker") {
		t.Fatalf("run reader lost helper input/result access: %v", helperFile)
	}
	result := call("runs", map[string]any{"action": "get", "workflow_id": script.ID, "run_folder": folder})
	metadata, _ := result["metadata"].(map[string]any)
	evidence, _ := result["evidence"].([]any)
	if metadata["execution_id"] != "exec-one" || metadata["python_steps"] == nil || len(evidence) != 1 || evidence[0].(map[string]any)["name"] != "Totals agree" {
		t.Fatalf("run record/evidence missing: %v", result)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "helper-request-fixture-marker") {
		t.Fatalf("run summary copied arbitrary helper file contents: %s", encoded)
	}
	owner.AccessToken.FileGuard = &wf.FolderGuard{ReadPaths: []string{"."}, BlockedPaths: []string{"runs/" + folder + "/run_metadata.json"}}
	body, _ = json.Marshal(map[string]any{"name": "runs", "arguments": map[string]any{"action": "get", "workflow_id": script.ID, "run_folder": folder}})
	w = httptest.NewRecorder()
	f.api.dispatchExternalCall(w, adminRequest(http.MethodPost, "/api/external/call", string(body), owner, nil), false)
	externalTestBody(t, w, http.StatusForbidden)
	// Permission to read a single public metadata file is not permission to
	// project checks/notices from the rest of this run's helper records.
	owner.AccessToken.FileGuard = &wf.FolderGuard{ReadPaths: []string{"runs/" + folder + "/run_metadata.json"}}
	w = httptest.NewRecorder()
	f.api.dispatchExternalCall(w, adminRequest(http.MethodPost, "/api/external/call", string(body), owner, nil), false)
	externalTestBody(t, w, http.StatusForbidden)
}
