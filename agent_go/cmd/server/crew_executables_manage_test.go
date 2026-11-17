package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// crewWorkflowRequest calls the Crew workflow endpoint as the Crew's owner.
func crewWorkflowRequest(api *StreamingAPI, ctx context.Context, method, crewID, query, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	url := "/api/crew-executables?crew_id=" + crewID + query
	api.handleCrewExecutables(w, httptest.NewRequest(method, url, strings.NewReader(body)).WithContext(ctx))
	return w
}

func createCrewWorkflowForTest(t *testing.T, api *StreamingAPI, ctx context.Context, crewID, name string) crewExecutableBinding {
	t.Helper()
	w := crewWorkflowRequest(api, ctx, http.MethodPost, crewID, "", `{"name":"`+name+`","kind":"script"}`)
	var created crewExecutableBinding
	if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &created) != nil {
		t.Fatalf("create workflow %q: HTTP%d %s", name, w.Code, w.Body)
	}
	return created
}

func listedCrewWorkflowLabels(t *testing.T, api *StreamingAPI, ctx context.Context, crewID string) map[string]string {
	t.Helper()
	w := crewWorkflowRequest(api, ctx, http.MethodGet, crewID, "", "")
	var response struct {
		Executables []struct {
			ID       string `json:"id"`
			Manifest struct {
				Label string `json:"label"`
			} `json:"manifest"`
		} `json:"executables"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &response) != nil {
		t.Fatalf("list workflows: HTTP%d %s", w.Code, w.Body)
	}
	labels := map[string]string{}
	for _, entry := range response.Executables {
		labels[entry.ID] = entry.Manifest.Label
	}
	return labels
}

// A rename with a bad name or an unknown workflow is refused and changes nothing. The successful save uses
// saveCrewScriptDefinition, the same path as every script.json edit, which needs a real workspace folder.
func TestCrewWorkflowRenameRefusesBadRequests(t *testing.T) {
	svc, _, ctx := newCrewCreationTestEnv(t)
	t.Setenv("AGENT_PRODUCTS", "work,agentworks")
	crew, err := svc.CreateCrewProject(ctx, CreateCrewRequest{UserID: "owner", WorkflowPath: "Workflow/build", Title: "Reports", Purpose: "Run reports", IdempotencyKey: "rename-workflow"})
	if err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{projectActions: svc}
	workflow := createCrewWorkflowForTest(t, api, ctx, crew.CrewID, "Daily report")

	for name, body := range map[string]string{
		"empty name":    `{"id":"` + workflow.ID + `","name":"   "}`,
		"too long":      `{"id":"` + workflow.ID + `","name":"` + strings.Repeat("a", 121) + `"}`,
		"unknown field": `{"id":"` + workflow.ID + `","name":"Report","kind":"script"}`,
		"not JSON":      `name=Report`,
		"missing name":  `{"id":"` + workflow.ID + `"}`,
	} {
		if w := crewWorkflowRequest(api, ctx, http.MethodPatch, crew.CrewID, "", body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: HTTP%d %s, want 400", name, w.Code, w.Body)
		}
	}
	if w := crewWorkflowRequest(api, ctx, http.MethodPatch, crew.CrewID, "", `{"id":"no-such-workflow","name":"Report"}`); w.Code != http.StatusNotFound {
		t.Errorf("unknown workflow: HTTP%d %s, want 404", w.Code, w.Body)
	}
	if labels := listedCrewWorkflowLabels(t, api, ctx, crew.CrewID); labels[workflow.ID] != "Daily report" {
		t.Fatalf("refused renames must not change the name, got %v", labels)
	}
}

// A Crew's owner can only manage that Crew's own workflow, never another Crew's.
func TestCrewWorkflowRenameAndDeleteStayInsideTheCrew(t *testing.T) {
	svc, _, ctx := newCrewCreationTestEnv(t)
	t.Setenv("AGENT_PRODUCTS", "work,agentworks")
	first, err := svc.CreateCrewProject(ctx, CreateCrewRequest{UserID: "owner", WorkflowPath: "Workflow/build", Title: "First", Purpose: "Run reports", IdempotencyKey: "scope-first"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.CreateCrewProject(ctx, CreateCrewRequest{UserID: "owner", WorkflowPath: "Workflow/build", Title: "Second", Purpose: "Run reports", IdempotencyKey: "scope-second"})
	if err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{projectActions: svc}
	createCrewWorkflowForTest(t, api, ctx, first.CrewID, "First report")
	other := createCrewWorkflowForTest(t, api, ctx, second.CrewID, "Second report")

	if w := crewWorkflowRequest(api, ctx, http.MethodPatch, first.CrewID, "", `{"id":"`+other.ID+`","name":"Taken over"}`); w.Code != http.StatusNotFound {
		t.Errorf("rename through another Crew: HTTP%d %s, want 404", w.Code, w.Body)
	}
	if w := crewWorkflowRequest(api, ctx, http.MethodDelete, first.CrewID, "&id="+other.ID, ""); w.Code != http.StatusNotFound {
		t.Errorf("delete through another Crew: HTTP%d %s, want 404", w.Code, w.Body)
	}
	if labels := listedCrewWorkflowLabels(t, api, ctx, second.CrewID); labels[other.ID] != "Second report" {
		t.Fatalf("the other Crew's workflow must be untouched, got %v", labels)
	}
	if w := crewWorkflowRequest(api, ctx, http.MethodDelete, first.CrewID, "&id=no-such-workflow", ""); w.Code != http.StatusNotFound {
		t.Errorf("delete unknown workflow: HTTP%d %s, want 404", w.Code, w.Body)
	}
}

// Rename and delete need the same edit access as creating a workflow.
func TestCrewWorkflowRenameAndDeleteNeedEditAccess(t *testing.T) {
	svc, _, ctx := newCrewCreationTestEnv(t)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("AGENT_PRODUCTS", "work,agentworks")
	directory := withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","role":"creator"}]}`)
	crew, err := svc.CreateCrewProject(ctx, CreateCrewRequest{UserID: "owner", WorkflowPath: "Workflow/build", Title: "Reports", Purpose: "Run reports", IdempotencyKey: "edit-access"})
	if err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{projectActions: svc}
	workflow := createCrewWorkflowForTest(t, api, ctx, crew.CrewID, "Daily report")

	*directory = `{"users":[{"id":"owner","username":"owner","role":"viewer"}]}`
	invalidateUserDirectoryCache()
	if w := crewWorkflowRequest(api, ctx, http.MethodPatch, crew.CrewID, "", `{"id":"`+workflow.ID+`","name":"Renamed"}`); w.Code != http.StatusForbidden {
		t.Errorf("demoted owner rename: HTTP%d %s, want 403", w.Code, w.Body)
	}
	if w := crewWorkflowRequest(api, ctx, http.MethodDelete, crew.CrewID, "&id="+workflow.ID, ""); w.Code != http.StatusForbidden {
		t.Errorf("demoted owner delete: HTTP%d %s, want 403", w.Code, w.Body)
	}
	if found, err := findCrewExecutableByID(workflow.ID); err != nil || found == nil {
		t.Fatalf("a refused delete must keep the workflow: %v", err)
	}
}

