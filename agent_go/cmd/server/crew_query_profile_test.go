package server

import (
	"context"
	"strings"
	"testing"
)

// A caller cannot omit the Work profile and enter a foreign Crew through the
// generic workflow path, which has a different tool surface.
func TestCrewQueryTargetRejectsProfileBypass(t *testing.T) {
	newCrewRunModeFixture(t)
	claims := &UserClaims{UserID: "reader", Username: "reader"}
	ctx := context.WithValue(context.Background(), UserContextKey, claims)
	for _, mode := range []string{"workflow", "workflow_phase"} {
		req := QueryRequest{Query: "Read this Crew", AgentMode: mode, PhaseID: "workflow-builder", SelectedFolder: crewRunModeOwnerRoot}
		_, access, err := (&StreamingAPI{}).admitQueryTarget(ctx, &req, "reader", "profile-bypass-test")
		if err == nil || access != WorkflowAccessNone || !strings.Contains(err.Error(), "verified Work conversation") {
			t.Fatalf("%s bypassed the verified Crew binding: access=%v error=%v", mode, access, err)
		}
	}
	// Other profiles cannot obtain a foreign root from normal workspace
	// validation either; only the Work resolver has the verified exception.
	if _, err := cleanAgentProfileWorkspace(crewRunModeOwnerRoot, "reader"); err == nil {
		t.Fatal("another profile admitted a foreign Crew root")
	}
}
