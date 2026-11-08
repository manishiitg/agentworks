package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// PLAT-746-468: a preset edit shares the script editor's authority and native
// contract validator; it cannot replace source/settings or widen file grants.
func TestExternalScriptPresetsUseCanonicalWriterAndAuthority(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	f, _ := newExternalCrewCommandFixture(t)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("AGENT_PRODUCTS", "work,agentworks")
	f.api.projectActions = NewProjectActionDispatcher(f.api, f.api.agentProfiles)
	owner := crewWriter("owner")
	owner.AccessToken.AllWorkflows = true
	owner.AccessToken.Scopes = append(owner.AccessToken.Scopes, "workflows:read", "files:read")
	crewCapabilityHTTP(t, f, owner, "workspace", map[string]any{"action": "automation_create", "kind": "script", "name": "Weekly report"}, 200)
	bindings, err := readCrewExecutables()
	if err != nil || len(bindings) != 1 {
		t.Fatalf("binding: %v %v", bindings, err)
	}
	var script crewExecutableBinding
	for _, binding := range bindings {
		script = binding
	}
	// The same native tool already supports nested source; exercise it through
	// the external adapter instead of introducing another source transport.
	crewCapabilityHTTP(t, f, owner, "workspace", map[string]any{"action": "automation_write", "workspace_path": script.Root, "file": "nested/report.py", "content": "def total():\n    return 7\n"}, 200)
	if got := crewCapabilityHTTP(t, f, owner, "workspace", map[string]any{"action": "automation_read", "workspace_path": script.Root, "file": "nested/report.py"}, 200); !strings.Contains(got, "return 7") {
		t.Fatal(got)
	}
	update := func(claims *UserClaims, args map[string]any, status int) {
		t.Helper()
		args["action"], args["workflow_id"] = "save_inputs", script.ID
		body, _ := json.Marshal(map[string]any{"name": "workflow", "arguments": args})
		w := httptest.NewRecorder()
		f.api.dispatchExternalCall(w, adminRequest(http.MethodPost, "/api/external/call", string(body), claims, nil), false)
		externalTestBody(t, w, status)
	}
	update(owner, map[string]any{"inputs": []any{map[string]any{"name": "count", "type": "integer", "value": "3"}}, "input_presets": []any{map[string]any{"name": "weekly", "values": map[string]string{"count": "7"}}}}, 200)
	before, err := readCrewScript(t.Context(), script.Root)
	if err != nil {
		t.Fatal(err)
	}
	update(owner, map[string]any{"input_presets": []any{map[string]any{"name": "weekly", "values": map[string]string{"count": "9"}}}}, 200)
	after, err := readCrewScript(t.Context(), script.Root)
	if err != nil || after.ID != before.ID || after.Name != before.Name || len(after.Inputs) != 1 || after.Inputs[0].Value != "3" || after.InputPresets[0].Values["count"] != "9" {
		t.Fatalf("preset-only change lost script definition: %+v %v", after, err)
	}
	update(owner, map[string]any{"input_presets": []any{map[string]any{"name": "invalid", "values": map[string]string{"unknown": "9"}}}}, 400)
	unchanged, _ := readCrewScript(t.Context(), script.Root)
	if unchanged.InputPresets[0].Name != "weekly" {
		t.Fatal("invalid preset replaced saved values")
	}
	for _, claims := range []*UserClaims{crewReader("owner"), crewWriter("other")} {
		claims.AccessToken.AllWorkflows = true
		claims.AccessToken.Scopes = append(claims.AccessToken.Scopes, "workflows:read")
		update(claims, map[string]any{"input_presets": []any{}}, 403)
	}
	owner.AccessToken.FileGuard = &wf.FolderGuard{ReadPaths: []string{"."}, WritePaths: []string{"code"}}
	update(owner, map[string]any{"input_presets": []any{}}, 403)
}

