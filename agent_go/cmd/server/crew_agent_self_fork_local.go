package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"

	"github.com/gorilla/mux"
)

// Both menu entry points and MCP land here. A remote Team ID is mapped only
// through this person's saved connection and owned, already joined local Crew.
func (api *StreamingAPI) handleCrewAgentSelfForkLocal(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	fail := func(status int, line string) { externalError(w, status, "agent_self_fork_unavailable", line) }
	if !IsLocalMode() || claims == nil || claims.UserID == "" || claims.Scope != "" {
		fail(403, "Fork from your own AgentWorks Mac app.")
		return
	}
	var in struct {
		Agent      string `json:"agent_id"`
		Connection string `json:"connection_id"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&in) != nil || in.Agent == "" {
		fail(400, "Choose the teammate's agent to fork.")
		return
	}
	project := mux.Vars(r)["project"]
	if in.Connection != "" {
		c, err := desktopConnectionFor(claims.UserID, in.Connection)
		if err != nil {
			fail(403, "Use one of your own saved connections.")
			return
		}
		project = crewLocalProjectForTeam(claims.UserID, c, project)
	}
	if project == "" || !crewTeamClaims(claims, project, true) || ownerOfCrewProjectID(project) != claims.UserID {
		fail(409, "Add this Team to your Mac first, then fork its agent.")
		return
	}
	localClaims, binding, connection, err := api.desktopCrewChatBinding(r.Context(), project)
	if err != nil || localClaims.UserID != claims.UserID {
		fail(409, "Connect your own copy of this Crew to its Team first.")
		return
	}
	base := "/api/crew-teams/" + url.PathEscape(binding.PeerProjectID) + "/agent-forks"
	query := "?pair=" + url.QueryEscape(binding.PairID)
	var offer crewAgentFork
	err = crewTeamRemoteJSON(r.Context(), claims.UserID, connection, http.MethodPost, base+query, map[string]string{"operation": "self", "agent_id": in.Agent}, &offer)
	if err != nil {
		fail(409, laptopActionErrorText(err.Error()))
		return
	}
	if !offer.SelfFork || !offer.Ready || offer.Agent.ID != in.Agent || offer.Recipient != connection.User.ID || offer.Project != binding.PeerProjectID {
		fail(409, "The shared copy changed. Try forking again.")
		return
	}
	result, err := api.acceptCrewAgentForkOnMac(r, project, connection, binding.PeerProjectID, offer.ID, base, query)
	if err != nil {
		fail(409, "Your copy is ready but wasn't imported. Open this Crew and accept the pending copy, or try again.")
		return
	}
	out, ok := result.(map[string]any)
	if !ok {
		fail(409, "The copy receipt is unavailable. Open your Crew to check it.")
		return
	}
	out["snapshot_at"], out["project_id"] = offer.SnapshotAt, project
	externalJSON(w, out)
}
