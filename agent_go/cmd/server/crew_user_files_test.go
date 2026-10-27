package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	ws "github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	wh "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
	"github.com/spf13/viper"
)

// PLAT-856-383: real HTTP file operations retain logical Crew paths while
// native grants exclude platform state and deletion keeps the workspace lifecycle.
func TestManagedCrewUserFilesHTTPAndNativeBoundary(t *testing.T) {
	base := t.TempDir()
	docs, state := filepath.Join(base, "docs"), filepath.Join(base, "state")
	for _, p := range []string{docs, state} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	docs, _ = filepath.EvalSymlinks(docs)
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	t.Setenv("AGENTWORKS_STATE_ROOT", state)
	t.Setenv("LOCAL_MODE", "false")
	old := viper.Get("docs-dir")
	viper.Set("docs-dir", docs)
	t.Cleanup(func() { viper.Set("docs-dir", old) })
	router := gin.New()
	router.PUT("/api/documents/*filepath", wh.UpdateDocument)
	router.GET("/api/documents/*filepath", wh.GetDocument)
	router.GET("/api/documents", wh.ListDocuments)
	router.POST("/api/folders", wh.CreateFolder)
	router.DELETE("/api/folders/*folderpath", wh.DeleteFolder)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	t.Setenv("WORKSPACE_API_URL", server.URL)
	const logical = "Crew/example-managed"
	ctx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "owner"})
	_, err := writeCrewCreationManifests(ctx, "owner", workCrewTestProfile(nil), "work", "", "Example", "", "", "managed-id", logical, nil, crewCreationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	roots, found, err := crewroot.Lookup(docs, logical)
	if err != nil || !found || roots.Local {
		t.Fatal(roots, found, err)
	}
	if _, bound, err := localCrewRoots(logical); bound || err != nil {
		t.Fatal("managed Crew treated as local binding", err)
	}
	if crewFilesPath(logical) != logical {
		t.Fatal("physical files prefix leaked into logical root")
	}
	for _, rel := range []string{"skills/custom/SKILL.md", "notes/new.txt", "db/assets/chart.txt"} {
		data, _ := json.Marshal(map[string]any{"content": "person content"})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("PUT", "/api/documents/"+logical+"/"+rel, bytes.NewReader(data)))
		if w.Code != 200 {
			t.Fatalf("write %s: %d %s", rel, w.Code, w.Body.String())
		}
		raw, err := os.ReadFile(filepath.Join(roots.Resources, rel))
		if err != nil || string(raw) != "person content" {
			t.Fatal(rel, string(raw), err)
		}
	}
	listing := httptest.NewRecorder()
	router.ServeHTTP(listing, httptest.NewRequest("GET", "/api/documents?folder="+logical+"&max_depth=4", nil))
	if listing.Code != 200 || !strings.Contains(listing.Body.String(), logical+"/notes/new.txt") || strings.Contains(listing.Body.String(), logical+"/files/") {
		t.Fatal("logical listing", listing.Code, listing.Body.String())
	}
	for _, name := range []string{"product.json", "workflow.json"} {
		if _, err := os.Stat(filepath.Join(roots.Private, name)); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(roots.Resources, name)); !os.IsNotExist(err) {
			t.Fatal("private manifest shadowed", name)
		}
	}
	cwd, err := crewCLIWorkingDir(logical, "owner", "managed-session", "codex-cli", false)
	if err != nil {
		t.Fatal(err)
	}
	target, err := os.Readlink(filepath.Join(cwd, "project"))
	if err != nil || target != roots.Resources {
		t.Fatal("native project root", target, err)
	}
	const session = "managed-file-policy"
	common.SetSessionFolderGuard(session, []string{logical}, []string{logical})
	common.SetSessionFolderGuardBlockedPaths(session, []string{logical + "/db/db.sqlite", logical + "/db/db.sqlite-wal"})
	common.SetSessionFolderGuardBlockedWritePaths(session, []string{logical + "/schedules.json", logical + "/webhooks.json"})
	t.Cleanup(func() { common.ClearSessionShellConfig(session) })
	policy := cliLandlockPolicyForSession(session, "codex-cli", cwd, nil)
	hasResources := false
	for _, p := range policy.WorkspaceWritePaths {
		hasResources = hasResources || p == roots.Resources
		if crewroot.Within(p, roots.Private) {
			t.Fatal("native platform write grant", p)
		}
	}
	if !hasResources {
		t.Fatal("missing whole resource write grant")
	}
	for _, p := range append(policy.BlockedPaths, policy.BlockedWritePaths...) {
		if crewroot.Within(roots.Resources, p) {
			t.Fatal("blocked descendant would split resource root", p)
		}
	}
	common.SetSessionCrewReader(session, true)
	common.SetSessionRunOutputPath(session, logical+"/runs/current/output")
	runPolicy := cliLandlockPolicyForSession(session, "codex-cli", cwd, nil)
	output := filepath.Join(roots.Private, "runs", "current", "output")
	foundOutput := false
	for _, grant := range runPolicy.WorkspaceWritePaths {
		foundOutput = foundOutput || grant == output
		if crewroot.Within(grant, roots.Resources) || grant == roots.Private {
			t.Fatal("Run policy broadened", grant)
		}
	}
	if !foundOutput {
		t.Fatal("Run output grant lost")
	}
	common.SetSessionCrewReader(session, false)
	read, err := externalFileRequest(ctx, wf.Request{Root: logical, Operation: "read", Path: "notes/new.txt"})
	if err != nil || read.Content != "person content" {
		t.Fatalf("MCP resource read: %+v %v", read, err)
	}
	call := &crewFunctionCall{ID: "fn-test", TargetPath: logical}
	out := filepath.Join(roots.Resources, ".calls", "fn-test")
	if err := os.MkdirAll(out, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "result.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	opened, err := openStructuredFunctionFolder(call)
	if err != nil {
		t.Fatal("function output", err)
	}
	opened.Close()
	if got, err := knowledgeProjectResource(&knowledgeProject{Workspace: logical, Path: filepath.Join(roots.Private, "workflow.json")}, "knowledgebase"); err != nil || got != filepath.Join(roots.Private, "knowledgebase") {
		t.Fatal("knowledge source", got, err)
	}
	record := &publishedProject{Root: logical, WorkspaceID: "managed-id", ProfileID: "work", OwnerID: "owner", ProjectID: "source-id", Revision: "rev1"}
	source := filepath.Join(docs, record.sourceRoot(), "code", "main.py")
	if err := os.MkdirAll(filepath.Dir(source), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("published source"), 0600); err != nil {
		t.Fatal(err)
	}
	publishedCtx := context.WithValue(ctx, publicationExecutionKey{}, record)
	publishedRead, err := externalFileRequest(publishedCtx, wf.Request{Root: logical, Operation: "read", Path: "code/main.py"})
	if err != nil || publishedRead.Content != "published source" {
		t.Fatalf("published MCP source selection: %+v %v", publishedRead, err)
	}
	client := ws.NewClient(server.URL, ws.WithUserID("owner"))
	previous := ws.BeforePathMutation
	called := false
	ws.BeforePathMutation = func(context.Context, ...string) error { called = true; return nil }
	t.Cleanup(func() { ws.BeforePathMutation = previous })
	if err := client.DeleteFolder(ctx, logical); err != nil {
		t.Fatal("Crew deletion", err)
	}
	if !called {
		t.Fatal("deletion bypassed mutation hook")
	}
	if _, err := os.Stat(roots.Private); !os.IsNotExist(err) {
		t.Fatal("Crew private root remains", err)
	}
	if err := client.DeleteFolder(ctx, logical); err != nil {
		t.Fatal("deletion retry", err)
	}
}
