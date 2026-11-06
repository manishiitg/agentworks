package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
)

// PLAT-856-509 (owner, 6 Nov; reviewer's header list): a public link serves a frozen copy to anyone with no claims, only
// the Crew's main owner controls it, Off stops it at once, Update keeps the code, and a secret value never goes public.
// Everything runs through the real handlers; only the headless browser's render is replaced.
func TestDashboardPublicLinkFrozenCopy(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","role":"editor","products":["work"]},{"id":"member","role":"editor","products":["work"]},{"id":"outsider","role":"editor","products":["work"]}]}`)
	if err := writeCrewTeam(crewTeam{ProjectID: "team", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "member"}}}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PUBLIC_URL", "https://app.example.com")
	static := t.TempDir()
	t.Setenv("STATIC_DIR", static)
	for _, name := range []string{"report-preview.js", "dashboard-react.js", "dashboard.css"} {
		if err := os.WriteFile(filepath.Join(static, name), []byte("/* runtime */"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const secretValue = "test-only-secret-value"
	withVaultSecretGrant(t, "owner", "CREW_TOKEN")
	t.Setenv("MULTI_USER_MODE", "true")
	managedGlobalsMu.Lock()
	oldGlobals, oldManaged := globalSecrets, managedGlobals
	globalSecrets, managedGlobals = nil, map[string]string{"CREW_TOKEN": secretValue, "NOT_GRANTED": "not-for-this-owner"}
	managedGlobalsMu.Unlock()
	t.Cleanup(func() {
		managedGlobalsMu.Lock()
		globalSecrets, managedGlobals = oldGlobals, oldManaged
		managedGlobalsMu.Unlock()
	})
	f.write(t, externalCommandCrewState+"/workflow.json", `{"id":"team","label":"Test Team","created_by":"owner","capabilities":{"selected_global_secret_names":["CREW_TOKEN"]}}`)

	owner := &UserClaims{UserID: "owner", Username: "owner"}
	ctx := context.WithValue(context.Background(), UserContextKey, owner)
	created, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: externalCommandCrewRoot, Action: "create", ID: "sales", Title: "Sales this week", IncludeDraft: true, Files: map[string]string{"index.html": "<p>Sales</p>"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: externalCommandCrewRoot, Action: "publish", ID: "sales", IncludeDraft: true, ExpectedRevision: created.Dashboard.Revision}); err != nil {
		t.Fatal(err)
	}

	shown := "total-42"
	realCapture := dashboardStaticBrowserCapture
	dashboardStaticBrowserCapture = func(_ *StreamingAPI, _ context.Context, _ *UserClaims, _ dashboardTarget, _ dashboardExportOptions, page dashboardStaticPage) (string, error) {
		if page.LiveLink || !strings.Contains(page.Footer, "Made with AgentWorks") {
			t.Errorf("public capture options: %+v", page)
		}
		return `<!doctype html><html><body><script id="agentworks-static-snapshot" type="application/json">{"data":"` + shown + `"}</script><script src="./report-preview.js"></script></body></html>`, nil
	}
	t.Cleanup(func() { dashboardStaticBrowserCapture = realCapture })

	change := func(user, mode string) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"dashboard_id": "sales", "mode": mode})
		w := httptest.NewRecorder()
		f.api.handleCrewTeamDashboardPublicLink(w, adminRequest(http.MethodPost, "/api/crew-teams/team/dashboards/public-link", string(body), &UserClaims{UserID: user, Username: user}, map[string]string{"project": "team"}))
		return w
	}
	direct := func(user string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		f.api.handleDashboards(w, adminRequest(http.MethodPost, "/api/dashboards", `{"workspace":"`+externalCommandCrewRoot+`","action":"public_link","mode":"on","dashboard_id":"sales"}`, &UserClaims{UserID: user, Username: user}, nil))
		return w
	}
	// The public route, behind the real auth middleware, with no credential of any kind.
	public := mux.NewRouter()
	public.Use(AuthMiddleware)
	f.api.registerDashboardPublicRoutes(public)
	open := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		public.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}

	// A Team member builds in this Crew, so this refusal is the main-owner rule itself, not missing access.
	if w := direct("member"); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "Only this Crew's owner") {
		t.Fatalf("Team member: %d %s", w.Code, w.Body.String())
	}
	if w := change("member", "on"); w.Code == http.StatusOK {
		t.Fatalf("a Team member turned it on: %s", w.Body.String())
	}
	if w := direct("outsider"); w.Code == http.StatusOK {
		t.Fatalf("another person turned it on: %s", w.Body.String())
	}
	if len(dashboardPublicRecords(externalCommandCrewRoot)) != 0 {
		t.Fatal("a refused person made a public link")
	}

	on := change("owner", "on")
	var link dashboardPublicView
	if on.Code != http.StatusOK || json.Unmarshal(on.Body.Bytes(), &link) != nil || !link.On || !strings.HasPrefix(link.URL, "https://app.example.com/p/") {
		t.Fatalf("owner on: %d %s", on.Code, on.Body.String())
	}
	code := strings.TrimPrefix(link.URL, "https://app.example.com/p/")
	if len(code) != 22 || !validDashboardPublicCode(code) { // the gateway admits exactly this shape
		t.Fatalf("code shape: %q", code)
	}
	page := open("/p/" + code)
	csp := page.Header().Get("Content-Security-Policy")
	for _, want := range []string{"sandbox allow-scripts;", "connect-src 'none'", "form-action 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP lacks %q: %s", want, csp)
		}
	}
	if strings.Contains(csp, "allow-same-origin") {
		t.Errorf("CSP keeps the site's origin: %s", csp)
	}
	if page.Code != 200 || !strings.Contains(page.Body.String(), "total-42") || !strings.Contains(page.Body.String(), `src="/p/`+code+`/report-preview.js"`) ||
		page.Header().Get("Referrer-Policy") != "no-referrer" || page.Header().Get("X-Robots-Tag") != "noindex" || page.Header().Get("X-Content-Type-Options") != "nosniff" || page.Header().Get("Set-Cookie") != "" {
		t.Fatalf("public page: %d %v %s", page.Code, page.Header(), page.Body.String())
	}
	if runtime := open("/p/" + code + "/dashboard.css"); runtime.Code != 200 || !strings.HasPrefix(runtime.Header().Get("Content-Type"), "text/css") {
		t.Fatalf("runtime: %d %v", runtime.Code, runtime.Header())
	}
	if open("/p/"+code+"/record.json").Code != 404 || open("/p/AAAAAAAAAAAAAAAAAAAAAA").Code != 404 {
		t.Fatal("a private file or an unknown code answered")
	}

	// Frozen: new data shows only after Update, under the same code. Turning on again changes nothing.
	shown = "total-84"
	if w := change("owner", "on"); w.Code != 200 || !strings.Contains(open("/p/"+code).Body.String(), "total-42") {
		t.Fatalf("on again moved the copy: %d", w.Code)
	}
	var updated dashboardPublicView
	if w := change("owner", "update"); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &updated) != nil || updated.URL != link.URL {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	if body := open("/p/" + code).Body.String(); !strings.Contains(body, "total-84") {
		t.Fatal("update did not replace the copy")
	}

	// A selected secret that reached the page's data stops the update, and the last good copy stays.
	shown = "token " + secretValue
	if w := change("owner", "update"); w.Code != 422 || strings.Contains(w.Body.String(), secretValue) {
		t.Fatalf("secret in page: %d %s", w.Code, w.Body.String())
	}
	if body := open("/p/" + code).Body.String(); strings.Contains(body, secretValue) || !strings.Contains(body, "total-84") {
		t.Fatal("a secret value reached the public copy")
	}

	// Fails closed: secrets that can't be read clear nothing, and the last good copy stays.
	shown = "total-99"
	f.write(t, externalCommandCrewState+"/workflow.json", `{"id":"team","label":"Test Team","created_by":"owner","capabilities":{"selected_global_secret_names":["CREW_TOKEN","NOT_GRANTED"]}}`)
	if w := change("owner", "update"); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "secrets_unavailable") {
		t.Fatalf("unreadable secrets: %d %s", w.Code, w.Body.String())
	}
	if body := open("/p/" + code).Body.String(); strings.Contains(body, "total-99") || !strings.Contains(body, "total-84") {
		t.Fatal("an unchecked page went public")
	}
	f.write(t, externalCommandCrewState+"/workflow.json", `{"id":"team","label":"Test Team","created_by":"owner","capabilities":{"selected_global_secret_names":["CREW_TOKEN"]}}`)

	if w := change("owner", "off"); w.Code != 200 {
		t.Fatalf("off: %d %s", w.Code, w.Body.String())
	}
	if open("/p/"+code).Code != 404 || open("/p/"+code+"/dashboard.css").Code != 404 {
		t.Fatal("the link kept working after Off")
	}
	if w := change("owner", "update"); w.Code != http.StatusConflict {
		t.Fatalf("update after off: %d", w.Code)
	}

	// A deleted Crew: the first request, even for a runtime file, removes the copy from disk.
	if w := change("owner", "on"); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &link) != nil {
		t.Fatalf("on again: %d %s", w.Code, w.Body.String())
	}
	code = strings.TrimPrefix(link.URL, "https://app.example.com/p/")
	if open("/p/"+code+"/dashboard.css").Code != 200 {
		t.Fatal("runtime of a live link")
	}
	if err := defaultProjectOwners().Remove("work", "test-team"); err != nil {
		t.Fatal(err)
	}
	if open("/p/"+code+"/dashboard.css").Code != 404 || open("/p/"+code).Code != 404 {
		t.Fatal("a deleted Crew's page answered")
	}
	dir, err := dashboardPublicDir()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, code)); !os.IsNotExist(err) {
		t.Fatalf("a deleted Crew's copy stayed on disk: %v", err)
	}
}
