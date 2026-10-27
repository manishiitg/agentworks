package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/sparkquillproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

func newSparkQuillExternalFixture(t *testing.T) (*externalToolsFixture, *UserClaims) {
	t.Helper()
	f := newExternalToolsFixture(t)
	t.Setenv("AGENT_PRODUCTS", "sparkquill")
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true,"products":["sparkquill"]},{"id":"reader","username":"reader","role":"viewer","can_edit":false,"products":["sparkquill"]},{"id":"outsider","username":"outsider","can_create":true,"products":["agentworks"]}]}`)
	root := "_users/owner/Chats/SparkQuill"
	f.write(t, root+"/family.json", `{"pin_hash":"NEVER_RETURN_PIN","connection_id":"NEVER_RETURN_ACCOUNT","child":{"name":"Learner","grade":"4","board":"IB","language":"English"}}`)
	f.write(t, root+"/memory/child-profile.json", `{"name":"Learner","grade":"4"}`)
	f.write(t, root+"/materials/lesson.md", "Fractions lesson")
	f.write(t, root+"/activities/fractions/activity.json", `{"title":"Fractions book","subject":"Math","items":["book.html"]}`)
	f.write(t, root+"/activities/fractions/book.html", "<h1>Fractions</h1>")
	f.write(t, root+"/activities/fractions/attempts/one.json", `{"score":3}`)
	f.write(t, root+"/reports/progress.html", "<h1>Progress</h1>")
	router := gin.New()
	router.POST("/api/shared-file-write", workspacehandlers.SharedFileWrite)
	router.POST("/api/shared-assets", workspacehandlers.SharedAssets)
	router.GET("/api/documents", func(c *gin.Context) {
		folder := c.Query("folder")
		entries := []map[string]string{}
		_ = filepath.WalkDir(filepath.Join(f.docs, folder), func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			rel, _ := filepath.Rel(f.docs, p)
			if rel == folder {
				return nil
			}
			kind := "file"
			if d.IsDir() {
				kind = "folder"
			}
			entries = append(entries, map[string]string{"filepath": filepath.ToSlash(rel), "type": kind})
			return nil
		})
		c.JSON(200, gin.H{"success": true, "data": entries})
	})
	router.GET("/api/documents/*file", func(c *gin.Context) {
		p := strings.TrimPrefix(c.Param("file"), "/")
		data, err := os.ReadFile(filepath.Join(f.docs, p))
		if err != nil {
			c.Status(404)
			return
		}
		c.JSON(200, gin.H{"success": true, "data": gin.H{"filepath": p, "content": string(data)}})
	})
	router.PUT("/api/documents/*file", func(c *gin.Context) {
		p := filepath.Join(f.docs, strings.TrimPrefix(c.Param("file"), "/"))
		var body struct {
			Content string `json:"content"`
		}
		if c.BindJSON(&body) != nil {
			return
		}
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Error(err)
		}
		if err := os.WriteFile(p, []byte(body.Content), 0600); err != nil {
			t.Error(err)
		}
		c.JSON(200, gin.H{"success": true})
	})
	upstream := httptest.NewServer(router)
	t.Cleanup(upstream.Close)
	t.Setenv("WORKSPACE_API_URL", upstream.URL)
	registry := agentprofiles.NewRegistry()
	for _, profile := range sparkquillproduct.BuiltinAgentProfiles() {
		if err := registry.RegisterProfile(profile); err != nil {
			t.Fatal(err)
		}
	}
	if err := sparkquillproduct.RegisterAgentProfileRuntime(registry, upstream.URL); err != nil {
		t.Fatal(err)
	}
	f.api.agentProfiles = registry
	return f, &UserClaims{UserID: "owner", Username: "owner", AccessToken: &accesstokens.Token{ID: "sparkquill-test", Scopes: slices.Clone(sparkquillScopes)}}
}
func sparkquillTestCall(t *testing.T, api *StreamingAPI, c *UserClaims, name string, args map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	data, _ := json.Marshal(map[string]any{"name": name, "arguments": args})
	w := httptest.NewRecorder()
	api.handleExternalCall(w, adminRequest("POST", "/api/external/v1/call", string(data), c, nil))
	return w
}
func TestSparkQuillExternalCatalogAndPlatformIsolation(t *testing.T) {
	f, c := newSparkQuillExternalFixture(t)
	srv := serveExternalMCP(t, f.api, c)
	ctx := t.Context()
	cli := dialExternalMCP(t, ctx, srv.URL)
	initialized := initializeExternalMCP(t, ctx, cli)
	if initialized.ServerInfo.Name != "SparkQuill" || strings.Contains(initialized.Instructions, "AgentWorks") {
		t.Fatalf("wrong product: %+v", initialized)
	}
	tools := listExternalMCPTools(t, ctx, cli)
	if len(tools) != 3 || !strings.Contains(tools[externalMCPToolSpec].Description, "SparkQuill") {
		t.Fatalf("tools: %+v", tools)
	}
	spec := callRemoteTool(t, ctx, cli, externalMCPToolSpec, nil)
	requireRemoteSuccess(t, spec, "catalog")
	catalog := marshalStructured(t, spec)
	for _, name := range []string{"create_file", "update_file", "upload_file", "create_learning_activity", "get_learning_progress"} {
		if !strings.Contains(catalog, name) {
			t.Fatalf("missing %s: %s", name, catalog)
		}
	}
	for _, name := range []string{"list_workflows", "manage_vault_access", "get_token_usage", "ask_crew"} {
		if strings.Contains(catalog, name) {
			t.Fatalf("platform tool %s leaked", name)
		}
		got := callRemoteTool(t, ctx, cli, externalMCPToolCall, map[string]any{"name": name, "arguments": map[string]any{}})
		if !got.IsError {
			t.Fatalf("called platform tool %s", name)
		}
	}
	read := callRemoteTool(t, ctx, cli, externalMCPToolCall, map[string]any{"name": "read_file", "arguments": map[string]any{"path": "materials/lesson.md"}})
	requireRemoteSuccess(t, read, "read")
	if !strings.Contains(marshalStructured(t, read), "Fractions lesson") {
		t.Fatal("missing material")
	}
	// An old broad platform connection on this deployment cannot fall back to the platform catalog.
	c.AccessToken.Scopes = []string{"workflows:read", "files:read", "files:write"}
	c.AccessToken.AllWorkflows = true
	if w := sparkquillTestCall(t, f.api, c, "list_workflows", nil); w.Code != 404 {
		t.Fatalf("legacy token fallback: %d %s", w.Code, w.Body.String())
	}
	t.Setenv("AGENT_PRODUCTS", "")
	c.AccessToken.Scopes = slices.Clone(sparkquillScopes)
	catalogTools, err := externalToolsForClaims(c)
	if err != nil || !catalogTools[0].sparkquill {
		t.Fatalf("shared server lost product scope: %v", err)
	}
	platform, err := externalToolsForClaims(writeTestClaims("owner"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range platform {
		if tool.sparkquill {
			t.Fatal("SparkQuill leaked into AgentWorks")
		}
	}
}
func TestSparkQuillExternalFileEditsUploadsAndConfinement(t *testing.T) {
	f, c := newSparkQuillExternalFixture(t)
	call := func(name string, args map[string]any, want int) *httptest.ResponseRecorder {
		w := sparkquillTestCall(t, f.api, c, name, args)
		if w.Code != want {
			t.Fatalf("%s: %d want %d: %s", name, w.Code, want, w.Body.String())
		}
		return w
	}
	args := map[string]any{"path": "activities/new/chapter.html", "content": "", "request_id": "create"}
	first := call("create_file", args, 200)
	call("create_file", args, 200)
	var receipt wf.WriteReceipt
	if err := json.Unmarshal(first.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	args["content"] = "changed"
	call("create_file", args, 409)
	args["request_id"] = "overwrite"
	call("create_file", args, 409)
	edit := map[string]any{"path": args["path"], "content": "<h1>Chapter one</h1>", "expected_revision": receipt.Revision, "request_id": "update"}
	call("update_file", edit, 200)
	edit["request_id"] = "stale"
	call("update_file", edit, 409)
	binary := []byte{0, 1, 2, 255, 137, 80, 78, 71}
	upload := map[string]any{"path": "materials/picture.png", "content_base64": base64.StdEncoding.EncodeToString(binary), "expected_revision": "missing", "request_id": "upload"}
	call("upload_file", upload, 200)
	call("upload_file", upload, 200)
	file := call("read_file", map[string]any{"path": "materials/picture.png"}, 200)
	if !strings.Contains(file.Body.String(), base64.StdEncoding.EncodeToString(binary)) {
		t.Fatal("binary round trip failed")
	}
	upload["content_base64"] = "invalid!"
	upload["request_id"] = "invalid-base64"
	call("upload_file", upload, 400)
	for _, p := range []string{"../other.txt", "/tmp/outside", "_users/outsider/Chats/SparkQuill/a.txt", "family.json", "activities/new/attempts/one.json", "activities/new/activity.json", "activities/new/product.json", "materials/.env", "memory/child-profile.json", "reports/progress.html"} {
		call("create_file", map[string]any{"path": p, "content": "bad", "request_id": p}, 403)
	}
	if err := os.Symlink(filepath.Join(f.docs, "Workflow"), filepath.Join(f.docs, "_users/owner/Chats/SparkQuill/materials/link")); err != nil {
		t.Fatal(err)
	}
	call("read_file", map[string]any{"path": "materials/link/invoices/docs/process.md"}, 403)
	for _, p := range []string{"family.json", "activities/fractions/product.json", "materials/../../family.json"} {
		call("read_file", map[string]any{"path": p}, 403)
	}
	c.AccessToken.Scopes = []string{"sparkquill:read"}
	call("create_file", map[string]any{"path": "materials/no.md", "content": "no", "request_id": "readonly"}, 403)
	c.AccessToken.Scopes = slices.Clone(sparkquillScopes)
	c.AccessToken.FileGuard = &wf.FolderGuard{ReadPaths: []string{"materials"}, WritePaths: []string{"materials/drafts"}, BlockedPaths: []string{"materials/private"}}
	call("read_file", map[string]any{"path": "activities/fractions/book.html"}, 403)
	call("create_file", map[string]any{"path": "materials/drafts/a.md", "content": "allowed", "request_id": "guarded"}, 200)
	call("create_file", map[string]any{"path": "materials/no.md", "content": "no", "request_id": "guarded-no"}, 403)
	call("create_learning_activity", map[string]any{"format": "book", "brief": "create a book", "request_id": "guarded-run"}, 403)
}
func TestSparkQuillExternalContextProgressFinalizeAndAssignment(t *testing.T) {
	f, c := newSparkQuillExternalFixture(t)
	call := func(name string, args map[string]any) *httptest.ResponseRecorder {
		w := sparkquillTestCall(t, f.api, c, name, args)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body.String())
		}
		return w
	}
	context := call("get_learning_context", nil).Body.String()
	if !strings.Contains(context, "Learner") || strings.Contains(context, "NEVER_RETURN") {
		t.Fatalf("context: %s", context)
	}
	progress := call("get_learning_progress", nil).Body.String()
	if !strings.Contains(progress, "Progress") || !strings.Contains(progress, "attempts/one.json") {
		t.Fatalf("progress: %s", progress)
	}
	f.write(t, "_users/owner/Chats/SparkQuill/activities/new/source.sq.html", `<section data-section="learn"><h1>Learning fractions</h1><p>A half is one of two equal parts.</p></section>`)
	call("finalize_activity", map[string]any{"dir": "activities/new", "title": "New book", "items": []any{"source.sq.html"}})
	if !strings.Contains(f.read(t, "_users/owner/Chats/SparkQuill/activities/new/product.json"), "sparkquill-child") {
		t.Fatal("child project missing")
	}
	call("assign_activity", map[string]any{"dir": "activities/new"})
	if !strings.Contains(f.read(t, "_users/owner/Chats/SparkQuill/current-activity.json"), "activities/new") {
		t.Fatal("assignment missing")
	}
	listed := call("list_activities", map[string]any{"query": "New book"}).Body.String()
	if !strings.Contains(listed, "source.html") || !strings.Contains(listed, "preview_url") {
		t.Fatalf("activities: %s", listed)
	}
	f.write(t, "_users/owner/Chats/SparkQuill/materials/large-video.mp4", strings.Repeat("v", wf.MaxFileBytes+1))
	link := call("get_file_link", map[string]any{"path": "materials/large-video.mp4"}).Body.String()
	if !strings.Contains(link, "sparkquill?file=") || strings.Contains(link, "token=") {
		t.Fatalf("bad preview: %s", link)
	}
	// Remote volumes use the same scoped catalog and exclude internal state during listing.
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir()+"/unmounted")
	files := call("list_files", map[string]any{"path": "activities", "depth": 4}).Body.String()
	if strings.Contains(files, "product.json") {
		t.Fatalf("remote private file leaked: %s", files)
	}
}
func TestSparkQuillExternalScopesAndJobOwnership(t *testing.T) {
	f, c := newSparkQuillExternalFixture(t)
	if _, ok := validMCPOAuthScopes("sparkquill:read sparkquill:write sparkquill:run"); !ok {
		t.Fatal("SparkQuill scopes rejected")
	}
	for _, raw := range []string{"sparkquill:write", "sparkquill:read files:read", "workflows:read"} {
		if _, ok := validMCPOAuthScopes(raw); ok {
			t.Fatalf("accepted %s", raw)
		}
	}
	scopes, ok := validMCPOAuthScopes("")
	if !ok || !slices.Equal(scopes, sparkquillScopes) {
		t.Fatalf("defaults: %v", scopes)
	}
	token := mcpOAuthTokenForGrant(mcpOAuthGrant{UserID: "owner", Scopes: scopes, Expires: time.Now().Add(time.Hour)})
	if token.AllWorkflows || token.AllCrews {
		t.Fatal("SparkQuill OAuth grant obtained platform bounds")
	}
	token.Name = "SparkQuill"
	if err := accesstokens.Validate(token, time.Now()); err != nil {
		t.Fatal(err)
	}
	metadata := httptest.NewRecorder()
	t.Setenv("PUBLIC_URL", "https://sparkquill.example")
	f.api.handleMCPOAuthProtectedResource(metadata, httptest.NewRequest("GET", mcpOAuthProtectedResourcePath, nil))
	if strings.Contains(metadata.Body.String(), "workflows:read") || !strings.Contains(metadata.Body.String(), "sparkquill:write") {
		t.Fatalf("metadata: %s", metadata.Body.String())
	}
	job := sparkquillExternalJob{ID: "sq_job_" + strings.Repeat("a", 64), UserID: c.UserID, ConnectionID: c.AccessToken.ID, Status: "completed", Result: "Finished", StartedAt: time.Now()}
	if err := writeSparkQuillJob(job); err != nil {
		t.Fatal(err)
	}
	w := sparkquillTestCall(t, f.api, c, "get_activity_status", map[string]any{"job_id": job.ID})
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Finished") {
		t.Fatalf("status: %s", w.Body.String())
	}
	c.AccessToken.ID = "another-connection"
	if w := sparkquillTestCall(t, f.api, c, "get_activity_status", map[string]any{"job_id": job.ID}); w.Code != 404 {
		t.Fatal("cross-connection job leak")
	}
	c.AccessToken.ID = job.ConnectionID
	job.Status = "running"
	if err := writeSparkQuillJob(job); err != nil {
		t.Fatal(err)
	}
	w = sparkquillTestCall(t, f.api, c, "get_activity_status", map[string]any{"job_id": job.ID})
	if !strings.Contains(w.Body.String(), "interrupted") {
		t.Fatalf("restart status: %s", w.Body.String())
	}
	c.UserID = "outsider"
	if w := sparkquillTestCall(t, f.api, c, "read_file", map[string]any{"path": "materials/lesson.md"}); w.Code != 403 {
		t.Fatal("product access not enforced")
	}
}

func TestSparkQuillOAuthGrantAuthenticatesOnlySparkQuillAndRevokes(t *testing.T) {
	f, c := newSparkQuillExternalFixture(t)
	t.Setenv("AUTH_SECRET", "sparkquill-mcp-oauth-test-secret")
	t.Setenv("PUBLIC_URL", "https://sparkquill.example")
	store, err := openMCPOAuthStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := t.Context()
	client, err := store.RegisterClient(ctx, "Test learning app", []string{"https://client.example/callback"})
	if err != nil {
		t.Fatal(err)
	}
	verifier := strings.Repeat("a", 43)
	sum := sha256.Sum256([]byte(verifier))
	resource := "https://sparkquill.example" + externalMCPPath
	requestID, err := store.SaveRequest(ctx, mcpOAuthRequest{ClientID: client.ID, RedirectURI: "https://client.example/callback", Resource: resource, State: "test", Scopes: slices.Clone(sparkquillScopes), Challenge: base64.RawURLEncoding.EncodeToString(sum[:]), ExpiresAt: time.Now().Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	parent := *c
	parent.AccessToken = nil
	_, code, err := store.Decide(ctx, requestID, &parent, true)
	if err != nil {
		t.Fatal(err)
	}
	grant, access, _, err := store.ExchangeCode(ctx, code, client.ID, "https://client.example/callback", resource, verifier)
	if err != nil {
		t.Fatal(err)
	}
	handler := AuthMiddleware(http.HandlerFunc(f.api.handleExternalMCP))
	call := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", externalMCPPath, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_api_spec","arguments":{}}}`))
		r.Header.Set("Authorization", "Bearer "+access)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	w := call()
	if w.Code != 200 || !strings.Contains(w.Body.String(), "create_file") || strings.Contains(w.Body.String(), "list_workflows") {
		t.Fatalf("OAuth catalog: %d %s", w.Code, w.Body.String())
	}
	if err := store.RevokeFamily(ctx, grant.FamilyID, c.UserID); err != nil {
		t.Fatal(err)
	}
	if w := call(); w.Code != 401 {
		t.Fatalf("revoked connection still works: %d", w.Code)
	}
}

