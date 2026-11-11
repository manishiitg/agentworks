package server

import (
	"errors"
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
