package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productschedule"
)

func TestMutateProjectRuntimeManifestSerializesConcurrentWriters(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	root := registerTestCrew(t, "manifest-concurrency", "owner", true)
	manifestFile := root + "/workflow.json"
	mock := &mockWorkspaceAPI{files: map[string]string{
		root + "/product.json": `{"schema_version":1,"product":"work","id":"manifest-concurrency","title":"Concurrency"}`,
		manifestFile: `{
  "schema_version": 1,
  "id": "manifest-concurrency",
  "label": "Concurrency",
  "capabilities": {
    "github_connection_id": "33333333333333333333333333333333",
    "selected_servers": [],
    "selected_skills": [],
    "selected_global_secret_names": []
  }
}`,
	}}
	ws := httptest.NewServer(mock)
	t.Cleanup(ws.Close)
	t.Setenv("WORKSPACE_API_URL", ws.URL)

	ctx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "owner", Username: "owner"})
	var wg sync.WaitGroup
	errs := make(chan error, 20)

	run := func(fn func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 4; i++ {
				if err := fn(); err != nil {
					errs <- err
					return
				}
			}
		}()
	}

	run(func() error {
		return updateProductSelectedSkills(ctx, crewProfileID, root, func(cur []string) []string {
			return appendUniqueStrings(cur, "skill-alpha")
		})
	})
	run(func() error {
		return updateProductSelectedGlobalSecrets(ctx, crewProfileID, root, func(cur []string) []string {
			return appendUniqueStrings(cur, "VLT_demo__API_KEY")
		})
	})
	run(func() error {
		return updateProductSelectedServers(ctx, crewProfileID, root, func(cur []string) []string {
			return appendUniqueStrings(cur, "vault_c-demo")
		}, true)
	})
	run(func() error {
		_, err := setRemoteProjectLink(ctx, root, remoteProjectLink{ProjectID: "remote-1", Name: "Remote", Server: "srv-1"})
		return err
	})

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent manifest mutation failed: %v", err)
	}

	mock.mu.Lock()
	raw := mock.files[manifestFile]
	mock.mu.Unlock()

	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("unmarshal workflow.json: %v\n%s", err, raw)
	}
	links, _ := doc["linked_remote_projects"].([]any)
	if len(links) != 1 {
		t.Fatalf("linked_remote_projects lost in concurrent writes: %s", raw)
	}
	caps, _ := doc["capabilities"].(map[string]any)
	if caps["github_connection_id"] != "33333333333333333333333333333333" {
		t.Fatalf("github_connection_id lost: %v", caps["github_connection_id"])
	}
	if caps["chosen_vault_apps_only"] != true {
		t.Fatalf("chosen_vault_apps_only lost: %v", caps["chosen_vault_apps_only"])
	}
	for field, want := range map[string]string{
		"selected_skills":              "skill-alpha",
		"selected_global_secret_names": "VLT_demo__API_KEY",
		"selected_servers":             "vault_c-demo",
	} {
		items, _ := caps[field].([]any)
		if len(items) != 1 || items[0] != want {
			t.Fatalf("%s = %v, want [%s]", field, items, want)
		}
	}
}

func TestProjectScheduleUpdateDoesNotRewriteCleanWorkflowManifest(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	env := newTriggerLinkEnv(t)
	ctx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "owner", Username: "owner"})

	manifestFile := linkBetaPath + "/workflow.json"
	initial := `{
  "schema_version": 1,
  "id": "beta",
  "label": "Beta",
  "updated_at": "2026-11-26T00:00:00Z",
  "capabilities": {
    "github_connection_id": "44444444444444444444444444444444"
  }
}`
	env.mock.mu.Lock()
	env.mock.files[manifestFile] = initial
	env.mock.mu.Unlock()

	var workflowWrites atomic.Int32
	origWrite := env.svc.writeFile
	env.svc.writeFile = func(ctx context.Context, path, content string) error {
		if path == manifestFile {
			workflowWrites.Add(1)
		}
		return origWrite(ctx, path, content)
	}

	created, err := env.svc.CreateProjectSchedule(ctx, "owner", "work", "beta", productschedule.Schedule{
		ID: "hourly", Name: "Hourly check", Enabled: true, CronExpression: "0 * * * *", Timezone: "UTC", Messages: []string{"Check status."},
	})
	if err != nil {
		t.Fatalf("CreateProjectSchedule failed: %v", err)
	}
	if _, err := env.svc.UpdateProjectSchedule(ctx, "owner", created.ID(), func(s *productschedule.Schedule) {
		s.Enabled = false
	}); err != nil {
		t.Fatalf("UpdateProjectSchedule failed: %v", err)
	}
	if err := env.svc.DeleteProjectSchedule(ctx, "owner", created.ID()); err != nil {
		t.Fatalf("DeleteProjectSchedule failed: %v", err)
	}

	if got := workflowWrites.Load(); got != 0 {
		t.Fatalf("clean workflow.json was rewritten %d time(s) during schedule updates, want 0", got)
	}
	env.mock.mu.Lock()
	after := env.mock.files[manifestFile]
	env.mock.mu.Unlock()
	if after != initial {
		t.Fatalf("clean workflow.json changed:\ngot:\n%s\nwant:\n%s", after, initial)
	}
}

func TestCrewAttachmentPreservesUnmodelledManifestFields(t *testing.T) {
	tools, mock, _, _ := newCrewBuilderToolsTestEnv(t)
	var doc map[string]any
	if err := json.Unmarshal([]byte(mock.files[manifestPath("Workflow/test")]), &doc); err != nil {
		t.Fatal(err)
	}
	doc["custom_subsystem_meta"] = map[string]any{"kept": true}
	caps, _ := doc["capabilities"].(map[string]any)
	if caps == nil {
		caps = map[string]any{}
		doc["capabilities"] = caps
	}
	caps["github_connection_id"] = "55555555555555555555555555555555"
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	mock.files[manifestPath("Workflow/test")] = string(raw)

	tool := tools["manage_crew_attachment"]
	if _, err := tool.exec(context.Background(), map[string]interface{}{
		"action": "attach", "alias": "project-a", "crew_project_id": "project-a", "crew_profile_id": "crewx",
	}); err != nil {
		t.Fatalf("attach failed: %v", err)
	}

	var updated map[string]any
	if err := json.Unmarshal([]byte(mock.files[manifestPath("Workflow/test")]), &updated); err != nil {
		t.Fatal(err)
	}
	if _, ok := updated["custom_subsystem_meta"]; !ok {
		t.Fatalf("unmodelled top-level field lost after crew attach: %s", mock.files[manifestPath("Workflow/test")])
	}
	updatedCaps, _ := updated["capabilities"].(map[string]any)
	if updatedCaps["github_connection_id"] != "55555555555555555555555555555555" {
		t.Fatalf("github_connection_id = %v, want preserved", updatedCaps["github_connection_id"])
	}
}
