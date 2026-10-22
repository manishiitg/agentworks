package server

import (
	"context"
	"testing"
)

func TestConditionCrewListResolvesRuntimePolicy(t *testing.T) {
	env, _ := projectMCPCrewFixture(t)
	env.svc.registry = env.api.agentProfiles
	ctx := internalBotRequestContext(context.Background(), "owner")
	service := env.svc.conditionService()
	result, err := service.manage(ctx, "owner", "work", "alpha", map[string]interface{}{"action": "list"})
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
	if _, err := service.manage(ctx, "other", "work", "alpha", map[string]interface{}{"action": "list"}); err == nil {
		t.Fatal("private Crew admitted another account")
	}
}
