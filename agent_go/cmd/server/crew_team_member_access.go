package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"slices"

	"github.com/gorilla/mux"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// PLAT-856: members manage their own participation, never the owner's Mac or settings.
func (api *StreamingAPI) handleCrewTeamMemberAction(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	project := mux.Vars(r)["project"]
	if !crewTeamClaims(claims, project, true) {
		externalError(w, 403, "team_forbidden", "Open this Team with your own account.")
		return
	}
	t, err := readCrewTeam(project)
	if err != nil || !t.member(claims.UserID) || ownerOfCrewProjectID(project) != t.OwnerID || !crewTeamAccountActive(t.OwnerID) {
		externalError(w, 403, "team_member_required", "Join this Crew's Team first.")
		return
	}
	if refuseCrewOwnerOnline(w, r, project, claims.UserID) {
		return
	}
	// The caller's own agent's "When others ask" rule and Answer only / Make changes (crew_when_others_ask.go).
	if mux.Vars(r)["op"] == "agent-rule" {
		handleCrewTeamAgentRule(w, r, project, claims.UserID)
		return
	}
	// In team / Only me for one of the caller's own agents, the owner's included, for every Crew kind (crew_team_in_team.go).
	if mux.Vars(r)["op"] == "agent-visibility" {
		var in struct {
			Key    string `json:"agent_key"`
			InTeam *bool  `json:"in_team"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in) != nil || in.InTeam == nil {
			externalError(w, 400, "invalid_agent", "Choose your agent and whether it is shared.")
			return
		}
		if err = setCrewTeamAgentChoice(r.Context(), project, claims.UserID, in.Key, *in.InTeam); errors.Is(err, errCrewTeamLastShared) {
			externalError(w, 400, "last_shared_agent", err.Error())
			return
		} else if errors.Is(err, errCrewTeamAgentNotYours) {
			externalError(w, 403, "agent_not_yours", "Choose one of your own agents in this Team.")
			return
		} else if err != nil {
			externalError(w, 409, "agent_update_failed", "It was not saved. Try again.")
			return
		}
		externalJSON(w, map[string]any{"in_team": *in.InTeam})
		return
	}
	if t.OwnerID == claims.UserID {
		externalError(w, 403, "team_owner_stays", "The Crew's owner stays in its Team. Turn the Team off instead.")
		return
	}
	dir, err := crewTeamDir(project)
	if err != nil {
		externalError(w, 409, "team_save_failed", err.Error())
		return
	}
	unlock, err := wf.LockWorkspace(r.Context(), dir)
	if err != nil {
		externalError(w, 409, "team_save_failed", err.Error())
		return
	}
	defer unlock()
	t, err = readCrewTeam(project)
	if err != nil || t.OwnerID == claims.UserID || !t.member(claims.UserID) {
		externalError(w, 403, "team_member_required", "You are not a member of this Team now.")
		return
	}
	previous := t
	previous.People = slices.Clone(t.People)
	t.People = slices.DeleteFunc(t.People, func(p crewTeamPerson) bool { return p.UserID == claims.UserID })
	t.Revision++
	if err = writeCrewTeam(t); err != nil {
		externalError(w, 409, "team_save_failed", err.Error())
		return
	}
	api.stopRemovedCrewTeamSessions(previous, t)
	log.Printf("[CREW_TEAM] project=%s actor=%s event=left revision=%d", project, logUsernameForUserID(claims.UserID), t.Revision)
	go api.removeStaleCrewTeamServerCopies(context.WithoutCancel(r.Context()))
	externalJSON(w, map[string]bool{"left": true})
}
