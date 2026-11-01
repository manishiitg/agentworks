package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	ws "github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	wh "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
	"github.com/spf13/viper"
)

// PLAT-856-145: real filesystem/HTTP handlers must preserve the person's
// project while Crew resources, private state and Run writes stay separate.
func TestLocalCrewFolderLifecyclePreservesProjectAndRunBoundary(t *testing.T) {
	base := t.TempDir()
	docs, project, state := filepath.Join(base, "docs"), filepath.Join(base, "project"), filepath.Join(base, "state")
	for _, dir := range []string{docs, project, state} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	docs, _ = filepath.EvalSymlinks(docs)
	project, _ = filepath.EvalSymlinks(project)
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	t.Setenv("AGENTWORKS_STATE_ROOT", state)
	t.Setenv("LOCAL_MODE", "true")
	t.Setenv("MULTI_USER_MODE", "")
	oldDocs := viper.Get("docs-dir")
	viper.Set("docs-dir", docs)
	t.Cleanup(func() { viper.Set("docs-dir", oldDocs) })
	originals := map[string]string{"AGENTS.md": "person guidance", "CLAUDE.md": "person Claude guidance", ".cursor/cli.json": "{\"permissions\":{}}", "product.json": "person data"}
	for p, raw := range originals {
		full := filepath.Join(project, p)
		os.MkdirAll(filepath.Dir(full), 0700)
		if err := os.WriteFile(full, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
	}
	router := gin.New()
	router.PUT("/api/documents/*filepath", wh.UpdateDocument)
	router.GET("/api/documents/*filepath", wh.GetDocument)
	router.GET("/api/documents", wh.ListDocuments)
	router.POST("/api/folders", wh.CreateFolder)
	router.POST("/api/shared-file-write", wh.SharedFileWrite)
	router.POST("/api/shared-file-lifecycle", wh.SharedFileLifecycle)
	router.PATCH("/api/patch/*filepath", wh.DiffPatchDocument)
	router.GET("/api/search", wh.SearchDocuments)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	t.Setenv("WORKSPACE_API_URL", server.URL)
	selected, err := validateNewLocalCrewRoot(project)
	if err != nil {
		t.Fatal(err)
	}
	const logical = "Crew/example-local"
	ctx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "owner"})
	_, err = writeCrewCreationManifests(ctx, "owner", workCrewTestProfile(nil), "work", "", "Example", "", "", "local-id", logical, nil, crewCreationOptions{LocalRoot: selected})
	if err != nil {
		t.Fatal(err)
	}
	roots, bound, err := localCrewRoots(logical)
	if err != nil || !bound {
		t.Fatalf("binding: %v %v", bound, err)
	}
	if roots.Files != project || crewFilesPath(logical) != logical+"/_files" {
		t.Fatal("wrong files root")
	}
	for _, name := range []string{"product.json", "workflow.json"} {
		if _, err := os.Stat(filepath.Join(roots.Private, name)); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(roots.Resources, name)); !os.IsNotExist(err) {
			t.Fatal("private manifest in resources", name)
		}
	}
	if cwd, err := crewCLIWorkingDir(logical, "owner", "local-session", "cursor-cli", false); err != nil || cwd != project {
		t.Fatalf("cwd %q %v", cwd, err)
	}
	write := func(path, body, request string, guard *wf.FolderGuard) *httptest.ResponseRecorder {
		data, _ := json.Marshal(wf.WriteRequest{Root: logical, Path: path, Content: body, ExpectedRevision: wf.MissingRevision, RequestID: request, Identity: wf.EditIdentity{UserID: "owner", Source: "mcp", ConnectionID: "fixture"}, Actor: "owner", Guard: guard})
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/api/shared-file-write", bytes.NewReader(data))
		r.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, r)
		return w
	}
	guard := &wf.FolderGuard{ReadPaths: []string{"_files", "reports"}, WritePaths: []string{"_files", "reports"}, BlockedWritePaths: []string{"_files/AGENTS.md"}}
	if w := write("_files/notes.txt", "actual project file", "write-project", guard); w.Code != 200 {
		t.Fatalf("write project: %d %s", w.Code, w.Body.String())
	}
	if w := write("reports/summary.md", "portable resource", "write-resource", guard); w.Code != 200 {
		t.Fatalf("write resource: %d %s", w.Code, w.Body.String())
	}
	// Patch/search must hit the selected directory, never a managed _files shadow.
	patch := httptest.NewRecorder()
	data, _ := json.Marshal(map[string]any{"diff": "--- a/patch.txt\n+++ b/patch.txt\n@@ -1 +1 @@\n-before\n+after\n"})
	if err := os.WriteFile(filepath.Join(project, "patch.txt"), []byte("before\n"), 0600); err != nil {
		t.Fatal(err)
	}
	router.ServeHTTP(patch, httptest.NewRequest("PATCH", "/api/patch/"+logical+"/_files/patch.txt", bytes.NewReader(data)))
	if patch.Code != 200 {
		t.Fatalf("patch selected folder: %d %s", patch.Code, patch.Body.String())
	}
	if raw, err := os.ReadFile(filepath.Join(project, "patch.txt")); err != nil || string(raw) != "after\n" {
		t.Fatal("patch missed selected folder", err)
	}
	search := httptest.NewRecorder()
	router.ServeHTTP(search, httptest.NewRequest("GET", "/api/search?folder="+roots.FilesPath()+"&query=actual&limit=20", nil))
	if search.Code != 200 || !strings.Contains(search.Body.String(), logical+"/_files/notes.txt") || strings.Contains(search.Body.String(), project) {
		t.Fatalf("search selected folder: %d %s", search.Code, search.Body.String())
	}
	// Private settings writes and subsequent reads agree on the physical root.
	for i, name := range []string{"product-schedules.json", "triggers/deliveries/new.json"} {
		if w := write(name, "{}", fmt.Sprintf("private-%d", i), &wf.FolderGuard{ReadPaths: []string{"."}, WritePaths: []string{"."}}); w.Code != 200 {
			t.Fatalf("private write: %d %s", w.Code, w.Body.String())
		}
		if raw, err := os.ReadFile(filepath.Join(roots.Private, name)); err != nil || string(raw) != "{}" {
			t.Fatal("settings write missed private root", err)
		}
		if _, err := os.Stat(filepath.Join(roots.Resources, name)); !os.IsNotExist(err) {
			t.Fatal("private write shadowed in resources", err)
		}
	}
	for _, pair := range [][2]string{{"reports/summary.md", "_files/moved.md"}, {"_files/notes.txt", "reports/moved.md"}, {"reports/summary.md", "product-schedules.json"}, {"product-schedules.json", "reports/moved.json"}} {
		data, _ := json.Marshal(wf.LifecycleRequest{Root: logical, Path: pair[0], Destination: pair[1], Action: "move", Guard: &wf.FolderGuard{ReadPaths: []string{"."}, WritePaths: []string{"."}}})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("POST", "/api/shared-file-lifecycle", bytes.NewReader(data)))
		if w.Code == 200 {
			t.Fatalf("cross-namespace move admitted: %v", pair)
		}
	}
	if _, _, _, _, _, err := wf.BoundCrewFileRequest(docs, roots.FilesPath(), "notes.txt", "", guard); err == nil {
		t.Fatal("direct _files root lost Crew-relative guard")
	}
	for _, p := range []string{"Chats/another-person/chat.json", "_users/another-person/chats/chat.json", "Downloads/report.pdf"} {
		if actual, err := fsutil.WorkspacePath(docs, p); err != nil || actual != filepath.Join(docs, p) {
			t.Fatalf("caller namespace rewritten: %s %s %v", p, actual, err)
		}
	}
	readCtx := context.WithValue(ctx, externalFileGuardKey{}, guard)
	result, err := externalFileRequest(readCtx, wf.Request{Root: logical, Operation: "read", Path: "_files/notes.txt"})
	if err != nil || result.Content != "actual project file" || result.Path != "_files/notes.txt" {
		t.Fatalf("MCP read: %+v %v", result, err)
	}
	if w := write("_files/AGENTS.md", "replace", "deny-guidance", guard); w.Code == 200 {
		t.Fatal("blocked write accepted")
	}
	if w := write("_files/.agentworks/reports/escape.md", "alias", "deny-alias", guard); w.Code == 200 {
		t.Fatal("resource alias accepted")
	}
	if err := os.Symlink(roots.Resources, filepath.Join(project, "resource-link")); err != nil {
		t.Fatal(err)
	}
	if w := write("_files/resource-link/reports/escape.md", "alias", "deny-link", guard); w.Code == 200 {
		t.Fatal("symlink resource alias accepted")
	}
	for _, name := range []string{"product-schedules.json", "schedule-runs.json", "triggers/deliveries/input.json", ".report-cache/result.txt"} {
		p, _, err := crewroot.Resolve(docs, logical+"/"+name)
		if err != nil || !crewroot.Within(roots.Private, p) {
			t.Fatalf("private path %s: %s %v", name, p, err)
		}
	}
	// The Files panel and bare bridge paths use the same issued files root.
	listing := httptest.NewRecorder()
	router.ServeHTTP(listing, httptest.NewRequest("GET", "/api/documents?folder="+roots.FilesPath()+"&max_depth=3", nil))
	if listing.Code != 200 || !strings.Contains(listing.Body.String(), logical+"/_files/notes.txt") || strings.Contains(listing.Body.String(), "../project") || strings.Contains(listing.Body.String(), "resource-link") {
		t.Fatalf("Files listing: %d %s", listing.Code, listing.Body.String())
	}
	const fileSession = "local-bare-files"
	common.SetSessionWorkingDir(fileSession, roots.FilesPath())
	common.SetSessionFolderGuard(fileSession, []string{roots.FilesPath()}, []string{roots.FilesPath()})
	t.Cleanup(func() { common.ClearSessionShellConfig(fileSession) })
	fileCtx := context.WithValue(ctx, common.ChatSessionIDKey, fileSession)
	cli := ws.NewClient(server.URL, ws.WithUserID("owner"))
	bare, err := cli.ReadWorkspaceFile(fileCtx, ws.ReadWorkspaceFileParams{Filepath: "notes.txt"})
	if err != nil || bare.Content != "actual project file" {
		t.Fatalf("bare file read: %+v %v", bare, err)
	}
	if _, err := cli.ReadWorkspaceFile(fileCtx, ws.ReadWorkspaceFileParams{Filepath: "Crew/other/_files/notes.txt"}); err == nil {
		t.Fatal("qualified other Crew bypassed guard")
	}
	call := &crewFunctionCall{ID: "fn-local", TargetPath: logical}
	env, err := checkedStructuredFunctionShellEnvironment(call)
	if err != nil || env["FUNCTION_OUTPUT_DIR"] != filepath.Join(roots.Resources, ".calls", "fn-local") {
		t.Fatalf("function output: %v %v", env, err)
	}
	const session = "local-run-boundary"
	runtimeDir := filepath.Join(state, "runtime")
	os.MkdirAll(runtimeDir, 0700)
	common.SetSessionFolderGuard(session, []string{logical, roots.FilesPath()}, []string{logical, roots.FilesPath()})
	common.SetSessionCrewReader(session, true)
	common.SetSessionRunOutputPath(session, logical+"/.calls/fn-local")
	t.Cleanup(func() { common.ClearSessionShellConfig(session) })
	policy := cliLandlockPolicyForSession(session, "cursor-cli", project, nil, runtimeDir)
	foundRuntime, foundOutput := false, false
	for _, p := range policy.WorkspaceWritePaths {
		if crewroot.Within(p, project) || p == roots.Resources {
			t.Fatalf("Run granted project writes: %v", policy.WorkspaceWritePaths)
		}
		foundRuntime = foundRuntime || p == runtimeDir
		foundOutput = foundOutput || p == env["FUNCTION_OUTPUT_DIR"]
	}
	if !foundRuntime || !foundOutput {
		t.Fatalf("missing allowed writes: %v", policy.WorkspaceWritePaths)
	}
	if _, err := validateNewLocalCrewRoot(project); err == nil {
		t.Fatal("existing Crew adopted twice")
	}
	if err := removeLocalCrew(logical); err != nil {
		t.Fatal(err)
	}
	for name, want := range originals {
		raw, err := os.ReadFile(filepath.Join(project, name))
		if err != nil || string(raw) != want {
			t.Fatalf("original changed %s: %v", name, err)
		}
	}
	if raw, err := os.ReadFile(filepath.Join(project, "notes.txt")); err != nil || string(raw) != "actual project file" {
		t.Fatal("person file removed", err)
	}
	for _, dir := range []string{roots.Resources, roots.Private} {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatal("Crew files not removed", dir, err)
		}
	}
	archives, _ := filepath.Glob(filepath.Join(state, "crew-removals", "local-id", "*.tar.gz"))
	if len(archives) != 1 {
		t.Fatal("missing removal archive", archives)
	}
	file, err := os.Open(archives[0])
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	saved := map[string]bool{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		saved[h.Name] = true
		if strings.HasPrefix(h.Name, "../") {
			t.Fatal("bad archive path")
		}
	}
	if !saved["resources/reports/summary.md"] || !saved["settings/product.json"] {
		t.Fatal("incomplete archive", saved)
	}
	// Retry after resources/private state have already been removed, as happens
	// when publication cleanup failed after the first removal.
	if err := removeLocalCrew(logical); err != nil {
		t.Fatalf("removal retry: %v", err)
	}
	if err := os.RemoveAll(project); err != nil {
		t.Fatal(err)
	}
	if err := removeLocalCrew(logical); err != nil {
		t.Fatalf("missing selected folder removal: %v", err)
	}
}
