package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/mux"
)

func TestScopeAllowsPath(t *testing.T) {
	t.Parallel()
	if !scopeAllowsPath("", "/api/anything") {
		t.Fatal("an unscoped token must reach every path")
	}
	if !scopeAllowsPath(reportPreviewScope, "/api/workflow/report-preview/query") {
		t.Fatal("preview scope must reach its own endpoints")
	}
	for _, denied := range []string{"/api/query", "/api/sessions/x/events", "/api/workflow/report-previews", "/api/auth/me"} {
		if scopeAllowsPath(reportPreviewScope, denied) {
			t.Fatalf("preview scope must not reach %s", denied)
		}
	}
	if scopeAllowsPath("something-else", "/api/workflow/report-preview/file") {
		t.Fatal("an unknown scope reaches nothing")
	}
}

// A preview token is admitted only to the preview endpoints, carries the
// workspace it was minted for, and expires.
func TestReportPreviewTokenIsScopedByTheMiddleware(t *testing.T) {
	t.Setenv("AUTH_SECRET", "report-preview-test-secret-0123456789abcdef")
	token, err := mintReportPreviewToken(&UserClaims{UserID: "u1", Username: "u1"}, "/Workflow/demo/")
	if err != nil {
		t.Fatal(err)
	}
	parsed := &UserClaims{}
	if _, err := jwt.ParseWithClaims(token, parsed, func(*jwt.Token) (interface{}, error) { return GetAuthSecret(), nil }); err != nil {
		t.Fatal(err)
	}
	if parsed.Scope != reportPreviewScope || parsed.ScopeWorkspace != "Workflow/demo" || parsed.UserID != "u1" {
		t.Fatalf("unexpected claims: %+v", parsed)
	}
	if parsed.ExpiresAt == nil || parsed.ExpiresAt.Sub(parsed.IssuedAt.Time) > reportPreviewTokenTTL {
		t.Fatalf("expected a %s expiry, got %v", reportPreviewTokenTTL, parsed.ExpiresAt)
	}

	router := mux.NewRouter()
	router.Use(AuthMiddleware)
	reached := ""
	router.HandleFunc("/api/workflow/report-preview/file", func(w http.ResponseWriter, r *http.Request) {
		reached = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	})
	router.HandleFunc("/api/sessions", func(w http.ResponseWriter, r *http.Request) {
		reached = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	})

	call := func(path string) int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		router.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := call("/api/workflow/report-preview/file?workspace=Workflow/demo&path=db/reports/index.html"); code != http.StatusNoContent || reached == "" {
		t.Fatalf("preview endpoint with preview token: %d", code)
	}
	reached = ""
	if code := call("/api/sessions"); code != http.StatusForbidden || reached != "" {
		t.Fatalf("other endpoint with preview token must be 403, got %d (reached=%q)", code, reached)
	}
	// The query-parameter form (what <img src> uses) works too.
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/workflow/report-preview/file?token="+token, nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("token as query parameter: %d", rec.Code)
	}
}

func TestReportPreviewWorkspaceBinding(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	bound := &UserClaims{Scope: reportPreviewScope, ScopeWorkspace: "Workflow/demo"}
	if _, err := reportPreviewWorkspace(req, bound, "Workflow/other"); err == nil {
		t.Fatal("a token bound to one workflow must not read another")
	}
	if got, err := reportPreviewWorkspace(req, bound, "/Workflow/demo/"); err != nil || got != "Workflow/demo" {
		t.Fatalf("bound workspace: %q %v", got, err)
	}
	if _, err := reportPreviewWorkspace(req, &UserClaims{}, ""); err == nil {
		t.Fatal("workspace is required")
	}
}

