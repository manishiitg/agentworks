package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path"
	"regexp"
	"slices"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	"github.com/manishiitg/coding-agent-loop/workspace/teamonline"
)

type teamOnlineItemMapping struct {
	SourceOwner          string `json:"source_owner"`
	SourceProjectID      string `json:"source_project_id"`
	Kind                 string `json:"kind"`
	ItemID               string `json:"item_id"`
	DestinationProjectID string `json:"destination_project_id"`
	DestinationItemID    string `json:"destination_item_id"`
	Revision             string `json:"revision"`
	SubmissionID         string `json:"submission_id"`
	AddedBy              string `json:"added_by"`
	UpdatedBy            string `json:"updated_by"`
	AddedAt              string `json:"added_at"`
	UpdatedAt            string `json:"updated_at"`
	Name                 string `json:"name"`
	WhenRuns             string `json:"when_runs,omitempty"`
}
type teamOnlineDependencies struct {
	Tables      []teamonline.Table      `json:"tables"`
	Automations []teamonline.Automation `json:"automations"`
}
type teamOnlineResult struct {
	TeamProjectID        string                       `json:"team_project_id"`
	DestinationProjectID string                       `json:"destination_project_id,omitempty"`
	ItemID               string                       `json:"item_id"`
	DestinationItemID    string                       `json:"destination_item_id"`
	Revision             string                       `json:"revision"`
	SubmissionID         string                       `json:"submission_id"`
	Preview              bool                         `json:"preview"`
	Updated              bool                         `json:"updated"`
	AddedBy              string                       `json:"added_by,omitempty"`
	Dependencies         teamOnlineDependencies       `json:"dependencies"`
	Notes                []string                     `json:"notes"`
	Location             *teamOnlineDashboardLocation `json:"location,omitempty"`
	Items                []teamOnlineItemMapping      `json:"items,omitempty"`
}

var teamOnlineIdentifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

func (in teamOnlineRequest) validate() error {
	if !teamOnlineIdentifier.MatchString(in.TeamProjectID) || !teamOnlineIdentifier.MatchString(in.SourceProjectID) || !teamOnlineIdentifier.MatchString(in.ItemID) || !teamOnlineIdentifier.MatchString(in.SubmissionID) {
		return fmt.Errorf("choose the Team, source item and a submission ID")
	}
	if in.Kind != "automation" && in.Kind != "dashboard" && in.Kind != "artifact" {
		return fmt.Errorf("choose an automation, dashboard or artifact")
	}
	if !(in.Preview && in.Kind == "automation" && in.Revision == "") && (len(in.Revision) != 64 || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(in.Revision)) {
		return fmt.Errorf("use the item's current published or automation revision")
	}
	if len(in.IncludeAutomations) > 100 || in.Kind == "automation" && len(in.IncludeAutomations) > 0 {
		return fmt.Errorf("choose dashboard dependencies from its preview")
	}
	return nil
}
func teamOnlineHome(ctx context.Context, project string) (crewTeam, error) {
	claims := GetUserFromContext(ctx)
	team, err := readCrewTeam(project)
	if claims == nil || !crewTeamClaims(claims, project, false) || err != nil || !team.On || !team.member(claims.UserID) || !crewTeamAccountActive(claims.UserID) || team.RosterSource != "" || ownerOfCrewProjectID(project) != team.OwnerID {
		return crewTeam{}, fmt.Errorf("join this Team before adding work to it")
	}
	return team, nil
}
func readTeamOnlineMappings(ctx context.Context, destination triggerTarget) ([]teamOnlineItemMapping, error) {
	raw, found, err := readFileFromWorkspace(ctx, path.Join(destination.Path, "team-online.json"))
	if err != nil || !found {
		return nil, err
	}
	var mappings []teamOnlineItemMapping
	err = json.Unmarshal([]byte(raw), &mappings)
	return mappings, err
}
func saveTeamOnlineMappings(ctx context.Context, destination triggerTarget, mappings []teamOnlineItemMapping) error {
	raw, err := json.MarshalIndent(mappings, "", "  ")
	if err != nil {
		return err
	}
	return writeFileToWorkspace(ctx, path.Join(destination.Path, "team-online.json"), string(raw)+"\n")
}