// PLAT-746-450: tightening a schema and its saved mappings cannot require
// either half to be saved against the incompatible old half. Exercise MCP,
// the native source tool and the actual workspace handlers/storage together.
func TestExternalScriptInputAndMappingSaveKeepsEntryAuthority(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	f, _ := newExternalCrewCommandFixture(t)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("AGENT_PRODUCTS", "work,agentworks")
	f.api.projectActions = NewProjectActionDispatcher(f.api, f.api.agentProfiles)
	owner := crewWriter("owner")
	owner.AccessToken.AllWorkflows = true
	owner.AccessToken.Scopes = append(owner.AccessToken.Scopes, "workflows:read", "files:read", "builder:chat", "runs:execute")
	crewCapabilityHTTP(t, f, owner, "workspace", map[string]any{"action": "automation_create", "kind": "script", "name": "Testing"}, 200)
	bindings, err := readCrewExecutables()
	if err != nil || len(bindings) != 1 {
		t.Fatal(bindings, err)
	}
	var script crewExecutableBinding
	for _, binding := range bindings {
		script = binding
	}
	call := func(claims *UserClaims, name string, args map[string]any, status int) map[string]any {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"name": name, "arguments": args})
		w := httptest.NewRecorder()
		f.api.dispatchExternalCall(w, adminRequest(http.MethodPost, "/api/external/call", string(body), claims, nil), false)
		return externalTestBody(t, w, status)
	}
	save := func(inputs []any, edits any, status int) {
		t.Helper()
		args := map[string]any{"action": "save_inputs", "workflow_id": script.ID, "inputs": inputs, "input_presets": []any{}}
		if edits != nil {
			args["entry_mappings"] = edits
		}
		call(owner, "workflow", args, status)
	}
	inputs := func(value string, constrained bool) []any {
		field := map[string]any{"name": "scenario", "type": "string", "value": value}
		if constrained {
			field["schema"] = map[string]any{"enum": []string{value}}
		}
		return []any{field, map[string]any{"name": "probe_count", "type": "integer", "value": "7"}}
	}
	save(inputs("full", false), nil, 200)
	mapping := func(value string) map[string]any {
		return map[string]any{"mode": "raw", "variables": []string{"scenario", "probe_count"}, "payload": map[string]any{"inputs": map[string]any{"scenario": map[string]any{"source": "$.scenario", "default": value}, "probe_count": map[string]any{"source": "$.probe.count"}}}}
	}
	seed := func(file, content string) {
		t.Helper()
		if err := writeFileToWorkspace(t.Context(), script.Root+"/"+file, content); err != nil {
			t.Fatal(err)
		}
	}
	seed("functions.json", `{"functions":[{"id":"bound","name":"testing","enabled":false,"action":{"type":"run"},"input":{},"inputs":["scenario","probe_count"],"input_mapping":`+mustJSON(t, mapping("legacy"))+`,"allowed_callers":[{"type":"crew","id":"original-caller","profile_id":"work"}],"created_by":"owner"}]}`)
	seed("webhooks.json", `{"webhooks":[{"id":"hook","name":"Testing hook","project":{"kind":"goal","id":"`+script.ID+`"},"enabled":false,"path":"/api/hooks/hook","action":{"type":"run"},"input":{},"run_as":"owner","auth":{"mode":"bearer","encrypted_secret":"sealed-fixture"},"input_mapping":`+mustJSON(t, mapping("legacy"))+`}]}`)
	files := []string{"code/script.json", "functions.json", "webhooks.json"}
	snapshot := func() []string {
		t.Helper()
		var result []string
		for _, file := range files {
			raw, found, err := readFileFromWorkspace(t.Context(), script.Root+"/"+file)
			if err != nil || !found {
				t.Fatal(file, err)
			}
			result = append(result, raw)
		}
		return result
	}
	originalFunctions, _ := readProjectFunctionDefinitions(t.Context(), script.Root)
	originalHooks, _ := readProjectWebhookDefinitions(t.Context(), script.Root)
	before := snapshot()
	save(inputs("full", true), nil, 400)
	if !reflect.DeepEqual(before, snapshot()) {
		t.Fatal("invalid final candidate changed saved files")
	}
	edits := func(value string) map[string]any {
		return map[string]any{"functions": map[string]any{"bound": map[string]any{"input_mapping": mapping(value)}}, "webhooks": map[string]any{"hook": map[string]any{"input_mapping": mapping(value)}}}
	}
	save(inputs("full", true), edits("full"), 200)
	changedFunctions, _ := readProjectFunctionDefinitions(t.Context(), script.Root)
	changedHooks, _ := readProjectWebhookDefinitions(t.Context(), script.Root)
	changedFunctions[0].InputMapping = originalFunctions[0].InputMapping
	changedHooks[0].InputMapping = originalHooks[0].InputMapping
	if !reflect.DeepEqual(changedFunctions, originalFunctions) || !reflect.DeepEqual(changedHooks, originalHooks) {
		t.Fatal("input save changed caller, auth, action, enabled state or identity")
	}
	before = snapshot()
	for _, bad := range []any{edits("wrong"), map[string]any{"functions": map[string]any{"missing": map[string]any{}}}, map[string]any{"webhooks": map[string]any{"hook": map[string]any{"enabled": true}}}} {
		save(inputs("full", true), bad, 400)
		if !reflect.DeepEqual(before, snapshot()) {
			t.Fatal("refused input edit changed a saved file")
		}
	}
	definition, _ := readCrewScript(t.Context(), script.Root)
	definition.Inputs[0].Value = "diagnostic"
	definition.Inputs[0].Schema = map[string]interface{}{"enum": []interface{}{"diagnostic"}}
	content, _ := json.Marshal(definition)
	crewCapabilityHTTP(t, f, owner, "workspace", map[string]any{"action": "automation_write", "workspace_path": script.Root, "file": "script.json", "content": string(content), "entry_mappings": edits("diagnostic")}, 200)
	call(owner, "functions", map[string]any{"action": "define", "project": script.ID, "definition": map[string]any{"name": "testing", "action": map[string]any{"type": "run"}, "input_mapping": mapping("diagnostic")}}, 200)
	for _, file := range []string{"functions.json", "webhooks.json"} {
		raw, _, _ := readFileFromWorkspace(t.Context(), script.Root+"/"+file)
		if !strings.Contains(raw, "diagnostic") {
			t.Fatal("native write did not commit dependent mapping", file)
		}
	}
	if _, found, err := readFileFromWorkspace(t.Context(), script.Root+"/workflow.json"); err != nil || found {
		t.Fatal("input edit wrote a second script manifest")
	}
}