func TestCleanReportDocumentPath(t *testing.T) {
	t.Parallel()
	for input, want := range map[interface{}]string{
		nil:                         "db/reports/index.html",
		"":                          "db/reports/index.html",
		"db/reports/tasks.html":     "db/reports/tasks.html",
		"db/reports/finance/q.html": "db/reports/finance/q.html",
	} {
		got, err := cleanReportDocumentPath(input)
		if err != nil || got != want {
			t.Fatalf("cleanReportDocumentPath(%v) = %q, %v; want %q", input, got, err, want)
		}
	}
	for _, input := range []string{"reports/x.html", "db/reports/../x.html", "db/reports/x.json", "/db/reports/x.html"} {
		if _, err := cleanReportDocumentPath(input); err == nil {
			t.Fatalf("expected %q to be rejected", input)
		}
	}
}

func TestReportPreviewScriptHandlerServesTheBuiltBundle(t *testing.T) {
	// Production serves browser assets from current/frontend, not cwd/static.
	// STATIC_DIR is the shared contract used by the SPA and preview handler.
	dir := t.TempDir()
	staticDir := filepath.Join(dir, "frontend")
	if err := os.MkdirAll(staticDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STATIC_DIR", staticDir)

	api := &StreamingAPI{}
	rec := httptest.NewRecorder()
	api.reportPreviewScriptHandler(rec, httptest.NewRequest(http.MethodGet, "/report-preview/report-preview.js", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 before the bundle is built, got %d", rec.Code)
	}
	page := httptest.NewRecorder()
	api.reportPreviewPageHandler(page, httptest.NewRequest(http.MethodGet, "/report-preview/", nil))
	if page.Code != http.StatusServiceUnavailable || !strings.Contains(page.Body.String(), `data-preview-state="failed"`) || !strings.Contains(page.Body.String(), "window.__reportPreview") {
		t.Fatalf("missing runtime must fail explicitly before polling, got %d: %s", page.Code, page.Body.String())
	}

	if err := os.WriteFile(filepath.Join(staticDir, "report-preview.js"), []byte("console.log('preview')"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	api.reportPreviewScriptHandler(rec, httptest.NewRequest(http.MethodGet, "/report-preview/report-preview.js", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "console.log") {
		t.Fatalf("expected the built bundle to be served, got %d body=%q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Fatalf("expected a javascript content type, got %q", ct)
	}

	rec = httptest.NewRecorder()
	api.reportPreviewPageHandler(rec, httptest.NewRequest(http.MethodGet, "/report-preview/", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "/report-preview/report-preview.js") {
		t.Fatalf("expected the page to reference the script, got %d body=%q", rec.Code, rec.Body.String())
	}
}

func TestTSXDashboardAssetsUseThePackagedFrontend(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("STATIC_DIR", dir)
	api := &StreamingAPI{}
	for _, name := range []string{"dashboard-react.js", "dashboard.css"} {
		body := "/* trusted renderer " + name + " */"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		api.dashboardRuntimeAssetHandler(rec, httptest.NewRequest(http.MethodGet, "/"+name, nil))
		if rec.Code != http.StatusOK || rec.Body.String() != body {
			t.Fatalf("asset %s: status=%d body=%q", name, rec.Code, rec.Body.String())
		}
	}
	files, err := dashboards.CompileTSX("Example", map[string]string{"index.tsx": `export default function Dashboard(){return <h1>Example</h1>}`})
	if err != nil {
		t.Fatal(err)
	}
	page := files["index.html"]
	if !strings.Contains(page, `src="/dashboard-react.js"`) || !strings.Contains(page, `href="/dashboard.css"`) || !strings.Contains(page, "Dashboard renderer unavailable") {
		t.Fatal("missing runtime assets or error state")
	}
}

func TestParseBrowserEvalOutputRejectsFailedEnvelope(t *testing.T) {
	t.Parallel()
	if _, err := parseBrowserEvalOutput(`{"success":false,"data":{},"error":"page crashed"}`); err == nil || !strings.Contains(err.Error(), "page crashed") {
		t.Fatalf("expected browser failure, got %v", err)
	}
}
