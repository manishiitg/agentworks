package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

func TestMovedProjectDiscoveryUsesOnlyRegisteredRoot(t *testing.T) {
	for _, origin := range []string{"work", "code"} {
		t.Run(origin, func(t *testing.T) {
			f := newMultiUserFixture(t, legacyIdentityLayout())
			profile, folder, id := f.Crew, fixtureCrewFolder, fixtureCrewID
			if origin == "code" {
				profile, folder, id = f.Code, fixtureCodeFolder, fixtureCodeID
			}
			product, _ := projectProductForProfile(origin)
			old := workspaceref.PhysicalPathOf(fixtureUserA, product.ProjectsRoot, folder)
			current := workspaceref.SharedProjectPath(folder)
			if err := os.MkdirAll(filepath.Join(f.Docs, "Crew"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(filepath.Join(f.Docs, old), filepath.Join(f.Docs, current)); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(f.Docs, current), filepath.Join(f.Docs, old)); err != nil {
				t.Fatal(err)
			}
			// The workspace listing follows the old link and reports both spellings.
			for path, raw := range f.Mock.files {
				if strings.HasPrefix(path, old+"/") {
					f.Mock.files[current+strings.TrimPrefix(path, old)] = raw
				}
			}
			if err := defaultProjectOwners().modify(func(all map[string]projectOwnerRecord) (bool, error) {
				key := projectOwnerKey(origin, folder)
				rec := all[key]
				rec.CanonicalRoot, rec.Shared, rec.BindingRoot, rec.Aliases = current, true, old, []string{old}
				all[key] = rec
				return true, nil
			}); err != nil {
				t.Fatal(err)
			}
			resetCrewLocationCaches()
			ctx := f.Ctx(fixtureUserA)
			paths, _, err := listProjectManifestPaths(ctx, defaultProductProjectStore(), fixtureUserA, profile)
			if err != nil || len(paths) != 1 || paths[0] != current+"/product.json" {
				t.Fatalf("manifests=%v err=%v", paths, err)
			}
			rows := listProjectsForOwner(ctx, f.Claims(fixtureUserA), profile, fixtureUserA, false)
			if len(rows) != 1 || rows[0].ID != id || rows[0].WorkspacePath != current {
				t.Fatalf("directory=%+v", rows)
			}
			binding, err := resolveProductProjectBinding(ctx, fixtureUserA, profile, id)
			if err != nil || binding.WorkspacePath != current {
				t.Fatalf("project-ID/delete binding=%+v err=%v", binding, err)
			}
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest("POST", "/", nil).WithContext(ctx)
			f.API.externalWorkspaceCall(recorder, request, map[string]any{"action": "list"})
			w := recorder.Body.Bytes()
			var result struct {
				Workspaces []struct {
					ID string `json:"id"`
				} `json:"workspaces"`
			}
			if err := json.Unmarshal(w, &result); err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, item := range result.Workspaces {
				if item.ID == id {
					count++
				}
			}
			if count != 1 {
				t.Fatalf("MCP listed project %d times: %s", count, w)
			}
			// The sidebar uses this same list, with the registered old spelling
			// supplied only to the signed-in app for grouping existing chats.
			nav := httptest.NewRecorder()
			f.API.handleWorkspaces(nav, httptest.NewRequest("POST", "/api/workspaces", strings.NewReader(`{"action":"list","detail":"navigation"}`)).WithContext(ctx))
			var navigation struct {
				Workspaces []struct {
					WorkspaceID string   `json:"workspace_id"`
					Path        string   `json:"workspace_path"`
					Aliases     []string `json:"aliases"`
				} `json:"workspaces"`
			}
			if nav.Code != 200 || json.Unmarshal(nav.Body.Bytes(), &navigation) != nil {
				t.Fatalf("navigation: %d %s", nav.Code, nav.Body.String())
			}
			count = 0
			for _, item := range navigation.Workspaces {
				if item.WorkspaceID == origin+":"+id {
					count++
					if item.Path != current || len(item.Aliases) != 1 || item.Aliases[0] != canonicalChatHistoryWorkspacePath(fixtureUserA, old) {
						t.Fatalf("wrong navigation identity: %+v", item)
					}
				}
			}
			if count != 1 || strings.Contains(string(w), "workspace_path") {
				t.Fatalf("navigation count=%d or MCP shape changed: %s", count, w)
			}
			if items, err := listWorkspaceProjectsForOwner(ctx, fixtureUserA, fixtureUserA, "", product); err != nil || len(items) != 1 {
				t.Fatalf("Builder directory=%v err=%v", items, err)
			}
			// A missing current manifest cannot redirect deletion to the old location.
			delete(f.Mock.files, current+"/product.json")
			if got, err := resolveProductProjectBinding(ctx, fixtureUserA, profile, id); err == nil {
				t.Fatalf("resolved retired root: %+v", got)
			}
			if err := os.RemoveAll(filepath.Join(f.Docs, current)); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(filepath.Join(f.Docs, old)); !os.IsNotExist(err) {
				t.Fatalf("old link remains after deletion: %v", err)
			}
		})
	}
}

// Owner navigation includes Local projects; publishing/reference discovery still
// excludes them, and a token cannot opt into the app's routing metadata.
func TestWorkspaceNavigationKeepsOwnerLocalAndRejectsTokenDetail(t *testing.T) {
	f := newMultiUserFixture(t, legacyIdentityLayout())
	root := legacyIdentityLayout().CodePhysical(fixtureUserA, fixtureCodeFolder)
	raw := strings.Replace(f.Mock.files[root+"/product.json"], `"product":"code"`, `"mode":"local","product":"code"`, 1)
	f.put(root+"/product.json", raw)
	request := func(claims *UserClaims, body string) *httptest.ResponseRecorder {
		out := httptest.NewRecorder()
		ctx := context.WithValue(context.Background(), UserContextKey, claims)
		f.API.handleWorkspaces(out, httptest.NewRequest("POST", "/api/workspaces", strings.NewReader(body)).WithContext(ctx))
		return out
	}
	owner := request(f.Claims(fixtureUserA), `{"action":"list","detail":"navigation"}`)
	if owner.Code != 200 || !strings.Contains(owner.Body.String(), `"workspace_id":"code:`+fixtureCodeID+`"`) || owner.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("owner Local navigation: %d %s", owner.Code, owner.Body.String())
	}
	other := request(f.Claims(fixtureUserB), `{"action":"list","detail":"navigation"}`)
	if other.Code != 200 || strings.Contains(other.Body.String(), fixtureCodeID) {
		t.Fatalf("other account saw Local: %d %s", other.Code, other.Body.String())
	}
	token := f.Claims(fixtureUserA)
	token.AccessToken = &accesstokens.Token{Scopes: []string{"code:run", "crews:read"}, AllCrews: true}
	if out := request(token, `{"action":"list","detail":"navigation"}`); out.Code != 403 {
		t.Fatalf("token got app detail: %d %s", out.Code, out.Body.String())
	}
	if out := request(token, `{"action":"list"}`); out.Code != 200 || strings.Contains(out.Body.String(), "workspace_path") {
		t.Fatalf("normal token list changed: %d %s", out.Code, out.Body.String())
	}
}
