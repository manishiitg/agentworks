package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

// fakeRemoteServer answers like a workspace server in server mode.
type fakeRemoteServer struct {
	mu        sync.Mutex
	root      string
	token     string
	calls     []string
	lastBody  string
	lastTok   string
	lastUser  string
	listing   string
	glob      string
	globFiles int // files reported by /api/glob after an import
	imported  bool
	down      bool
}

func (f *fakeRemoteServer) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.down {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		f.lastBody, f.lastTok, f.lastUser = string(body), r.Header.Get("X-Workspace-Token"), r.Header.Get("X-User-ID")
		if f.lastTok != f.token {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/remote/whoami":
			_ = json.NewEncoder(w).Encode(map[string]string{"user": "srv-user", "docs_dir": f.root})
		case r.URL.Path == "/api/documents" && r.Method == http.MethodGet:
			_, _ = io.WriteString(w, f.listing)
		case r.URL.Path == "/api/glob":
			if f.glob != "" {
				_, _ = io.WriteString(w, f.glob)
				return
			}
			files := []map[string]string{}
			if f.imported {
				for i := 0; i < f.globFiles; i++ {
					files = append(files, map[string]string{"filepath": "Workflow/moving/f" + string(rune('a'+i))})
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "data": files})
		case r.URL.Path == "/api/workspace/import":
			f.imported = true
			_, _ = io.WriteString(w, `{"success":true}`)
		case r.Method == http.MethodDelete:
			f.imported = false
			_, _ = io.WriteString(w, `{"success":true}`)
		default:
			// Echo the (already root-rewritten) body back, like a shell
			// command printing its absolute working directory.
			_, _ = io.WriteString(w, `{"success":true,"data":{"stdout":"`+f.root+`/Workflow/remote"}}`)
		}
	})
}

