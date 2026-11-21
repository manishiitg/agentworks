package server

// GitHub #2993: after Move online there is one workflow, on the Team. A Team
// member changes it with Edit on Mac (a local draft that is never scheduled)
// and Update online, which replaces the Team copy only if nobody updated it
// since the draft was opened. Another member's open draft is a heads-up, not a
// lock.
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/publication"
)

type teamOnlineDraft struct {
	By     string `json:"by"`
	ByName string `json:"by_name"`
	At     string `json:"at"`
}

// The words the Mac looks for to offer Keep mine / Keep theirs.
const teamOnlineChangedMarker = "since you opened your draft"

func teamOnlineChangedError(m teamOnlineItemMapping) error {
	who := firstNonEmptyTrimmed(m.UpdatedByName, "a teammate")
	return fmt.Errorf("%s was updated online by %s %s (%s)", firstNonEmptyTrimmed(m.Name, "This workflow"), who, teamOnlineChangedMarker, m.UpdatedAt)
}

func lookupTeamOnlineWorkflow(ctx context.Context, teamProject, onlineID string) (teamOnlineItemMapping, bool, error) {
	team, err := teamOnlineHome(ctx, teamProject)
	if err != nil || team.OnlineHome == "" {
		return teamOnlineItemMapping{}, false, err
	}
	destination, err := resolveTriggerTarget(ctx, GetUserFromContext(ctx), "workspace:work:"+team.OnlineHome)
	if err != nil {
		return teamOnlineItemMapping{}, false, err
	}
	mappings, err := readTeamOnlineMappings(ctx, destination)
	if err != nil {
		return teamOnlineItemMapping{}, false, err
	}
	i := slices.IndexFunc(mappings, func(m teamOnlineItemMapping) bool { return m.Kind == "workflow" && m.DestinationItemID == onlineID })
	if i < 0 || mappings[i].DestinationProjectID != team.OnlineHome {
		return teamOnlineItemMapping{}, false, nil
	}
	return mappings[i], true, nil
}

type teamOnlineWorkflowDraftFiles struct {
	OnlineID string            `json:"online_id"`
	Name     string            `json:"name"`
	Revision string            `json:"revision"`
	Files    map[string]string `json:"files"`
}

// POST /api/crew-teams/{project}/online-workflows/{id}/draft (server): the
// Team workflow's code for a member's draft. The member is noted as having a
// draft open, so teammates see a heads-up.
func (api *StreamingAPI) handleCrewTeamOnlineWorkflowDraft(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	project, id := mux.Vars(r)["project"], mux.Vars(r)["id"]
	claims := GetUserFromContext(ctx)
	if claims == nil || !userAccessForClaims(claims).CanEdit {
		externalError(w, 403, "team_member_required", "Use your own Builder connection to edit Team workflows.")
		return
	}
	team, err := teamOnlineHome(ctx, project)
	if err != nil || team.OnlineHome == "" {
		externalError(w, 403, "team_member_required", "Join this Team before editing its workflows.")
		return
	}
	destination, err := resolveTriggerTarget(ctx, claims, "workspace:work:"+team.OnlineHome)
	if err != nil {
		externalError(w, 403, "team_home_unavailable", "This Team's online home is unavailable.")
		return
	}
	mu := crewTeamRecordMutex("team-online:" + destination.CrewID)
	mu.Lock()
	defer mu.Unlock()
	mappings, err := readTeamOnlineMappings(ctx, destination)
	if err != nil {
		externalError(w, 409, "team_items_unavailable", "The Team workflows could not be read.")
		return
	}
	i := slices.IndexFunc(mappings, func(m teamOnlineItemMapping) bool { return m.Kind == "workflow" && m.DestinationItemID == id })
	binding, bindErr := crewWorkflowByID(destination.CrewID, id)
	if i < 0 || bindErr != nil {
		externalError(w, 404, "workflow_not_found", "This workflow is no longer on the Team.")
		return
	}
	out := teamOnlineWorkflowDraftFiles{OnlineID: id, Name: mappings[i].Name, Revision: mappings[i].Revision, Files: map[string]string{}}
	codeRoot := path.Join(binding.Root, "code")
	listed, err := listWorkspaceFilesRecursive(ctx, codeRoot)
	if err != nil {
		externalError(w, 409, "workflow_unavailable", "This workflow's files could not be read.")
		return
	}
	var size int
	for _, name := range listed {
		rel := strings.TrimPrefix(strings.TrimPrefix(name, codeRoot), "/")
		if rel == "" || !teamOnlineWorkflowFileMoves(rel) {
			continue
		}
		raw, found, err := readFileFromWorkspace(ctx, path.Join(codeRoot, rel))
		if err != nil || !found {
			externalError(w, 409, "workflow_unavailable", "This workflow's files could not be read.")
			return
		}
		if size += len(raw); int64(size) > publication.MaxBytes {
			externalError(w, 409, "workflow_too_large", "This workflow is too large to edit on a computer.")
			return
		}
		out.Files[rel] = raw
	}
	draft := teamOnlineDraft{By: claims.UserID, ByName: teamOnlinePersonName(claims.UserID), At: time.Now().UTC().Format(time.RFC3339)}
	mappings[i].Drafts = append(slices.DeleteFunc(mappings[i].Drafts, func(d teamOnlineDraft) bool { return d.By == draft.By }), draft)
	if err = saveTeamOnlineMappings(ctx, destination, mappings); err != nil {
		externalError(w, 409, "team_items_unavailable", "The draft could not be noted on the Team.")
		return
	}
	log.Printf("[TEAM_ONLINE] event=workflow_draft_opened person=%s team=%s workflow=%s revision=%s", claims.UserID, project, id, out.Revision)
	externalJSON(w, out)
}