// Shared by source Open and DB-action routing: one accepted mapping, with
// current Team membership and normal home access. It never creates a home.
func lookupTeamOnlineItem(ctx context.Context, teamProject, sourceProject, kind, itemID string) (teamOnlineItemMapping, bool, error) {
	team, err := teamOnlineHome(ctx, teamProject)
	if err != nil {
		return teamOnlineItemMapping{}, false, err
	}
	if team.OnlineHome == "" {
		return teamOnlineItemMapping{}, false, nil
	}
	destination, err := resolveTriggerTarget(ctx, GetUserFromContext(ctx), "workspace:work:"+team.OnlineHome)
	if err != nil {
		return teamOnlineItemMapping{}, false, err
	}
	mappings, err := readTeamOnlineMappings(ctx, destination)
	if err != nil {
		return teamOnlineItemMapping{}, false, err
	}
	i := slices.IndexFunc(mappings, func(v teamOnlineItemMapping) bool {
		return v.SourceProjectID == sourceProject && v.Kind == kind && v.ItemID == itemID
	})
	if i < 0 {
		return teamOnlineItemMapping{}, false, nil
	}
	if mappings[i].DestinationProjectID != team.OnlineHome || mappings[i].DestinationItemID != teamOnlineDashboardItemID(sourceProject, kind, itemID) {
		return teamOnlineItemMapping{}, false, fmt.Errorf("this Team item mapping is unavailable")
	}
	return mappings[i], true, nil
}
func acceptTeamOnlineMapping(ctx context.Context, destination triggerTarget, m teamOnlineItemMapping) (teamOnlineItemMapping, error) {
	mappings, err := readTeamOnlineMappings(ctx, destination)
	if err != nil {
		return m, err
	}
	i := slices.IndexFunc(mappings, func(v teamOnlineItemMapping) bool {
		return v.SourceProjectID == m.SourceProjectID && v.Kind == m.Kind && v.ItemID == m.ItemID
	})
	m.SourceOwner = GetUserIDFromContext(ctx)
	m.UpdatedBy = GetUserIDFromContext(ctx)
	m.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if i >= 0 {
		m.AddedBy = mappings[i].AddedBy
		m.AddedAt = mappings[i].AddedAt
		mappings[i] = m
	} else {
		m.AddedBy = m.UpdatedBy
		m.AddedAt = m.UpdatedAt
		mappings = append(mappings, m)
	}
	if err = saveTeamOnlineMappings(ctx, destination, mappings); err != nil {
		return m, err
	}
	log.Printf("[TEAM_ONLINE] event=mapped person=%s source_project=%s destination=%s kind=%s item_id=%s destination_item_id=%s revision=%s", m.UpdatedBy, m.SourceProjectID, m.DestinationProjectID, m.Kind, m.ItemID, m.DestinationItemID, m.Revision)
	return m, nil
}

