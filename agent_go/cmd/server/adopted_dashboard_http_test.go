package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
)

// PLAT-746-511: same-ID adoption left a saved short link on the old workflow
// root, and dashboard storage bypassed the Crew's adopted database mapping.
// Use real workspace HTTP, the actual repair CLI, and authenticated app routes.
func TestAdoptedDashboardHTTPKeepsShortLinkAndRefusesOtherPeople(t *testing.T) {
	f, workspaceURL := newExternalCrewCommandFixture(t)
	t.Setenv("AUTH_SECRET", "adopted-dashboard-http-test-secret")
	t.Setenv("PUBLIC_URL", "https://example.com")
	const oldRoot = "Workflow/example"
	const identity = "example"
	const code = "abcdefghjk"
	f.write(t, oldRoot+"/workflow.json", `{"id":"example","label":"Example","created_by":"owner"}`)
	workspaceCall := func(req dashboards.Request) dashboards.Result {
		t.Helper()
		raw, _ := json.Marshal(req)
		request, _ := http.NewRequest(http.MethodPost, workspaceURL+"/api/dashboards", bytes.NewReader(raw))
		request.Header.Set("X-Workspace-Token", "crew-command-test-token")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, _ := io.ReadAll(response.Body)
		var out dashboards.Result
		if response.StatusCode != 200 || json.Unmarshal(data, &out) != nil {
			t.Fatalf("workspace %s: %d %s", req.Action, response.StatusCode, data)
		}
		return out
	}
	created := workspaceCall(dashboards.Request{Root: oldRoot, Action: "create", ID: "overview", Title: "Overview", IncludeDraft: true,
		Files: map[string]string{"index.html": "<title>Overview</title><p>original-publication</p>", "scripts/data.py": "print('{}')\n"}})
	workspaceCall(dashboards.Request{Root: oldRoot, Action: "publish", ID: "overview", ExpectedRevision: created.Dashboard.Revision, IncludeDraft: true})
	resource := map[string]any{"id": "resource-kept", "root": oldRoot, "project_id": identity, "kind": "workflow", "dashboard": "overview", "code": code}
	raw, _ := json.Marshal(map[string]any{"resources": []any{resource}})
	f.write(t, dashboardQuestionRegistryPath, string(raw))
	// The converter's trusted same-ID binding and database move. Deliberately
	// retain its old render-source placement so the repair must correct both.
	private := filepath.Join(f.docs, externalCommandCrewState)
	runtime := filepath.Join(private, "workflows", identity)
	if err := os.MkdirAll(filepath.Dir(runtime), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(f.docs, oldRoot), runtime); err != nil {
		t.Fatal(err)
	}
	authored := filepath.Join(f.docs, externalCommandCrewRoot, ".agentworks/code", identity)
	if err := os.MkdirAll(filepath.Dir(authored), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(runtime, "code"), authored); err != nil {
		t.Fatal(err)
	}
	owners := defaultProjectOwners()
	if err := owners.modify(func(rows map[string]projectOwnerRecord) (bool, error) {
		row := rows["work/test-team"]
		row.DatabaseRoot = "workflows/example/db"
		row.Shared = false
		row.Private = true
		rows["work/test-team"] = row
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := registerCrewExecutable(crewExecutableBinding{ID: identity, CrewID: "team", CrewRoot: externalCommandCrewRoot, Root: oldRoot, Kind: "script", RuntimeRoot: "workflows/example"}); err != nil {
		t.Fatal(err)
	}
	state := os.Getenv("AGENTWORKS_STATE_ROOT")
	if state == "" {
		t.Fatal("fixture must use isolated state")
	}
	if err := os.WriteFile(filepath.Join(state, "ownership/workflows.json"), []byte(`{"Workflow/example":"owner"}`), 0600); err != nil {
		t.Fatal(err)
	}
	migration, err := filepath.Abs("../../../scripts/migrate-workflow-to-python.py")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("python3", migration, "--docs", f.docs, "--state", state, "--workflow-id", identity, "--repair-dashboard-links", "--apply", "--backup", t.TempDir())
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("repair CLI: %v\n%s", err, output)
	}

	router := mux.NewRouter()
	apiRoutes := router.PathPrefix("/api").Subrouter()
	apiRoutes.HandleFunc("/dashboards", f.api.handleDashboards).Methods(http.MethodPost)
	apiRoutes.HandleFunc("/workflow/report-preview/file", f.api.handleReportPreviewFile).Methods(http.MethodGet)
	f.api.registerDashboardShortLinkRoutes(apiRoutes)
	app := httptest.NewServer(AuthMiddleware(router))
	defer app.Close()
	tokens := map[string]string{}
	for _, person := range []string{"owner", "other"} {
		token, err := GenerateJWT(person, person, "")
		if err != nil {
			t.Fatal(err)
		}
		tokens[person] = token
	}
	call := func(person, method, path string, body any) (int, []byte) {
		t.Helper()
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, app.URL+path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		if person != "" {
			req.Header.Set("Authorization", "Bearer "+tokens[person])
		}
		response, err := app.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, _ := io.ReadAll(response.Body)
		return response.StatusCode, data
	}
	for _, action := range []string{"list", "get"} {
		status, body := call("owner", http.MethodPost, "/api/dashboards", map[string]any{"action": action, "workspace": externalCommandCrewRoot, "dashboard_id": "overview"})
		if status != 200 || !bytes.Contains(body, []byte("overview")) {
			t.Fatalf("adopted %s: %d %s", action, status, body)
		}
	}
	status, body := call("owner", http.MethodGet, "/api/dashboards/by-code/"+code, nil)
	var resolved map[string]any
	_ = json.Unmarshal(body, &resolved)
	if status != 200 || resolved["root"] != externalCommandCrewRoot || resolved["project_id"] != "team" || resolved["resource_id"] != "resource-kept" {
		t.Fatalf("same short link: %d %s", status, body)
	}
	render := "/api/workflow/report-preview/file?workspace=" + url.QueryEscape(externalCommandCrewRoot) + "&path=" + url.QueryEscape(resolved["document_path"].(string))
	status, body = call("owner", http.MethodGet, render, nil)
	if status != 200 || !strings.Contains(string(body), "original-publication") {
		t.Fatalf("published render: %d %s", status, body)
	}
	for _, person := range []string{"other", ""} {
		status, body = call(person, http.MethodGet, "/api/dashboards/by-code/"+code, nil)
		expected := 404
		if person == "" {
			expected = 401
		}
		if status != expected {
			t.Fatalf("refuse %q: %d %s", person, status, body)
		}
	}
	// The bundle's script is read from the canonical Crew location, never copied
	// beside the private database or left in an old execution identity's source.
	script := filepath.Join(f.docs, externalCommandCrewRoot, "code/reports/managed/overview", created.Dashboard.Revision, "data.py")
	if data, err := os.ReadFile(script); err != nil || string(data) != "print('{}')\n" {
		t.Fatalf("canonical render source: %q %v", data, err)
	}
	workspaceCall(dashboards.Request{Root: externalCommandCrewRoot, Action: "get", ID: "overview"})
}