func TestSparkQuillJobsPersistResultsAndRetryWithoutRerunning(t *testing.T) {
	f, c := newSparkQuillExternalFixture(t)
	args := map[string]any{"format": "book", "brief": "Make a fractions book", "request_id": "one-creation"}
	data, _ := json.Marshal(map[string]any{"tool": "create_learning_activity", "arguments": args})
	job := sparkquillExternalJob{ID: "sq_job_" + wf.Revision([]byte(c.UserID+"\x00"+c.AccessToken.ID+"\x00one-creation")), UserID: c.UserID, ConnectionID: c.AccessToken.ID, Fingerprint: wf.Revision(data), Status: "running", StartedAt: time.Now()}
	sparkquillJobs.Lock()
	sparkquillJobs.active[c.UserID] = job.ID
	sparkquillJobs.Unlock()
	executeSparkQuillJob(t.Context(), job, func(context.Context) (internalSessionTurnResult, error) {
		return internalSessionTurnResult{FinalResponse: "Book ready: activities/fractions/book.html"}, nil
	})
	saved, err := readSparkQuillJob(job.ID)
	if err != nil || saved.Status != "completed" || saved.CompletedAt == nil || sparkquillJobActive(saved) {
		t.Fatalf("completed receipt: %+v %v", saved, err)
	}
	retry := sparkquillTestCall(t, f.api, c, "create_learning_activity", args)
	if retry.Code != 200 || !strings.Contains(retry.Body.String(), "Book ready") {
		t.Fatalf("retry: %d %s", retry.Code, retry.Body.String())
	}
	args["brief"] = "Different book"
	conflict := sparkquillTestCall(t, f.api, c, "create_learning_activity", args)
	if conflict.Code != 409 {
		t.Fatalf("changed retry: %d %s", conflict.Code, conflict.Body.String())
	}
	status := sparkquillTestCall(t, f.api, c, "get_activity_status", map[string]any{"job_id": job.ID})
	if status.Code != 200 || !strings.Contains(status.Body.String(), "Fractions book") {
		t.Fatalf("job preview: %s", status.Body.String())
	}
	job.ID = "sq_job_" + strings.Repeat("b", 64)
	executeSparkQuillJob(t.Context(), job, func(context.Context) (internalSessionTurnResult, error) {
		return internalSessionTurnResult{}, errors.New("model unavailable")
	})
	saved, err = readSparkQuillJob(job.ID)
	if err != nil || saved.Status != "error" || saved.Error != "model unavailable" {
		t.Fatalf("failure receipt: %+v %v", saved, err)
	}
}

