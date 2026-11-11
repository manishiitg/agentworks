package server

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

// PLAT-746-887: a target-open failure must not expose internal storage paths or
// other people's identifiers while explaining the known stale-target case.
func TestCrewTeamTargetFailureKeepsInternalDetailsPrivate(t *testing.T) {
	for _, reason := range []string{"open /private/team.json: permission denied", "registry owner=private-person-id unavailable"} {
		got := crewTeamTargetFailureMessage(errors.New(reason))
		if strings.Contains(got, reason) || strings.Contains(got, "private") {
			t.Fatalf("internal detail exposed: %q", got)
		}
	}
	if got := crewTeamTargetFailureMessage(errors.New("Choose a chat_id from list_agents for this Crew")); !strings.Contains(got, "chat_id") {
		t.Fatalf("missing corrective action: %q", got)
	}
}

// Old chat-key addresses are refused with a refresh action, never resolved by a second lookup.
func TestCrewTeamMessageRejectsStaleAddressWithRefresh(t *testing.T) {
	for _, id := range []string{"example:chat:side", "example:chat:mac-main", "old-tab"} {
		w := httptest.NewRecorder()
		_, _, ok := (&StreamingAPI{}).crewTeamMessageTarget(w, httptest.NewRequest("POST", "/", nil), "example", id, &UserClaims{UserID: "member"})
		if ok || w.Code != 409 || !strings.Contains(w.Body.String(), "stale_team_address") || !strings.Contains(w.Body.String(), "list_agents") {
			t.Fatalf("stale address %q: status=%d body=%s", id, w.Code, w.Body.String())
		}
	}
}
