package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestWorkspaceExecutionToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv(workspaceAPITokenEnv, "server-only-token")
	router := gin.New()
	router.POST("/api/execute", requireWorkspaceAPIToken(), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	unauthorized := httptest.NewRequest(http.MethodPost, "/api/execute", nil)
	unauthorizedRecorder := httptest.NewRecorder()
	router.ServeHTTP(unauthorizedRecorder, unauthorized)
	if unauthorizedRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorizedRecorder.Code)
	}

	authorized := httptest.NewRequest(http.MethodPost, "/api/execute", nil)
	authorized.Header.Set("X-Workspace-Token", "server-only-token")
	authorizedRecorder := httptest.NewRecorder()
	router.ServeHTTP(authorizedRecorder, authorized)
	if authorizedRecorder.Code != http.StatusNoContent {
		t.Fatalf("authorized status = %d", authorizedRecorder.Code)
	}
}

func TestSharedAssetsRequireConfiguredServiceToken(t *testing.T) {
	router := gin.New()
	called := false
	router.POST("/api/shared-assets", requireConfiguredWorkspaceAPIToken(), func(c *gin.Context) { called = true; c.Status(200) })
	for _, tc := range []struct {
		configured, supplied string
		status               int
	}{{"", "", 503}, {"secret", "", 401}, {"secret", "wrong", 401}, {"secret", "secret", 200}} {
		t.Setenv(workspaceAPITokenEnv, tc.configured)
		req := httptest.NewRequest("POST", "/api/shared-assets", nil)
		req.Header.Set("X-Workspace-Token", tc.supplied)
		rec := httptest.NewRecorder()
		called = false
		router.ServeHTTP(rec, req)
		if rec.Code != tc.status || called != (tc.status == 200) {
			t.Fatalf("status=%d called=%v, want %d", rec.Code, called, tc.status)
		}
	}
}

// Every /api route needs the token when one is configured: the service
// trusts X-User-ID, so a caller without the token (a coding-CLI shell, any
// local process) must never read or write documents through it.
func TestEveryAPIRouteRequiresConfiguredToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv(workspaceAPITokenEnv, "server-only-token")
	router := gin.New()
	registerAPIRoutes(router)
	for _, route := range router.Routes() {
		path := strings.NewReplacer("*filepath", "x", "*folderpath", "x", ":session", "s").Replace(route.Path)
		req := httptest.NewRequest(route.Method, path, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s without token = %d, want 401", route.Method, route.Path, rec.Code)
		}
	}
	if len(router.Routes()) < 20 {
		t.Fatalf("only %d routes registered", len(router.Routes()))
	}
}
