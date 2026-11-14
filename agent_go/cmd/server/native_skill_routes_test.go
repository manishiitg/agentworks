package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// PLAT-746-1008: the real workspace edge may copy only an explicitly selected
// skill from this owned project, never another person's source or a symlink.
func TestNativeSkillDiscoveryImportKeepsOwnerAndFileBoundaries(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	f, _ := newExternalCrewCommandFixture(t)
	var install struct {
		WorkspacePath string            `json:"workspace_path"`
		Name          string            `json:"name"`
		Files         map[string][]byte `json:"files"`
	}
	boundary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/skills/workspace/import" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&install); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		w.WriteHeader(200)
	}))
	defer boundary.Close()
	t.Setenv("WORKSPACE_API_URL", boundary.URL)
	roots, bound, err := crewStorageRoots(externalCommandCrewRoot)
	if err != nil || !bound {
		t.Fatalf("roots: %v %t", err, bound)
	}
	original := "---\nname: review\ndescription: Review a change\n---\nRead references/rules.md before reviewing.\n"
	recognitionWrite(t, filepath.Join(roots.Files, ".agents/skills/review/SKILL.md"), original)
	recognitionWrite(t, filepath.Join(roots.Files, ".agents/skills/review/references/rules.md"), "Review the actual diff.")
	recognitionWrite(t, filepath.Join(roots.Files, "outside-secret"), "must not read")
	recognitionWrite(t, filepath.Join(roots.Files, ".agents/skills/linked/placeholder"), "")
	if err := os.Symlink(filepath.Join(roots.Files, "outside-secret"), filepath.Join(roots.Files, ".agents/skills/linked/SKILL.md")); err != nil {
		t.Fatal(err)
	}
	call := func(user, method, body, id string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, "/api/skills/discover?workspace_path="+url.QueryEscape(externalCommandCrewRoot)+"&provider=claude-code&id="+id, strings.NewReader(body))
		request = request.WithContext(context.WithValue(request.Context(), UserContextKey, &UserClaims{UserID: user, Username: user}))
		response := httptest.NewRecorder()
		f.api.nativeSkillsRoute(method == http.MethodPost)(response, request)
		return response
	}
	listed := call("owner", http.MethodGet, "", "")
	var inventory struct {
		Skills []skills.DiscoveredSkill `json:"skills"`
	}
	if listed.Code != 200 || json.Unmarshal(listed.Body.Bytes(), &inventory) != nil || len(inventory.Skills) != 1 {
		t.Fatalf("list: %d %s", listed.Code, listed.Body.String())
	}
	if strings.Contains(listed.Body.String(), "Read references") || strings.Contains(listed.Body.String(), "must not read") {
		t.Fatal("list returned document or linked secret")
	}
	id := inventory.Skills[0].ID
	for _, kind := range []string{"own-project", "different-project", "limited-files"} {
		claims := crewWriter("owner")
		claims.AccessToken.AllCrews = false
		claims.AccessToken.CrewIDs = []string{roots.ID}
		if kind == "different-project" {
			claims.AccessToken.CrewIDs = []string{"another-crew"}
		}
		if kind == "limited-files" {
			claims.AccessToken.FileGuard = &wf.FolderGuard{}
		}
		request := httptest.NewRequest(http.MethodGet, "/api/skills/discover?workspace_path="+url.QueryEscape(externalCommandCrewRoot)+"&provider=claude-code", nil)
		request = request.WithContext(context.WithValue(request.Context(), UserContextKey, claims))
		response := httptest.NewRecorder()
		f.api.nativeSkillsRoute(false)(response, request)
		if kind == "own-project" && (response.Code != 200 || !strings.Contains(response.Body.String(), id)) {
			t.Fatalf("allowed token missing source: %d %s", response.Code, response.Body.String())
		}
		if kind != "own-project" && response.Code != 403 {
			t.Fatalf("%s token read native source: %d %s", kind, response.Code, response.Body.String())
		}
	}
	preview := call("owner", http.MethodGet, "", id)
	if preview.Code != 200 || !strings.Contains(preview.Body.String(), "Read references") {
		t.Fatalf("preview: %d %s", preview.Code, preview.Body.String())
	}
	foreign := call("other", http.MethodPost, `{"id":"`+id+`"}`, "")
	if foreign.Code == 200 {
		t.Fatalf("foreign imported: %s", foreign.Body.String())
	}
	imported := call("owner", http.MethodPost, `{"id":"`+id+`"}`, "")
	if imported.Code != 200 {
		t.Fatalf("import: %d %s", imported.Code, imported.Body.String())
	}
	if install.WorkspacePath != externalCommandCrewRoot || install.Name != "review" || string(install.Files["SKILL.md"]) != original || string(install.Files["references/rules.md"]) != "Review the actual diff." {
		t.Fatalf("wrong scoped import: %+v", install)
	}
	after, _ := os.ReadFile(filepath.Join(roots.Files, ".agents/skills/review/SKILL.md"))
	if string(after) != original {
		t.Fatal("import modified source")
	}
	// Folder upload also uses the same scoped importer; preview never writes,
	// browser filenames are not host paths, and traversal is refused.
	install.Name = ""
	folderCall := func(user string, copying bool, names []string) *httptest.ResponseRecorder {
		t.Helper()
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		paths, _ := json.Marshal(names)
		_ = form.WriteField("paths", string(paths))
		for i, content := range []string{original, "Review the actual diff."} {
			field, err := form.CreateFormFile(fmt.Sprintf("file%d", i), "upload")
			if err != nil {
				t.Fatal(err)
			}
			_, _ = field.Write([]byte(content))
		}
		form.Close()
		request := httptest.NewRequest(http.MethodPost, "/api/skills/import-folder?workspace_path="+url.QueryEscape(externalCommandCrewRoot), &body)
		request.Header.Set("Content-Type", form.FormDataContentType())
		request = request.WithContext(context.WithValue(request.Context(), UserContextKey, &UserClaims{UserID: user, Username: user}))
		response := httptest.NewRecorder()
		workspaceSkillRoute(copying, skillFolderUpload(copying))(response, request)
		return response
	}
	names := []string{"review/SKILL.md", "review/references/rules.md"}
	if result := folderCall("owner", false, names); result.Code != 200 || !strings.Contains(result.Body.String(), "Read references") || install.Name != "" {
		t.Fatalf("folder preview wrote or failed: %d %s", result.Code, result.Body.String())
	}
	if result := folderCall("other", true, names); result.Code != 403 || install.Name != "" {
		t.Fatalf("foreign folder import: %d %s", result.Code, result.Body.String())
	}
	if result := folderCall("owner", true, []string{"review/SKILL.md", "review/../escape"}); result.Code != 400 || install.Name != "" {
		t.Fatalf("traversal folder import: %d %s", result.Code, result.Body.String())
	}
	if result := folderCall("owner", true, names); result.Code != 200 || install.Name != "review" || string(install.Files["references/rules.md"]) != "Review the actual diff." {
		t.Fatalf("folder copy: %d %s", result.Code, result.Body.String())
	}
}

