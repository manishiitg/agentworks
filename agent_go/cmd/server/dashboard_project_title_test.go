package server

import (
	"context"
	"encoding/json"
	"testing"
)

func TestDashboardCrewUsesIdentityDisplayTitle(t *testing.T) {
	env, _ := projectMCPCrewFixture(t)
	env.mock.mu.Lock()
	var manifest map[string]interface{}
	if err := json.Unmarshal([]byte(env.mock.files[projectMCPAlphaRoot+"/product.json"]), &manifest); err != nil {
		t.Fatal(err)
	}
	manifest["identity"] = map[string]interface{}{"name": "Team assistant"}
	raw, _ := json.Marshal(manifest)
	env.mock.files[projectMCPAlphaRoot+"/product.json"] = string(raw)
	env.mock.mu.Unlock()
	target, err := env.api.dashboardTarget(context.Background(), &UserClaims{UserID: "owner"}, projectMCPAlphaRoot)
	if err != nil || target.Title != "Team assistant" || target.ID != "alpha" {
		t.Fatalf("display title changed identity or failed: %+v, %v", target, err)
	}
}
