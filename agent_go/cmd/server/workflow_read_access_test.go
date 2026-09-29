package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
)

// readAccessRouter serves the real workflow read registrations with the
// caller named by the X-Test-User header (none: unauthenticated context).
func readAccessRouter(t *testing.T) http.Handler {
	h, _ := readAccessRouterWithAPI(t)
	return h
}

func readAccessRouterWithAPI(t *testing.T) (http.Handler, *StreamingAPI) {
	t.Helper()
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	withMemoryUserDirectory(t, `{"users":[
	  {"id":"alice","username":"alice","can_create":true,"products":[]},
	  {"id":"bob","username":"bob","can_create":true,"products":[]},
	  {"id":"carol","username":"carol","can_create":true,"products":[]},
	  {"id":"root","username":"root","admin":true,"products":[]}
	]}`)
	workspace := httptest.NewServer(&mockWorkspaceAPI{files: map[string]string{
		"Workflow/w/workflow.json":                       `{"id":"w","name":"W","access":{"owners":["alice"]}}`,
		"Workflow/w/planning/plan.json":                  `{"steps":[]}`,
		"Workflow/shared/planning/plan.json":             `{"steps":[]}`,
		"Workflow/shared/workflow.json":                  `{"id":"shared","name":"Shared","access":{"owners":["alice"],"readers":["bob","carol"]}}`,
		"config/user-product-access.json":                `{"carol":{"products":["agentworks"],"workflow_ids":["w"]}}`,
		"Workflow/w/runs/iteration-0/logs/step.json":     `{"secret":"alice-run"}`,
		"Workflow/shared/runs/iteration-0/logs/a.json":   `{"ok":true}`,
		"_users/alice/Chats/runs/private/logs/chat.json": `{"secret":"alice-chat"}`,
		"_users/bob/Chats/runs/own/logs/chat.json":       `{"ok":true}`,
	}})
	t.Cleanup(workspace.Close)
	t.Setenv("WORKSPACE_API_URL", workspace.URL)

	router := mux.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if id := r.Header.Get("X-Test-User"); id != "" {
				r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: id, Username: id}))
			}
			next.ServeHTTP(w, r)
		})
	})
	api := &StreamingAPI{}
	registerWorkflowReadRoutes(router.PathPrefix("/api").Subrouter(), api)
	return router, api
}

