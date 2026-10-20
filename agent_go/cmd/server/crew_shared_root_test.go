package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

const (
	fixtureLegacyCrewFolder = "beta-b2c3d4e5"
	fixtureLegacyCrewID     = "b2c3d4e5-0000"
)

// withLegacyCrew adds a second Crew owned by A that stays in A's own tree, next to the fixture's (shared-root) Crew:
// the half-migrated state a server is in while the move runs.
func (f *multiUserFixture) withLegacyCrew() string {
	f.t.Helper()
	root := workspaceref.PhysicalPath(fixtureUserA, workspaceref.CrewProjectsRoot, fixtureLegacyCrewFolder)
	f.put(root+"/product.json", `{"schema_version":1,"product":"work","id":"`+fixtureLegacyCrewID+`","title":"Beta","session_id":"work:project:`+fixtureLegacyCrewID+`","owner_id":"`+fixtureUserA+`"}`)
	f.put(root+"/workflow.json", `{"schema_version":1,"id":"`+fixtureLegacyCrewID+`","label":"Beta","capabilities":{}}`)
	f.put(root+"/code/notes.md", "beta notes")
	migrateProductOwners(f.Docs)
	// This fixture represents a pre-existing shared Crew. A new registration
	// after the audience baseline correctly starts private, so name its
	// historical audience explicitly rather than relying on creation defaults.
	if err := defaultProjectOwners().SetAudience("work", fixtureLegacyCrewFolder, false, nil); err != nil {
		f.t.Fatal(err)
	}
	return root
}

// A mixed server: some Crews moved to Crew/<id>, some not. Every access rule holds for both kinds, and the owner's
// listings, schedule discovery and webhook discovery find both.
func TestMixedCrewLayoutSomeMovedSomeNot(t *testing.T) {
	f := newMultiUserFixture(t, sharedIdentityLayout())
	legacy := f.withLegacyCrew()
	shared := f.Layout.CrewPhysical(fixtureUserA, fixtureCrewFolder)
	f.WithSharing(true)

	for _, tc := range []struct{ id, root string }{{fixtureCrewID, shared}, {fixtureLegacyCrewID, legacy}} {
		own, err := resolveCrewProjectBinding(f.Ctx(fixtureUserA), fixtureUserA, f.Crew, tc.id, "")
		if err != nil || !own.OwnedByCaller || own.Binding.WorkspacePath != tc.root {
			t.Fatalf("A's binding of %s = %+v err=%v, want the owner's at %s", tc.id, own, err, tc.root)
		}
		reader, err := resolveCrewProjectBinding(f.Ctx(fixtureUserB), fixtureUserB, f.Crew, tc.id, tc.root)
		if err != nil || reader.OwnedByCaller || reader.OwnerID != fixtureUserA || reader.Binding.WorkspacePath != tc.root {
			t.Fatalf("B's binding of %s = %+v err=%v, want a reader of A's at %s", tc.id, reader, err, tc.root)
		}
		if got, err := resolveCrewProjectBinding(f.Ctx(fixtureUserC), fixtureUserC, f.Crew, tc.id, tc.root); err == nil && got.OwnedByCaller {
			t.Fatalf("C owns %s", tc.id)
		}
		if status := f.Proxy(fixtureUserB, http.MethodGet, tc.root+"/code/notes.md"); status != http.StatusForbidden {
			t.Errorf("raw proxy let B read %s: %d", tc.root, status)
		}
		if status := f.Proxy(fixtureUserC, http.MethodPut, tc.root+"/code/notes.md"); status != http.StatusForbidden {
			t.Errorf("raw proxy let C write %s: %d", tc.root, status)
		}
		if status := f.Proxy(fixtureUserA, http.MethodPut, tc.root+"/code/notes.md"); status != 0 {
			t.Errorf("raw proxy refused A's own write to %s: %d", tc.root, status)
		}
		if owner, ok := crewProjectOwnerID(tc.root); !ok || owner != fixtureUserA {
			t.Errorf("crewProjectOwnerID(%s) = %q ok=%v", tc.root, owner, ok)
		}
		if !crewProjectOwnedByCaller(fixtureUserA, tc.root) || crewProjectOwnedByCaller(fixtureUserB, tc.root) {
			t.Errorf("crewProjectOwnedByCaller(%s) is wrong", tc.root)
		}
		if !isCrewProjectPath(tc.root) {
			t.Errorf("isCrewProjectPath(%s) = false", tc.root)
		}
	}

	// One listing of the owner's Crews finds both kinds; B's lists none of A's (their own tree has none and they own
	// nothing at the shared root).
	paths, exists, err := listProjectManifestPaths(f.Ctx(fixtureUserA), defaultProductProjectStore(), fixtureUserA, f.Crew)
	if err != nil || !exists || len(paths) != 2 {
		t.Fatalf("A's Crew manifests = %v exists=%v err=%v", paths, exists, err)
	}
	if paths, _, _ := listProjectManifestPaths(f.Ctx(fixtureUserB), defaultProductProjectStore(), fixtureUserB, f.Crew); len(paths) != 0 {
		t.Fatalf("B's Crew manifests = %v", paths)
	}
	items, err := listAccessibleCrewProjects(f.Ctx(fixtureUserA), fixtureUserA, "")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, item := range items {
		seen[item["id"].(string)] = item["workspace_path"].(string)
	}
	if seen[fixtureCrewID] != shared || seen[fixtureLegacyCrewID] != workspaceref.CrewProjectsRoot+"/"+fixtureLegacyCrewFolder {
		t.Fatalf("accessible Crews = %v", seen)
	}
	// No Crew is anyone else's by a sibling's name.
	if shared == legacy {
		t.Fatal("fixture is wrong")
	}
}

