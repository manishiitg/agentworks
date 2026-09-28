package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// fakeRemoteServer records the last request and answers like workspace-api.
type fakeRemoteServer struct {
	root     string
	lastPath string
	lastBody string
	lastTok  string
	listing  string
}

func (f *fakeRemoteServer) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.lastPath, f.lastBody, f.lastTok = r.URL.RequestURI(), string(body), r.Header.Get("X-Workspace-Token")
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/health":
			_ = json.NewEncoder(w).Encode(map[string]string{"docs_dir": f.root})
		case r.URL.Path == "/api/documents" && r.Method == http.MethodGet:
			_, _ = io.WriteString(w, f.listing)
		default:
			// Echo the (already root-rewritten) body back, like a shell
			// command printing its absolute working directory.
			_, _ = io.WriteString(w, `{"success":true,"data":{"stdout":"`+f.root+`/Workflow/remote"}}`)
		}
	})
}

func newRouterFixture(t *testing.T) (*gin.Engine, *fakeRemoteServer, string, *httptest.Server) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	localRoot := t.TempDir()
	fake := &fakeRemoteServer{root: "/srv/workspace-docs"}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	cfg := map[string]interface{}{
		"servers":   map[string]interface{}{"team": map[string]string{"url": srv.URL, "token": "server-token"}},
		"workflows": map[string]string{"Workflow/remote": "team"},
	}
	raw, _ := json.Marshal(cfg)
	if err := os.MkdirAll(filepath.Join(localRoot, "_system"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(localRoot, remotePlacementRelPath), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	r.Use(newRemoteRouter(localRoot).middleware())
	local := func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"served": "local"}) }
	r.GET("/api/documents/*filepath", local)
	r.POST("/api/execute", func(c *gin.Context) {
		body, _ := io.ReadAll(c.Request.Body)
		c.JSON(http.StatusOK, gin.H{"served": "local", "request": string(body)})
	})
	r.POST("/api/folders/copy", local)
	r.GET("/api/documents", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": []gin.H{{
			"filepath": "Workflow", "type": "folder", "children": []gin.H{
				{"filepath": "Workflow/local", "type": "folder"},
				{"filepath": "Workflow/remote", "type": "folder"}, // stray local copy
			},
		}}})
	})
	return r, fake, localRoot, srv
}

