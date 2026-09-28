package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// The ZIP workspace backup endpoints (POST /api/workspace/export and
// POST /api/workspace/import) were removed. Pin the removal so a
// reintroduction fails loudly instead of silently restoring the
// upload/download surface.
func TestWorkspaceBackupRoutesRemoved(t *testing.T) {
	t.Setenv("WORKSPACE_API_TOKEN", "")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	registerAPIRoutes(r)
	for _, target := range []string{"/api/workspace/export", "/api/workspace/import"} {
		req := httptest.NewRequest(http.MethodPost, target, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("POST %s status = %d, want 404 (route must stay removed)", target, rec.Code)
		}
	}
}

func TestRemoteWorkflowTransfersUnavailableOnLaptop(t *testing.T) {
	t.Setenv(serverUserTokensEnv, "")
	t.Setenv(workspaceAPITokenEnv, "")
	t.Setenv(remotePlacementFileEnv, t.TempDir()+"/placements.json")
	gin.SetMode(gin.TestMode)
	r := newWorkspaceEngine(t.TempDir())
	for _, target := range []string{"/api/remote/workflow/export", "/api/remote/workflow/import"} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, target, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("POST %s status = %d, want 404", target, rec.Code)
		}
	}
}
