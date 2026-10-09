package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/mux"
)

func TestReportPreviewAllowedPath(t *testing.T) {
	t.Parallel()
	for input, want := range map[string]string{
		"db/reports/index.html":                    "db/reports/index.html",
		"/db/assets/logo.png":                      "db/assets/logo.png",
		"knowledgebase/notes/a.md":                 "knowledgebase/notes/a.md",
		"soul.md":                                  "soul.md",
		"db/../workflow.json":                      "", // reject ambiguous traversal
		"db/db.sqlite":                             "",
		"db/reports/.dashboard-state/a/state.json": "",
		"runs/iteration-1/out.txt":                 "",
		"../other/db/reports/x.html":               "",
		"db/../../etc/passwd":                      "",
		"":                                         "",
	} {
		got, ok := reportPreviewAllowedPath(input)
		if got != want || ok != (want != "") {
			t.Fatalf("reportPreviewAllowedPath(%q) = %q,%v want %q", input, got, ok, want)
		}
	}
}

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

func TestReportPreviewChoices(t *testing.T) {
	t.Parallel()
	both := []string{"dark", "light"}
	if got := reportPreviewChoices(nil, both); len(got) != 2 {
		t.Fatalf("nil -> both, got %v", got)
	}
	if got := reportPreviewChoices("light", both); len(got) != 1 || got[0] != "light" {
		t.Fatalf("light -> [light], got %v", got)
	}
	if got := reportPreviewChoices("sepia", both); len(got) != 2 {
		t.Fatalf("unknown -> both, got %v", got)
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

func TestReportPreviewWidthsDefaultTabletFirstAndPreserveLegacyBoth(t *testing.T) {
	t.Parallel()
	if got := reportPreviewWidthChoices(nil); !reflect.DeepEqual(got, []string{"tablet", "mobile", "desktop"}) {
		t.Fatalf("default widths = %v", got)
	}
	if got := reportPreviewWidthChoices("tablet"); !reflect.DeepEqual(got, []string{"tablet"}) {
		t.Fatalf("tablet width = %v", got)
	}
	if got := reportPreviewWidthChoices("both"); !reflect.DeepEqual(got, []string{"desktop", "mobile"}) {
		t.Fatalf("legacy both widths = %v", got)
	}
	if got := reportPreviewWidthChoices("unknown"); !reflect.DeepEqual(got, []string{"tablet", "mobile", "desktop"}) {
		t.Fatalf("unknown widths = %v", got)
	}
	if reportPreviewWidthPixels("tablet") != 768 || reportPreviewWidthPixels("mobile") != 480 || reportPreviewWidthPixels("desktop") != 1280 {
		t.Fatal("preview width pixel contract changed")
	}
}

func TestReportPreviewScreenshotPathsStayInsideTheWorkflow(t *testing.T) {
	t.Parallel()
	destination, reported := reportPreviewScreenshotPaths("Workflow/sales", "db/reports/index.html", "dark", "desktop")
	if destination != "Workflow/sales/db/reports/preview/dark-desktop.png" {
		t.Fatalf("destination = %q", destination)
	}
	if reported != "db/reports/preview/dark-desktop.png" {
		t.Fatalf("reported = %q", reported)
	}
	destination, reported = reportPreviewScreenshotPaths("Workflow/sales", "db/reports/finance/q1.html", "light", "tablet")
	if destination != "Workflow/sales/db/reports/preview/finance-q1/light-tablet.png" {
		t.Fatalf("document destination = %q", destination)
	}
	if reported != "db/reports/preview/finance-q1/light-tablet.png" {
		t.Fatalf("document reported = %q", reported)
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

func TestUnquoteBrowserEvalOutput(t *testing.T) {
	t.Parallel()
	if got := unquoteBrowserEvalOutput(`"ready"`); got != "ready" {
		t.Fatalf("quoted string: %q", got)
	}
	if got := unquoteBrowserEvalOutput(`"{\"a\":1}"`); got != `{"a":1}` {
		t.Fatalf("double-encoded JSON: %q", got)
	}
	if got := unquoteBrowserEvalOutput(`{"a":1}`); got != `{"a":1}` {
		t.Fatalf("plain JSON: %q", got)
	}
}

func TestParseBrowserEvalOutputUnwrapsStructuredResult(t *testing.T) {
	t.Parallel()

	got, err := parseBrowserEvalOutput(`{"success":true,"data":{"result":"failed"},"error":null}`)
	if err != nil || got != "failed" {
		t.Fatalf("structured state = %q, %v; want failed", got, err)
	}

	got, err = parseBrowserEvalOutput(`{"success":true,"data":{"result":"{\"previewState\":\"failed\"}"},"error":null}`)
	if err != nil || got != `{"previewState":"failed"}` {
		t.Fatalf("structured snapshot = %q, %v", got, err)
	}
}

func TestParseBrowserEvalOutputRejectsFailedEnvelope(t *testing.T) {
	t.Parallel()
	if _, err := parseBrowserEvalOutput(`{"success":false,"data":{},"error":"page crashed"}`); err == nil || !strings.Contains(err.Error(), "page crashed") {
		t.Fatalf("expected browser failure, got %v", err)
	}
}

func TestReportPreviewSummaryNeverCallsUnknownStateClean(t *testing.T) {
	t.Parallel()
	snapshot := reportPreviewSnapshot{PreviewState: `{"success":true,"data":{"result":"failed"}}`}
	got := reportPreviewSummary(snapshot, nil, "db/reports/index.html")
	if strings.Contains(got, "Rendered cleanly") || !strings.Contains(got, "not verified") {
		t.Fatalf("unknown state summary = %q", got)
	}
}

func TestReportPreviewSummaryFlagsUnrenderedMarkdown(t *testing.T) {
	t.Parallel()
	snapshot := reportPreviewSnapshot{PreviewState: "ready"}
	snapshot.Report.MarkdownTexts = []string{"Placed **3** bids today"}
	got := reportPreviewSummary(snapshot, nil, "db/reports/index.html")
	if strings.Contains(got, "Rendered cleanly") || !strings.Contains(got, "unrendered markdown") {
		t.Fatalf("markdown summary = %q", got)
	}
}
