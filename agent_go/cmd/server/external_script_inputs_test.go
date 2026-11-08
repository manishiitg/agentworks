package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
