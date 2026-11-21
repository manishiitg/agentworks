package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"slices"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// #2993: before a workflow goes online, its page lists what it needs on the Team (decisions log, 19 Nov): online it
// runs as the Team with only the Team's Vault. Skills, notes and tables are copied with the workflow. A key or an
// API-key app is copied to the Team's Vault by the person's click, through the Team's own "way" (crew_team_way.go), so
// a value goes from Vault to Vault and never through an agent, the browser or this reply. An app you sign in to is
// never copied; the Team needs its own sign-in.

type crewWorkflowTeamNeed struct {
	Kind string `json:"kind"` // copied, key or app
	Name string `json:"name"`
	// ID is what Copy sends: a key's name or an app's connection ID. Set only when State is can_copy.
	ID string `json:"id,omitempty"`
	// State: on_team, can_copy, not_in_vault (a key you don't have either), this_mac_only (an app only in this Mac's
	// Vault) or needs_sign_in (an app you sign in to).
	State   string `json:"state"`
	AddedBy string `json:"added_by,omitempty"`
}

type crewWorkflowUsesTeam struct {
	Items []crewWorkflowTeamNeed `json:"items"`
	// NeedYou counts the rows that are not on the Team yet.
	NeedYou int `json:"need_you"`
}

// crewWorkflowTeamWay calls the Crew's Team "way" (what the Team shares) as the person: on a Mac through the Team's
// server, otherwise on this server. It is the same call as team_sync way / way_add from chat.
func (api *StreamingAPI) crewWorkflowTeamWay(ctx context.Context, crewProject, action string, args map[string]any) (string, error) {
	claims := GetUserFromContext(ctx)
	if claims == nil || crewProject == "" {
		return "", errors.New("This workflow's Crew is unavailable.")
	}
	if !IsLocalMode() {
		return api.crewTeamWayChat(ctx, claims, crewProject, action, args)
	}
	project, id := crewProject, ""
	if b, err := crewroot.ReadSync(crewProject); err == nil && b != nil && b.Side == "local" {
		project, id = b.PeerProjectID, b.ConnectionID
	} else if connections, err := readDesktopConnections(claims.UserID); err == nil && len(connections) == 1 {
		id = connections[0].ID
	}
	if id == "" {
		return "", errors.New("This Crew is not linked to a Team on a server.")
	}
	c, err := desktopConnectionFor(claims.UserID, id)
	if err != nil {
		return "", err
	}
	return api.crewTeamWayRemote(ctx, claims.UserID, c, project, action, args)
}

// crewWorkflowTeamNeeds is nil when the Crew has no Team this person is in (the way refuses), so the page shows no list.
func (api *StreamingAPI) crewWorkflowTeamNeeds(ctx context.Context, person string, binding *crewExecutableBinding, keys []string, apps []crewWorkflowUsesItem) *crewWorkflowUsesTeam {
	raw, err := api.crewWorkflowTeamWay(ctx, crewProjectIDOfRoot(binding.CrewRoot), "way", nil)
	if err != nil {
		return nil
	}
	var view teamWayView
	if json.Unmarshal([]byte(raw), &view) != nil {
		return nil
	}
	out := &crewWorkflowUsesTeam{Items: []crewWorkflowTeamNeed{{Kind: "copied", Name: "Skills, notes, tables", State: "on_team"}}}
	teamItem := func(kind, name string) (teamWayItem, bool) {
		i := slices.IndexFunc(view.Items, func(t teamWayItem) bool { return t.Kind == kind && strings.EqualFold(t.Name, name) })
		if i < 0 {
			return teamWayItem{}, false
		}
		return view.Items[i], true
	}
	for _, name := range keys {
		need := crewWorkflowTeamNeed{Kind: "key", Name: name, State: "not_in_vault"}
		if t, ok := teamItem("secret", name); ok {
			need.State, need.AddedBy = "on_team", t.AddedBy
		} else if _, mine := ownPersonalVaultKey(ctx, person, name); mine {
			need.State, need.ID = "can_copy", name
		}
		out.Items = append(out.Items, need)
	}
	for _, app := range apps {
		if app.server == "" || !app.Ready {
			continue // built-in tools need nothing on the Team; a missing app shows on the Apps row already
		}
		need := crewWorkflowTeamNeed{Kind: "app", Name: app.Name}
		id, local := strings.CutPrefix(app.server, "vault_")
		if !local {
			_, serverID, ok := parseServerVaultName(app.server)
			if !ok {
				continue
			}
			id = serverID
		}
		if t, ok := teamItem("app", app.Name); ok || slices.Contains(view.OnTeam, id) {
			need.State, need.AddedBy = "on_team", t.AddedBy
		} else if local && IsLocalMode() {
			// The Team's server cannot reach this Mac's own Vault, so there is nothing to copy from here.
			need.State = "this_mac_only"
			if c, err := vaultSavedConnection(ctx, id); err == nil && !teamWayShared(c) {
				need.State = "needs_sign_in"
			}
		} else if local {
			need.State, need.ID = "can_copy", id
			if c, err := vaultSavedConnection(ctx, id); err == nil && !teamWayShared(c) {
				need.State, need.ID = "needs_sign_in", ""
			}
		} else {
			// The server decides by login type when it is added; an app you sign in to comes back as a sign-in need.
			need.State, need.ID = "can_copy", id
		}
		out.Items = append(out.Items, need)
	}
	for _, need := range out.Items {
		if need.State != "on_team" {
			out.NeedYou++
		}
	}
	return out
}

// copyCrewWorkflowNeedToTeam is POST /api/crew-workflow-uses: Copy one key or app this workflow needs to the Team's
// Vault. Only a key the script declares or an app a run gets; the Team's way checks membership and copies the value.
func (api *StreamingAPI) copyCrewWorkflowNeedToTeam(w http.ResponseWriter, r *http.Request, claims *UserClaims, binding *crewExecutableBinding) {
	var in struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&in) != nil || (in.Kind != "key" && in.Kind != "app") || in.ID == "" {
		http.Error(w, "Choose a key or app to copy to the Team.", 400)
		return
	}
	uses, err := api.crewWorkflowUsesFor(r.Context(), claims.UserID, binding)
	if err != nil || uses.Team == nil {
		http.Error(w, "This workflow's Crew has no Team to copy to.", 409)
		return
	}
	if !slices.ContainsFunc(uses.Team.Items, func(n crewWorkflowTeamNeed) bool { return n.Kind == in.Kind && n.ID == in.ID && n.State == "can_copy" }) {
		http.Error(w, "Choose a key or app this workflow needs that you can copy.", 400)
		return
	}
	item := "secret"
	if in.Kind == "app" {
		item = "app"
	}
	reply, err := api.crewWorkflowTeamWay(r.Context(), crewProjectIDOfRoot(binding.CrewRoot), "way_add", map[string]any{"item": item, "id": in.ID})
	if err != nil {
		log.Printf("[CREW_WORKFLOW_TEAM] workflow=%s kind=%s result=refused", binding.ID, in.Kind)
		http.Error(w, err.Error(), 409)
		return
	}
	var view teamWayView
	_ = json.Unmarshal([]byte(reply), &view)
	log.Printf("[CREW_WORKFLOW_TEAM] workflow=%s kind=%s result=ok", binding.ID, in.Kind)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"notice": view.Notice})
}