// teamOnlineConnection is how this Crew reaches its Team: on a Mac, the paired
// server connection; on the server itself, nil (call locally).
func teamOnlineConnection(ctx context.Context, source triggerTarget) (string, *desktopConnection, error) {
	if !IsLocalMode() {
		return source.CrewID, nil, nil
	}
	claims := GetUserFromContext(ctx)
	binding, err := crewroot.ReadSync(source.CrewID)
	if err != nil || binding == nil || binding.Side != "local" || binding.ConnectionID == "" {
		return "", nil, fmt.Errorf("join a Team in this Crew first; the workflow runs online on the Team")
	}
	c, err := desktopConnectionFor(claims.UserID, binding.ConnectionID)
	if err != nil {
		return "", nil, fmt.Errorf("connect to your Team's server and try again")
	}
	return binding.PeerProjectID, &c, nil
}

// teamOnlineRequestJSON calls a Team route: over the connection from a Mac, or
// in-process on the server with the same handler and checks.
func (api *StreamingAPI) teamOnlineRequestJSON(ctx context.Context, connection *desktopConnection, method, route string, handler func(http.ResponseWriter, *http.Request), vars map[string]string, out any) error {
	claims := GetUserFromContext(ctx)
	if connection != nil {
		return crewTeamRemoteJSON(ctx, claims.UserID, *connection, method, route, nil, out)
	}
	request, _ := http.NewRequestWithContext(ctx, method, route, nil)
	request = mux.SetURLVars(request, vars)
	response := &internalResponseCapture{header: http.Header{}}
	handler(response, request)
	if response.status >= 400 {
		var failure struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(response.body.Bytes(), &failure) == nil && failure.Error.Message != "" {
			return errors.New(failure.Error.Message)
		}
		return fmt.Errorf("%s", strings.TrimSpace(response.body.String()))
	}
	return json.Unmarshal(response.body.Bytes(), out)
}

type crewOnlineWorkflowRow struct {
	OnlineID      string   `json:"online_id"`
	Name          string   `json:"name"`
	UpdatedByName string   `json:"updated_by_name,omitempty"`
	UpdatedAt     string   `json:"updated_at,omitempty"`
	Revision      string   `json:"revision"`
	OtherDrafts   []string `json:"other_drafts"`
	// This person's own draft of it on this computer, if any.
	DraftWorkspacePath string `json:"draft_workspace_path,omitempty"`
	DraftStale         bool   `json:"draft_stale,omitempty"`
}

func (api *StreamingAPI) teamOnlineItems(ctx context.Context, teamProject string, connection *desktopConnection) ([]teamOnlineItemMapping, error) {
	var out struct {
		Items []teamOnlineItemMapping `json:"items"`
	}
	route := "/api/crew-teams/" + url.PathEscape(teamProject) + "/online-items"
	err := api.teamOnlineRequestJSON(ctx, connection, http.MethodGet, route, api.handleCrewTeamOnlineItems, map[string]string{"project": teamProject}, &out)
	return out.Items, err
}

