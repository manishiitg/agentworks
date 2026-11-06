package server

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"time"
)

// Public on AgentBridge from the website (owner, 6 Nov, approved mockup "Mac online: three cards"): the same Mac
// online page on the Mac app and the website, and on the website its switch works too. Anyone in the Crew's Team makes
// THEIR OWN agent public, not only the owner (owner, 6 Nov). The choice is kept on the caller's own Mac
// (crew_mac_shared_agents.go), so this server passes it to that Mac over its relay (the owner's from the Crew's
// binding, a member's from their Team record, crewMacPairFor), where the Mac app's own path applies it
// (runDesktopCrewChat "agentbridge" -> crewAccessChatAction). The call only ever reaches the caller's own Mac, so
// another person's agent is out of reach. Nothing keeps a change for later: while the Mac is off the switch is refused
// with a plain line.

const crewAgentBridgeMacOffline = "Your Mac is offline; turn this on when it's back."

// handleCrewTeamAgentBridge is POST /crew-teams/{project}/agentbridge: {tab, public}, for the caller's own agent (anyone in the Team).
func (api *StreamingAPI) handleCrewTeamAgentBridge(w http.ResponseWriter, r *http.Request) {
	t, claims, ok := crewTeamForTab(w, r, true)
	if !ok {
		return
	}
	if refuseCrewOwnerOnline(w, r, t.ProjectID, claims.UserID) {
		return
	}
	var in struct {
		Tab    string `json:"tab"`
		Public *bool  `json:"public"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in) != nil || in.Public == nil || !crewMacTabID(in.Tab) {
		externalError(w, 400, "invalid_agent", "Choose one of your agents and whether it is public.")
		return
	}
	if !crewRelayTwin(t.ProjectID) {
		externalError(w, 403, "agentbridge_not_on_mac", "This Crew's agents are made public from its Share, not from a Mac.")
		return
	}
	action := "only_my_agents"
	if *in.Public {
		action = "public"
	}
	result := crewChatResult{Status: http.StatusServiceUnavailable}
	if pair, found := crewMacPairFor(claims.UserID, t.ProjectID); found {
		result = callCrewMacPair(r.Context(), claims.UserID, t.ProjectID, pair, crewChatCall{Operation: "agentbridge", Tab: in.Tab, Action: action}, 30*time.Second)
	}
	log.Printf("[CREW_TEAM] project=%s event=agentbridge_website tab=%s action=%s status=%d", t.ProjectID, in.Tab, action, result.Status)
	switch result.Status {
	case http.StatusOK:
		publishCrewPanelChanged(t.ProjectID)
		externalJSON(w, map[string]any{"public": *in.Public})
	case http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		externalError(w, 409, "mac_offline", crewAgentBridgeMacOffline)
	default:
		externalError(w, result.Status, "agentbridge_failed", crewRelayErrorText(result.Body, "It was not saved. Try again."))
	}
}
