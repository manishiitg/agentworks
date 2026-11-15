package server

import (
	"errors"
	"fmt"
	"net/http"
)

// A native helper can put only its exact joined source online. It cannot use
// its child token as a general desktop sign-in or choose a different agent.
func (api *StreamingAPI) externalTerminalPutOnline(w http.ResponseWriter, r *http.Request, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.AccessToken == nil || claims.AccessToken.BoundAgentID == "" || claims.AccessToken.BoundAgentName == "" {
		externalError(w, 403, "source_unavailable", "Use this exact connected session's helper.")
		return
	}
	name := claims.AccessToken.BoundAgentName
	if supplied := externalArg(args, "agent_name"); supplied != "" && supplied != name {
		externalError(w, 403, "source_unavailable", "Use this exact connected session's helper.")
		return
	}
	attachment := externalArg(args, "attachment_id")
	named, _, err := currentAgentAttachment(r.Context(), claims, name, attachment)
	if err != nil || named.Attachment == nil || named.ID != claims.AccessToken.BoundAgentID {
		externalError(w, 403, "source_unavailable", "This exact attachment changed.")
		return
	}
	team, row, joined, err := terminalCrewForNamed(named.ID)
	if err != nil || !joined {
		externalError(w, 403, "source_unavailable", "Join this source session to its Crew first.")
		return
	}
	b := crewOfflineBinding{Provider: named.Attachment.Provider, Session: named.Attachment.SessionID, Attachment: attachment}
	if crewOfflineTerminalOwner(r.Context(), team, row.ID, b, name) != nil {
		externalError(w, 403, "source_unavailable", "This source session or its maker changed.")
		return
	}
	if project := externalArg(args, "project"); project != "" && project != team.ProjectID {
		externalError(w, 403, "source_unavailable", "This exact session is joined to a different Crew.")
		return
	}
	in := crewPutOnlineRequest{Agent: row.ID, Audience: externalArg(args, "audience")}
	if people, ok := args["chosen_people"].([]any); ok {
		for _, p := range people {
			id, ok := p.(string)
			if !ok {
				externalError(w, 400, "invalid_online", "Choose people from this Crew.")
				return
			}
			in.People = append(in.People, id)
		}
	}
	out, err := api.putCrewAgentOnline(r.Context(), claims.UserID, team.ProjectID, in)
	if err != nil {
		code := "online_unavailable"
		if errors.Is(err, errCrewTeamLastShared) {
			code = "last_shared_agent"
		}
		externalError(w, 400, code, fmt.Sprint(err))
		return
	}
	externalJSON(w, out)
}