func readAs(t *testing.T, h http.Handler, user, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if user != "" {
		req.Header.Set("X-Test-User", user)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

var workflowReadRoutes = []string{
	"/api/workflow/pulse-module-state",
	"/api/workflow/pulse-findings",
	"/api/workflow/pulse-reviews",
	"/api/workflow/pulse-agent-metrics",
	"/api/workflow/pulse-impact",
	"/api/workflow/pulse-context",
	"/api/workspace/state",
	"/api/workflow/run-folders",
	"/api/workflow/learnings/all",
	"/api/workflow/variable-groups",
	"/api/workflow/logs",
	"/api/workflow/costs",
	"/api/workflow/review-data",
	"/api/workflow/builder-doc",
	"/api/workflow/framework-health",
	"/api/workflow/backup",
	"/api/workflow/publish",
	"/api/workflow/notifications",
	"/api/workflow/active-executions",
}

func workflowReadTarget(route, workspacePath string) string {
	q := url.Values{"workspace_path": {workspacePath}}
	switch route {
	case "/api/workflow/builder-doc":
		q.Set("doc", "soul")
	case "/api/workflow/logs":
		q.Set("run_folder", "iteration-0")
	}
	return route + "?" + q.Encode()
}

func TestWorkflowReadRoutesRequireWorkflowAccess(t *testing.T) {
	h := readAccessRouter(t)
	for _, route := range workflowReadRoutes {
		t.Run(strings.TrimPrefix(route, "/api/"), func(t *testing.T) {
			if rec := readAs(t, h, "bob", workflowReadTarget(route, "Workflow/w")); rec.Code != http.StatusForbidden {
				t.Fatalf("user without access: got %d, want 403: %s", rec.Code, rec.Body.String())
			}
			if rec := readAs(t, h, "alice", workflowReadTarget(route, "Workflow/w")); rec.Code != http.StatusOK {
				t.Fatalf("owner: got %d, want 200: %s", rec.Code, rec.Body.String())
			}
			if rec := readAs(t, h, "bob", workflowReadTarget(route, "Workflow/shared")); rec.Code != http.StatusOK {
				t.Fatalf("read-only share: got %d, want 200: %s", rec.Code, rec.Body.String())
			}
			if rec := readAs(t, h, "root", workflowReadTarget(route, "Workflow/w")); rec.Code != http.StatusOK {
				t.Fatalf("admin: got %d, want 200: %s", rec.Code, rec.Body.String())
			}
			// Leading/trailing slashes address the same workflow.
			if rec := readAs(t, h, "bob", workflowReadTarget(route, "/Workflow/w/")); rec.Code != http.StatusForbidden {
				t.Fatalf("slash-wrapped path: got %d, want 403", rec.Code)
			}
			if rec := readAs(t, h, "bob", workflowReadTarget(route, "_users/alice/Chats/Work/projects/p")); rec.Code != http.StatusForbidden {
				t.Fatalf("another user's tree: got %d, want 403", rec.Code)
			}
		})
	}
}

func TestLogFileReadAccess(t *testing.T) {
	h := readAccessRouter(t)
	file := func(p string) string { return "/api/workflow/logs/file?" + url.Values{"file_path": {p}}.Encode() }

	if rec := readAs(t, h, "bob", file("Workflow/w/runs/iteration-0/logs/step.json")); rec.Code != http.StatusForbidden {
		t.Fatalf("no access: got %d", rec.Code)
	}
	if rec := readAs(t, h, "alice", file("Workflow/w/runs/iteration-0/logs/step.json")); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "alice-run") {
		t.Fatalf("owner: got %d %s", rec.Code, rec.Body.String())
	}
	if rec := readAs(t, h, "bob", file("Workflow/shared/runs/iteration-0/logs/a.json")); rec.Code != http.StatusOK {
		t.Fatalf("read-only share: got %d", rec.Code)
	}
	if rec := readAs(t, h, "bob", file("_users/bob/Chats/runs/own/logs/chat.json")); rec.Code != http.StatusOK {
		t.Fatalf("own tree: got %d", rec.Code)
	}
	// Path tricks: every spelling that reaches alice's data is refused.
	for _, trick := range []string{
		"_users/alice/Chats/runs/private/logs/chat.json",
		"Workflow/shared/../w/runs/iteration-0/logs/step.json",
		"Workflow/shared/runs/../../w/runs/iteration-0/logs/step.json",
		"/Workflow/w/runs/iteration-0/logs/step.json",
		"Workflow//w/runs/iteration-0/logs/step.json",
		"Workflow/./w/runs/iteration-0/logs/step.json",
		"Workflow\\w/runs/iteration-0/logs/step.json",
		"_users/bob/../alice/Chats/runs/private/logs/chat.json",
		"Workflow/.relay_releases/x/runs/logs/a.json",
		"config/runs/logs/a.json",
		"Chats/runs/private/logs/chat.json",
	} {
		if rec := readAs(t, h, "bob", file(trick)); rec.Code != http.StatusForbidden {
			t.Fatalf("%q: got %d, want 403: %s", trick, rec.Code, rec.Body.String())
		}
	}
	// Admins may read dot-folders and other users' trees.
	if rec := readAs(t, h, "root", file("_users/alice/Chats/runs/private/logs/chat.json")); rec.Code != http.StatusOK {
		t.Fatalf("admin: got %d", rec.Code)
	}
}

func TestWorkspaceReadAllowedRoots(t *testing.T) {
	readAccessRouter(t)
	ctx := context.Background()
	bob := &UserClaims{UserID: "bob"}
	cases := map[string]bool{
		"Workflow/w":                               false,
		"Workflow/shared":                          true,
		"Workflow/new-folder":                      true, // no manifest yet: account tier
		"Workflow":                                 false,
		"_users/bob/Chats/Work/projects/p":         true,
		"_users/alice/Chats/Code/projects/app":     false,
		"Crew/unknown":                             false,
		"skills/x":                                 false,
		"Workflow/shared/../w":                     false,
		"Chats/Work/projects/p":                    false, // resolves under the server's default user
		"_users/bob/Chats/Work/projects/p/../../x": false,
	}
	for p, want := range cases {
		if got := workspaceReadAllowed(ctx, bob, p, logicalPathIsDefaultUser, true); got != want {
			t.Errorf("%q: got %v, want %v", p, got, want)
		}
	}
	if !workspaceReadAllowed(ctx, bob, "Chats/Work/projects/p", logicalPathIsCaller, true) {
		t.Error("caller-relative logical path is the caller's own tree")
	}
	// The per-user workflow allow-list narrows even a granted share.
	carol := &UserClaims{UserID: "carol", Username: "carol"}
	if workspaceReadAllowed(ctx, carol, "Workflow/shared", logicalPathIsDefaultUser, true) {
		t.Error("allow-list must hide a shared workflow not on it")
	}
}

func TestWorkflowReadHandlersCheckInside(t *testing.T) {
	h, api := readAccessRouterWithAPI(t)
	now := time.Now()
	api.trackedWorkflowExecutions = map[string]*TrackedWorkflowExecution{
		"e1": {ExecutionID: "e1", SessionID: "s-alice", Source: trackedExecutionSourceWorkflowRun, Status: trackedExecutionStatusRunning, WorkspacePath: "Workflow/w", UserID: "alice", StartedAt: now},
		"e2": {ExecutionID: "e2", SessionID: "s-shared", Source: trackedExecutionSourceWorkflowRun, Status: trackedExecutionStatusRunning, WorkspacePath: "Workflow/shared", UserID: "alice", StartedAt: now},
	}

	// Unfiltered active executions list only what the caller may read.
	rec := readAs(t, h, "bob", "/api/workflow/active-executions")
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `"Workflow/w"`) || !strings.Contains(rec.Body.String(), `"Workflow/shared"`) {
		t.Fatalf("active executions for bob: %d %s", rec.Code, rec.Body.String())
	}
	if rec := readAs(t, h, "alice", "/api/workflow/active-executions"); !strings.Contains(rec.Body.String(), `"Workflow/w"`) {
		t.Fatalf("owner must see her run: %s", rec.Body.String())
	}

	// A running execution by session: another user's run is not found.
	if rec := readAs(t, h, "bob", "/api/workflow/running/s-alice"); rec.Code != http.StatusNotFound {
		t.Fatalf("bob reading alice's run: %d", rec.Code)
	}
	if rec := readAs(t, h, "alice", "/api/workflow/running/s-alice"); rec.Code != http.StatusOK {
		t.Fatalf("alice reading her run: %d", rec.Code)
	}
	if rec := readAs(t, h, "bob", "/api/workflow/running/s-shared"); rec.Code != http.StatusOK {
		t.Fatalf("bob reading a shared workflow's run: %d", rec.Code)
	}

	// Summary and overview drop the paths the caller may not read.
	for _, route := range []string{"/api/workflows/summary", "/api/workflows/overview"} {
		target := route + "?workspace_paths=" + url.QueryEscape("Workflow/w,Workflow/shared")
		rec := readAs(t, h, "bob", target)
		if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `"Workflow/w"`) || !strings.Contains(rec.Body.String(), `"Workflow/shared"`) {
			t.Fatalf("%s for bob: %d %s", route, rec.Code, rec.Body.String())
		}
		if rec := readAs(t, h, "alice", target); !strings.Contains(rec.Body.String(), `"Workflow/w"`) {
			t.Fatalf("%s for alice: %s", route, rec.Body.String())
		}
	}

	// Runtime status is keyed by the workflow's manifest ID.
	setWorkflowRuntime(&WorkflowRuntimeState{ID: "rt-w", PresetQueryID: "w", CreatedAt: now, UpdatedAt: now})
	setWorkflowRuntime(&WorkflowRuntimeState{ID: "rt-shared", PresetQueryID: "shared", CreatedAt: now, UpdatedAt: now})
	t.Cleanup(func() { deleteWorkflowRuntime("w"); deleteWorkflowRuntime("shared") })
	if rec := readAs(t, h, "bob", "/api/workflow/status?preset_query_id=w"); rec.Code != http.StatusForbidden {
		t.Fatalf("status for bob: %d", rec.Code)
	}
	if rec := readAs(t, h, "alice", "/api/workflow/status?preset_query_id=w"); rec.Code != http.StatusOK {
		t.Fatalf("status for alice: %d", rec.Code)
	}
	if rec := readAs(t, h, "bob", "/api/workflow/status?preset_query_id=shared"); rec.Code != http.StatusOK {
		t.Fatalf("status for a reader: %d", rec.Code)
	}

	// Report preview reads name their workflow in ?workspace=.
	preview := func(ws string) string {
		return "/api/workflow/report-preview/file?" + url.Values{"workspace": {ws}, "path": {"db/reports/index.html"}}.Encode()
	}
	if rec := readAs(t, h, "bob", preview("Workflow/w")); rec.Code != http.StatusForbidden {
		t.Fatalf("preview file for bob: %d", rec.Code)
	}
	if rec := readAs(t, h, "bob", preview("Workflow/shared")); rec.Code == http.StatusForbidden {
		t.Fatalf("preview file for a reader: %d", rec.Code)
	}
	if rec := readAs(t, h, "bob", "/api/workflow/report-preview/costs?workspace=Workflow/w"); rec.Code != http.StatusForbidden {
		t.Fatalf("preview costs for bob: %d", rec.Code)
	}
	if rec := readAs(t, h, "alice", "/api/workflow/report-preview/costs?workspace=Workflow/w"); rec.Code != http.StatusOK {
		t.Fatalf("preview costs for alice: %d %s", rec.Code, rec.Body.String())
	}
}
