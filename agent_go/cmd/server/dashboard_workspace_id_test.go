package server

import "testing"

// Stable IDs returned by workspace tools must survive the old Share aliases
// too. Exercise the same native admission and real dashboard store for both
// origins, including an admitted Run caller's refused sharing mutation.
func TestDashboardShareAliasesAcceptWorkspaceIDs(t *testing.T) {
	_, codeProfile := newCodePrivacyFixture(t)
	f, _ := newExternalCrewCommandFixture(t)
	if err := f.api.agentProfiles.RegisterProfile(codeProfile); err != nil {
		t.Fatal(err)
	}
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","role":"editor","products":["work","code"]},{"id":"other","role":"editor","email":"other@example.com","products":["work","code"]}]}`)
	f.write(t, codePrivacyOwnerRoot+"/product.json", `{"schema_version":1,"product":"code","id":"c0de0001-0000","title":"App","session_id":"code:project:c0de0001-0000"}`)
	for _, id := range []string{"work:team", "code:c0de0001-0000"} {
		t.Run(id, func(t *testing.T) {
			shared := externalTestBody(t, f.call(t, "owner", "share_dashboard", map[string]any{"workspace": id, "mode": "people", "emails": []string{"other@example.com"}}), 200)
			if shared["mode"] != "people" {
				t.Fatalf("stable ID did not share its project: %v", shared)
			}
			state := externalTestBody(t, f.call(t, "owner", "get_dashboard_share", map[string]any{"workspace_id": id}), 200)
			if state["mode"] != "people" {
				t.Fatalf("workspace_id did not inspect the same share: %v", state)
			}
			externalTestBody(t, f.call(t, "other", "share_dashboard", map[string]any{"workspace_id": id, "mode": "private"}), 403)
			externalTestBody(t, f.call(t, "owner", "share_dashboard", map[string]any{"workspace_id": id, "workspace": id, "mode": "private"}), 400)
		})
	}
}