// Removing a workflow frees the Crew's one workflow slot, and only for that workflow.
func TestRemoveCrewExecutableBindingRemovesOnlyThatWorkflow(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	first := crewExecutableBinding{ID: "first", CrewID: "crew", CrewRoot: "Crew/example", Root: "Workflow/first", Kind: "script"}
	if err := registerCrewExecutable(first); err != nil {
		t.Fatal(err)
	}
	other := crewExecutableBinding{ID: "other", CrewID: "another-crew", CrewRoot: "Crew/another", Root: "Workflow/other", Kind: "script"}
	if err := registerCrewExecutable(other); err != nil {
		t.Fatal(err)
	}
	// A Crew can hold several workflows.
	if err := registerCrewExecutable(crewExecutableBinding{ID: "second", CrewID: "crew", CrewRoot: "Crew/example", Root: "Workflow/second", Kind: "script"}); err != nil {
		t.Fatalf("a second workflow in the same Crew must be accepted: %v", err)
	}
	// The ID of another Crew's workflow, or a missing ID, removes nothing.
	for _, wrong := range [][2]string{{"crew", "other"}, {"another-crew", "first"}, {"crew", "missing"}} {
		if err := removeCrewExecutableBinding(wrong[0], wrong[1]); err != nil {
			t.Fatal(err)
		}
	}
	if records, err := readCrewExecutables(); err != nil || len(records) != 3 {
		t.Fatalf("nothing should be removed yet: %d records, error %v", len(records), err)
	}
	if err := removeCrewExecutableBinding("crew", "first"); err != nil {
		t.Fatal(err)
	}
	records, err := readCrewExecutables()
	if err != nil || len(records) != 2 {
		t.Fatalf("only that workflow should go: %d records, error %v", len(records), err)
	}
	for _, record := range records {
		if record.ID == "first" {
			t.Fatal("the removed workflow is still registered")
		}
	}
}

