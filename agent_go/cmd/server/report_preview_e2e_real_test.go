package server

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
)

// Real end-to-end run of preview_report against the local workspace service
// (headless Chromium vian agent-browser) and a workflow that has a report.
//
//	REPORT_PREVIEW_E2E_WORKSPACE=Workflow/social-media \
//	WORKSPACE_API_URL=http://127.0.0.1:18744 AUTH_SECRET=... \
//	WORKSPACE_API_TOKEN=... \  # must match the running workspace service's own token
//	go test ./cmd/server/ -run TestReportPreviewRealE2E -v -count=1
//
// It stands up only the preview routes (page, script, file, query) on a
// loopback httptest server, points the tool at it, and asserts the report
// settled and produced screenshots. Skipped unless the variable is set.
func TestReportPreviewRealE2E(t *testing.T) {
	workspacePath := strings.TrimSpace(os.Getenv("REPORT_PREVIEW_E2E_WORKSPACE"))
	if workspacePath == "" {
		t.Skip("set REPORT_PREVIEW_E2E_WORKSPACE=Workflow/<name> to run")
	}
	if err := ValidateConfiguredAuthSecret(); err != nil {
		t.Skipf("AUTH_SECRET not usable: %v", err)
	}

	api := &StreamingAPI{config: ServerConfig{Host: "127.0.0.1"}}
	router := mux.NewRouter()
	router.Use(AuthMiddleware)
	apiRouter := router.PathPrefix("/api").Subrouter()
	apiRouter.HandleFunc("/workflow/report-preview/file", api.handleReportPreviewFile).Methods("GET")
	apiRouter.HandleFunc("/workflow/report-preview/query", api.handleReportPreviewQuery).Methods("POST")
	router.HandleFunc(reportPreviewPagePath, api.reportPreviewPageHandler).Methods("GET")
	router.HandleFunc(reportPreviewPagePath+"report-preview.js", api.reportPreviewScriptHandler).Methods("GET")

	server := httptest.NewUnstartedServer(router)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server.Listener = listener
	server.Start()
	defer server.Close()
	_, portText, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	api.config.Port = port
	t.Setenv("MCP_API_URL", "http://127.0.0.1:"+portText) // GetCodeExecAPIURL honours it

	// The page and script are reachable without auth.
	if resp, err := http.Get(server.URL + reportPreviewPagePath + "report-preview.js"); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("preview script: %v %v", err, resp)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, err := api.runReportPreview(ctx, "e2e-preview-session", GetDefaultUserID(), workspacePath, map[string]interface{}{"theme": "dark", "width": "desktop"})
	if err != nil {
		t.Fatalf("preview_report: %v", err)
	}
	t.Logf("preview_report result:\n%s", out)

	var result struct {
		State        string   `json:"state"`
		Summary      string   `json:"summary"`
		Title        string   `json:"title"`
		Tabs         []string `json:"tabs"`
		ScriptErrors []string `json:"script_errors"`
		FetchErrors  []string `json:"fetch_errors"`
		Screenshots  []struct {
			Path  string `json:"path"`
			Error string `json:"error"`
		} `json:"screenshots"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.State != "ready" && result.State != "error" {
		t.Fatalf("expected the report to settle, got state %q: %s", result.State, result.Summary)
	}
	if len(result.Screenshots) == 0 {
		t.Fatal("expected at least one screenshot")
	}
	for _, shot := range result.Screenshots {
		if shot.Error != "" {
			t.Fatalf("screenshot %s failed: %s", shot.Path, shot.Error)
		}
	}
}
