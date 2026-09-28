package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/workspace/security"
	"github.com/spf13/viper"
)

type serverFixture struct {
	engine *gin.Engine
	root   string
}

const (
	ownerTok    = "owner-token"
	readerTok   = "reader-token"
	strangerTok = "stranger-token"
)

// newServerFixture builds a server-mode engine with two workflows:
// Workflow/shared (owner=alice, reader=bob) and Workflow/private (owner=carol).
func newServerFixture(t *testing.T) *serverFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	hash := func(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }
	users, _ := json.Marshal(map[string]interface{}{"users": map[string]string{
		"alice": hash(ownerTok), "bob": hash(readerTok), "mallory": hash(strangerTok),
	}})
	usersFile := filepath.Join(t.TempDir(), "users.json")
	if err := os.WriteFile(usersFile, users, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(serverUserTokensEnv, usersFile)
	t.Setenv(workspaceAPITokenEnv, "agent-go-shared-token")
	t.Setenv(remotePlacementFileEnv, filepath.Join(t.TempDir(), "none.json"))
	viper.Set("docs-dir", root)
	writeWorkflow(t, root, "shared", map[string]interface{}{"owners": []string{"alice"}, "readers": []string{"bob"}})
	writeWorkflow(t, root, "private", map[string]interface{}{"owners": []string{"carol"}, "readers": []string{}})
	return &serverFixture{engine: newWorkspaceEngine(root), root: root}
}