// Schedule and webhook discovery find a Crew at the shared root through its registered owner; a manifest that names
// someone else changes nothing.
func TestSharedCrewSchedulesAndWebhooksAreDiscoveredByTheirOwner(t *testing.T) {
	f := newMultiUserFixture(t, sharedIdentityLayout())
	f.WithSharing(true)
	crewRoot := f.Layout.CrewPhysical(fixtureUserA, fixtureCrewFolder)
	f.put(crewRoot+"/workflow.json", `{"schema_version":1,"id":"`+fixtureCrewID+`","label":"Alpha","capabilities":{},`+
		`"schedules":[{"id":"daily","name":"Daily","enabled":true,"cron_expression":"0 8 * * *","timezone":"UTC","messages":["go"]}],`+
		`"triggers":[{"id":"11111111-1111-4111-8111-111111111111","name":"Hook","enabled":true,"message":"hi","kind":"webhook"}]}`)
	// The manifest claims B owns it: ignored.
	f.put(crewRoot+"/product.json", `{"schema_version":1,"product":"work","id":"`+fixtureCrewID+`","title":"Alpha","session_id":"work:project:`+fixtureCrewID+`","owner_id":"`+fixtureUserB+`"}`)

	registry := agentprofiles.NewRegistry()
	profile := f.Crew
	profile.UIPanels.Schedules = true
	profile.ResolvedFeatures = []agentprofiles.ResolvedFeature{{ID: "triggers"}}
	if err := registry.RegisterProfile(profile); err != nil {
		t.Fatal(err)
	}
	svc := NewProductScheduleService(nil, registry)
	svc.users = func(string) []string { return []string{fixtureUserA, fixtureUserB} }

	jobs, err := svc.projectJobsForUser(f.Ctx(fixtureUserA), fixtureUserA, profile, nil)
	if err != nil || len(jobs) != 1 || jobs[0].WorkspacePath != crewRoot || jobs[0].UserID != fixtureUserA {
		t.Fatalf("A's schedule jobs = %+v err=%v", jobs, err)
	}
	if jobs, err := svc.projectJobsForUser(f.Ctx(fixtureUserB), fixtureUserB, profile, nil); err != nil || len(jobs) != 0 {
		t.Fatalf("B (named by the manifest) got schedule jobs: %+v err=%v", jobs, err)
	}
	match, err := svc.findProductWebhook(f.Ctx(fixtureUserA), "11111111-1111-4111-8111-111111111111")
	if err != nil || match.UserID != fixtureUserA || match.Binding.WorkspacePath != crewRoot {
		t.Fatalf("webhook match = %+v err=%v", match, err)
	}
}

// Stored references to a Crew that has since moved keep working through the alias resolver, whichever way they
// spell the old place. One test per kind of stored reference this code base keeps.
func TestOldSpellingIsNotAWayIntoAnotherOwnersMovedCrew(t *testing.T) {
	f := newMultiUserFixture(t, sharedIdentityLayout())
	moved := f.Layout.CrewPhysical(fixtureUserA, fixtureCrewFolder)
	oldPhysical := workspaceref.PhysicalPath(fixtureUserA, workspaceref.CrewProjectsRoot, fixtureCrewFolder)
	// A hostile alias in someone's hands is impossible (the registry is server-controlled), but even a registered alias
	// leads only to the registered owner's Crew: B naming A's old path as their own gets the shared Crew's real owner.
	if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: fixtureCrewFolder, OwnerID: fixtureUserA, Shared: true, Aliases: []string{oldPhysical}}); err != nil {
		t.Fatal(err)
	}
	ref, ok := resolveCrewPath(f.Ctx(fixtureUserB), fixtureUserB, oldPhysical+"/product.json")
	if !ok || ref.Root != moved || ref.OwnerID != fixtureUserA {
		t.Fatalf("resolved %+v ok=%v", ref, ok)
	}
	if crewAccessFor(f.Claims(fixtureUserB), ref) == crewAccessOwner {
		t.Fatal("B is the owner through an old path")
	}
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		if status := f.Proxy(fixtureUserB, method, oldPhysical+"/product.json"); status != http.StatusForbidden {
			t.Errorf("proxy %s as B through the old path: %d", method, status)
		}
	}
	if !strings.HasPrefix(moved, "Crew/") {
		t.Fatal("fixture is wrong")
	}
	_ = context.Background()
}

