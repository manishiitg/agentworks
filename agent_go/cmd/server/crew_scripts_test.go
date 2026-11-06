package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/projectactions"
)

// PLAT-746-265: an admitted Run person may execute a script, but cannot edit
// shared source. A workflow reference cannot select another Crew's source.
func TestCrewScriptSourceUsesParentAuthorityAndReferencesStayInCrew(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("AGENT_PRODUCTS", "work,agentworks")
	resetCrewOwnerCache()
	t.Cleanup(resetCrewOwnerCache)
	t.Setenv("AUTH_SECRET", "crew-script-test-auth-secret-01234567890123456789")
	directory := withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","role":"creator"},{"id":"runner","username":"runner","role":"creator"}]}`)
	for _, record := range []projectOwnerRecord{{Product: "work", Folder: "lab", ProjectID: "crew-lab", OwnerID: "owner", Shared: true, Private: true, AudienceConfigured: true, RunPeople: []string{"runner"}}, {Product: "work", Folder: "other", ProjectID: "crew-other", OwnerID: "owner", Shared: true, Private: true, AudienceConfigured: true}} {
		if err := defaultProjectOwners().Register(record); err != nil {
			t.Fatal(err)
		}
	}
	script := crewExecutableBinding{ID: "qualify", CrewID: "crew-lab", CrewRoot: "Crew/lab", Root: "Workflow/qualify", Kind: "script"}
	other := crewExecutableBinding{ID: "other-script", CrewID: "crew-other", CrewRoot: "Crew/other", Root: "Workflow/other-script", Kind: "script"}
	parent := crewExecutableBinding{ID: "sequence", CrewID: "crew-lab", CrewRoot: "Crew/lab", Root: "Workflow/sequence", Kind: "workflow"}
	for _, binding := range []crewExecutableBinding{script, other, parent} {
		if err := registerCrewExecutable(binding); err != nil {
			t.Fatal(err)
		}
	}
	files := &mockWorkspaceAPI{files: map[string]string{"Workflow/qualify/code/script.json": `{"id":"qualify","name":"Qualify","inputs":[{"name":"count","type":"integer","value":"2"}]}`, "Workflow/qualify/code/main.py": "print('saved')\n", "Workflow/other-script/code/script.json": `{"id":"other-script","name":"Other"}`, "Workflow/other-script/code/main.py": "print('private')\n"}}
	workspace := httptest.NewServer(files)
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	api := &StreamingAPI{}
	endpoint := httptest.NewServer(AuthMiddleware(http.HandlerFunc(api.handleCrewScriptFile)))
	defer endpoint.Close()
	request := func(user, content string) int {
		t.Helper()
		token, err := GenerateJWT(user, user, "")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(map[string]string{"content": content})
		req, _ := http.NewRequest(http.MethodPut, endpoint.URL+"/api/crew-script-file?workspace_path="+script.Root+"&file=main.py", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		bodyText, _ := io.ReadAll(response.Body)
		t.Logf("source edit user=%s HTTP%d %s", user, response.StatusCode, strings.TrimSpace(string(bodyText)))
		return response.StatusCode
	}
	if code := request("runner", "print('forged')"); code != 403 {
		t.Fatalf("Run-person edit HTTP%d", code)
	}
	if code := request("owner", "print('owner update')\n"); code != 200 {
		t.Fatalf("Builder edit HTTP%d", code)
	}
	// PLAT-746-265 / PR498: ownership alone cannot restore revoked account rights.
	originalDirectory := *directory
	for _, record := range []string{
		`{"id":"owner","username":"owner","role":"viewer"}`,
		`{"id":"owner","username":"owner","role":"creator","disabled":true}`,
		`{"id":"owner","username":"owner","role":"creator","products":["agentworks"]}`,
	} {
		*directory = `{"users":[` + record + `]}`
		invalidateUserDirectoryCache()
		if code := request("owner", "print('revoked')"); code != 403 {
			t.Errorf("revoked account source edit HTTP%d: %s", code, record)
		}
	}
	*directory = originalDirectory
	invalidateUserDirectoryCache()
	if code := request("owner", "print('owner update')\n"); code != 200 {
		t.Fatalf("restored Builder edit HTTP%d", code)
	}
	manifest, found, err := ReadWorkflowManifest(context.Background(), script.Root)
	if err != nil || !found {
		t.Fatal(err)
	}
	if workflowAccessForManifest(&UserClaims{UserID: "runner", Username: "runner"}, manifest) != WorkflowAccessRead {
		t.Fatal("Run person lost admitted execution access")
	}
	resolver := &crewStepRunner{userID: "owner"}
	resolved, err := resolver.ResolveCrewScript(context.Background(), parent.Root, "script:qualify")
	if err != nil || resolved.Files["main.py"] != "print('owner update')\n" {
		t.Fatalf("same-Crew script: %+v %v", resolved, err)
	}
	if _, err := resolver.ResolveCrewScript(context.Background(), parent.Root, "other-script"); err == nil || !strings.Contains(err.Error(), "shared function") {
		t.Fatal("cross-Crew source reference accepted", err)
	}
	if _, err := resolver.ResolveCrewScript(context.Background(), parent.Root, "../../other"); err == nil {
		t.Fatal("script id selected a filesystem path")
	}
	if _, present := files.files[script.Root+"/workflow.json"]; present {
		t.Fatal("script projection persisted a duplicate manifest")
	}
	// All existing entry types persist against the one script definition.
	functionRecord := projectFunctionDefinition{ID: "call", Name: "qualify", Enabled: true, Action: projectactions.Action{Type: "run", Tab: "main"}, Input: json.RawMessage(`{}`), Inputs: []string{"count"}}
	if err := writeProjectFunctionDefinitions(context.Background(), script.Root, []projectFunctionDefinition{functionRecord}); err != nil {
		t.Fatal(err)
	}
	projected, found, err := ReadWorkflowManifest(context.Background(), script.Root)
	if err != nil || !found || len(projected.Schedules) != 1 {
		t.Fatal("script function projection", err)
	}
	values, _, err := workflowFunctionArgs(projected.Schedules[0], map[string]interface{}{"count": 3})
	if err != nil || values["count"] != "3" {
		t.Fatal("script function input", err)
	}
	if err := WriteWorkflowManifest(context.Background(), script.Root, projected); err != nil {
		t.Fatal(err)
	}
	if _, present := files.files[script.Root+"/workflow.json"]; present {
		t.Fatal("entry save wrote a duplicate script manifest")
	}
	definition, err := readWorkflowInputDefinitions(context.Background(), script.Root)
	if err != nil || len(definition.Variables) != 1 {
		t.Fatal("script lost its input contract", err)
	}
	if err := writeProjectFunctionDefinitions(context.Background(), script.Root, nil); err != nil {
		t.Fatal(err)
	}
	files.mu.Lock()
	files.files[parent.Root+"/planning/plan.json"] = `{"steps":[{"id":"use","type":"scripted","title":"Qualify step","description":"Use saved code","script":"qualify","script_inputs":{"count":{"value":3}}}]}`
	files.mu.Unlock()
	if err := saveCrewScriptInputs(context.Background(), script.Root, &workflow.VariablesManifest{Variables: []workflow.Variable{{Name: "count", Type: "string"}}}); err == nil || !strings.Contains(err.Error(), "Qualify step") {
		t.Fatal("breaking script input edit did not identify the referencing step", err)
	}
	// Referenced specialist definitions live outside the ordinary step list.
	files.mu.Lock()
	files.files[parent.Root+"/planning/plan.json"] = `{"steps":[],"orphan_steps":[{"id":"specialist","type":"scripted","title":"Saved specialist","description":"Uses shared code","script":"qualify","script_inputs":{"count":{"value":3}}}]}`
	files.mu.Unlock()
	if err := saveCrewScriptInputs(context.Background(), script.Root, &workflow.VariablesManifest{Variables: []workflow.Variable{{Name: "count", Type: "string"}}}); err == nil || !strings.Contains(err.Error(), "Saved specialist") {
		t.Errorf("specialist input edit lost consumer validation: %v", err)
	}
	if err := saveCrewScriptInputs(context.Background(), script.Root, &workflow.VariablesManifest{Variables: []workflow.Variable{{Name: "count", Type: "integer", Value: "4"}}}); err != nil {
		t.Fatal(err)
	}
	if err := saveCrewScriptInputs(context.Background(), script.Root, &workflow.VariablesManifest{Variables: []workflow.Variable{{Name: "count", Type: "integer", Required: true}}}); err != nil {
		t.Fatal(err)
	}
	projected, _, err = ReadWorkflowManifest(context.Background(), script.Root)
	if err != nil {
		t.Fatal(err)
	}
	projected.Schedules = []WorkflowSchedule{{ID: "timer", Name: "Required input timer", ScheduleType: "cron", CronExpression: "0 0 * * *", Timezone: "UTC", Enabled: true}}
	if err := WriteWorkflowManifest(context.Background(), script.Root, projected); err == nil || !strings.Contains(strings.ToLower(err.Error()), "count") {
		t.Errorf("enabled script timer accepted missing required input: %v", err)
	} else {
		response := httptest.NewRecorder()
		writeScheduleSaveError(response, err)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("missing input looked like server failure: HTTP%d", response.Code)
		}
	}
	projected.Schedules[0].Inputs = map[string]string{"count": "3"}
	if err := WriteWorkflowManifest(context.Background(), script.Root, projected); err != nil {
		t.Fatalf("complete timer inputs refused: %v", err)
	}
	if _, present := files.files[script.Root+"/variables/variables.json"]; present {
		t.Fatal("script wrote a duplicate input definition")
	}
	// PLAT-746: a closed destination must block running, but never block Pause.
	projected.Schedules[0].ChatID = "closed-chat"
	if err := WriteWorkflowManifest(context.Background(), script.Root, projected); err == nil {
		t.Fatal("enabled timer accepted an unavailable chat")
	}
	projected.Schedules[0].Enabled = false
	if err := WriteWorkflowManifest(context.Background(), script.Root, projected); err != nil {
		t.Fatalf("could not pause timer after its chat closed: %v", err)
	}
}
