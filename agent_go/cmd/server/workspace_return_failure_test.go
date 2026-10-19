package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceStateTemporaryAccessFailureKeepsSavedPreferenceAndCanRetry(t *testing.T) {
	f := newExternalToolsFixture(t)
	t.Setenv("AGENT_PRODUCTS", "agentworks")
	put := func(user, id string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		body := `{"version":1,"revision":0,"last":{"surface":"agentworks","id":"` + id + `"}}`
		f.api.handleWorkspaceReturnState(w, adminRequest(http.MethodPut, "/api/workspace-state", body, &UserClaims{UserID: user}, nil))
		return w
	}
	if w := put("owner", "invoices"); w.Code != http.StatusOK {
		t.Fatalf("initial save: %d %s", w.Code, w.Body)
	}
	statePath := filepath.Join(f.docs, workspaceReturnPath("owner"))
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	oldIndex := workflowManifests
	t.Cleanup(func() { workflowManifests = oldIndex })
	workflowManifests = &workflowManifestIndex{list: func(context.Context) ([]string, map[string]manifestStamp, error) {
		return nil, nil, errors.New("temporary workspace service failure")
	}}
	var unavailableBody string
	for _, id := range []string{"invoices", "not-a-project", "secret"} {
		w := put("owner", id)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("temporary resolver failure became revocation for %s: %d %s", id, w.Code, w.Body)
		}
		if unavailableBody == "" {
			unavailableBody = w.Body.String()
		} else if w.Body.String() != unavailableBody {
			t.Fatal("temporary failure revealed whether a project exists or is visible")
		}
	}
	after, err := os.ReadFile(statePath)
	if err != nil || string(after) != string(before) {
		t.Fatalf("failed access check changed the saved preference: %v", err)
	}
	workflowManifests = oldIndex
	if w := put("owner", "invoices"); w.Code != http.StatusOK {
		t.Fatalf("retry after service recovery failed: %d %s", w.Code, w.Body)
	}
	// Recovery never turns a missing or foreign project into a grant.
	for _, id := range []string{"not-a-project", "secret"} {
		w := put("owner", id)
		if w.Code != http.StatusForbidden || w.Body.String() != "This workspace is no longer available.\n" {
			t.Fatalf("missing/denied project refusal differs for %s: %d %s", id, w.Code, w.Body)
		}
	}
}
