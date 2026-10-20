package server

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDashboardErrorPreservesTheUnderlyingRefusal(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	for _, tc := range []struct {
		err    error
		status int
	}{
		{errors.New("Preview browser is unavailable. Reconnect your browser and try again."), http.StatusServiceUnavailable},
		{dashboardFailure(http.StatusForbidden, "This dashboard is outside your project's shared files."), http.StatusForbidden},
	} {
		w := httptest.NewRecorder()
		dashboardError(w, tc.err)
		body := externalTestBody(t, w, tc.status)
		failure := body["error"].(map[string]any)
		if failure["message"] != tc.err.Error() {
			t.Fatalf("refusal was hidden: %v", body)
		}
	}
	w := httptest.NewRecorder()
	dashboardError(w, fmt.Errorf("read %s/Workflow/invoices/report.html: file not found", root))
	failure := externalTestBody(t, w, http.StatusServiceUnavailable)["error"].(map[string]any)
	if failure["message"] != "read Workflow/invoices/report.html: file not found" {
		t.Fatalf("host path leaked: %v", failure)
	}
}