func do(r http.Handler, method, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestRemoteRouterForwardsPlacedWorkflowDocuments(t *testing.T) {
	r, fake, _, _ := newRouterFixture(t)
	w := do(r, http.MethodGet, "/api/documents/Workflow/remote/plan.json", "")
	if got := w.Header().Get("X-Workspace-Placement"); got != "team" {
		t.Fatalf("placement header = %q, body %s", got, w.Body.String())
	}
	if fake.lastPath != "/api/documents/Workflow/remote/plan.json" || fake.lastTok != "server-token" {
		t.Fatalf("forwarded %q with token %q", fake.lastPath, fake.lastTok)
	}
	w = do(r, http.MethodGet, "/api/documents/Workflow/remote-other/x.json", "")
	if !strings.Contains(w.Body.String(), `"local"`) {
		t.Fatalf("prefix sibling must stay local, got %s", w.Body.String())
	}
}

func TestRemoteRouterExecuteRouting(t *testing.T) {
	r, fake, localRoot, _ := newRouterFixture(t)
	cases := []struct {
		name, body string
		remote     bool
	}{
		{"working dir in placed workflow", `{"command":"ls","working_directory":"Workflow/remote"}`, true},
		{"local workflow", `{"command":"ls","working_directory":"Workflow/local"}`, false},
		{"absolute path in command", `{"command":"cat ` + localRoot + `/Workflow/remote/a.txt"}`, true},
		{"browser stays local", `{"command":"agent-browser snapshot","working_directory":"Workflow/remote","folder_guard":{"write_paths":["Workflow/remote/runs"]}}`, false},
		{"tool API call stays local", `{"command":"curl -H \"$MCP_AUTH\" \"$MCP_CUSTOM/run_full_workflow\"","working_directory":"Workflow/remote"}`, false},
		{"literal tool URL stays local", `{"command":"curl http://127.0.0.1:1/s/abc/tools/custom/x","working_directory":"Workflow/remote","extra_env":{"MCP_API_URL":"http://127.0.0.1:1/s/abc"}}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake.lastPath = ""
			w := do(r, http.MethodPost, "/api/execute", tc.body)
			remote := w.Header().Get("X-Workspace-Placement") == "team"
			if remote != tc.remote {
				t.Fatalf("remote=%v want %v (body %s)", remote, tc.remote, w.Body.String())
			}
			if remote && strings.Contains(fake.lastBody, localRoot) {
				t.Fatalf("local root leaked to server: %s", fake.lastBody)
			}
			// A local-forced command from a server workflow must not recreate
			// that workflow's folder here: its paths move to the scratch area.
			if !remote && strings.Contains(tc.body, "Workflow/remote") && !strings.Contains(tc.body, "cat ") {
				if strings.Contains(w.Body.String(), `Workflow/remote`) || !strings.Contains(w.Body.String(), "_system/remote-scratch/team/remote") {
					t.Fatalf("placed paths not remapped to scratch: %s", w.Body.String())
				}
			}
		})
	}
}

func TestRemoteRouterRewritesRootsBothWays(t *testing.T) {
	r, fake, localRoot, _ := newRouterFixture(t)
	w := do(r, http.MethodPost, "/api/execute", `{"command":"cat `+localRoot+`/Workflow/remote/a.txt"}`)
	if !strings.Contains(fake.lastBody, fake.root+"/Workflow/remote/a.txt") {
		t.Fatalf("request not rewritten to server root: %s", fake.lastBody)
	}
	if !strings.Contains(w.Body.String(), localRoot+"/Workflow/remote") || strings.Contains(w.Body.String(), fake.root) {
		t.Fatalf("response not rewritten to local root: %s", w.Body.String())
	}
}

func TestRemoteRouterMergesListing(t *testing.T) {
	r, fake, _, _ := newRouterFixture(t)
	fake.listing = `{"success":true,"data":[{"filepath":"Workflow","type":"folder","children":[
		{"filepath":"Workflow/remote","type":"folder","children":[{"filepath":"Workflow/remote/workflow.json","type":"file"}]},
		{"filepath":"Workflow/someone-elses","type":"folder"}]}]}`
	w := do(r, http.MethodGet, "/api/documents?folder=Workflow&max_depth=1", "")
	var resp struct {
		Data []struct {
			Filepath string `json:"filepath"`
			Children []struct {
				Filepath string        `json:"filepath"`
				Children []interface{} `json:"children"`
			} `json:"children"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || len(resp.Data) != 1 {
		t.Fatalf("bad merged listing: %v %s", err, w.Body.String())
	}
	got := map[string]int{}
	for _, c := range resp.Data[0].Children {
		got[c.Filepath] = len(c.Children)
	}
	if _, ok := got["Workflow/local"]; !ok {
		t.Fatalf("local workflow missing: %v", got)
	}
	if got["Workflow/remote"] != 1 {
		t.Fatalf("remote workflow must come from the server with its children: %v", got)
	}
	if _, ok := got["Workflow/someone-elses"]; ok {
		t.Fatalf("unplaced server workflow leaked into listing: %v", got)
	}
}

func TestRemoteRouterRejectsCrossPlacementCopy(t *testing.T) {
	r, _, _, _ := newRouterFixture(t)
	w := do(r, http.MethodPost, "/api/folders/copy", `{"source_path":"Workflow/local","destination_path":"Workflow/remote/copy"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("cross-placement copy must be rejected, got %d %s", w.Code, w.Body.String())
	}
}

func TestRemoteRouterNoopWithoutPlacementFile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(newRemoteRouter(t.TempDir()).middleware())
	r.GET("/api/documents/*filepath", func(c *gin.Context) { c.String(http.StatusOK, "local") })
	w := do(r, http.MethodGet, "/api/documents/Workflow/remote/plan.json", "")
	if w.Body.String() != "local" {
		t.Fatalf("router must be a no-op without placement config, got %q", w.Body.String())
	}
}

func TestWriteFileAtomicKeepsModeAndNeverHalfWrites(t *testing.T) {
	// Covered in handlers; here just ensure the placement file writer is atomic.
	root := t.TempDir()
	rr := newRemoteRouter(root)
	if err := rr.setPlacement("Workflow/a", "team"); err != nil {
		t.Fatal(err)
	}
	if err := rr.setPlacement("Workflow/a", ""); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, remotePlacementRelPath))
	if err != nil || strings.Contains(string(raw), "Workflow/a") {
		t.Fatalf("placement not removed: %v %s", err, raw)
	}
	if _, err := os.Stat(filepath.Join(root, remotePlacementRelPath+".tmp")); !os.IsNotExist(err) {
		t.Fatalf("temp file left behind")
	}
}
