package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
)

// crewTeamAgentProgram is one provider the Team agent can run on: the programs this server offers the Team owner.
type crewTeamAgentProgram struct {
	Engine  string `json:"engine"`
	ModelID string `json:"model_id,omitempty"` // its default model
	Label   string `json:"label"`
	// Models are the models of this provider the owner may pick for the Team agent (#3059).
	Models []crewTeamAgentModel `json:"models,omitempty"`
}

type crewTeamAgentModel struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// handleCrewTeamAgentProgram lets the Team owner see and change the Team agent's provider and model from the Team
// panel or chat (#2969, #3059). GET lists the providers on this server with their models and the current choice;
// POST {engine, model_id} saves it on the online home's Main chat (only providers and models the Team's own account
// can use are offered or accepted), then gives every per-person Team agent chat the same model.
func (api *StreamingAPI) handleCrewTeamAgentProgram(w http.ResponseWriter, r *http.Request) {
	t, claims, ok := crewTeamForTab(w, r, r.Method == http.MethodPost)
	if !ok {
		return
	}
	if claims.UserID != t.OwnerID {
		externalError(w, http.StatusForbidden, "owner_only", "Only the Team owner can change the Team agent's provider. Ask them to change it.")
		return
	}
	home, ok := crewTeamAgentHome(t)
	if !ok {
		externalError(w, http.StatusConflict, "team_agent_unavailable", "The Team agent isn't set up yet. Turn the Team on and try again in a minute.")
		return
	}
	// The Team agent runs as the Team's own account (team-<project>) with the server's AI accounts, never the owner's
	// personal logins, so the choices and the saved model are that account's.
	store := defaultProductConversationRegistryStore()
	mainKey := crewOwnerMainChatKey(t.OnlineHome)
	programs := api.crewTeamAgentPrograms(r, home.OwnerID)
	if r.Method == http.MethodGet {
		current, _ := store.agentIdentity(r.Context(), home.OwnerID, crewProfileID, mainKey)
		externalJSON(w, map[string]any{"programs": programs, "engine": current.Engine, "model_id": current.ModelID})
		return
	}
	var in struct {
		Engine  string `json:"engine"`
		ModelID string `json:"model_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in); err != nil || strings.TrimSpace(in.Engine) == "" {
		externalError(w, http.StatusBadRequest, "invalid_program", "Choose a provider for the Team agent.")
		return
	}
	var chosen *crewTeamAgentProgram
	for i := range programs {
		if programs[i].Engine == strings.TrimSpace(in.Engine) {
			chosen = &programs[i]
		}
	}
	if chosen == nil {
		externalError(w, http.StatusBadRequest, "program_unavailable", "That provider isn't available on this server. Pick one from the list.")
		return
	}
	model := chosen.ModelID
	if want := strings.TrimSpace(in.ModelID); want != "" {
		model = ""
		for _, m := range chosen.Models {
			if m.ID == want {
				model = want
			}
		}
		if model == "" {
			externalError(w, http.StatusBadRequest, "model_unavailable", "That model isn't available for "+chosen.Label+" on this server. Pick one from the list.")
			return
		}
	}
	if _, err := store.setAgentModel(r.Context(), home.OwnerID, crewProfileID, mainKey, chosen.Engine, model, "", "", claims.UserID); err != nil {
		log.Printf("[TEAM_AGENT] project=%s home=%s event=program_change engine=%s model=%s status=failed error=%q", t.ProjectID, t.OnlineHome, chosen.Engine, model, err.Error())
		externalError(w, http.StatusConflict, "team_agent_unavailable", "The Team agent hasn't started yet. Ask it something first, then change its provider.")
		return
	}
	// The panel's runtime labels are cached for 30s; drop them so the row shows the new program at once.
	crewTeamPanelRuntimeCache.Lock()
	clear(crewTeamPanelRuntimeCache.entries)
	crewTeamPanelRuntimeCache.Unlock()
	publishCrewPanelChanged(t.ProjectID)
	log.Printf("[TEAM_AGENT] project=%s home=%s event=program_change engine=%s model=%s status=done", t.ProjectID, t.OnlineHome, chosen.Engine, model)
	externalJSON(w, map[string]any{"program": chosen.Label, "engine": chosen.Engine, "model_id": model})
}

// crewTeamAgentHome is the Team's online home when it is set up: owned by the Team's own account, rostered from t.
func crewTeamAgentHome(t crewTeam) (crewTeam, bool) {
	if t.RosterSource != "" || t.OnlineHome == "" {
		return crewTeam{}, false
	}
	home, err := readCrewTeam(t.OnlineHome)
	if err != nil || home.RosterSource != t.ProjectID || home.OwnerID != "team-"+t.ProjectID || !crewTeamAccountActive(home.OwnerID) {
		return crewTeam{}, false
	}
	return home, true
}

// crewTeamAgentPrograms lists each provider the account may use once, with its default model and its models.
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
	seenModel := map[string]bool{}
	addModel := func(i int, id string) {
		id = strings.TrimSpace(id)
		if id == "" || seenModel[out[i].Engine+"\x00"+id] {
			return
		}
		seenModel[out[i].Engine+"\x00"+id] = true
		label := chatModelDisplayLabel(profile.Runtime, out[i].Engine, id)
		if _, after, found := strings.Cut(label, " · "); found {
			label = after
		}
		out[i].Models = append(out[i].Models, crewTeamAgentModel{ID: id, Label: label})
	}
	for _, option := range profile.Runtime.ProviderOptions {
		engine := strings.TrimSpace(option.Provider)
		if engine == "" {
			continue
		}
		i, seen := index[engine]
		if !seen {
			i = len(out)
			index[engine] = i
			out = append(out, crewTeamAgentProgram{Engine: engine, ModelID: option.ModelID, Label: providerAccountProductName(engine)})
		} else if option.Default {
			out[i].ModelID = option.ModelID
		}
		addModel(i, option.ModelID)
		for _, m := range option.Models {
			addModel(i, m)
		}
	}
	return out
}