func writeWorkflow(t *testing.T, root, name string, access map[string]interface{}) {
	t.Helper()
	dir := filepath.Join(root, "Workflow", name)
	if err := os.MkdirAll(filepath.Join(dir, "planning"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest, _ := json.Marshal(map[string]interface{}{"label": name, "access": access})
	if err := os.WriteFile(filepath.Join(dir, "workflow.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "planning", "plan.json"), []byte(`{"steps":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (s *serverFixture) as(token, method, target, body string, headers ...string) *httptest.ResponseRecorder {
	return do(s.engine, method, target, body, append([]string{"X-Workspace-Token", token}, headers...)...)
}

func TestServerModeRequiresPerUserToken(t *testing.T) {
	s := newServerFixture(t)
	for _, tok := range []string{"", "nope", "agent-go-shared-token"} {
		if w := s.as(tok, http.MethodGet, "/api/documents/Workflow/shared/workflow.json", ""); w.Code != http.StatusUnauthorized {
			t.Fatalf("token %q: status %d, want 401", tok, w.Code)
		}
	}
	var health map[string]interface{}
	_ = json.Unmarshal(do(s.engine, http.MethodGet, "/health", "").Body.Bytes(), &health)
	if _, leaked := health["docs_dir"]; leaked {
		t.Fatal("server-mode /health must not reveal docs_dir")
	}
}

func TestServerModeWorkflowAccess(t *testing.T) {
	s := newServerFixture(t)
	read := "/api/documents/Workflow/shared/planning/plan.json"
	write := `{"content":"{\"steps\":[1]}"}`
	cases := []struct {
		name, token, method, target, body string
		want                              int
	}{
		{"owner reads", ownerTok, http.MethodGet, read, "", http.StatusOK},
		{"owner writes", ownerTok, http.MethodPut, read, write, http.StatusOK},
		{"reader reads", readerTok, http.MethodGet, read, "", http.StatusOK},
		{"reader cannot write", readerTok, http.MethodPut, read, write, http.StatusForbidden},
		{"stranger cannot read", strangerTok, http.MethodGet, read, "", http.StatusNotFound},
		{"owner cannot read another's workflow", ownerTok, http.MethodGet, "/api/documents/Workflow/private/workflow.json", "", http.StatusNotFound},
		{"missing and forbidden look the same", ownerTok, http.MethodGet, "/api/glob?pattern=**/*&folder=Workflow/not-there", "", http.StatusNotFound},
		{"stranger cannot write", strangerTok, http.MethodPut, read, write, http.StatusForbidden},
		{"no reach outside workflows", ownerTok, http.MethodGet, "/api/documents/config/users.json", "", http.StatusNotFound},
		{"traversal is refused", ownerTok, http.MethodGet, "/api/documents/Workflow/shared/../private/workflow.json", "", http.StatusNotFound},
		{"own user area is writable", ownerTok, http.MethodPost, "/api/documents", `{"filepath":"_users/alice/notes.md","content":"x"}`, http.StatusCreated},
		{"another user's area is not", ownerTok, http.MethodPost, "/api/documents", `{"filepath":"_users/bob/notes.md","content":"x"}`, http.StatusForbidden},
		{"copy out of a readable workflow into own area", readerTok, http.MethodPost, "/api/folders/copy", `{"source_path":"Workflow/shared","destination_path":"_users/bob/copy"}`, http.StatusOK},
		{"copy into a read-only workflow", readerTok, http.MethodPost, "/api/folders/copy", `{"source_path":"_users/bob/copy","destination_path":"Workflow/shared/x"}`, http.StatusForbidden},
		{"skills install is not served", ownerTok, http.MethodPost, "/api/skills/cli/install", `{}`, http.StatusForbidden},
		{"process management is not served", ownerTok, http.MethodPost, "/api/processes/cleanup", `{}`, http.StatusForbidden},
		{"remote export is workflow-only", ownerTok, http.MethodPost, "/api/remote/workflow/export", `{"workspace_path":"_users/alice"}`, http.StatusForbidden},
		{"laptop move API is not served", ownerTok, http.MethodPost, "/api/remote/move-to-server", `{}`, http.StatusForbidden},
		{"reader cannot execute", readerTok, http.MethodPost, "/api/execute", `{"command":"true","working_directory":"Workflow/shared"}`, http.StatusForbidden},
		{"execute outside a workflow", ownerTok, http.MethodPost, "/api/execute", `{"command":"true","working_directory":""}`, http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if w := s.as(tc.token, tc.method, tc.target, tc.body); w.Code != tc.want {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
		})
	}
}

func TestServerModeIgnoresSpoofedUser(t *testing.T) {
	s := newServerFixture(t)
	w := s.as(strangerTok, http.MethodGet, "/api/documents/Workflow/shared/workflow.json", "", "X-User-ID", "alice")
	if w.Code != http.StatusNotFound {
		t.Fatalf("spoofed X-User-ID must not grant access: %d", w.Code)
	}
	var who map[string]interface{}
	_ = json.Unmarshal(s.as(strangerTok, http.MethodGet, "/api/remote/whoami", "", "X-User-ID", "alice").Body.Bytes(), &who)
	if who["user"] != "mallory" {
		t.Fatalf("whoami = %v, want the token's user", who["user"])
	}
}

func TestServerModeFiltersListings(t *testing.T) {
	s := newServerFixture(t)
	got := listedWorkflows(t, s.as(readerTok, http.MethodGet, "/api/documents?folder=Workflow&max_depth=1", "").Body.Bytes())
	if _, ok := got["Workflow/shared"]; !ok {
		t.Fatalf("readable workflow missing: %v", got)
	}
	if _, ok := got["Workflow/private"]; ok {
		t.Fatalf("unreadable workflow leaked into listing: %v", got)
	}
	body := s.as(readerTok, http.MethodGet, "/api/glob?pattern=**/*.json&folder=Workflow", "").Body.String()
	if !strings.Contains(body, "Workflow/shared/") || strings.Contains(body, "Workflow/private/") {
		t.Fatalf("glob not filtered: %s", body)
	}
}

func TestServerModePinsExecuteToWorkflowFolder(t *testing.T) {
	if !security.CurrentSandboxCapability().Available {
		t.Skip("no sandbox on this machine; server mode refuses execution")
	}
	s := newServerFixture(t)
	// The client asks for a wide guard; the server must replace it.
	body := `{"command":"cat ../private/workflow.json","working_directory":"Workflow/shared","use_shell":true,
		"folder_guard":{"enabled":true,"read_paths":["Workflow"],"write_paths":["Workflow"]},
		"extra_env":{"SECRET_X":"leak"}}`
	w := s.as(ownerTok, http.MethodPost, "/api/execute", body)
	if strings.Contains(w.Body.String(), "carol") {
		t.Fatalf("command read another workflow through a client-supplied guard: %s", w.Body.String())
	}
	w = s.as(ownerTok, http.MethodPost, "/api/execute", `{"command":"cat planning/plan.json","working_directory":"Workflow/shared","use_shell":true}`)
	if !strings.Contains(w.Body.String(), "steps") {
		t.Fatalf("command could not read its own workflow: %d %s", w.Code, w.Body.String())
	}
}

func TestServerModeImportMakesImporterOwner(t *testing.T) {
	s := newServerFixture(t)
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	f, _ := zw.Create("workflow.json")
	_, _ = f.Write([]byte(`{"label":"moved","access":{"owners":["laptop-user"],"readers":[]}}`))
	_ = zw.Close()
	upload := func(token, workflow string) *httptest.ResponseRecorder {
		var form bytes.Buffer
		mw := multipart.NewWriter(&form)
		part, _ := mw.CreateFormFile("file", "moved.zip")
		_, _ = part.Write(archive.Bytes())
		_ = mw.WriteField("workspace_path", workflow)
		_ = mw.WriteField("overwrite", "true")
		_ = mw.Close()
		req := httptest.NewRequest(http.MethodPost, "/api/remote/workflow/import", &form)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.Header.Set("X-Workspace-Token", token)
		w := httptest.NewRecorder()
		s.engine.ServeHTTP(w, req)
		return w
	}
	if w := upload(strangerTok, "Workflow/private"); w.Code != http.StatusForbidden {
		t.Fatalf("overwriting someone else's workflow must be refused: %d", w.Code)
	}
	if w := upload(ownerTok, "Workflow/moved"); w.Code != http.StatusOK {
		t.Fatalf("import failed: %d %s", w.Code, w.Body.String())
	}
	raw, _ := os.ReadFile(filepath.Join(s.root, "Workflow/moved/workflow.json"))
	if !strings.Contains(string(raw), `"alice"`) || !strings.Contains(string(raw), `"laptop-user"`) {
		t.Fatalf("importer not added as owner (laptop owners kept): %s", raw)
	}
	if w := s.as(ownerTok, http.MethodGet, "/api/documents/Workflow/moved/workflow.json", ""); w.Code != http.StatusOK {
		t.Fatalf("importer cannot read the imported workflow: %d", w.Code)
	}
}

// TestMoveRoundTripAgainstServerMode moves a workflow to a real server-mode
// engine and back, through its authorization layer.
func TestMoveRoundTripAgainstServerMode(t *testing.T) {
	s := newServerFixture(t)
	srv := httptest.NewServer(s.engine)
	t.Cleanup(srv.Close)

	localRoot := t.TempDir()
	cfgPath := filepath.Join(t.TempDir(), "placement.json")
	t.Setenv(remotePlacementFileEnv, cfgPath)
	raw, _ := json.Marshal(map[string]interface{}{
		"servers":   map[string]interface{}{"team": map[string]string{"url": srv.URL, "token": ownerTok}},
		"workflows": map[string]string{},
	})
	if err := os.WriteFile(cfgPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(localRoot, "Workflow/moving")
	for name, content := range map[string]string{
		"workflow.json":                  `{"label":"moving","access":{"owners":["laptop-user"],"readers":[]}}`,
		"planning/plan.json":             `{"steps":[]}`,
		".sandbox-cache/home/.gitconfig": "secret",
	} {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rr := newRemoteRouter(localRoot)
	ctx := func() *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/api/remote/move", nil)
		return c
	}

	res, err := rr.moveToServer(ctx(), "Workflow/moving", "team")
	if err != nil {
		t.Fatalf("move to server: %v", err)
	}
	if res.Files != 2 {
		t.Fatalf("moved %d files, want 2", res.Files)
	}
	if _, err := os.Stat(filepath.Join(s.root, "Workflow/moving/.sandbox-cache")); !os.IsNotExist(err) {
		t.Fatal(".sandbox-cache travelled to the server")
	}
	manifest, _ := os.ReadFile(filepath.Join(s.root, "Workflow/moving/workflow.json"))
	if !strings.Contains(string(manifest), `"alice"`) {
		t.Fatalf("mover is not an owner on the server: %s", manifest)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("local copy was not retired")
	}

	res, err = rr.moveToLocal(ctx(), "Workflow/moving")
	if err != nil {
		t.Fatalf("move to local: %v", err)
	}
	if _, err := os.Stat(filepath.Join(localRoot, "Workflow/moving/planning/plan.json")); err != nil {
		t.Fatal("workflow not restored locally")
	}
	if !strings.HasPrefix(res.Retired, "server:_users/alice/moved-workflows/") {
		t.Fatalf("server copy archived at %q, want the mover's own area", res.Retired)
	}
	if _, err := os.Stat(filepath.Join(s.root, "Workflow/moving")); !os.IsNotExist(err) {
		t.Fatal("server copy still live after moving back")
	}
}
