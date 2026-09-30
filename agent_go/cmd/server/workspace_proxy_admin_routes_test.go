package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Routes that kill server processes are admin-only through the proxy: in multi-user mode an
// ordinary account gets a 403, and on a single-user machine (everyone is an admin) they work.
func TestWorkspaceProxyProcessKillRoutesAreAdminOnly(t *testing.T) {
	kill := func() *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/api/wp/api/browser/cleanup", strings.NewReader(`{"pids":[1]}`))
		req.Header.Set("Content-Type", "application/json")
		return req
	}
	sweep := func() *http.Request {
		return httptest.NewRequest(http.MethodPost, "/api/wp/api/processes/cleanup", strings.NewReader(""))
	}

	t.Setenv("MULTI_USER_MODE", "true")
	for name, req := range map[string]*http.Request{"browser cleanup": kill(), "process sweep": sweep()} {
		status, detail, cleanup := workspaceProxyCrossUserBlock(req, "ordinary-user")
		if cleanup != nil {
			t.Cleanup(cleanup)
		}
		if status != http.StatusForbidden || detail != "admin-only route" {
			t.Fatalf("%s for an ordinary account: status %d %q, want 403 admin-only route", name, status, detail)
		}
	}

	t.Setenv("MULTI_USER_MODE", "false")
	status, detail, cleanup := workspaceProxyCrossUserBlock(kill(), "local-user")
	if cleanup != nil {
		t.Cleanup(cleanup)
	}
	if status != 0 {
		t.Fatalf("single-user machine: blocked (%d %s)", status, detail)
	}
}
