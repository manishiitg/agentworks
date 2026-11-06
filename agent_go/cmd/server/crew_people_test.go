package server

import (
	"fmt"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// PLAT-856-509 (owner and reviewer, 6 Nov): one People list over the Team record and the Share record. Added at a level,
// moved between levels or removed, a person changes the same two records as before; every change says what the person
// gains or loses and is saved only on confirm=true. Works with me is any builder's, dashboards only and moves between
// the levels are the owner's, dashboards only needs the Crew on the server, and a Mac Crew that was never online has no
// People list yet. One table over the three kinds of Crew.
func TestCrewPeopleLevelsOverTheTeamAndShareRecords(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	withMemoryUserDirectory(t, `{"users":[`+strings.Join([]string{
		`{"id":"owner","username":"owner","email":"owner@example.com","role":"editor","products":["work"]}`,
		`{"id":"member","username":"member","email":"member@example.com","role":"editor","products":["work"]}`,
		`{"id":"anita","username":"anita","email":"anita@example.com","role":"editor","products":["work"]}`,
		`{"id":"newbie","username":"newbie","email":"newbie@example.com","role":"editor","products":["work"]}`,
		`{"id":"outsider","username":"outsider","email":"outsider@example.com","role":"editor","products":["work"]}`,
	}, ",")+`]}`)
	if err := writeCrewTeam(crewTeam{ProjectID: "team", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "member"}}}); err != nil {
		t.Fatal(err)
	}
	call := func(user string, args map[string]any) (int, map[string]any) {
		t.Helper()
		args["crew_id"], args["action"] = "team", "people"
		w := httptest.NewRecorder()
		f.api.externalCrewTeamSyncCall(w, adminRequest(http.MethodPost, "/api/external/v1/call", "", &UserClaims{UserID: user, Username: user}, nil), args)
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	level := func(user string, who, to string, confirm bool) (int, map[string]any) {
		return call(user, map[string]any{"person": who, "level": to, "confirm": confirm, "create_dashboard": true})
	}
	teamHas := func(id string) bool {
		team, err := readCrewTeam("team")
		return err == nil && slices.ContainsFunc(team.People, func(p crewTeamPerson) bool { return p.UserID == id })
	}
	shareHas := func(id string) bool {
		rec, _ := defaultProjectOwners().Lookup("work", "test-team")
		return slices.Contains(rec.RunPeople, id)
	}
	listed := func(user, who string) map[string]any {
		_, view := call(user, map[string]any{})
		for _, p := range view["people"].([]any) {
			if person := p.(map[string]any); person["user_id"] == who {
				return person
			}
		}
		return nil
	}
	// change refuses first, then previews without saving, then saves on confirm.
	change := func(user, who, to string, want func() bool) {
		t.Helper()
		code, preview := level(user, who, to, false)
		if code != 200 || preview["confirm_needed"] != true || preview["preview"].(map[string]any)["summary"] == "" {
			t.Fatalf("%s -> %s %s without confirm: %d %v", who, to, user, code, preview)
		}
		if want() {
			t.Fatalf("%s -> %s was saved before confirm", who, to)
		}
		if code, out := level(user, who, to, true); code != 200 || out["changed"] != true {
			t.Fatalf("%s -> %s confirmed: %d %v", who, to, code, out)
		}
		if !want() {
			t.Fatalf("%s -> %s was not saved on confirm", who, to)
		}
	}

	// Server Crew: both levels.
	if code, out := level("outsider", "newbie", "works", true); code < 400 || teamHas("newbie") {
		t.Fatalf("an outsider changed the people: %d %v", code, out)
	}
	_, view := call("member", map[string]any{})
	if view["kind"] != crewKindServer || view["can_share"] != false || view["can_add"] != true || len(view["gets"].([]any)) != 6 {
		t.Fatalf("a builder's People list: %v", view)
	}
	change("member", "newbie", "works", func() bool { return teamHas("newbie") })
	if p := listed("owner", "newbie"); p == nil || p["invited"] != true || p["line"] != "Will get files, agents and dashboards when they accept." || p["level_label"] != "Works with me" {
		t.Fatalf("the invited person's line: %v", p)
	}
	// Open to everyone (how the fixture's Crew starts): nobody needs dashboards only, and nothing is saved.
	if code, out := level("owner", "anita", "dashboards", true); code != 200 || out["changed"] != false || !strings.Contains(out["note"].(string), "open to everyone") || shareHas("anita") {
		t.Fatalf("dashboards only on a Crew open to everyone: %d %v", code, out)
	}
	if err := defaultProjectOwners().SetAudience("work", "test-team", true, nil, nil); err != nil {
		t.Fatal(err)
	}
	if code, out := level("member", "anita", "dashboards", true); code != http.StatusForbidden || shareHas("anita") {
		t.Fatalf("a builder gave dashboards only: %d %v", code, out)
	}
	// No dashboard yet and no yes to create one: a plain refusal with the next step, and nothing saved.
	if code, out := call("owner", map[string]any{"person": "anita", "level": "dashboards", "confirm": true}); code != http.StatusConflict || !strings.Contains(fmt.Sprint(out["error"]), "dashboard_needed") || !strings.Contains(fmt.Sprint(out["error"]), "standard dashboard") || shareHas("anita") {
		t.Fatalf("giving dashboards only without a dashboard: %d %v", code, out)
	}
	change("owner", "anita", "dashboards", func() bool { return shareHas("anita") })
	if teamHas("anita") {
		t.Fatal("dashboards only put the person in the Team")
	}
	if p := listed("owner", "anita"); p == nil || p["level"] != crewPeopleDashboards || p["line"] != "All this Crew's dashboards. Can ask this Crew. No files." {
		t.Fatalf("the dashboards-only line: %v", p)
	}
	change("owner", "newbie", "dashboards", func() bool { return shareHas("newbie") && !teamHas("newbie") })
	_, preview := level("owner", "anita", "works", false)
	if got := preview["preview"].(map[string]any)["gains"].([]any); len(got) != 2 {
		t.Fatalf("what moving to works with me gives: %v", preview)
	}
	change("owner", "anita", "works", func() bool { return teamHas("anita") && !shareHas("anita") })
	change("member", "anita", "none", func() bool { return !teamHas("anita") && !shareHas("anita") })
	change("owner", "newbie", "none", func() bool { return !teamHas("newbie") && !shareHas("newbie") })
	if rec, _ := defaultProjectOwners().Lookup("work", "test-team"); len(rec.RunPeople) != 0 || !rec.Private {
		t.Fatalf("the last dashboards-only person left but the Share still names people: %+v", rec)
	}
	// The old add/remove shorthand is gone: a change names a person and a level, and nothing is saved without them.
	body, _ := json.Marshal(map[string]string{"add": "anita"})
	w := httptest.NewRecorder()
	f.api.handleCrewTeamPeople(w, adminRequest(http.MethodPost, "/api/crew-teams/team/people", string(body), &UserClaims{UserID: "owner", Username: "owner"}, map[string]string{"project": "team"}))
	if w.Code != http.StatusBadRequest || teamHas("anita") {
		t.Fatalf("the old add shorthand: %d %s", w.Code, w.Body.String())
	}

	// A Crew on a Mac that is online: Works with me only; dashboards only needs the Crew online first.
	if err := crewroot.WriteSync("team", crewroot.SyncBinding{Mode: "relay", Side: "server", Editing: "local", PairID: "owner-pair", PeerProjectID: "mac-crew"}); err != nil {
		t.Fatal(err)
	}
	_, view = call("owner", map[string]any{})
	levels := view["levels"].([]any)
	if view["kind"] != crewKindMacOnline || view["put_online"] != crewPeoplePutOnlineLine || levels[0].(map[string]any)["available"] != true || levels[1].(map[string]any)["available"] != false || view["can_share"] != false {
		t.Fatalf("a Mac Crew's People list: %v", view)
	}
	if code, out := level("owner", "newbie", "dashboards", true); code != http.StatusConflict || out["error"] == nil || shareHas("newbie") {
		t.Fatalf("dashboards only on a Mac Crew: %d %v", code, out)
	}
	change("owner", "newbie", "works", func() bool { return teamHas("newbie") })
	if err := crewroot.ReplaceSync("team", crewroot.SyncBinding{Side: "server"}); err != nil {
		t.Fatal(err)
	}

	// A Crew on a Mac that was never online has no People list yet.
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	_, view = call("owner", map[string]any{})
	if view["kind"] != crewKindMacNever || view["blocked"] != crewPeopleNeverOnlineLine || len(view["people"].([]any)) == 0 {
		t.Fatalf("a never-online Crew's People list: %v", view)
	}
	if p := listed("owner", "newbie"); p == nil || p["moves"] != nil {
		t.Fatalf("a never-online Crew offered a change: %v", p)
	}
	if code, out := level("owner", "outsider", "works", true); code != http.StatusConflict || teamHas("outsider") {
		t.Fatalf("a never-online Crew changed its people: %d %v", code, out)
	}
}