func (api *StreamingAPI) receiveTeamOnline(ctx context.Context, in teamOnlineRequest) (teamOnlineResult, error) {
	result := teamOnlineResult{TeamProjectID: in.TeamProjectID, ItemID: in.ItemID, Revision: in.Revision, SubmissionID: in.SubmissionID, Preview: in.Preview, Notes: []string{}, Dependencies: teamOnlineDependencies{Tables: []teamonline.Table{}, Automations: []teamonline.Automation{}}}
	if IsLocalMode() {
		return result, fmt.Errorf("send this hand-off to the Team's server")
	}
	claims := GetUserFromContext(ctx)
	if !crewTeamClaims(claims, in.TeamProjectID, true) || !userAccessForClaims(claims).CanEdit {
		return result, fmt.Errorf("use your own Builder connection to add work to this Team")
	}
	if err := in.validate(); err != nil {
		return result, err
	}
	team, err := teamOnlineHome(ctx, in.TeamProjectID)
	if err != nil {
		return result, err
	}
	previous, exists, err := lookupTeamOnlineItem(ctx, in.TeamProjectID, in.SourceProjectID, in.Kind, in.ItemID)
	if err != nil {
		return result, err
	}
	if exists && previous.SourceOwner != claims.UserID {
		return result, fmt.Errorf("update only items from your own source Crew")
	}
	result.Updated = exists
	result.DestinationProjectID = team.OnlineHome
	source, p, stop, closePackage, err := api.fetchTeamOnlineSource(ctx, in, !exists)
	if err != nil {
		return result, err
	}
	defer closePackage()
	if in.Kind == "automation" {
		item, err := readTeamOnlineAutomations(p)
		if err != nil {
			return result, err
		}
		if len(item.Schedules) != 1 || item.Schedules[0].ID != in.ItemID {
			return result, fmt.Errorf("choose one owned automation")
		}
		result.Revision = teamOnlineScheduleRevision(item.Schedules[0])
	}
	// Recheck after export; no actor, owner or destination is accepted from JSON.
	if _, err = teamOnlineHome(ctx, in.TeamProjectID); err != nil {
		return result, err
	}
	var destination triggerTarget
	if team.OnlineHome != "" {
		destination, err = resolveTriggerTarget(ctx, GetUserFromContext(ctx), "workspace:work:"+team.OnlineHome)
		if err != nil {
			return result, err
		}
	}
	result.DestinationItemID = teamOnlineDashboardItemID(source.CrewID, in.Kind, in.ItemID)
	var dashboardRef teamOnlineDashboardRef
	if in.Kind != "automation" {
		dashboardRef = teamOnlineDashboardRef{ItemID: in.ItemID, Kind: in.Kind, Revision: in.Revision, SubmissionID: in.SubmissionID, Package: p.root, Seed: !exists}
		dashboardRef.SourceAdmission = &teamOnlineDashboardSourceAdmission{person: GetUserIDFromContext(ctx), owner: source.CrewOwner, sourceProjectID: source.CrewID, kind: in.Kind, itemID: in.ItemID, revision: in.Revision, submissionID: in.SubmissionID, packageRoot: p.root}
		preview, err := api.receiveTeamOnlineDashboard(ctx, source, destination, dashboardRef, true)
		if err != nil {
			return result, err
		}
		result.Dependencies.Tables = preview.Tables
		result.Dependencies.Automations = preview.Automations
		for _, id := range in.IncludeAutomations {
			if !slices.ContainsFunc(preview.Automations, func(a teamonline.Automation) bool { return a.ID == id }) {
				return result, fmt.Errorf("choose only automations from this item's dependency preview")
			}
		}
	}
	if time.Now().After(p.expires) {
		return result, fmt.Errorf("the item package expired; preview it again")
	}
	if in.Preview {
		log.Printf("[TEAM_ONLINE] event=preview person=%s team=%s source_project=%s kind=%s item_id=%s revision=%s", GetUserIDFromContext(ctx), in.TeamProjectID, source.CrewID, in.Kind, in.ItemID, in.Revision)
		return result, nil
	}
	if destination.CrewID == "" {
		// Only this explicit hand-off may provision a home. An agent ask never does.
		home, err := api.ensureCrewTeamOnlineHome(ctx, in.TeamProjectID)
		if err != nil {
			return result, err
		}
		destination, err = resolveTriggerTarget(ctx, GetUserFromContext(ctx), "workspace:work:"+home)
		if err != nil {
			return result, err
		}
		result.DestinationProjectID = home
	}
	if destination.CrewOwner != "team-"+in.TeamProjectID {
		return result, fmt.Errorf("this Team home is unavailable")
	}
	if api.projectActions == nil {
		return result, fmt.Errorf("the scheduler is unavailable")
	}
	if _, err = api.projectActions.projectBuilderOwner(ctx, claims.UserID, crewProfileID, destination.CrewID); err != nil {
		return result, err
	}
	if token := claims.AccessToken; token != nil {
		if !token.AllowsCrew(destination.CrewID) {
			return result, fmt.Errorf("allow this Team Crew in your connection settings")
		}
		if guard := token.FileGuard; guard != nil {
			for _, name := range []string{"team-online.json", "schedules.json", "functions.json", "code/triggers", "db"} {
				if !guard.Allows(name, true) {
					return result, fmt.Errorf("this connection does not allow the Team item files")
				}
			}
		}
	}
	// One item admission at a time in the ordinary destination. Membership is
	// rechecked here, after the network round trip and before any installation.
	mu := crewTeamRecordMutex("team-online:" + destination.CrewID)
	mu.Lock()
	defer mu.Unlock()
	if _, err = teamOnlineHome(ctx, in.TeamProjectID); err != nil {
		return result, err
	}
	current, found, err := lookupTeamOnlineItem(ctx, in.TeamProjectID, in.SourceProjectID, in.Kind, in.ItemID)
	if err != nil {
		return result, err
	}
	if found != exists || found && current.Revision != previous.Revision {
		return result, fmt.Errorf("the Team item changed; preview it again")
	}
	// The managed DB is ordinary provisioning, outside an item's atomic seed.
	if len(result.Dependencies.Tables) > 0 || in.Kind == "automation" {
		client := workspace.NewClient(getWorkspaceAPIURL(), workspace.WithUserID(destination.CrewOwner))
		if _, err = client.InitializeWorkflowDB(ctx, workspace.InitializeWorkflowDBParams{DBPath: path.Join(destination.Path, "db", "db.sqlite")}); err != nil {
			return result, err
		}
	}
	packageAutomations, err := readTeamOnlineAutomations(p)
	if err != nil {
		return result, err
	}
	for _, schedule := range packageAutomations.Schedules {
		mapping, found, err := lookupTeamOnlineItem(ctx, in.TeamProjectID, source.CrewID, "automation", schedule.ID)
		if err != nil {
			return result, err
		}
		if found && mapping.SourceOwner != claims.UserID {
			return result, fmt.Errorf("update only automations from your own source Crew")
		}
	}
	// Destination routines stay off until the authenticated source confirms its
	// copies have stopped. No watcher ledgers, logins or caller grants are copied.
	automations, notes, err := api.installTeamOnlineAutomations(ctx, source, destination, p, false)
	if err != nil {
		return result, err
	}
	result.Notes = append(result.Notes, notes...)
	if in.Kind != "automation" {
		applied, err := api.receiveTeamOnlineDashboard(ctx, source, destination, dashboardRef, false)
		if err != nil {
			return result, err
		}
		mapping := teamOnlineItemMapping{SourceProjectID: source.CrewID, Kind: in.Kind, ItemID: in.ItemID, DestinationProjectID: destination.CrewID, DestinationItemID: applied.DestinationItemID, Revision: in.Revision, SubmissionID: in.SubmissionID, Name: in.ItemID}
		mapping, err = acceptTeamOnlineMapping(ctx, destination, mapping)
		if err != nil {
			return result, err
		}
		result.AddedBy = mapping.AddedBy
		result.Items = append(result.Items, mapping)
		location, err := api.teamOnlineDashboardLocation(ctx, destination, applied.DestinationItemID)
		if err != nil {
			return result, err
		}
		result.Location = &location
	}
	if len(automations) > 0 {
		if err = stop(); err != nil {
			return result, fmt.Errorf("the Team copy is paused because the source routine could not be stopped: %w", err)
		}
		automations, notes, err = api.installTeamOnlineAutomations(ctx, source, destination, p, true)
		if err != nil {
			return result, err
		}
		result.Notes = append(result.Notes, notes...)
		for _, mapping := range automations {
			mapping.SubmissionID = in.SubmissionID
			mapping, err = acceptTeamOnlineMapping(ctx, destination, mapping)
			if err != nil {
				return result, err
			}
			result.Items = append(result.Items, mapping)
			if in.Kind == "automation" {
				result.AddedBy = mapping.AddedBy
			}
		}
	}
	log.Printf("[TEAM_ONLINE] event=received person=%s team=%s source_project=%s destination=%s kind=%s item_id=%s revision=%s", GetUserIDFromContext(ctx), in.TeamProjectID, source.CrewID, destination.CrewID, in.Kind, in.ItemID, in.Revision)
	return result, nil
}

