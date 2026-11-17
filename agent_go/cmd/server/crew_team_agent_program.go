package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
)

// crewTeamAgentProgram is one AI program the Team agent can run on: the programs this server offers the Team owner.
type crewTeamAgentProgram struct {
	Engine  string `json:"engine"`
	ModelID string `json:"model_id,omitempty"`
	Label   string `json:"label"`
}

// handleCrewTeamAgentProgram lets the Team owner see and change the Team agent's AI program from the Team panel
// (#2969). GET lists the programs on this server and the current one; POST {engine} switches the online home's Main
// chat through the same chat-model path as the Crew chat's own model picker, so its account checks still apply.
func (api *StreamingAPI) handleCrewTeamAgentProgram(w http.ResponseWriter, r *http.Request) {
	t, claims, ok := crewTeamForTab(w, r, r.Method == http.MethodPost)
	if !ok {
		return
	}
	if claims.UserID != t.OwnerID {
		externalError(w, http.StatusForbidden, "owner_only", "Only the Team owner can change the Team agent's AI program. Ask them to change it.")
		return
	}
	home, err := readCrewTeam(t.OnlineHome)
	if t.RosterSource != "" || t.OnlineHome == "" || err != nil || home.RosterSource != t.ProjectID || home.OwnerID != t.OwnerID {
		externalError(w, http.StatusConflict, "team_agent_unavailable", "The Team agent isn't set up yet. Turn the Team on and try again in a minute.")
		return
	}
	programs := api.crewTeamAgentPrograms(r, claims.UserID)
	if r.Method == http.MethodGet {
		externalJSON(w, map[string]any{"programs": programs})
		return
	}
	var in struct {
		Engine string `json:"engine"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in); err != nil || strings.TrimSpace(in.Engine) == "" {
		externalError(w, http.StatusBadRequest, "invalid_program", "Choose an AI program for the Team agent.")
		return
	}
	var chosen *crewTeamAgentProgram
	for i := range programs {
		if programs[i].Engine == strings.TrimSpace(in.Engine) {
			chosen = &programs[i]
		}
	}
	if chosen == nil {
		externalError(w, http.StatusBadRequest, "program_unavailable", "That AI program isn't available on this server. Pick one from the list.")
		return
	}
	status, body := scheduleHandler(r, api.handleAgentProfileChatModels, http.MethodPost, "/api/agent-profiles/"+workproduct.ProfileID+"/chat-models",
		map[string]string{"id": workproduct.ProfileID}, nil,
		map[string]string{"project_id": t.OnlineHome, "conversation_key": t.OnlineHome, "engine": chosen.Engine, "model_id": chosen.ModelID})
	if status >= 400 {
		log.Printf("[TEAM_AGENT] project=%s home=%s event=program_change engine=%s status=%d", t.ProjectID, t.OnlineHome, chosen.Engine, status)
		externalScheduleRespond(w, status, body)
		return
	}
	// The panel's runtime labels are cached for 30s; drop them so the row shows the new program at once.
	crewTeamPanelRuntimeCache.Lock()
	clear(crewTeamPanelRuntimeCache.entries)
	crewTeamPanelRuntimeCache.Unlock()
	publishCrewPanelChanged(t.ProjectID)
	log.Printf("[TEAM_AGENT] project=%s home=%s event=program_change engine=%s status=done", t.ProjectID, t.OnlineHome, chosen.Engine)
	externalJSON(w, map[string]any{"program": chosen.Label, "engine": chosen.Engine})
}

// crewTeamAgentPrograms lists each AI program this server offers the owner once, with its default model.
func (api *StreamingAPI) crewTeamAgentPrograms(r *http.Request, owner string) []crewTeamAgentProgram {
	out := []crewTeamAgentProgram{}
	if api.agentProfiles == nil {
		return out
	}
	profile, err := api.agentProfiles.Resolve(workproduct.ProfileID, 0, owner)
	if err != nil {
		return out
	}
	profile = limitProfileModelsToAccount(r.Context(), owner, profileWithAvailableProviders(profile))
	index := map[string]int{}
	for _, option := range profile.Runtime.ProviderOptions {
		engine := strings.TrimSpace(option.Provider)
		if engine == "" {
			continue
		}
		if i, seen := index[engine]; seen {
			if option.Default {
				out[i].ModelID = option.ModelID
			}
			continue
		}
		index[engine] = len(out)
		out = append(out, crewTeamAgentProgram{Engine: engine, ModelID: option.ModelID, Label: providerAccountProductName(engine)})
	}
	return out
}