// The Team's workflows as this Crew's menu shows them: one row each, with
// this person's own draft on this computer.
func (api *StreamingAPI) crewOnlineWorkflowRows(ctx context.Context, source triggerTarget) ([]crewOnlineWorkflowRow, error) {
	rows := []crewOnlineWorkflowRow{}
	teamProject, connection, err := teamOnlineConnection(ctx, source)
	if err != nil {
		return rows, nil
	}
	items, err := api.teamOnlineItems(ctx, teamProject, connection)
	if err != nil {
		return rows, err
	}
	notes, _ := readTeamOnlineMovedWorkflows(ctx, source)
	me := GetUserIDFromContext(ctx)
	for _, item := range items {
		if item.Kind != "workflow" {
			continue
		}
		row := crewOnlineWorkflowRow{OnlineID: item.DestinationItemID, Name: item.Name, UpdatedByName: item.UpdatedByName, UpdatedAt: item.UpdatedAt, Revision: item.Revision, OtherDrafts: []string{}}
		for _, d := range item.Drafts {
			if d.By != me {
				row.OtherDrafts = append(row.OtherDrafts, firstNonEmptyTrimmed(d.ByName, "A teammate"))
			}
		}
		for _, note := range notes {
			if note.DestinationItemID != item.DestinationItemID || note.DraftID == "" {
				continue
			}
			if binding, err := crewWorkflowByID(source.CrewID, note.DraftID); err == nil {
				row.DraftWorkspacePath, row.DraftStale = binding.Root, note.BaseRevision != item.Revision
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// editOnlineWorkflow makes a local draft of a Team workflow: its code, no
// schedules, so it never runs by itself. Returns the draft's workspace path.
func (api *StreamingAPI) editOnlineWorkflow(ctx context.Context, source triggerTarget, onlineID string) (string, error) {
	teamProject, connection, err := teamOnlineConnection(ctx, source)
	if err != nil {
		return "", err
	}
	if !teamOnlineIdentifier.MatchString(onlineID) {
		return "", fmt.Errorf("choose a workflow from the Team")
	}
	notes, _ := readTeamOnlineMovedWorkflows(ctx, source)
	for _, note := range notes {
		if note.DestinationItemID == onlineID && note.DraftID != "" {
			if binding, err := crewWorkflowByID(source.CrewID, note.DraftID); err == nil {
				return binding.Root, nil
			}
		}
	}
	var draft teamOnlineWorkflowDraftFiles
	route := "/api/crew-teams/" + url.PathEscape(teamProject) + "/online-workflows/" + url.PathEscape(onlineID) + "/draft"
	if err = api.teamOnlineRequestJSON(ctx, connection, http.MethodPost, route, api.handleCrewTeamOnlineWorkflowDraft, map[string]string{"project": teamProject, "id": onlineID}, &draft); err != nil {
		return "", err
	}
	if draft.Files["main.py"] == "" || draft.Files["script.json"] == "" {
		return "", fmt.Errorf("this Team workflow has no saved script yet")
	}
	id := uuid.NewString()
	binding := crewExecutableBinding{ID: id, CrewID: source.CrewID, CrewRoot: canonicalCrewWorkspaceRoot(source.Path), Root: "Workflow/" + id, Kind: "script"}
	crewWorkflowNamesMu.Lock()
	defer crewWorkflowNamesMu.Unlock()
	if taken, err := crewWorkflowNameTaken(ctx, source.CrewID, draft.Name, ""); err != nil {
		return "", err
	} else if taken {
		return "", fmt.Errorf("this Crew already has a workflow named %q; rename it first", draft.Name)
	}
	if err = registerCrewExecutable(binding); err != nil {
		return "", err
	}
	registerWorkflowOwner(binding.Root, source.CrewOwner)
	made := false
	defer func() {
		// A draft that could not be written leaves no empty workflow behind.
		if !made {
			_ = removeCrewExecutableBinding(binding.CrewID, binding.ID)
		}
	}()
	writeCtx := context.WithValue(ctx, crewScriptCreationKey{}, binding.Root)
	// script.json first: every other code file is checked against it.
	names := []string{"script.json"}
	for rel := range draft.Files {
		if rel != "script.json" {
			names = append(names, rel)
		}
	}
	for _, rel := range names {
		content := draft.Files[rel]
		if _, err := publication.CleanPath(rel); err != nil || !fs.ValidPath(rel) || !teamOnlineWorkflowFileMoves(rel) {
			continue
		}
		if rel == "script.json" {
			var definition map[string]any
			if err := json.Unmarshal([]byte(content), &definition); err != nil {
				return "", fmt.Errorf("this Team workflow's script.json is not valid")
			}
			definition["id"] = id
			encoded, _ := json.MarshalIndent(definition, "", "  ")
			content = string(encoded)
		}
		if err = writeFileToWorkspace(writeCtx, path.Join(binding.Root, "code", rel), content); err != nil {
			return "", err
		}
	}
	if api.scheduler != nil {
		api.scheduler.InvalidateWorkflowManifestCache()
	}
	notes = slices.DeleteFunc(notes, func(n teamOnlineMovedWorkflow) bool { return n.DestinationItemID == onlineID })
	notes = append(notes, teamOnlineMovedWorkflow{Name: draft.Name, TeamProjectID: teamProject, DestinationItemID: onlineID, Revision: draft.Revision, DraftID: id, BaseRevision: draft.Revision, MovedAt: time.Now().UTC().Format(time.RFC3339)})
	if err = writeTeamOnlineMovedWorkflows(ctx, source, notes); err != nil {
		return "", err
	}
	made = true
	log.Printf("[TEAM_ONLINE] event=workflow_draft_made source=%s workflow=%s online=%s", source.CrewID, id, onlineID)
	return binding.Root, nil
}

// draftNoteFor is the note of a local draft, if this workflow is one.
func draftNoteFor(ctx context.Context, source triggerTarget, workflowID string) (teamOnlineMovedWorkflow, bool) {
	notes, _ := readTeamOnlineMovedWorkflows(ctx, source)
	for _, note := range notes {
		if note.DraftID != "" && note.DraftID == workflowID {
			return note, true
		}
	}
	return teamOnlineMovedWorkflow{}, false
}

// discardOnlineDraft deletes the local draft; the Team copy is unchanged.
func (api *StreamingAPI) discardOnlineDraft(ctx context.Context, source triggerTarget, binding *crewExecutableBinding) error {
	note, ok := draftNoteFor(ctx, source, binding.ID)
	if !ok {
		return fmt.Errorf("this workflow is not a draft of a Team workflow")
	}
	if _, err := api.crewWorkflowRenameOrDelete(ctx, source.Path, "delete", binding, map[string]any{"confirm": true}); err != nil {
		return err
	}
	notes, _ := readTeamOnlineMovedWorkflows(ctx, source)
	for i := range notes {
		if notes[i].DraftID == note.DraftID {
			notes[i].DraftID, notes[i].BaseRevision = "", ""
		}
	}
	return writeTeamOnlineMovedWorkflows(ctx, source, notes)
}

func writeTeamOnlineMovedWorkflows(ctx context.Context, source triggerTarget, notes []teamOnlineMovedWorkflow) error {
	raw, _ := json.MarshalIndent(notes, "", "  ")
	return writeFileToWorkspace(ctx, path.Join(source.Path, teamOnlineMovedFile), string(raw))
}

func crewWorkflowSource(ctx context.Context, binding *crewExecutableBinding) (triggerTarget, error) {
	claims := GetUserFromContext(ctx)
	source, err := resolveTriggerTarget(ctx, claims, "workspace:work:"+binding.CrewID)
	if err != nil || source.CrewOwner != claims.UserID {
		return source, fmt.Errorf("use a workflow from your own Crew")
	}
	return source, nil
}

// GET /api/crew-online-workflows?crew_id=  (rows)
// POST /api/crew-online-workflows?crew_id=&action=edit&online_id=
// POST /api/crew-online-workflows?workspace_path=&action=discard
func (api *StreamingAPI) handleCrewOnlineWorkflows(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := GetUserFromContext(ctx)
	reply := func(value any, err error) {
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(value)
	}
	if r.URL.Query().Get("action") == "discard" {
		binding := api.crewScriptAuthoringBinding(w, r)
		if binding == nil {
			return
		}
		source, err := crewWorkflowSource(ctx, binding)
		if err == nil {
			err = api.discardOnlineDraft(ctx, source, binding)
		}
		reply(map[string]bool{"discarded": true}, err)
		return
	}
	if claims == nil || !userAccessForClaims(claims).CanEdit {
		http.Error(w, "Only this Crew's Builder can change its workflows.", http.StatusForbidden)
		return
	}
	source, err := resolveTriggerTarget(ctx, claims, "workspace:work:"+r.URL.Query().Get("crew_id"))
	if err != nil || source.CrewOwner != claims.UserID {
		http.Error(w, "Open one of your own Crews.", http.StatusForbidden)
		return
	}
	if r.Method == http.MethodGet {
		rows, err := api.crewOnlineWorkflowRows(ctx, source)
		reply(map[string]any{"workflows": rows}, err)
		return
	}
	if r.URL.Query().Get("action") != "edit" {
		http.Error(w, "Choose edit or discard.", http.StatusBadRequest)
		return
	}
	root, err := api.editOnlineWorkflow(ctx, source, r.URL.Query().Get("online_id"))
	reply(map[string]string{"workspace_path": root}, err)
}