func (api *StreamingAPI) handleCrewTeamReceiveOnline(w http.ResponseWriter, r *http.Request) {
	var in teamOnlineRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil {
		externalError(w, 400, "invalid_item", "Choose a source item to add to the Team.")
		return
	}
	project := mux.Vars(r)["project"]
	if in.TeamProjectID != "" && in.TeamProjectID != project {
		externalError(w, 400, "wrong_team", "Choose the same Team for this hand-off.")
		return
	}
	in.TeamProjectID = project
	result, err := api.receiveTeamOnline(r.Context(), in)
	if err != nil {
		externalError(w, 409, "team_online_refused", err.Error())
		return
	}
	externalJSON(w, result)
}

func (api *StreamingAPI) teamOnlineChat(ctx context.Context, claims *UserClaims, connection *desktopConnection, teamProject string, args map[string]any) (string, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return "", err
	}
	var in teamOnlineRequest
	if err = json.Unmarshal(raw, &in); err != nil {
		return "", err
	}
	in.TeamProjectID = teamProject
	if in.SourceProjectID == "" {
		in.SourceProjectID = externalArg(args, "source_project_id")
	}
	if connection != nil {
		var out teamOnlineResult
		if err = crewTeamRemoteJSON(ctx, claims.UserID, *connection, http.MethodPost, "/api/crew-teams/"+teamProject+"/receive-online", in, &out); err != nil {
			return "", err
		}
		data, err := json.Marshal(out)
		return string(data), err
	}
	result, err := api.receiveTeamOnline(ctx, in)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(result)
	return string(data), err
}