func TestSparkQuillExternalFreshFamily(t *testing.T) {
	f, c := newSparkQuillExternalFixture(t)
	root := filepath.Join(f.docs, "_users/owner/Chats/SparkQuill")
	for _, dir := range []string{"materials", "activities", "reports", "memory"} {
		if err := os.RemoveAll(filepath.Join(root, dir)); err != nil {
			t.Fatal(err)
		}
	}
	for _, tool := range []string{"get_learning_context", "get_learning_progress", "list_activities", "list_materials"} {
		w := sparkquillTestCall(t, f.api, c, tool, nil)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", tool, w.Code, w.Body.String())
		}
	}
}

func TestSparkQuillExternalFinalizeRejectsSymlinkTargets(t *testing.T) {
	f, c := newSparkQuillExternalFixture(t)
	root := "_users/owner/Chats/SparkQuill"
	f.write(t, root+"/activities/unsafe/source.SQ.HTML", "<h1>Lesson</h1>")
	for _, filename := range []string{"activity.json", "product.json", "source.html"} {
		target := filepath.Join(f.docs, root, "activities/unsafe", filename)
		if err := os.Symlink(filepath.Join(f.docs, root, "materials/lesson.md"), target); err != nil {
			t.Fatal(err)
		}
		w := sparkquillTestCall(t, f.api, c, "finalize_activity", map[string]any{"dir": "activities/unsafe", "title": "Lesson", "items": []any{"source.SQ.HTML"}})
		if w.Code != 403 {
			t.Fatalf("%s: %d %s", filename, w.Code, w.Body.String())
		}
		if err := os.Remove(target); err != nil {
			t.Fatal(err)
		}
	}
}
