package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
}
