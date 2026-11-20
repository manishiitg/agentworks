package server

import (
	"context"
	"log"
	"net/http"
)

// Read-only metadata for this exact attachment. No native journal, message body,
// acknowledgement, join, setting or new grant is returned or changed here.
func (api *StreamingAPI) externalTerminalStatus(w http.ResponseWriter, r *http.Request, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	named, _, err := currentAgentAttachment(r.Context(), claims, externalArg(args, "agent_name"), externalArg(args, "attachment_id"))
	if err != nil || named.Attachment == nil || (named.Attachment.Provider != "claude" && named.Attachment.Provider != "codex") {
		externalError(w, 403, "attachment_unavailable", "This exact session is unavailable.")
		return
	}
	t, row, joined, err := terminalCrewForNamed(named.ID)
	if err != nil {
		externalError(w, 503, "status_unavailable", "Crew status is unavailable.")
		return
	}
	out := map[string]any{"attached": true}
	if !joined {
		externalJSON(w, out)
		return
	}
	if _, err = terminalCrewAdmission(t.ProjectID, claims.UserID); err != nil {
		externalError(w, 403, "crew_unavailable", "Crew membership changed.")
		return
	}
	current, err := currentCrewTeamTerminal(r.Context(), t, row)
	if err != nil || current.Attachment.ID != named.Attachment.ID {
		externalError(w, 403, "attachment_changed", "The attached session changed.")
		return
	}
	ownerCtx := context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: t.OwnerID, Username: t.OwnerID})
	manifest, err := readCrewProjectMetadata(ownerCtx, crewProfileID, crewProjectRootByID(t.ProjectID))
	if err != nil {
		externalError(w, 503, "status_unavailable", "The Crew name is unavailable.")
		return
	}
	out["crew_name"] = firstNonEmptyTrimmed(manifest.Title, manifest.Label, t.Name, "Crew")
	out["team_on"], out["in_team"] = t.On, row.InTeam
	pane := externalArg(args, "action") == "team_pane"
	if t.On && row.InTeam {
		online := 0
		reach := crewTeamReachableIn(r.Context(), t, claims.UserID)
		for _, a := range reach {
			if a.Online {
				online++
			}
		}
		out["online"] = online
		if pane && externalArg(args, "query") != "" {
			// One agent opened in the pane (#3357): its row and, when shared Live, its last messages.
			detail, ok := api.teamPaneAgentDetail(r.Context(), t, claims.UserID, externalArg(args, "query"))
			if !ok {
				externalError(w, 404, "agent_unavailable", "That agent is not in your Team view anymore.")
				return
			}
			log.Printf("[TERMINAL_STATUS] agent=%s project=%s pane_agent=%s note=%s messages=%d last=%t", named.ID, t.ProjectID, detail.ID, detail.Note, len(detail.Messages), detail.Last != "")
			out["agent"] = detail
			externalJSON(w, out)
			return
		}
		if pane {
			out["agents"] = teamPaneAgents(crewTeamOwnSourceRows(r.Context(), t, claims.UserID), reach)
			out["kit"] = api.teamPaneKit(r.Context(), claims, t)
			out["needs"] = teamPaneNeeds(crewWorkNeedsFromRequests(agentTeamRequests(r.Context(), t, claims.UserID), claims.UserID))
		}
		// Count only currently admitted incoming mail; counting never acknowledges it.
		if snapshot, e := readAgentMessageSnapshot(r.Context()); e == nil {
			if pane {
				out["asks"] = teamPaneAsks(crewTeamTasks(r.Context(), t, claims.UserID, snapshot), row.ID)
			}
			unread := 0
			for _, c := range snapshot.Conversations {
				checked, admitted := false, false
				for _, m := range c.Messages {
					side := 1 - m.From
					if side < 0 || side > 1 {
						continue
					}
					dest := c.Endpoints[side]
					if dest.UserID != claims.UserID || dest.NamedAgentID != named.ID || !m.InboxRemovedAt.IsZero() || !m.DeliveredAt.IsZero() || m.Delivery == "delivered" {
						continue
					}
					if !checked {
						admitted = api.externalAuthorizeConversationPeer(r.Context(), claims, c, side, false) == nil
						checked = true
					}
					if admitted {
						unread++
					}
				}
			}
			out["unread"] = unread
		}
	}
	latest, e := readCrewTeam(t.ProjectID)
	latestRow, ok := latest.Agents[row.ID]
	if e != nil || !ok || latest.Revision != t.Revision {
		externalError(w, 403, "status_changed", "Crew status changed; try the next refresh.")
		return
	}
	if _, e = currentCrewTeamTerminal(r.Context(), latest, latestRow); e != nil {
		externalError(w, 403, "attachment_changed", "The attached session changed.")
		return
	}
	log.Printf("[TERMINAL_STATUS] agent=%s project=%s pane=%t outcome=ready", named.ID, t.ProjectID, pane)
	externalJSON(w, out)
}
