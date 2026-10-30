package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

const (
	fixtureLegacyCrewFolder = "beta-b2c3d4e5"
	fixtureLegacyCrewID     = "b2c3d4e5-0000"
)

// PLAT-856-416: a stale registry entry returned workspace HTTP 500 and
// prevented opening another, correctly shared Crew, even after it was found.
func TestCrewBindingAndListsIgnoreUnrelatedUnreadableCrew(t *testing.T) {
	f := newMultiUserFixture(t, sharedIdentityLayout())
	f.WithSharing(true)
	const brokenFolder = "z-broken-11111111"
	const brokenID = "11111111-0000"
	if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: brokenFolder, ProjectID: brokenID, OwnerID: fixtureUserA, Shared: true}); err != nil {
		t.Fatal(err)
	}
	ws := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/Crew/"+brokenFolder+"/") {
			http.Error(w, "Crew folder is missing", http.StatusInternalServerError)
			return
		}
		f.Mock.ServeHTTP(w, r)
	}))
	defer ws.Close()
	t.Setenv("WORKSPACE_API_URL", ws.URL)

	for _, user := range []string{fixtureUserA, fixtureUserB} {
		binding, err := resolveCrewProjectBinding(f.Ctx(user), user, f.Crew, fixtureCrewID, "")
		if err != nil || binding.Binding.ResourceID != fixtureCrewID || binding.OwnerID != fixtureUserA {
			t.Fatalf("healthy Crew for %s: binding=%+v err=%v", user, binding, err)
		}
		items, err := listCrewProjectsForOwner(f.Ctx(user), user, fixtureUserA, "")
		if err != nil || len(items) != 1 || items[0]["id"] != fixtureCrewID {
			t.Fatalf("healthy Crew picker for %s: items=%v err=%v", user, items, err)
		}
	}
	if _, err := resolveCrewProjectBinding(f.Ctx(fixtureUserA), fixtureUserA, f.Crew, brokenID, ""); err == nil {
		t.Fatal("opening the broken Crew itself must still fail")
	}
	if err := defaultProjectOwners().SetPrivate("work", fixtureCrewFolder, true); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveCrewProjectBinding(f.Ctx(fixtureUserB), fixtureUserB, f.Crew, fixtureCrewID, ""); err == nil {
		t.Fatal("an unrelated broken Crew must not bypass the target's sharing check")
	}
}

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

// Schedule and webhook discovery find a Crew at the shared root through its registered owner; a manifest that names
// someone else changes nothing.
func TestSharedCrewSchedulesAndWebhooksAreDiscoveredByTheirOwner(t *testing.T) {
	f := newMultiUserFixture(t, sharedIdentityLayout())
	f.WithSharing(true)
	crewRoot := f.Layout.CrewPhysical(fixtureUserA, fixtureCrewFolder)
	f.put(crewRoot+"/workflow.json", `{"schema_version":1,"id":"`+fixtureCrewID+`","label":"Alpha","capabilities":{}}`)
	// Timers and webhooks are stored in schedules.json and webhooks.json (PLAT-871).
	f.put(crewRoot+"/webhooks.json", `{"webhooks":[{"id":"11111111-1111-4111-8111-111111111111","name":"Hook","enabled":true,"path":"/api/hooks/11111111-1111-4111-8111-111111111111",`+
		`"project":{"kind":"crew","id":"`+fixtureCrewID+`"},"action":{"type":"message"},"input":{"messages":["hi"]},"run_as":"`+fixtureUserA+`","auth":{"mode":"none"}}]}`)
	f.put(crewRoot+"/schedules.json", `{"schedules":[{"id":"daily","name":"Daily","project":{"kind":"crew","id":"`+fixtureCrewID+`"},`+
		`"when":{"cron":"0 8 * * *","timezone":"UTC"},"action":{"type":"message"},"input":{"messages":["go"]},"run_as":"`+fixtureUserA+`","enabled":true}]}`)
	// The manifest claims B owns it: ignored.
	f.put(crewRoot+"/product.json", `{"schema_version":1,"product":"work","id":"`+fixtureCrewID+`","title":"Alpha","session_id":"work:project:`+fixtureCrewID+`","owner_id":"`+fixtureUserB+`"}`)

	registry := agentprofiles.NewRegistry()
	profile := f.Crew
	profile.UIPanels.Schedules = true
	profile.ResolvedFeatures = []agentprofiles.ResolvedFeature{{ID: "triggers"}}
	if err := registry.RegisterProfile(profile); err != nil {
		t.Fatal(err)
	}
	svc := NewProjectActionDispatcher(nil, registry)
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
	// The shared-root scan checks the registry owner of every folder before reading manifests; the reader binding must
	// still name the matched Crew's owner, not the last folder checked (here a later Crew owned by someone else).
	if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: "zeta-f0f0f0f0", OwnerID: fixtureUserC, Shared: true}); err != nil {
		t.Fatal(err)
	}
	if got, err := resolveCrewProjectBinding(f.Ctx(fixtureUserB), fixtureUserB, f.Crew, fixtureCrewID, ""); err != nil || got.OwnedByCaller || got.OwnerID != fixtureUserA {
		t.Fatalf("reader binding through the shared root = %+v err=%v, want owner %q", got, err, fixtureUserA)
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