// Keep lookup independent of model-provided destination paths.
func (api *StreamingAPI) teamOnlineAcceptedLocation(ctx context.Context, team, source, kind, item string) (teamOnlineDashboardLocation, bool, error) {
	mapping, found, err := lookupTeamOnlineItem(ctx, team, source, kind, item)
	if err != nil || !found {
		return teamOnlineDashboardLocation{}, found, err
	}
	destination, err := resolveTriggerTarget(ctx, GetUserFromContext(ctx), "workspace:work:"+mapping.DestinationProjectID)
	if err != nil {
		return teamOnlineDashboardLocation{}, false, err
	}
	location, err := api.teamOnlineDashboardLocation(ctx, destination, mapping.DestinationItemID)
	return location, true, err
}

// The How we work card lists these mappings, not a second schedule copy.
func (api *StreamingAPI) handleCrewTeamOnlineItems(w http.ResponseWriter, r *http.Request) {
	project := mux.Vars(r)["project"]
	team, err := teamOnlineHome(r.Context(), project)
	if err != nil {
		externalError(w, 403, "team_member_required", err.Error())
		return
	}
	if team.OnlineHome == "" {
		externalJSON(w, map[string]any{"items": []teamOnlineItemMapping{}})
		return
	}
	destination, err := resolveTriggerTarget(r.Context(), GetUserFromContext(r.Context()), "workspace:work:"+team.OnlineHome)
	if err != nil {
		externalError(w, 403, "team_home_unavailable", err.Error())
		return
	}
	items, err := readTeamOnlineMappings(r.Context(), destination)
	if err != nil {
		externalError(w, 409, "team_items_unavailable", "The Team items could not be read.")
		return
	}
	externalJSON(w, map[string]any{"home_project_id": team.OnlineHome, "items": items})
}
