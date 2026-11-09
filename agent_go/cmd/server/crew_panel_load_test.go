package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// PLAT-746-513: a busy Crew menu must not launch twenty inspectors or reread
// every historical run through the workspace service on each two-second poll.
func TestCrewPanelTwentyWorkflowsAvoidsInspectionAndDeepHistory(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	f, workspaceURL := newExternalCrewCommandFixture(t)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("AGENTWORKS_SLOTS", "off")
	t.Setenv("NATIVE_WORKSPACE", "true")
	t.Setenv("AUTH_SECRET", "panel-load-fixture-secret-01234567890123456789")
	var inspections, walks atomic.Int64
	target, _ := url.Parse(workspaceURL)
	proxy := httputil.NewSingleHostReverseProxy(target)
	workspace := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/execute" {
			inspections.Add(1)
		}
		if r.URL.Path == "/api/documents" && r.URL.Query().Get("max_depth") == "3" && strings.HasSuffix(strings.ToLower(r.URL.Query().Get("folder")), "/runs") {
			walks.Add(1)
		}
		proxy.ServeHTTP(w, r)
	}))
	defer workspace.Close()
	t.Setenv("WORKSPACE_API_URL", workspace.URL)
	write := func(name, content string) {
		t.Helper()
		physical, _, err := crewroot.Resolve(f.docs, name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(physical), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(physical, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("panel-load-%02d", i)
		binding := crewExecutableBinding{ID: id, CrewID: "team", CrewRoot: externalCommandCrewRoot, Root: "Workflow/" + id, Kind: "script", RuntimeRoot: "workflows/" + id}
		if err := registerCrewExecutable(binding); err != nil {
			t.Fatal(err)
		}
		write(binding.Root+"/code/script.json", fmt.Sprintf(`{"id":%q,"name":%q}`, id, id))
		write(binding.Root+"/code/main.py", "from agentworks import step\n@step('First')\ndef first(): return 1\n@step('Second')\ndef second(): return 2\nfirst()\nsecond()\n")
		for run := 0; run < 100; run++ {
			name := fmt.Sprintf("%s/runs/run-%03d", binding.Root, run)
			write(name+"/run_metadata.json", `{"status":"completed","started_at":"2026-11-09T00:00:00Z"}`)
			write(name+"/artifacts/nested/result.json", `{"sample":true}`)
		}
	}
	router := mux.NewRouter()
	router.HandleFunc("/api/crews/{project}/panel", f.api.handleCrewPanel).Methods("GET")
	router.HandleFunc("/api/crew-script-graph", f.api.handleCrewScriptGraph).Methods("GET")
	call := func(actor string) (time.Duration, crewPanelReply, int) {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/api/crews/team/panel", nil)
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: actor, Username: actor}))
		w := httptest.NewRecorder()
		started := time.Now()
		router.ServeHTTP(w, r)
		elapsed := time.Since(started)
		var out crewPanelReply
		if w.Code == 200 && json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatal(w.Body.String())
		}
		return elapsed, out, w.Code
	}
	for i := 0; i < 3; i++ {
		elapsed, out, code := call("owner")
		rows := 0
		for _, section := range out.Sections {
			if section.ID == "workflow" {
				rows = len(section.Rows)
			}
		}
		t.Logf("PANEL_LOAD workflows=20 runs=2000 read=%d elapsed_ms=%d inspections=%d deep_walks=%d rows=%d status=%d", i, elapsed.Milliseconds(), inspections.Load(), walks.Load(), rows, code)
		if code != 200 || rows != 20 || inspections.Load() != 0 || walks.Load() != 0 || elapsed > 1500*time.Millisecond {
			t.Fatalf("panel performed expensive per-poll work: status=%d rows=%d time=%s inspections=%d walks=%d", code, rows, elapsed, inspections.Load(), walks.Load())
		}
		for _, section := range out.Sections {
			if section.ID == "workflow" && !strings.Contains(section.Rows[0].Detail, "Step count not loaded") {
				t.Fatal("unknown outline count presented as a real count")
			}
		}
	}
	// A normal outline read, not the menu, supplies the saved-source count.
	graphRequest := httptest.NewRequest(http.MethodGet, "/api/crew-script-graph?workspace_path=Workflow/panel-load-00", nil)
	graphRequest = graphRequest.WithContext(context.WithValue(graphRequest.Context(), UserContextKey, &UserClaims{UserID: "owner", Username: "owner"}))
	graphReply := httptest.NewRecorder()
	router.ServeHTTP(graphReply, graphRequest)
	if graphReply.Code != 200 {
		t.Fatalf("ordinary outline: %d %s", graphReply.Code, graphReply.Body.String())
	}
	var outline struct {
		Nodes []struct {
			Role string `json:"role"`
		} `json:"nodes"`
	}
	if json.Unmarshal(graphReply.Body.Bytes(), &outline) != nil {
		t.Fatal("outline response is invalid")
	}
	count := 0
	for _, node := range outline.Nodes {
		if node.Role == "" {
			count++
		}
	}
	if got, known := cachedCrewStepCount(t.Context(), "Workflow/panel-load-00"); !known || got != count || count == 0 {
		t.Fatalf("saved outline count unavailable: %d %t want %d", got, known, count)
	}
	write("Workflow/panel-load-00/code/main.py", "# edited saved source\nprint('different outline')\n")
	if _, known := cachedCrewStepCount(t.Context(), "Workflow/panel-load-00"); known {
		t.Fatal("edited saved source reused its previous outline count")
	}
	// Warm display data never admits someone who cannot open the Crew.
	if _, _, code := call("outsider"); code != 403 {
		t.Fatalf("private Crew panel admitted outsider: %d", code)
	}
}
