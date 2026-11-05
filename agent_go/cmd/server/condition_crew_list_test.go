package server

import (
	"context"
	"net/http/httptest"
	"testing"
)

func TestConditionCrewListResolvesRuntimePolicy(t *testing.T) {
	// Use today's registered private records and real workspace handlers; the
	// old mock kept product.json in the public files tree.
	f, _ := newExternalCrewCommandFixture(t)
	if err := defaultProjectOwners().SetPrivate("work", "test-team", true); err != nil {
		t.Fatal(err)
	}
	svc := NewProjectActionDispatcher(f.api, f.api.agentProfiles)
	root := externalCommandCrewRoot
	ctx := internalBotRequestContext(context.Background(), "owner")
	service := svc.conditionService()
	result, err := service.manage(ctx, "owner", "work", "team", map[string]interface{}{"action": "list"})
	if err != nil {
		t.Fatal(err)
	}
	listed := result.(map[string]interface{})
	if _, ok := listed["chats"]; !ok {
		t.Fatal("Crew watcher list lost its available chats")
	}
	if len(listed["triggers"].([]interface{})) != 0 {
		t.Fatal("listing created a watcher")
	}
	if _, err := service.manage(ctx, "other", "work", "team", map[string]interface{}{"action": "list"}); err == nil {
		t.Fatal("private Crew admitted another account")
	}
	// PLAT-856-542 / PR1126: the aggregated reader must call list, not
	// watcher history via an unrelated profile route variable named id.
	f.api.projectActions = svc
	request := httptest.NewRequest("GET", "/api/crews/team/panel", nil).WithContext(ctx)
	checked := false
	for _, source := range f.api.crewPanelSources(request, "team", root, "", crewProjectBinding{OwnedByCaller: true}) {
		if source.id == "watchers" {
			checked = true
			if _, err := source.read(ctx); err != nil {
				t.Fatalf("panel watcher list: %v", err)
			}
		}
	}
	if !checked {
		t.Fatal("missing watcher section")
	}

}