// The browser (or any stored link) still sends an old spelling of a moved Crew: the proxy judges the Crew it names,
// translates the path on the way out (a read finds the Crew's files; a write never creates a folder at the old place),
// and keeps refusing everyone who does not own it.
func TestProxyTranslatesOldSpellingsOfAMovedCrew(t *testing.T) {
	f := newMultiUserFixture(t, sharedIdentityLayout())
	moved := f.Layout.CrewPhysical(fixtureUserA, fixtureCrewFolder)
	oldPhysical := workspaceref.PhysicalPath(fixtureUserA, workspaceref.CrewProjectsRoot, fixtureCrewFolder)
	oldLogical := workspaceref.CrewProjectsRoot + "/" + fixtureCrewFolder
	if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: fixtureCrewFolder, OwnerID: fixtureUserA, Shared: true, Aliases: []string{oldPhysical}}); err != nil {
		t.Fatal(err)
	}

	var seen []string
	var seenMu sync.Mutex
	recorder := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		// Only this test's own calls count: a background goroutine of another test may reach the same URL.
		if r.Header.Get("X-Test-Call") != "" {
			seenMu.Lock()
			seen = append(seen, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery+" "+string(body))
			seenMu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer recorder.Close()
	t.Setenv("WORKSPACE_API_URL", recorder.URL)
	handler := workspaceProxyHandler()
	call := func(user, method, target, body string) (int, string) {
		seenMu.Lock()
		seen = nil
		seenMu.Unlock()
		var reader io.Reader
		if body != "" {
			reader = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, target, reader)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("X-Test-Call", "1")
		req = req.WithContext(f.Ctx(user))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		seenMu.Lock()
		defer seenMu.Unlock()
		return rec.Code, strings.Join(seen, "|")
	}

	for _, p := range []string{oldPhysical, oldLogical, moved} {
		code, got := call(fixtureUserA, http.MethodGet, "/api/wp/api/documents/"+p+"/code/notes.md", "")
		if code != http.StatusOK || !strings.HasPrefix(got, "GET /api/documents/"+moved+"/code/notes.md?") {
			t.Errorf("owner GET %s: %d, workspace saw %q", p, code, got)
		}
	}
	code, got := call(fixtureUserA, http.MethodPost, "/api/wp/api/folders", `{"folder_path":"`+oldLogical+`/new-folder"}`)
	if code != http.StatusOK || !strings.Contains(got, `"folder_path":"`+moved+`/new-folder"`) {
		t.Errorf("owner POST folder by the old path: %d, workspace saw %q", code, got)
	}
	code, got = call(fixtureUserA, http.MethodGet, "/api/wp/api/documents?folder="+url.QueryEscape(oldLogical)+"&max_depth=2", "")
	if code != http.StatusOK || !strings.Contains(got, "folder="+url.QueryEscape(moved)) {
		t.Errorf("owner listing by the old path: %d, workspace saw %q", code, got)
	}
	// Nobody else: the old spellings are not a way in, and the workspace never sees the request.
	for _, user := range []string{fixtureUserB, fixtureUserC} {
		for _, p := range []string{oldPhysical, moved} {
			for _, method := range []string{http.MethodGet, http.MethodPut} {
				code, got := call(user, method, "/api/wp/api/documents/"+p+"/code/notes.md", `{"content":"x"}`)
				if code != http.StatusForbidden || got != "" {
					t.Errorf("%s %s %s: %d, workspace saw %q", user, method, p, code, got)
				}
			}
		}
	}
	// B's short spelling is B's OWN (empty) tree: forwarded untouched, never translated to A's Crew.
	if code, got := call(fixtureUserB, http.MethodGet, "/api/wp/api/documents/"+oldLogical+"/code/notes.md", ""); code != http.StatusOK || !strings.HasPrefix(got, "GET /api/documents/"+oldLogical+"/code/notes.md?") {
		t.Errorf("B's own-tree path: %d, workspace saw %q", code, got)
	}
}