// The public MCP Skills actions use the same own-project native file boundary
// as the screen, not a second catalog or an arbitrary host path argument.
func TestNativeSkillDiscoveryThroughExistingSkillsMCP(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	f, _ := newExternalCrewCommandFixture(t)
	roots, _, err := crewStorageRoots(externalCommandCrewRoot)
	if err != nil {
		t.Fatal(err)
	}
	recognitionWrite(t, filepath.Join(roots.Files, ".agents/skills/review/SKILL.md"), "---\nname: review\ndescription: Review changes\n---\nRead the real diff.")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	srv := serveExternalMCP(t, f.api, crewWriter("owner"))
	cli := dialExternalMCP(t, ctx, srv.URL+externalMCPPath)
	initializeExternalMCP(t, ctx, cli)
	result := callRemoteTool(t, ctx, cli, "call_tool", map[string]any{"name": "skills", "arguments": map[string]any{"crew_id": "team", "action": "native_discover", "provider": "claude-code"}})
	requireRemoteSuccess(t, result, "native_discover")
	listed := marshalStructured(t, result)
	if !strings.Contains(listed, "review") || strings.Contains(listed, "Read the real diff") {
		t.Fatal("MCP discovery lost source or read instructions implicitly", listed)
	}
	// Direct HTTP application edge exposes the same selector as the MCP result.
	r := httptest.NewRequest(http.MethodGet, "/api/skills/discover?workspace_path="+url.QueryEscape(externalCommandCrewRoot)+"&provider=claude-code", nil)
	r = r.WithContext(context.WithValue(r.Context(), UserContextKey, crewWriter("owner")))
	w := httptest.NewRecorder()
	f.api.nativeSkillsRoute(false)(w, r)
	var rows struct {
		Skills []skills.DiscoveredSkill `json:"skills"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &rows)
	if w.Code != 200 || len(rows.Skills) != 1 || !strings.Contains(listed, rows.Skills[0].ID) {
		t.Fatal("MCP and screen returned different source identity", w.Body.String(), listed)
	}
	preview := callRemoteTool(t, ctx, cli, "call_tool", map[string]any{"name": "skills", "arguments": map[string]any{"crew_id": "team", "action": "native_discover", "provider": "claude-code", "id": rows.Skills[0].ID}})
	requireRemoteSuccess(t, preview, "native preview")
	if !strings.Contains(marshalStructured(t, preview), "Read the real diff") {
		t.Fatal("explicit preview did not read instructions")
	}
	// Consent for another Crew cannot reuse the captured source selector.
	foreign := crewWriter("owner")
	foreign.AccessToken.AllCrews = false
	foreign.AccessToken.CrewIDs = []string{"other-crew"}
	body, _ := json.Marshal(map[string]any{"name": "skills", "arguments": map[string]any{"crew_id": "team", "action": "native_discover", "provider": "claude-code"}})
	w = httptest.NewRecorder()
	f.api.handleExternalCall(w, adminRequest(http.MethodPost, "/api/external/call", string(body), foreign, nil))
	if w.Code != 403 {
		t.Fatalf("wrong Crew token read native source: %d %s", w.Code, w.Body.String())
	}
}
