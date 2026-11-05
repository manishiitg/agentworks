package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"slices"
	"strings"

	"github.com/gorilla/mux"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// PLAT-856: members manage their own participation, never the owner's Mac or settings.
type crewTeamOwnAgent struct {
	Key    string `json:"key"`
	Name   string `json:"name"`
	InTeam bool   `json:"in_team"`
}
type crewTeamMemberAccess struct {
	Name       string                 `json:"name"`
	People     []crewTeamAgentsPerson `json:"people"`
	Shared     string                 `json:"shared"`
	CopyStatus string                 `json:"copy_status"`
	OwnAgents  []crewTeamOwnAgent     `json:"own_agents"`
}

func (api *StreamingAPI) crewTeamMemberAccess(ctx context.Context, t crewTeam, caller string) *crewTeamMemberAccess {
	out := &crewTeamMemberAccess{Name: t.Name, People: teamAgentsByPerson(ctx, t, caller), Shared: "Nothing shared yet", OwnAgents: []crewTeamOwnAgent{}}
	if t.Folder.ID != "" && t.edits(caller) {
		out.Shared = t.Folder.display()
		out.CopyStatus = "Your copy is being prepared"
		if dir, err := crewTeamServerCopyDir(caller, t.ProjectID, t.Folder.ID); err == nil {
			if b, err := readCrewTeamLocal(dir); err == nil {
				switch {
				case b.Error != "":
					out.CopyStatus = "Can't sync: " + b.Error
				case b.Status == "up_to_date":
					out.CopyStatus = fmt.Sprintf("Up to date · %d files", len(b.LocalBase))
				default:
					out.CopyStatus = "Syncing your copy"
				}
			}
		}
	}
	store := defaultProductConversationRegistryStore()
	records, err := store.projectChats(ctx, caller, crewProfileID, t.ProjectID)
	if err != nil {
		return out
	}
	identities, _ := store.agentIdentities(ctx, caller, crewProfileID, t.ProjectID)
	for _, r := range records {
		identity := identities[r.ConversationKey]
		out.OwnAgents = append(out.OwnAgents, crewTeamOwnAgent{Key: r.ConversationKey, Name: firstNonEmptyTrimmed(identity.Name, r.Title, "Main"), InTeam: !identity.OnlyMe})
	}
	return out
}
func (api *StreamingAPI) handleCrewTeamMemberAction(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	project := mux.Vars(r)["project"]
	if !crewTeamClaims(claims, project, true) {
		externalError(w, 403, "team_forbidden", "Open this Team with your own account.")
		return
	}
	t, err := readCrewTeam(project)
	if err != nil || t.OwnerID == claims.UserID || !t.member(claims.UserID) || ownerOfCrewProjectID(project) != t.OwnerID || !crewTeamAccountActive(t.OwnerID) {
		externalError(w, 403, "team_member_required", "Only a joined member can change their own participation here.")
		return
	}
	if mux.Vars(r)["op"] == "agent-visibility" {
		var in struct {
			Key    string `json:"agent_key"`
			InTeam *bool  `json:"in_team"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in) != nil || in.InTeam == nil {
			externalError(w, 400, "invalid_agent", "Choose your agent and In team or Only me.")
			return
		}
		in.Key = strings.TrimSpace(in.Key)
		store := defaultProductConversationRegistryStore()
		records, e := store.projectChats(r.Context(), claims.UserID, crewProfileID, project)
		if e != nil {
			externalError(w, 409, "agent_unavailable", "Your agents could not be read. Try again.")
			return
		}
		if !slices.ContainsFunc(records, func(record ProductConversationRecord) bool {
			return record.ConversationKey == strings.TrimSpace(in.Key)
		}) {
			externalError(w, 403, "agent_not_yours", "Choose one of your own agents in this Team.")
			return
		}
		if err = store.setAgentOnlyMe(r.Context(), claims.UserID, crewProfileID, in.Key, !*in.InTeam); err != nil {
			externalError(w, 409, "agent_update_failed", err.Error())
			return
		}
		log.Printf("[CREW_TEAM] agent visibility user=%s tab=%s in_team=%t", logUsernameForUserID(claims.UserID), in.Key, *in.InTeam)
		externalJSON(w, map[string]any{"in_team": *in.InTeam})
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
	t.People = slices.DeleteFunc(t.People, func(p crewTeamPerson) bool { return p.UserID == claims.UserID })
	t.Revision++
	if err = writeCrewTeam(t); err != nil {
		externalError(w, 409, "team_save_failed", err.Error())
		return
	}
	log.Printf("[CREW_TEAM] project=%s actor=%s event=left revision=%d", project, logUsernameForUserID(claims.UserID), t.Revision)
	go removeStaleCrewTeamServerCopies(context.WithoutCancel(r.Context()))
	externalJSON(w, map[string]bool{"left": true})
}
