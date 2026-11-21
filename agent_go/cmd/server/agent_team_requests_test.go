package server

import "testing"

// #2965: a Mac app chat that runs as a hosted AgentBridge agent sends with the person's
// stamp, so its Team and self requests must take the Crew from its own chat origin.
// Someone else's request running in that chat (no owner authority) gets no Crew.
func TestAgentCallerCrewProjectForHostedAppChats(t *testing.T) {
	origin := &agentMessageEndpoint{Kind: triggerCallerCrew, ID: "crew-1"}
	for _, c := range []struct {
		name   string
		caller triggerLinkCaller
		want   string
	}{
		{"crew chat", triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerCrew, ID: "crew-1"}}, "crew-1"},
		{"hosted own chat", triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerUser, ID: "owner"}, BridgeOrigin: origin, OwnerAuthority: true}, "crew-1"},
		{"another person's request", triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerUser, ID: "owner"}, BridgeOrigin: origin}, ""},
	} {
		if got := agentCallerCrewProject(c.caller); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