func (f *fakeRemoteServer) called(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

type routerFixture struct {
	engine    *gin.Engine
	fake      *fakeRemoteServer
	localRoot string
	cfgPath   string
}

// newRouterFixture builds a laptop engine (the real middleware stack) with
// Workflow/remote placed on a fake server. token is the laptop's shared
// workspace token ("" = standalone mode).
func newRouterFixture(t *testing.T, token string) *routerFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	localRoot := t.TempDir()
	cfgPath := filepath.Join(t.TempDir(), "remote-workflows.json")
	t.Setenv(remotePlacementFileEnv, cfgPath)
	t.Setenv(workspaceAPITokenEnv, token)
	t.Setenv(serverUserTokensEnv, "")
	viper.Set("docs-dir", localRoot)
	fake := &fakeRemoteServer{root: "/srv/workspace-docs", token: "server-token"}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	writePlacement(t, cfgPath, srv.URL, map[string]string{"Workflow/remote": "team"})
	for _, dir := range []string{"Workflow/local", "Workflow/remote-other"} {
		if err := os.MkdirAll(filepath.Join(localRoot, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return &routerFixture{engine: newWorkspaceEngine(localRoot), fake: fake, localRoot: localRoot, cfgPath: cfgPath}
}

func writePlacement(t *testing.T, cfgPath, url string, workflows map[string]string) {
	t.Helper()
	raw, _ := json.Marshal(map[string]interface{}{
		"servers":   map[string]interface{}{"team": map[string]string{"url": url, "token": "server-token"}},
		"workflows": workflows,
	})
	if err := os.WriteFile(cfgPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func do(r http.Handler, method, target, body string, headers ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestRemotePlacementFileLivesOutsideDocsRoot(t *testing.T) {
	t.Setenv(remotePlacementFileEnv, "")
	root := filepath.Join(t.TempDir(), "workspace-docs")
	got := remotePlacementFile(root)
	if rel, err := filepath.Rel(root, got); err == nil && !strings.HasPrefix(rel, "..") {
		t.Fatalf("placement file %s is inside the docs root", got)
	}
}

func TestRemoteRouterRequiresWorkspaceTokenBeforeForwarding(t *testing.T) {
	fx := newRouterFixture(t, "laptop-token")
	for _, tc := range []struct{ method, target, body string }{
		{http.MethodPost, "/api/execute", `{"command":"id","working_directory":"Workflow/remote"}`},
		{http.MethodGet, "/api/documents/Workflow/remote/plan.json", ""},
		{http.MethodGet, "/api/remote/placements", ""},
		{http.MethodPost, "/api/remote/move-to-local", `{"workflow":"Workflow/remote"}`},
	} {
		w := do(fx.engine, tc.method, tc.target, tc.body)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s without token: status %d, want 401 (%s)", tc.method, tc.target, w.Code, w.Body.String())
		}
	}
	if fx.fake.called("POST /api/execute") || fx.fake.called("GET /api/documents") {
		t.Fatal("an unauthenticated request reached the server")
	}
	w := do(fx.engine, http.MethodGet, "/api/documents/Workflow/remote/plan.json", "", "X-Workspace-Token", "laptop-token")
	if w.Header().Get("X-Workspace-Placement") != "team" {
		t.Fatalf("authenticated request not forwarded: %d %s", w.Code, w.Body.String())
	}
}

func TestRemoteRouterForwardsWithServerTokenNotLaptopIdentity(t *testing.T) {
	fx := newRouterFixture(t, "")
	w := do(fx.engine, http.MethodGet, "/api/documents/Workflow/remote/plan.json", "", "X-User-ID", "laptop-user")
	if w.Header().Get("X-Workspace-Placement") != "team" {
		t.Fatalf("not forwarded: %d %s", w.Code, w.Body.String())
	}
	if fx.fake.lastTok != "server-token" || fx.fake.lastUser != "" {
		t.Fatalf("forwarded token %q user %q; the server must derive identity from its own token", fx.fake.lastTok, fx.fake.lastUser)
	}
	w = do(fx.engine, http.MethodGet, "/api/documents/Workflow/remote-other/x.json", "")
	if w.Header().Get("X-Workspace-Placement") != "" {
		t.Fatalf("prefix sibling must stay local")
	}
}

func TestRemoteRouterExecuteRouting(t *testing.T) {
	fx := newRouterFixture(t, "")
	cases := []struct {
		name, body string
		remote     bool
	}{
		{"working dir in placed workflow", `{"command":"ls","working_directory":"Workflow/remote"}`, true},
		{"local workflow", `{"command":"ls","working_directory":"Workflow/local"}`, false},
		{"absolute path in command", `{"command":"cat ` + fx.localRoot + `/Workflow/remote/a.txt"}`, true},
		{"sibling name is not a mention", `{"command":"cat Workflow/remote-other/a.txt"}`, false},
		{"browser stays local", `{"command":"agent-browser snapshot","working_directory":"Workflow/remote","folder_guard":{"write_paths":["Workflow/remote/runs"]}}`, false},
		{"tool API call stays local", `{"command":"curl -H \"$MCP_AUTH\" \"$MCP_CUSTOM/run_full_workflow\"","working_directory":"Workflow/remote"}`, false},
		{"literal tool URL stays local", `{"command":"curl http://127.0.0.1:1/s/abc/tools/custom/x","working_directory":"Workflow/remote","extra_env":{"MCP_API_URL":"http://127.0.0.1:1/s/abc"}}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/execute", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = req
			rr := newRemoteRouter(fx.localRoot)
			d, err := rr.decideExecute(c, rr.config())
			if err != nil {
				t.Fatal(err)
			}
			if remote := d.kind == routeForward; remote != tc.remote {
				t.Fatalf("remote=%v want %v", remote, tc.remote)
			}
			if !tc.remote && strings.Contains(tc.body, "Workflow/remote\"") {
				body, _ := io.ReadAll(c.Request.Body)
				if strings.Contains(string(body), `"Workflow/remote`) || !strings.Contains(string(body), "_system/remote-scratch/team/remote") {
					t.Fatalf("placed paths not remapped to scratch: %s", body)
				}
			}
		})
	}
}

func TestRemoteRouterStripsSecretsAndToolCredentialsWhenForwarding(t *testing.T) {
	fx := newRouterFixture(t, "")
	body := `{"command":"ls","working_directory":"Workflow/remote","extra_env":{"SECRET_API_KEY":"s3cr3t","MCP_API_TOKEN":"tok","WORKSPACE_API_TOKEN":"ws","RUNLOOP_STEP_ID":"step-1"}}`
	w := do(fx.engine, http.MethodPost, "/api/execute", body)
	if w.Header().Get("X-Workspace-Placement") != "team" {
		t.Fatalf("not forwarded: %s", w.Body.String())
	}
	for _, leaked := range []string{"s3cr3t", "MCP_API_TOKEN", "WORKSPACE_API_TOKEN"} {
		if strings.Contains(fx.fake.lastBody, leaked) {
			t.Fatalf("%s reached the server: %s", leaked, fx.fake.lastBody)
		}
	}
	if !strings.Contains(fx.fake.lastBody, "RUNLOOP_STEP_ID") {
		t.Fatalf("ordinary env was dropped: %s", fx.fake.lastBody)
	}
}

func TestRemoteRouterRewritesRootsBothWays(t *testing.T) {
	fx := newRouterFixture(t, "")
	w := do(fx.engine, http.MethodPost, "/api/execute", `{"command":"cat `+fx.localRoot+`/Workflow/remote/a.txt"}`)
	if !strings.Contains(fx.fake.lastBody, fx.fake.root+"/Workflow/remote/a.txt") {
		t.Fatalf("request not rewritten to server root: %s", fx.fake.lastBody)
	}
	if !strings.Contains(w.Body.String(), fx.localRoot+"/Workflow/remote") || strings.Contains(w.Body.String(), fx.fake.root) {
		t.Fatalf("response not rewritten to local root: %s", w.Body.String())
	}
}

type listedWorkflow struct {
	children int
	status   string
}

func listedWorkflows(t *testing.T, body []byte) map[string]listedWorkflow {
	t.Helper()
	var resp struct {
		Data []struct {
			Filepath string `json:"filepath"`
			Children []struct {
				Filepath     string        `json:"filepath"`
				Children     []interface{} `json:"children"`
				RemoteStatus string        `json:"remote_status"`
			} `json:"children"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("bad listing: %v %s", err, body)
	}
	got := map[string]listedWorkflow{}
	for _, root := range resp.Data {
		if root.Filepath != "Workflow" {
			continue
		}
		for _, c := range root.Children {
			got[c.Filepath] = listedWorkflow{len(c.Children), c.RemoteStatus}
		}
	}
	return got
}

func TestRemoteRouterMergesListing(t *testing.T) {
	fx := newRouterFixture(t, "")
	if err := os.MkdirAll(filepath.Join(fx.localRoot, "Workflow/remote"), 0o755); err != nil { // stray local copy
		t.Fatal(err)
	}
	fx.fake.listing = `{"success":true,"data":[{"filepath":"Workflow","type":"folder","children":[
		{"filepath":"Workflow/remote","type":"folder","children":[{"filepath":"Workflow/remote/workflow.json","type":"file"}]},
		{"filepath":"Workflow/someone-elses","type":"folder"}]}]}`
	got := listedWorkflows(t, do(fx.engine, http.MethodGet, "/api/documents?folder=Workflow&max_depth=1", "").Body.Bytes())
	if _, ok := got["Workflow/local"]; !ok {
		t.Fatalf("local workflow missing: %v", got)
	}
	if got["Workflow/remote"].children != 1 {
		t.Fatalf("remote workflow must come from the server with its children: %v", got)
	}
	if _, ok := got["Workflow/someone-elses"]; ok {
		t.Fatalf("unplaced server workflow leaked into listing: %v", got)
	}
}

func TestRemoteRouterShowsUnreachableServerWorkflows(t *testing.T) {
	fx := newRouterFixture(t, "")
	fx.fake.down = true
	w := do(fx.engine, http.MethodGet, "/api/documents?folder=Workflow&max_depth=1", "")
	got := listedWorkflows(t, w.Body.Bytes())
	if got["Workflow/remote"].status != "unreachable" || w.Header().Get("X-Workspace-Remote-Unreachable") != "team" {
		t.Fatalf("offline server's workflow must stay listed as unreachable: %v %v", got, w.Header())
	}
	if _, ok := got["Workflow/local"]; !ok {
		t.Fatalf("local workflows must still list: %v", got)
	}
}

func TestRemoteRouterMergesGlob(t *testing.T) {
	fx := newRouterFixture(t, "")
	if err := os.WriteFile(filepath.Join(fx.localRoot, "Workflow/local/a.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	fx.fake.glob = `{"success":true,"data":[{"filepath":"Workflow/remote/b.md"},{"filepath":"Workflow/other/c.md"}]}`
	w := do(fx.engine, http.MethodGet, "/api/glob?pattern=**/*.md&folder=Workflow", "")
	body := w.Body.String()
	if !strings.Contains(body, "Workflow/local/a.md") || !strings.Contains(body, "Workflow/remote/b.md") || strings.Contains(body, "Workflow/other/c.md") {
		t.Fatalf("glob not merged correctly: %s", body)
	}
}

func TestRemoteRouterRejectsCrossPlacementOperations(t *testing.T) {
	fx := newRouterFixture(t, "")
	for _, tc := range []struct{ target, body string }{
		{"/api/folders/copy", `{"source_path":"Workflow/local","destination_path":"Workflow/remote/copy"}`},
		{"/api/documents/Workflow/remote/a.md/move", `{"destination_path":"Workflow/local/a.md"}`},
	} {
		if w := do(fx.engine, http.MethodPost, tc.target, tc.body); w.Code != http.StatusBadRequest {
			t.Fatalf("%s must be rejected, got %d %s", tc.target, w.Code, w.Body.String())
		}
	}
}

func TestRemoteRouterRejectsPlainHTTPToNonLoopback(t *testing.T) {
	if err := validateServerURL("http://confida.example.com"); err == nil {
		t.Fatal("plain http to a remote host must be rejected")
	}
	for _, ok := range []string{"https://confida.example.com", "http://127.0.0.1:9", "http://localhost:9"} {
		if err := validateServerURL(ok); err != nil {
			t.Fatalf("%s: %v", ok, err)
		}
	}
}

func TestRemoteRouterNoopWithoutPlacementFile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv(remotePlacementFileEnv, filepath.Join(t.TempDir(), "missing.json"))
	r := gin.New()
	r.Use(newRemoteRouter(t.TempDir()).middleware())
	r.GET("/api/documents/*filepath", func(c *gin.Context) { c.String(http.StatusOK, "local") })
	w := do(r, http.MethodGet, "/api/documents/Workflow/remote/plan.json", "")
	if w.Body.String() != "local" {
		t.Fatalf("router must be a no-op without placement config, got %q", w.Body.String())
	}
}

func moveRequest(t *testing.T, fx *routerFixture, path, body string) (*httptest.ResponseRecorder, *gin.Context) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	return w, c
}

func TestMoveToServerRollsBackOnVerificationFailure(t *testing.T) {
	fx := newRouterFixture(t, "")
	dir := filepath.Join(fx.localRoot, "Workflow/moving")
	for _, f := range []string{"workflow.json", "planning/plan.json", ".sandbox-cache/home/.gitconfig"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, f)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rr := newRemoteRouter(fx.localRoot)

	fx.fake.globFiles = 1 // server reports fewer files than the 2 that travel
	_, c := moveRequest(t, fx, "/api/remote/move-to-server", "")
	if _, err := rr.moveToServer(c, "Workflow/moving", "team"); err == nil || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("expected a rolled-back move, got %v", err)
	}
	if !fx.fake.called("DELETE /api/folders/Workflow/moving") {
		t.Fatal("partial server copy was not removed")
	}
	if _, id := rr.placement(rr.config(), "Workflow/moving"); id != "" {
		t.Fatal("placement changed after a failed move")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("local copy must stay after a failed move")
	}

	fx.fake.globFiles = 2 // .sandbox-cache is excluded, so 2 files travel
	_, c = moveRequest(t, fx, "/api/remote/move-to-server", "")
	res, err := rr.moveToServer(c, "Workflow/moving", "team")
	if err != nil {
		t.Fatalf("retry after rollback failed: %v", err)
	}
	if res.Files != 2 {
		t.Fatalf("moved %d files, want 2 (.sandbox-cache must not travel)", res.Files)
	}
	if _, id := rr.placement(rr.config(), "Workflow/moving"); id != "team" {
		t.Fatal("placement not flipped after a verified move")
	}
	info, err := os.Stat(fx.cfgPath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("placement file must be 0600: %v %v", err, info)
	}
}

func TestZipFolderExcludesSandboxCache(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"a.txt", ".sandbox-cache/home/.git-credentials"} {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(dir, f)), 0o755)
		_ = os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644)
	}
	archive, n, err := zipFolderToTemp(dir, "/a", "/b")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(archive)
	raw, _ := os.ReadFile(archive)
	zr, _ := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if n != 1 || len(zr.File) != 1 || zr.File[0].Name != "a.txt" {
		t.Fatalf("archive must hold only a.txt, got %d entries", len(zr.File))
	}
}