// The owner's list of their Crews at the shared root: only their own, by the registry, never the manifest's claim.
func TestOwnSharedProjectsEndpointListsOnlyTheCallersCrews(t *testing.T) {
	f := newMultiUserFixture(t, sharedIdentityLayout())
	crewRoot := f.Layout.CrewPhysical(fixtureUserA, fixtureCrewFolder)
	// A second shared Crew owned by B, and one whose manifest CLAIMS A but is registered to B.
	f.put("Crew/b-crew-11111111/product.json", `{"schema_version":1,"product":"work","id":"b1","title":"B","session_id":"work:project:b1","owner_id":"`+fixtureUserB+`"}`)
	f.put("Crew/forged-22222222/product.json", `{"schema_version":1,"product":"work","id":"x1","title":"Forged","session_id":"work:project:x1","owner_id":"`+fixtureUserA+`"}`)
	for folder, owner := range map[string]string{"b-crew-11111111": fixtureUserB, "forged-22222222": fixtureUserB} {
		if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: folder, OwnerID: owner, Shared: true}); err != nil {
			t.Fatal(err)
		}
	}
	// A hidden entry and a Crew nobody registered are listed for no one.
	f.put("Crew/.migrating/stray/product.json", `{"schema_version":1,"product":"work","id":"s","title":"Stray","session_id":"s","owner_id":"`+fixtureUserA+`"}`)
	f.put("Crew/unregistered-33333333/product.json", `{"schema_version":1,"product":"work","id":"u","title":"U","session_id":"u","owner_id":"`+fixtureUserA+`"}`)

	get := func(user string) (int, []map[string]interface{}) {
		req := httptest.NewRequest(http.MethodGet, "/api/agent-profiles/work/own-shared-projects", nil)
		req = req.WithContext(f.Ctx(user))
		req = mux.SetURLVars(req, map[string]string{"id": "work"})
		rec := httptest.NewRecorder()
		f.API.handleListOwnSharedProjects(rec, req)
		var body struct {
			Projects []map[string]interface{} `json:"projects"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return rec.Code, body.Projects
	}
	if code, rows := get(fixtureUserA); code != 200 || len(rows) != 1 || rows[0]["workspace_path"] != crewRoot || rows[0]["id"] != fixtureCrewID {
		t.Fatalf("A's own shared Crews: %d %v", code, rows)
	}
	if code, rows := get(fixtureUserB); code != 200 || len(rows) != 2 {
		t.Fatalf("B's own shared Crews: %d %v", code, rows)
	}
	if code, rows := get(fixtureUserC); code != 200 || len(rows) != 0 {
		t.Fatalf("C (no Crew product) got %d %v", code, rows)
	}
}

// With project sharing off (AGENTWORKS_PROJECT_SHARING=off) another user's Crew is invisible to everyone but its owner:
// no reader access, no live-feed notices.
func TestCrewAccessReaderNeedsProjectSharing(t *testing.T) {
	f := newMultiUserFixture(t, sharedIdentityLayout())
	ref := crewPathRef{Root: "Crew/" + fixtureCrewFolder, OwnerID: fixtureUserA, Shared: true}
	f.WithSharing(true)
	if crewAccessFor(f.Claims(fixtureUserB), ref) != crewAccessReader || !newLiveFeedAccess(f.Claims(fixtureUserB)).visible(f.Ctx(fixtureUserB), ref.Root) {
		t.Fatal("with sharing on, a user with the Crew product is a reader")
	}
	// The owner can keep one Crew to themselves while sharing is on (PLAT-725).
	if err := defaultProjectOwners().SetPrivate("work", fixtureCrewFolder, true); err != nil {
		t.Fatal(err)
	}
	if crewAccessFor(f.Claims(fixtureUserB), ref) != crewAccessNone || newLiveFeedAccess(f.Claims(fixtureUserB)).visible(f.Ctx(fixtureUserB), ref.Root) {
		t.Fatal("another user still has access to a Crew its owner made private")
	}
	if got, err := resolveCrewProjectBinding(f.Ctx(fixtureUserB), fixtureUserB, f.Crew, fixtureCrewID, ref.Root); err == nil {
		t.Fatalf("another user opened a private Crew: %+v", got)
	}
	if crewAccessFor(f.Claims(fixtureUserA), ref) != crewAccessOwner {
		t.Fatal("the owner lost access to their private Crew")
	}
	if err := defaultProjectOwners().SetPrivate("work", fixtureCrewFolder, false); err != nil {
		t.Fatal(err)
	}
	f.WithSharing(false)
	if crewAccessFor(f.Claims(fixtureUserB), ref) != crewAccessNone {
		t.Fatal("with sharing off, another user has access to a private Crew")
	}
	if newLiveFeedAccess(f.Claims(fixtureUserB)).visible(f.Ctx(fixtureUserB), ref.Root) {
		t.Fatal("with sharing off, another user sees a private Crew's notices")
	}
	if crewAccessFor(f.Claims(fixtureUserA), ref) != crewAccessOwner || !newLiveFeedAccess(f.Claims(fixtureUserA)).visible(f.Ctx(fixtureUserA), ref.Root) {
		t.Fatal("the owner lost access when sharing is off")
	}
}
