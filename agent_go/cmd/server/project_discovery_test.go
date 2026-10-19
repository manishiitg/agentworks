package server

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
			// A missing current manifest cannot redirect deletion to the old location.
			delete(f.Mock.files, current+"/product.json")
			if got, err := resolveProductProjectBinding(ctx, fixtureUserA, profile, id); err == nil {
				t.Fatalf("resolved retired root: %+v", got)
			}
		})
	}
}
