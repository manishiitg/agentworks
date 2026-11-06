package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/chunks"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// The Share page's Dashboards tab (owner, 6 Nov): one server-written list for the three kinds of Crew (on the server,
// on a Mac that is online, on a Mac that never went online) and every viewer. The owner and a joined teammate open it
// (the Team's own check); an invited person, a person only shared the Crew and an outsider are refused. Only the owner is
// offered a change or a public link, and a Mac Crew has no public link and says to put it online first.
func TestCrewDashboardsTabIsServerWrittenForEachCrewKindAndViewer(t *testing.T) {
	type tab struct {
		CanChange bool   `json:"can_change"`
		OnMac     bool   `json:"on_mac"`
		PutOnline bool   `json:"put_online"`
		Note      string `json:"note"`
		Filter    string `json:"filter"`
		Filters   []struct {
			Key   string `json:"key"`
			Count int    `json:"count"`
		} `json:"filters"`
		Items []struct {
			Title      string          `json:"title"`
			Kind       string          `json:"kind"`
			Line       string          `json:"line"`
			Who        string          `json:"who"`
			PublicLink json.RawMessage `json:"public_link"`
		} `json:"items"`
		Empty *struct{ Title, Line, Ask string } `json:"empty"`
	}
	setup := func(t *testing.T, page bool) *externalToolsFixture {
		f, _ := newExternalCrewCommandFixture(t)
		withMemoryUserDirectory(t, `{"users":[{"id":"owner","role":"editor","products":["work"]},{"id":"other","role":"editor","products":["work"]},{"id":"invited","role":"editor","products":["work"]},{"id":"runner","role":"editor","products":["work"]},{"id":"outsider","role":"editor","products":["work"]}]}`)
		if err := writeCrewTeam(crewTeam{ProjectID: "team", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "other"}, {UserID: "invited", Invited: true}}}); err != nil {
			t.Fatal(err)
		}
		if page {
			dir := filepath.Join(f.docs, externalCommandCrewRoot, "db", "reports")
			if err := os.MkdirAll(dir, 0o770); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html><head><title>Sales report</title></head><body>numbers</body></html>"), 0o660); err != nil {
				t.Fatal(err)
			}
		}
		return f
	}
	read := func(t *testing.T, f *externalToolsFixture, user, query string) (int, tab) {
		t.Helper()
		w := httptest.NewRecorder()
		f.api.handleCrewTeamDashboardsTab(w, adminRequest(http.MethodGet, "/api/crew-teams/team/dashboards-tab"+query, "", &UserClaims{UserID: user, Username: user}, map[string]string{"project": "team"}))
		var out tab
		if w.Code == 200 {
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatalf("%s: %v %s", user, err, w.Body.String())
			}
		}
		return w.Code, out
	}
	toMac := func(t *testing.T, f *externalToolsFixture, online bool) {
		if online { // a Crew whose Mac put it online: the server holds its twin and the copy of its dashboards
			store, page := t.TempDir(), "<html><head><title>Sales report</title></head><body>from the Mac</body></html>"
			version, err := chunks.Split(strings.NewReader(page), int64(len(page)), false, func(id string, data []byte) error { return putChunk(store, id, data) })
			if err == nil {
				err = swapCrewMacDashboards(filepath.Join(f.docs, externalCommandCrewRoot), store, map[string]chunks.File{"db/reports/index.html": version})
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := crewroot.WriteSync("team", crewroot.SyncBinding{PairID: "pair", Side: "server", Editing: "local", PeerProjectID: "mac"}); err != nil {
				t.Fatal(err)
			}
			manifest, err := crewSyncManifestPath("team") // when the Mac's dashboards last reached the server
			if err == nil {
				err = os.MkdirAll(filepath.Dir(manifest), 0o700)
			}
			if err == nil {
				err = os.WriteFile(manifest+".dashboards-updated", []byte("{}"), 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			return
		}
		t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local") // the Mac app's own server, before the Crew ever went online
	}
	for _, kind := range []struct {
		name   string
		mac    *bool
		line   string // the owner's note
		onMac  bool
		put    bool
		public bool
	}{
		{name: "server Crew", public: true},
		{name: "Mac Crew online", mac: crewDashTabPtr(true), onMac: true, put: true, line: crewDashboardsMacOwnerNote},
		{name: "Mac Crew never online", mac: crewDashTabPtr(false), onMac: true, put: true, line: crewDashboardsMacOwnerNote},
	} {
		t.Run(kind.name, func(t *testing.T) {
			f := setup(t, true)
			if kind.mac != nil {
				toMac(t, f, *kind.mac)
			}
			// The owner: can change, sees the kind's note and, on a server Crew only, may have public link rows.
			status, owner := read(t, f, "owner", "")
			if status != 200 || !owner.CanChange || owner.OnMac != kind.onMac || owner.PutOnline != kind.put || owner.Note != kind.line || owner.Filter != "all" {
				t.Fatalf("owner: %d %+v", status, owner)
			}
			if len(owner.Items) != 1 || owner.Items[0].Title != "Sales report" || owner.Items[0].Kind != "dashboard" || !strings.HasPrefix(owner.Items[0].Line, "Dashboard") || !strings.HasPrefix(owner.Items[0].Who, "people who work with you") {
				t.Fatalf("owner rows: %+v", owner.Items)
			}
			if len(owner.Filters) != 2 || owner.Filters[0].Key != "all" || owner.Filters[0].Count != 1 || owner.Filters[1].Key != "dashboard" {
				t.Fatalf("filters: %+v", owner.Filters)
			}
			if kind.onMac && len(owner.Items[0].PublicLink) > 0 {
				t.Fatalf("a Mac Crew offered a public link: %s", owner.Items[0].PublicLink)
			}
			if kind.mac != nil && *kind.mac && !strings.Contains(owner.Items[0].Line, "copy on the server") {
				t.Fatalf("a Mac Crew's rows say where they were read from: %q", owner.Items[0].Line)
			}
			// A filter keeps the other kinds' counts and lists only its own.
			if _, artifacts := read(t, f, "owner", "?kind=artifact"); artifacts.Filter != "artifact" || len(artifacts.Items) != 0 || artifacts.Filters[0].Count != 1 {
				t.Fatalf("artifact filter: %+v", artifacts)
			}
			// A joined teammate sees the same rows, with no change and no public link, and is told who changes them.
			status, mate := read(t, f, "other", "")
			if status != 200 || mate.CanChange || mate.PutOnline || len(mate.Items) != 1 || len(mate.Items[0].PublicLink) > 0 || !strings.Contains(mate.Note, crewDashboardsTeammateNote) {
				t.Fatalf("teammate: %d %+v", status, mate)
			}
			// Not in the Team: invited only, shared the Crew only (a Run person), or outside: refused, nothing listed.
			for _, who := range []string{"invited", "runner", "outsider"} {
				if status, got := read(t, f, who, ""); status != http.StatusForbidden || len(got.Items) != 0 {
					t.Fatalf("%s reached the Dashboards tab: %d", who, status)
				}
			}
		})
	}
	t.Run("nothing published", func(t *testing.T) {
		f := setup(t, false)
		if _, owner := read(t, f, "owner", ""); owner.Empty == nil || owner.Empty.Title != "No dashboards yet" || owner.Empty.Ask == "" || len(owner.Items) != 0 {
			t.Fatalf("owner empty state: %+v", owner)
		}
		if _, mate := read(t, f, "other", ""); mate.Empty == nil || mate.Empty.Title != "Nothing published yet" || !strings.Contains(mate.Empty.Line, "hasn't published anything from this Crew yet") {
			t.Fatalf("teammate empty state: %+v", mate)
		}
	})
}

func crewDashTabPtr[T any](v T) *T { return &v }
