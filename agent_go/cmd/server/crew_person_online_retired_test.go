package server

import (
	"context"
	"strings"
	"testing"
)

// PLAT-746-1024: old clients must not create a new person-slot placement after
// conversion. Only the canonical Put-online source/copy mechanism remains.
func TestCrewPersonSlotPublishCreatorsAreRemoved(t *testing.T) {
	env := newTriggerLinkEnv(t)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	claims := &UserClaims{UserID: "owner"}
	ctx := context.WithValue(t.Context(), UserContextKey, claims)
	for _, action := range []string{"publish_accounts", "publish_agent", "stage_agent"} {
		if _, err := env.api.crewSync(ctx, claims, "alpha", crewSyncRequest{Action: action}); err == nil || !strings.Contains(err.Error(), "unknown sync action") {
			t.Fatalf("old creator %s was not rejected by removed action: %v", action, err)
		}
	}
	rows, err := readCrewAgentPlacements("alpha")
	if err != nil || len(rows) != 0 {
		t.Fatal("retired action created a placement", err)
	}
}