// Delete removes the workflow's code, runs, saved settings and definition, and nothing of its neighbours.
func TestRemoveCrewWorkflowFilesRemovesOnlyThatWorkflow(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	docs, state := filepath.Join(base, "docs"), filepath.Join(base, "state")
	project := filepath.Join(docs, "Crew", "example")
	for _, dir := range []string{filepath.Join(project, ".agentworks"), filepath.Join(docs, crewroot.PrivateDirectory, "crew"), filepath.Join(state, "ownership", "crew-executables")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("AGENTWORKS_STATE_ROOT", state)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	records, _ := json.Marshal(map[string]any{"projects": map[string]crewroot.Record{"work/example": {Product: "work", Folder: "example", ID: "crew", Owner: "owner"}}})
	if err := os.WriteFile(filepath.Join(state, "ownership", "projects.json"), records, 0o600); err != nil {
		t.Fatal(err)
	}
	binding := crewExecutableBinding{ID: "report", CrewID: "crew", CrewRoot: "Crew/example", Root: "Workflow/report", Kind: "script", RuntimeRoot: "automations/report"}
	raw, _ := json.Marshal(binding)
	if err := os.WriteFile(filepath.Join(state, "ownership", "crew-executables", fmt.Sprintf("%x.json", sha256.Sum256([]byte(binding.Root)))), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	automation := filepath.Join(project, ".agentworks")
	mine := []string{
		filepath.Join(automation, "code", "report", "main.py"),
		filepath.Join(automation, "automations", "report", "runs", "run-1", "result.json"),
		filepath.Join(automation, "settings", "automations", "report", "schedules.json"),
		filepath.Join(automation, "workflows", "report", "workflow.json"),
	}
	kept := []string{
		filepath.Join(automation, "code", "neighbour", "main.py"),
		filepath.Join(automation, "automations", "neighbour", "runs", "run-1", "result.json"),
		filepath.Join(automation, "db", "db.sqlite"),
	}
	for _, file := range append(append([]string{}, mine...), kept...) {
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := removeCrewWorkflowFiles(binding); err != nil {
		t.Fatal(err)
	}
	for _, file := range mine {
		if _, err := os.Stat(file); !os.IsNotExist(err) {
			t.Errorf("%s must be removed, stat error %v", file, err)
		}
	}
	for _, folder := range []string{"code/report", "automations/report", "settings/automations/report", "workflows/report"} {
		if _, err := os.Stat(filepath.Join(automation, filepath.FromSlash(folder))); !os.IsNotExist(err) {
			t.Errorf("folder %s must be gone, stat error %v", folder, err)
		}
	}
	for _, file := range kept {
		if _, err := os.Stat(file); err != nil {
			t.Errorf("%s belongs to someone else and must stay: %v", file, err)
		}
	}
	// A second try (for example after a failed delete) must not fail on what is already gone.
	if err := removeCrewWorkflowFiles(binding); err != nil {
		t.Fatalf("removing twice: %v", err)
	}
}

// A Crew can hold several workflows, and no two of them share a name (ignoring case and extra spaces).
func TestCrewWorkflowNamesAreUniqueInsideACrew(t *testing.T) {
	svc, _, ctx := newCrewCreationTestEnv(t)
	t.Setenv("AGENT_PRODUCTS", "work,agentworks")
	crew, err := svc.CreateCrewProject(ctx, CreateCrewRequest{UserID: "owner", WorkflowPath: "Workflow/build", Title: "Names", Purpose: "Run reports", IdempotencyKey: "unique-names"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := svc.CreateCrewProject(ctx, CreateCrewRequest{UserID: "owner", WorkflowPath: "Workflow/build", Title: "Other", Purpose: "Run reports", IdempotencyKey: "unique-names-other"})
	if err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{projectActions: svc}
	github := createCrewWorkflowForTest(t, api, ctx, crew.CrewID, "GitHub sync")
	slack := createCrewWorkflowForTest(t, api, ctx, crew.CrewID, "Slack sync")
	if labels := listedCrewWorkflowLabels(t, api, ctx, crew.CrewID); len(labels) != 2 {
		t.Fatalf("a Crew must list both workflows, got %v", labels)
	}
	// Same name, differing only in case or spacing, is refused and adds nothing.
	for _, name := range []string{"GitHub sync", "github SYNC", "  GitHub   sync "} {
		w := crewWorkflowRequest(api, ctx, http.MethodPost, crew.CrewID, "", `{"name":"`+name+`","kind":"script"}`)
		if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "already has a workflow with that name") {
			t.Errorf("create %q: HTTP%d %s, want 409 name taken", name, w.Code, w.Body)
		}
	}
	if labels := listedCrewWorkflowLabels(t, api, ctx, crew.CrewID); len(labels) != 2 {
		t.Fatalf("a refused create must add nothing, got %v", labels)
	}
	// Rename onto another workflow's name is refused and changes nothing. (A successful rename saves script.json, which
	// needs a real workspace folder; TestCrewWorkflowRenameRefusesBadRequests covers the other refusals the same way.)
	if w := crewWorkflowRequest(api, ctx, http.MethodPatch, crew.CrewID, "", `{"id":"`+slack.ID+`","name":"GITHUB sync"}`); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "already has a workflow with that name") {
		t.Errorf("rename onto a taken name: HTTP%d %s, want 409", w.Code, w.Body)
	}
	if labels := listedCrewWorkflowLabels(t, api, ctx, crew.CrewID); labels[slack.ID] != "Slack sync" || labels[github.ID] != "GitHub sync" {
		t.Fatalf("a refused rename must change nothing, got %v", labels)
	}
	// Another Crew may use the same names.
	createCrewWorkflowForTest(t, api, ctx, other.CrewID, "GitHub sync")
}

// A new workflow keeps the one-line description it was created with, and a description that is too long is refused
// without creating anything.
func TestCrewWorkflowCreateKeepsItsDescription(t *testing.T) {
	svc, _, ctx := newCrewCreationTestEnv(t)
	t.Setenv("AGENT_PRODUCTS", "work,agentworks")
	crew, err := svc.CreateCrewProject(ctx, CreateCrewRequest{UserID: "owner", WorkflowPath: "Workflow/build", Title: "Reports", Purpose: "Run reports", IdempotencyKey: "desc-1"})
	if err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{projectActions: svc}

	long := `{"name":"Too long","kind":"script","description":"` + strings.Repeat("a", maxCrewWorkflowDescription+1) + `"}`
	if w := crewWorkflowRequest(api, ctx, http.MethodPost, crew.CrewID, "", long); w.Code != http.StatusBadRequest {
		t.Fatalf("a description over %d characters: HTTP%d %s, want 400", maxCrewWorkflowDescription, w.Code, w.Body)
	}
	if labels := listedCrewWorkflowLabels(t, api, ctx, crew.CrewID); len(labels) != 0 {
		t.Fatalf("a refused create must not leave a workflow, got %v", labels)
	}

	w := crewWorkflowRequest(api, ctx, http.MethodPost, crew.CrewID, "", `{"name":"Daily report","kind":"script","description":"  Sends the daily sales report.  "}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create with description: HTTP%d %s", w.Code, w.Body)
	}
	list := crewWorkflowRequest(api, ctx, http.MethodGet, crew.CrewID, "", "")
	var response struct {
		Executables []struct {
			Manifest struct {
				Description string `json:"description"`
			} `json:"manifest"`
		} `json:"executables"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &response); err != nil || len(response.Executables) != 1 {
		t.Fatalf("list: %v %s", err, list.Body)
	}
	if got := response.Executables[0].Manifest.Description; got != "Sends the daily sales report." {
		t.Fatalf("description: got %q", got)
	}
}
