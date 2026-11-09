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
		crewID := fmt.Sprintf("panel-crew-%02d", i)
		crewRoot := "Crew/" + crewID
		if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: crewID, ProjectID: crewID, OwnerID: "owner"}); err != nil {
			t.Fatal(err)
		}
		f.write(t, "_system/crew-data/"+crewID+"/product.json", fmt.Sprintf(`{"schema_version":1,"product":"work","id":%q,"title":%q}`, crewID, crewID))
		f.write(t, "_system/crew-data/"+crewID+"/workflow.json", fmt.Sprintf(`{"id":%q,"label":%q,"created_by":"owner"}`, crewID, crewID))
		if err := os.MkdirAll(filepath.Join(f.docs, crewRoot), 0700); err != nil {
			t.Fatal(err)
		}
		binding := crewExecutableBinding{ID: id, CrewID: crewID, CrewRoot: crewRoot, Root: "Workflow/" + id, Kind: "script", RuntimeRoot: "workflows/" + id}
		if err := registerCrewExecutable(binding); err != nil {
			t.Fatal(err)
		}
		write(binding.Root+"/workflow.json", fmt.Sprintf(`{"id":%q,"label":%q,"created_by":"owner"}`, id, id))
		write(binding.Root+"/code/script.json", fmt.Sprintf(`{"id":%q,"name":%q}`, id, id))
		write(binding.Root+"/code/helper.py", "def helper(): return 1\n")
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
	call := func(actor, crewID string) (time.Duration, crewPanelReply, int) {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/api/crews/"+crewID+"/panel", nil)
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
	for i := 0; i < 20; i++ {
		crewID := fmt.Sprintf("panel-crew-%02d", i)
		for read := 0; read < 3; read++ {
			elapsed, out, code := call("owner", crewID)
			rows := 0
			for _, section := range out.Sections {
				if section.ID == "workflow" {
					rows = len(section.Rows)
				}
			}
			t.Logf("PANEL_LOAD workflows=20 runs=2000 crew=%d read=%d elapsed_ms=%d inspections=%d deep_walks=%d rows=%d status=%d", i, read, elapsed.Milliseconds(), inspections.Load(), walks.Load(), rows, code)
			if code != 200 || rows != 1 || inspections.Load() != 0 || walks.Load() != 0 || elapsed > 1500*time.Millisecond {
				t.Fatalf("panel performed expensive per-poll work: status=%d rows=%d time=%s inspections=%d walks=%d", code, rows, elapsed, inspections.Load(), walks.Load())
			}
		}
	}
	// Overflow does not hide the workflow or fabricate a latest run from a partial list.
	for run := 100; run < 5000; run++ {
		write(fmt.Sprintf("Workflow/panel-load-00/runs/run-%04d/run_metadata.json", run), `{"status":"completed"}`)
	}
	// Expire only this safe display cache, without sleeping or changing authority.
	f.api.crewPanels.mu.Lock()
	for key, value := range f.api.crewPanels.entries {
		value.at = time.Now().Add(-6 * time.Second)
		f.api.crewPanels.entries[key] = value
	}
	f.api.crewPanels.mu.Unlock()
	elapsed, out, code := call("owner", "panel-crew-00")
	longHistory := false
	for _, section := range out.Sections {
		if section.ID == "workflow" && len(section.Rows) == 1 {
			longHistory = section.Error == "" && strings.Contains(section.Rows[0].Detail, "Run history is long")
		}
	}
	t.Logf("PANEL_LOAD long_history=5000 elapsed_ms=%d status=%d row_preserved=%t", elapsed.Milliseconds(), code, longHistory)
	if code != 200 || !longHistory || elapsed > 1500*time.Millisecond {
		t.Fatal("long history hid the workflow or exceeded the panel deadline")
	}
	// Concurrent cold reads return Loading, then reuse only this scope's saved snapshot.
	cache := crewPanelCache{}
	key := crewPanelCacheKey{caller: "owner", project: "panel-crew-00", authority: "current"}
	entered, release, complete := make(chan struct{}), make(chan struct{}), make(chan struct{})
	source := crewPanelSource{id: "workflow", title: "Workflow", allowed: true, refreshFor: time.Millisecond, read: func(context.Context) (crewPanelSection, error) {
		close(entered)
		<-release
		return crewPanelSection{Rows: []crewPanelRow{{ID: "workflow:one", Label: "Example"}}}, nil
	}}
	go func() { cache.collect(t.Context(), key, []crewPanelSource{source}, time.Second); close(complete) }()
	<-entered
	cold := cache.collect(t.Context(), key, []crewPanelSource{source}, time.Second)
	close(release)
	<-complete
	if cold[0].Error != "" || len(cold[0].Rows) != 1 || cold[0].Rows[0].Label != "Loading" {
		t.Fatalf("cold overlapping panel did not show Loading: %+v", cold)
	}
	entered, release, complete = make(chan struct{}), make(chan struct{}), make(chan struct{})
	// Force revalidation while retaining a previous snapshot.
	source.refreshFor = time.Nanosecond
	go func() { cache.collect(t.Context(), key, []crewPanelSource{source}, time.Second); close(complete) }()
	<-entered
	warm := cache.collect(t.Context(), key, []crewPanelSource{source}, time.Second)
	close(release)
	<-complete
	if warm[0].Error != "" || !warm[0].Stale || len(warm[0].Rows) != 1 || warm[0].Rows[0].Label != "Example" {
		t.Fatalf("overlapping refresh lost last snapshot: %+v", warm)
	}
	// A normal saved-script outline read, not the menu, supplies its saved-source count.
	graphRoot := "Workflow/panel-saved-script"
	if err := registerCrewExecutable(crewExecutableBinding{ID: "panel-saved-script", CrewID: "team", CrewRoot: externalCommandCrewRoot, Root: graphRoot, Kind: "script"}); err != nil {
		t.Fatal(err)
	}
	write(graphRoot+"/code/script.json", `{"id":"panel-saved-script","name":"Example"}`)
	write(graphRoot+"/code/main.py", "from agentworks import step\n@step('First')\ndef first(): return 1\nfirst()\n")
	graphRequest := httptest.NewRequest(http.MethodGet, "/api/crew-script-graph?workspace_path=Workflow/panel-saved-script", nil)
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
	if got, known := cachedCrewStepCount(t.Context(), graphRoot); !known || got != count || count == 0 {
		t.Fatalf("saved outline count unavailable: %d %t want %d", got, known, count)
	}
	write(graphRoot+"/code/main.py", "# edited saved source\nprint('different outline')\n")
	if _, known := cachedCrewStepCount(t.Context(), graphRoot); known {
		t.Fatal("edited saved source reused its previous outline count")
	}
	// Warm display data never admits someone who cannot open the Crew.
	if _, _, code := call("outsider", "panel-crew-00"); code != 403 {
		t.Fatalf("private Crew panel admitted outsider: %d", code)
	}
}
