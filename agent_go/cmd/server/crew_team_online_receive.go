package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/redaction"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
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
	AddedByName          string `json:"added_by_name"`
	UpdatedBy            string `json:"updated_by"`
	UpdatedByName        string `json:"updated_by_name"`
	AddedAt              string `json:"added_at"`
	UpdatedAt            string `json:"updated_at"`
	Name                 string `json:"name"`
	WhenRuns             string `json:"when_runs,omitempty"`
	AutomationKind       string `json:"automation_kind,omitempty"`
	// Workflows: Team members with a draft open (Edit on Mac).
	Drafts []teamOnlineDraft `json:"drafts,omitempty"`
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
	if in.Kind == "automation" && in.SourceProjectID != in.TeamProjectID {
		return fmt.Errorf("Add to Team moves only this Team Crew’s own schedules and Watchers")
	}
	if in.Kind != "automation" && in.Kind != "dashboard" && in.Kind != "artifact" && in.Kind != "workflow" {
		return fmt.Errorf("choose a workflow, automation, dashboard or artifact")
	}
	if !(in.Preview && (in.Kind == "automation" || in.Kind == "workflow") && in.Revision == "") && (len(in.Revision) != 64 || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(in.Revision)) {
		return fmt.Errorf("use the item's current published or automation revision")
	}
	if in.OnlineID != "" && (in.Kind != "workflow" || !teamOnlineIdentifier.MatchString(in.OnlineID) || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(in.BaseRevision)) {
		return fmt.Errorf("update an online workflow from a draft opened with Edit on Mac")
	}
	if len(in.IncludeAutomations) > 100 || (in.Kind == "automation" || in.Kind == "workflow") && len(in.IncludeAutomations) > 0 {
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

// Server-only Team state, outside the Crew's model-writable file roots.
func teamOnlineMappingsPath(destination triggerTarget) (string, error) {
	dir, err := crewTeamDir(destination.CrewID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "team-online.json"), nil
}
func readTeamOnlineMappings(ctx context.Context, destination triggerTarget) ([]teamOnlineItemMapping, error) {
	filename, err := teamOnlineMappingsPath(destination)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(filename)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var mappings []teamOnlineItemMapping
	err = json.Unmarshal(raw, &mappings)
	return mappings, err
}
func saveTeamOnlineMappings(ctx context.Context, destination triggerTarget, mappings []teamOnlineItemMapping) error {
	filename, err := teamOnlineMappingsPath(destination)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
		return err
	}
	return fsutil.WriteJSONAtomic(filename, mappings, 0600)
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
	expected := teamOnlineDashboardItemID(sourceProject, kind, itemID)
	if kind == "workflow" {
		expected = teamOnlineWorkflowDestinationID(sourceProject, itemID)
	}
	if mappings[i].DestinationProjectID != team.OnlineHome || mappings[i].DestinationItemID != expected {
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
	m.UpdatedByName = teamOnlinePersonName(m.UpdatedBy)
	m.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if i >= 0 {
		m.AddedBy = mappings[i].AddedBy
		m.AddedByName = mappings[i].AddedByName
		m.AddedAt = mappings[i].AddedAt
		mappings[i] = m
	} else {
		m.AddedBy = m.UpdatedBy
		m.AddedByName = m.UpdatedByName
		m.AddedAt = m.UpdatedAt
		mappings = append(mappings, m)
	}
	if err = saveTeamOnlineMappings(ctx, destination, mappings); err != nil {
		return m, err
	}
	log.Printf("[TEAM_ONLINE_ITEM] step=mapped source_project=%s destination=%s kind=%s item_id=%s item_revision=%s", m.SourceProjectID, m.DestinationProjectID, m.Kind, m.ItemID, m.Revision)
	log.Printf("[TEAM_ONLINE] event=mapped person=%s source_project=%s destination=%s kind=%s item_id=%s destination_item_id=%s revision=%s", m.UpdatedBy, m.SourceProjectID, m.DestinationProjectID, m.Kind, m.ItemID, m.DestinationItemID, m.Revision)
	return m, nil
}

func (api *StreamingAPI) receiveTeamOnline(ctx context.Context, in teamOnlineRequest) (final teamOnlineResult, failure error) {
	defer func() {
		if failure != nil && in.validate() == nil {
			log.Printf("[TEAM_ONLINE_ITEM] step=failed team=%s source_project=%s kind=%s item_id=%s item_revision=%s reason=handoff_refused error=%q", in.TeamProjectID, in.SourceProjectID, in.Kind, in.ItemID, in.Revision, redaction.Text(failure.Error()))
		}
	}()
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
	log.Printf("[TEAM_ONLINE_ITEM] step=received team=%s source_project=%s kind=%s item_id=%s item_revision=%s", in.TeamProjectID, in.SourceProjectID, in.Kind, in.ItemID, in.Revision)
	team, err := teamOnlineHome(ctx, in.TeamProjectID)
	if err != nil {
		return result, err
	}
	// A draft updates the Team workflow it was opened from; any Team member may.
	lookupThis := func() (teamOnlineItemMapping, bool, error) {
		if in.OnlineID != "" {
			m, found, err := lookupTeamOnlineWorkflow(ctx, in.TeamProjectID, in.OnlineID)
			if err == nil && !found {
				err = fmt.Errorf("this workflow is no longer on the Team")
			}
			return m, found, err
		}
		return lookupTeamOnlineItem(ctx, in.TeamProjectID, in.SourceProjectID, in.Kind, in.ItemID)
	}
	previous, exists, err := lookupThis()
	if err != nil {
		return result, err
	}
	if in.OnlineID != "" && previous.Revision != in.BaseRevision {
		return result, teamOnlineChangedError(previous)
	}
	if in.OnlineID == "" && exists && previous.SourceOwner != claims.UserID {
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
	itemName := "Automation"
	if in.Kind == "workflow" {
		item, err := readTeamOnlineWorkflowItem(p)
		if err != nil {
			return result, err
		}
		result.Revision, itemName = item.Revision, item.Name
		result.DestinationItemID = teamOnlineWorkflowDestinationID(source.CrewID, in.ItemID)
		if in.OnlineID != "" {
			result.DestinationItemID = in.OnlineID
		}
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
	if in.Kind != "workflow" {
		result.DestinationItemID = teamOnlineDashboardItemID(source.CrewID, in.Kind, in.ItemID)
	}
	var dashboardRef teamOnlineDashboardRef
	if in.Kind != "automation" && in.Kind != "workflow" {
		file, err := p.root.Open(teamonline.Descriptor)
		if err != nil {
			return result, err
		}
		descriptor, err := teamonline.DecodeItem(file)
		file.Close()
		if err != nil {
			return result, err
		}
		itemName = descriptor.Snapshot.Title
		if itemName == "" {
			if in.Kind == "artifact" {
				itemName = "Artifact"
			} else {
				itemName = "Dashboard"
			}
		}
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
	current, found, err := lookupThis()
	if err != nil {
		return result, err
	}
	if found != exists || found && current.Revision != previous.Revision {
		return result, fmt.Errorf("the Team item changed; preview it again")
	}
	// The managed DB is ordinary provisioning, outside an item's atomic seed.
	if len(result.Dependencies.Tables) > 0 || in.Kind == "automation" || in.Kind == "workflow" {
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
	var workflowMapping teamOnlineItemMapping
	if in.Kind == "workflow" {
		if workflowMapping, err = api.installTeamOnlineWorkflow(ctx, source, destination, p, result.DestinationItemID); err != nil {
			return result, err
		}
		if in.OnlineID != "" {
			// Same Team workflow, a newer version: keep its identity; the
			// updater's draft is done, other drafts stay as heads-ups.
			workflowMapping.SourceProjectID, workflowMapping.ItemID = previous.SourceProjectID, previous.ItemID
			workflowMapping.Drafts = slices.DeleteFunc(slices.Clone(previous.Drafts), func(d teamOnlineDraft) bool { return d.By == claims.UserID })
		}
	}
	// Destination routines stay off until the authenticated source confirms its
	// copies have stopped. No watcher ledgers, logins or caller grants are copied.
	automations, notes, err := api.installTeamOnlineAutomations(ctx, source, destination, p, false)
	if err != nil {
		return result, err
	}
	result.Notes = append(result.Notes, notes...)
	if len(automations) > 0 {
		log.Printf("[TEAM_ONLINE] event=automation_move step=installed_paused source_project=%s item_id=%s destination=%s", source.CrewID, in.ItemID, destination.CrewID)
	}
	if in.Kind != "automation" && in.Kind != "workflow" {
		applied, err := api.receiveTeamOnlineDashboard(ctx, source, destination, dashboardRef, false)
		if err != nil {
			return result, err
		}
		mapping := teamOnlineItemMapping{SourceProjectID: source.CrewID, Kind: in.Kind, ItemID: in.ItemID, DestinationProjectID: destination.CrewID, DestinationItemID: applied.DestinationItemID, Revision: in.Revision, SubmissionID: in.SubmissionID, Name: itemName}
		log.Printf("[TEAM_ONLINE_ITEM] step=code_installed team=%s destination=%s kind=%s item_id=%s item_revision=%s", in.TeamProjectID, destination.CrewID, in.Kind, in.ItemID, in.Revision)
		if dashboardRef.Seed && len(applied.Tables) > 0 {
			log.Printf("[TEAM_ONLINE_ITEM] step=data_seeded team=%s destination=%s item_id=%s item_revision=%s", in.TeamProjectID, destination.CrewID, in.ItemID, in.Revision)
		}
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
		if len(packageAutomations.Schedules) > 0 {
			if err = stop("stop"); err != nil {
				return result, fmt.Errorf("the Team copy is paused because the source routine could not be stopped: %w", err)
			}
			log.Printf("[TEAM_ONLINE] event=automation_move step=source_stopped source_project=%s item_id=%s destination=%s", source.CrewID, in.ItemID, destination.CrewID)
		}
		automations, notes, err = api.installTeamOnlineAutomations(ctx, source, destination, p, true)
		if err == nil {
			err = confirmTeamOnlineSchedulesEnabled(ctx, destination, p)
		}
		if err == nil && len(packageAutomations.Schedules) > 0 {
			err = stop("complete")
		}
		if err != nil {
			_, _, _ = api.installTeamOnlineAutomations(ctx, source, destination, p, false)
			// The source is still online in this hand-off. Restore its exact
			// exported enable bits if destination enable fails.
			var restoreErr error
			if len(packageAutomations.Schedules) > 0 {
				restoreErr = stop("restart")
			}
			if restoreErr != nil {
				log.Printf("[TEAM_ONLINE] event=automation_move step=failed source_project=%s item_id=%s reason=source_restart_failed", source.CrewID, in.ItemID)
				return result, fmt.Errorf("the Team routine could not start and its source could not be restarted; turn it on in the source app")
			}
			if len(packageAutomations.Schedules) > 0 {
				log.Printf("[TEAM_ONLINE] event=automation_move step=source_restarted source_project=%s item_id=%s", source.CrewID, in.ItemID)
			}
			return result, err
		}
		log.Printf("[TEAM_ONLINE] event=automation_move step=enabled source_project=%s item_id=%s destination=%s", source.CrewID, in.ItemID, destination.CrewID)
		if len(packageAutomations.Schedules) > 0 {
			log.Printf("[TEAM_ONLINE_ITEM] step=schedule_moved team=%s source_project=%s destination=%s item_id=%s item_revision=%s", in.TeamProjectID, source.CrewID, destination.CrewID, in.ItemID, in.Revision)
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
	if in.Kind == "workflow" {
		workflowMapping.SubmissionID = in.SubmissionID
		if workflowMapping, err = acceptTeamOnlineMapping(ctx, destination, workflowMapping); err != nil {
			return result, err
		}
		result.AddedBy = workflowMapping.AddedBy
		result.Items = append(result.Items, workflowMapping)
		// One workflow: the source backs itself up and removes its copy now that
		// the online one is in place and its triggers are running here.
		p.moved = &teamOnlineMovedWorkflow{TeamProjectID: in.TeamProjectID, DestinationProjectID: destination.CrewID, DestinationItemID: workflowMapping.DestinationItemID}
		if err = stop("remove"); err != nil {
			result.Notes = append(result.Notes, "The workflow is online, but your own copy could not be removed: "+err.Error())
			log.Printf("[TEAM_ONLINE] event=workflow_moved step=source_remove_failed source_project=%s item_id=%s", source.CrewID, in.ItemID)
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
			if !in.Preview {
				api.restartLocalTeamOnlineSource(ctx, claims, in)
			}
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
	requested := project
	// Adapt the execution home to its source Team; that Team's normal consent
	// and current membership checks still run before any mappings are read.
	if home, err := readCrewTeam(project); err == nil && home.RosterSource != "" && home.OwnerID == "team-"+home.RosterSource {
		project = home.RosterSource
	}
	team, err := teamOnlineHome(r.Context(), project)
	if err != nil {
		externalError(w, 403, "team_member_required", err.Error())
		return
	}
	if requested != project && team.OnlineHome != requested {
		externalError(w, 403, "team_home_unavailable", "This Team's online home is unavailable.")
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

// The Mac that initiated the hand-off can compensate locally even if its relay
// disconnects while the server is enabling the routine. Use only its retained,
// source-validated export, never a request-supplied path or schedule body.
func (api *StreamingAPI) restartLocalTeamOnlineSource(ctx context.Context, claims *UserClaims, in teamOnlineRequest) {
	teamOnlineExports.Range(func(key, value any) bool {
		p := value.(*teamOnlinePackage)
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.owner != claims.UserID || !p.sourceStopped || p.request.TeamProjectID != in.TeamProjectID || p.request.SubmissionID != in.SubmissionID || p.request.ItemID != in.ItemID || p.request.Revision != in.Revision {
			return true
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if err := stopTeamOnlineSource(cleanup, p.sourceLocal, p, true); err != nil {
			log.Printf("[TEAM_ONLINE] event=automation_move step=failed source_project=%s item_id=%s reason=local_restart_failed", p.sourceLocal.CrewID, in.ItemID)
			return true
		}
		p.sourceStopped = false
		log.Printf("[TEAM_ONLINE] event=automation_move step=source_restarted source_project=%s item_id=%s", p.sourceLocal.CrewID, in.ItemID)
		return true
	})
}

func teamOnlinePersonName(id string) string {
	name := shareOwnerLabel(id)
	if name == "" || name == id {
		return "Teammate"
	}
	return name
}
