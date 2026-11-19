package server

import (
	"context"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/projectactions"
)

// #3286: a Crew has no message webhooks any more. A saved one is not loaded, the next save clears it, and only a
// caller binding (internal kind with a caller) validates.
func TestCrewMessageWebhooksAreGone(t *testing.T) {
	_, _ = newScheduleRunWorkspaceStub(t)
	ctx := context.Background()
	root := "Crew/project-1"
	old := projectWebhookDefinition{ID: "4c98bba9-b433-4bf8-b1de-68eebd143a6c", Name: "Deploy", Project: projectactions.Project{Kind: "crew", ID: "project-1"}, Enabled: true, Path: "/api/hooks/4c98bba9-b433-4bf8-b1de-68eebd143a6c", Action: projectactions.Action{Type: "message"}, Input: []byte(`{"messages":["Inspect"]}`), RunAs: "owner", Auth: projectWebhookAuth{Mode: "bearer", EncryptedSecret: "sealed-fixture"}}
	if err := writeProjectWebhookDefinitions(ctx, root, []projectWebhookDefinition{old}); err != nil {
		t.Fatal(err)
	}
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
	if records, err := readProjectWebhookDefinitions(ctx, root); err != nil || len(records) != 0 {
		t.Fatalf("the next save must clear old Crew webhooks: records=%d err=%v", len(records), err)
	}
	if err := validateProductWebhook(productWebhookTrigger{ID: old.ID, Name: "Deploy", Message: "Inspect"}); err == nil {
		t.Fatal("a Crew message webhook must be refused")
	}
	binding := productWebhookTrigger{ID: old.ID, Name: "Report", Message: "Summarise", Kind: triggerKindInternal, Caller: &triggerCaller{Type: triggerCallerWorkflow, ID: "workflow-1"}}
	if err := validateProductWebhook(binding); err != nil {
		t.Fatalf("a caller binding must still validate: %v", err)
	}
}
