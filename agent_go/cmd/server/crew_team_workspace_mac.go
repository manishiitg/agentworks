package server

import (
	"encoding/json"
	"log"
	"net/http"
	"time"
)

// A member's own workspace calls follow their saved working copy, never the
// maker's Mac or a server shadow. Peer asks still use AgentBridge.
func (api *StreamingAPI) relayTeamMemberWorkspace(w http.ResponseWriter, r *http.Request, project string, args map[string]any) bool {
	if IsLocalMode() {
		return false
	}
	action := externalArg(args, "action")
	scope := "crews:read"
	switch action {
	case "chats", "read_chat":
	case "open_chat":
		scope = "crews:write"
	case "ask", "reply_input":
		scope = "crews:run"
	default:
		return false
	}
	claims := GetUserFromContext(r.Context())
	team, err := readCrewTeam(project)
	if claims == nil || err != nil || team.OwnerID == claims.UserID {
		return false
	}
	pair, onMac := team.memberMac(claims.UserID)
	if !onMac {
		return false // members who chose web keep their normal server tabs
	}
	refuse := func(status int, reason, message string) bool {
		log.Printf("[TEAM_WORKSPACE] project=%s actor=%s action=%s outcome=refused reason=%s", project, claims.UserID, action, reason)
		externalError(w, status, "member_mac_unavailable", message)
		return true
	}
	if !crewTeamReachOpen(team, claims.UserID) || !crewTeamAccountActive(claims.UserID) {
		return refuse(403, "membership", "Join this Team with your own account to use your agents.")
	}
	if token := claims.AccessToken; token != nil && (!token.Allows(scope) || !token.AllowsCrew(project)) {
		return refuse(403, "scope", "This connection does not allow that action on this Crew.")
	}
	if action == "open_chat" && !userAccessForClaims(claims).CanEdit {
		return refuse(403, "builder_required", "Creating an agent needs Builder access.")
	}
	if crewMacSocketFor(claims.UserID, project, pair) == nil {
		return refuse(503, "offline", "Open AgentWorks on your Mac to start this chat.")
	}
	// Only normal workspace fields cross the existing exact-pair channel. The
	// Mac supplies its local project and owner, not identities from this JSON.
	raw, _ := json.Marshal(args)
	result := callCrewMacPair(r.Context(), claims.UserID, project, pair, crewChatCall{Operation: "workspace", Action: action, Args: raw, Caller: externalCallerTabName(r.Context(), claims)}, externalCrewWait(args)+40*time.Second)
	current, err := readCrewTeam(project)
	now, stillOnMac := current.memberMac(claims.UserID)
	if err != nil || !crewTeamReachOpen(current, claims.UserID) || !crewTeamAccountActive(claims.UserID) || !stillOnMac || now != pair {
		return refuse(403, "pair_changed", "Your Team connection changed. Reopen the Crew and try again.")
	}
	if result.Status < 300 {
		var out map[string]any
		if json.Unmarshal(result.Body, &out) == nil {
			if _, ok := out["workspace_id"]; ok {
				out["workspace_id"] = "work:" + project
			}
			if _, ok := out["crew_id"]; ok {
				out["crew_id"] = project
			}
			result.Body, _ = json.Marshal(out)
		}
	}
	log.Printf("[TEAM_WORKSPACE] project=%s actor=%s action=%s outcome=returned status=%d", project, claims.UserID, action, result.Status)
	writeCrewChatResult(w, result)
	return true
}
