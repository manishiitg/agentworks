package server

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #3286: a Crew has no message webhooks any more. A saved one is not loaded; the next save backs it up and removes
// the Crew's webhooks record; only a caller binding (internal kind with a caller) validates.
func TestCrewMessageWebhooksAreGone(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	root := "Crew/project-1"
	old := `{"webhooks":[{"id":"4c98bba9-b433-4bf8-b1de-68eebd143a6c","name":"Deploy","project":{"kind":"crew","id":"project-1"},"enabled":true,"path":"/api/hooks/4c98bba9-b433-4bf8-b1de-68eebd143a6c","action":{"type":"message"},"input":{"messages":["Inspect"]},"run_as":"owner","auth":{"mode":"bearer","encrypted_secret":"sealed-fixture"}}]}`
	mock := &mockWorkspaceAPI{files: map[string]string{root + "/webhooks.json": old}}
	ws := httptest.NewServer(mock)
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	ctx := context.Background()

	manifest := productProjectManifest{Product: "work", ID: "project-1", OwnerID: "owner"}
	if err := loadCrewEntryActions(ctx, root, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Triggers) != 0 {
		t.Fatalf("an old Crew message webhook was loaded: %+v", manifest.Triggers)
	}
	if err := saveCrewEntryActions(ctx, root, manifest); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := readFileFromWorkspace(ctx, root+"/webhooks.json"); found {
		t.Fatal("the next save must remove the Crew's old webhooks record")
	}
	db, err := serverStateDatabasePath()
	if err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(filepath.Join(filepath.Dir(db), "automation-records.imported.jsonl"))
	if err != nil || !strings.Contains(string(backup), "4c98bba9-b433-4bf8-b1de-68eebd143a6c") {
		t.Fatalf("the removed webhooks were not backed up first: %v", err)
	}

	if err := validateProductWebhook(productWebhookTrigger{ID: "4c98bba9-b433-4bf8-b1de-68eebd143a6c", Name: "Deploy", Message: "Inspect"}); err == nil {
		t.Fatal("a Crew message webhook must be refused")
	}
	binding := productWebhookTrigger{ID: "4c98bba9-b433-4bf8-b1de-68eebd143a6c", Name: "Report", Message: "Summarise", Kind: triggerKindInternal, Caller: &triggerCaller{Type: triggerCallerWorkflow, ID: "workflow-1"}}
	if err := validateProductWebhook(binding); err != nil {
		t.Fatalf("a caller binding must still validate: %v", err)
	}
}
