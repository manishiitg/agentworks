package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
)

func withProjectSharing(t *testing.T, on bool) {
	t.Helper()
	prior := projectSharingEnabled
	projectSharingEnabled = func() bool { return on }
	t.Cleanup(func() { projectSharingEnabled = prior })
}

// With project sharing off (AGENTWORKS_PROJECT_SHARING=off), a project is private to its owner: another user can neither
// resolve it nor see it in the shared-project list, while the owner is unaffected.
func TestProjectsArePrivateWhenSharingIsOff(t *testing.T) {
	fx := newCrewRunModeFixture(t)
	withProjectSharing(t, false)
	ctx := context.Background()

	if owned, err := resolveCrewProjectBinding(ctx, "owner", fx.profile, "crew-aaa", crewRunModeOwnerRoot); err != nil || !owned.OwnedByCaller {
		t.Fatalf("the owner lost their own project: %+v err=%v", owned, err)
	}
	if got, err := resolveCrewProjectBinding(ctx, "reader", fx.profile, "crew-aaa", crewRunModeOwnerRoot); err == nil {
		t.Fatalf("a non-owner resolved the owner's project with a folder hint: %+v", got)
	}
	if got, err := resolveCrewProjectBinding(ctx, "reader", fx.profile, "crew-aaa", ""); err == nil {
		t.Fatalf("a non-owner found the owner's project by scanning: %+v", got)
	}

	req := mux.SetURLVars(profileRouteRequest(http.MethodGet, "/api/agent-profiles/work/shared-projects", nil, "reader"), map[string]string{"id": "work"})
	rec := httptest.NewRecorder()
	fx.api.handleListSharedProjects(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d: %s", rec.Code, rec.Body.String())
	}
	var decoded struct {
		Projects []sharedProjectSummary `json:"projects"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil || len(decoded.Projects) != 0 {
		t.Fatalf("shared projects with sharing off = %+v (%v)", decoded.Projects, err)
	}
}
