package server

// GitHub #2993: Move online. A workflow moves from a person's Crew to their
// Team's online home as one server action over the existing item package
// (prepare -> chunks -> install). The online copy runs as the Team with Team
// keys; once it is installed the source keeps a plain backup and removes its
// own copy, so there is one workflow.
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/projectactions"
	"github.com/manishiitg/coding-agent-loop/workspace/publication"
)

const teamOnlineWorkflowDescriptor = "workflow-item.json"

// The package keeps the workflow's code under code/ and the Crew skills it
// selected under skills/<name>/.
type teamOnlineWorkflowItem struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Revision    string   `json:"revision"`
	Skills      []string `json:"skills"`
}

type teamOnlineMovedWorkflow struct {
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	TeamProjectID        string `json:"team_project_id"`
	DestinationProjectID string `json:"destination_project_id"`
	DestinationItemID    string `json:"destination_item_id"`
	Revision             string `json:"revision"`
	Backup               string `json:"backup"`
	MovedAt              string `json:"moved_at"`
	// Edit on Mac: the local draft and the online revision it started from.
	DraftID      string `json:"draft_id,omitempty"`
	BaseRevision string `json:"base_revision,omitempty"`
}

const teamOnlineMovedFile = "moved-online.json"

// One stable online identity per source workflow, so a later update replaces
// the same Team workflow instead of adding another.
func teamOnlineWorkflowDestinationID(sourceProject, workflowID string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("agentworks-team-online\x00"+sourceProject+"\x00"+workflowID)).String()
}

// Skip caches and local environments; only the workflow's own source moves.
func teamOnlineWorkflowFileMoves(rel string) bool {
	for _, part := range strings.Split(rel, "/") {
		if part == "__pycache__" || part == ".venv" || part == "venv" || part == "node_modules" || strings.HasPrefix(part, ".") {
			return false
		}
	}
	return !strings.HasSuffix(rel, ".pyc")
}

func teamOnlineWorkflowRevision(files map[string]string) string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	hash := sha256.New()
	for _, name := range names {
		io.WriteString(hash, name+"\x00"+files[name]+"\x00")
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// readTeamOnlineWorkflowSource reads the files that move: the code folder and
// the Crew skills the workflow selected. Keys never move; scripts read them
// from the run environment by name.
func (api *StreamingAPI) readTeamOnlineWorkflowSource(ctx context.Context, source triggerTarget, id string) (*crewExecutableBinding, teamOnlineWorkflowItem, map[string]string, error) {
	item := teamOnlineWorkflowItem{Skills: []string{}}
	binding, err := crewWorkflowByID(source.CrewID, id)
	if err != nil {
		return nil, item, nil, fmt.Errorf("choose a workflow in this Crew")
	}
	definition, err := readCrewScript(ctx, binding.Root)
	if err != nil {
		return nil, item, nil, fmt.Errorf("this workflow's script could not be read")
	}
	item.ID, item.Name, item.Description = binding.ID, definition.Name, definition.Description
	files := map[string]string{}
	codeRoot := path.Join(binding.Root, "code")
	listed, err := listWorkspaceFilesRecursive(ctx, codeRoot)
	if err != nil {
		return nil, item, nil, err
	}
	for _, name := range listed {
		rel := strings.TrimPrefix(strings.TrimPrefix(name, codeRoot), "/")
		if rel == "" || !teamOnlineWorkflowFileMoves(rel) {
			continue
		}
		raw, found, err := readFileFromWorkspace(ctx, path.Join(codeRoot, rel))
		if err != nil || !found {
			return nil, item, nil, fmt.Errorf("a workflow file could not be read")
		}
		files["code/"+rel] = raw
	}
	if manifest, found, err := ReadWorkflowManifest(ctx, binding.Root); err == nil && found {
		for _, skill := range manifest.Capabilities.SelectedSkills {
			if !teamOnlineIdentifier.MatchString(skill) || slices.Contains(item.Skills, skill) {
				continue
			}
			skillRoot := path.Join(source.Path, "skills", skill)
			names, err := listWorkspaceFilesRecursive(ctx, skillRoot)
			if err != nil || len(names) == 0 {
				continue
			}
			for _, name := range names {
				rel := strings.TrimPrefix(strings.TrimPrefix(name, skillRoot), "/")
				if rel == "" || !teamOnlineWorkflowFileMoves(rel) {
					continue
				}
				raw, found, err := readFileFromWorkspace(ctx, path.Join(skillRoot, rel))
				if err != nil || !found {
					return nil, item, nil, fmt.Errorf("the skill %s could not be read", skill)
				}
				files["skills/"+skill+"/"+rel] = raw
			}
			item.Skills = append(item.Skills, skill)
		}
	}
	if files["code/main.py"] == "" || files["code/script.json"] == "" {
		return nil, item, nil, fmt.Errorf("save this workflow's main.py and script.json first")
	}
	item.Revision = teamOnlineWorkflowRevision(files)
	return binding, item, files, nil
}

// exportTeamOnlineWorkflow stages the workflow into the package and returns the
// IDs of the schedules, Watchers and webhooks that start it.
func (api *StreamingAPI) exportTeamOnlineWorkflow(ctx context.Context, source triggerTarget, p *teamOnlinePackage) ([]string, error) {
	_, item, files, err := api.readTeamOnlineWorkflowSource(ctx, source, p.request.ItemID)
	if err != nil {
		return nil, err
	}
	if p.request.Revision != "" && p.request.Revision != item.Revision {
		return nil, fmt.Errorf("the workflow changed; preview it again")
	}
	write := func(name, content string) error {
		if int64(len(content)) > publication.MaxFileBytes {
			return fmt.Errorf("a workflow file is larger than the normal publication limit")
		}
		if dir := path.Dir(name); dir != "." {
			if err := p.root.MkdirAll(dir, 0700); err != nil {
				return err
			}
		}
		f, err := p.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, err = io.WriteString(f, content)
		if ce := f.Close(); err == nil {
			err = ce
		}
		return err
	}
	for name, content := range files {
		if err := write(name, content); err != nil {
			return nil, err
		}
	}
	raw, _ := json.Marshal(item)
	if err := write(teamOnlineWorkflowDescriptor, string(raw)); err != nil {
		return nil, err
	}
	schedules, err := readProjectSchedules(ctx, source.Path)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, s := range schedules {
		if s.Action.WorkflowID == item.ID {
			ids = append(ids, s.ID)
		}
	}
	return ids, nil
}

func readTeamOnlineWorkflowItem(p *teamOnlinePackage) (teamOnlineWorkflowItem, error) {
	var item teamOnlineWorkflowItem
	f, err := p.root.Open(teamOnlineWorkflowDescriptor)
	if err != nil {
		return item, fmt.Errorf("the workflow package is incomplete")
	}
	defer f.Close()
	decoder := json.NewDecoder(f)
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&item); err != nil {
		return item, fmt.Errorf("the workflow package is incomplete")
	}
	files := map[string]string{}
	err = fs.WalkDir(p.root.FS(), ".", func(name string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == ".chunks" {
			return fs.SkipDir
		}
		if d.IsDir() || !(strings.HasPrefix(name, "code/") || strings.HasPrefix(name, "skills/")) {
			return nil
		}
		raw, err := fs.ReadFile(p.root.FS(), name)
		files[name] = string(raw)
		return err
	})
	if err != nil {
		return item, err
	}
	if item.ID != p.request.ItemID || teamOnlineWorkflowRevision(files) != item.Revision {
		return item, fmt.Errorf("the workflow package does not match its revision")
	}
	return item, nil
}

// installTeamOnlineWorkflow writes the workflow into the Team's home under its
// stable online ID. An update replaces the code, keeps the Team's own settings
// (keys, apps) and runs. Skills go into the workflow's own code folder, where
// its agent() calls load them.
func (api *StreamingAPI) installTeamOnlineWorkflow(ctx context.Context, source, destination triggerTarget, p *teamOnlinePackage, id string) (teamOnlineItemMapping, error) {
	item, err := readTeamOnlineWorkflowItem(p)
	if err != nil {
		return teamOnlineItemMapping{}, err
	}
	crewWorkflowNamesMu.Lock()
	defer crewWorkflowNamesMu.Unlock()
	if taken, err := crewWorkflowNameTaken(ctx, destination.CrewID, item.Name, id); err != nil {
		return teamOnlineItemMapping{}, err
	} else if taken {
		return teamOnlineItemMapping{}, fmt.Errorf("the Team already has a workflow named %q; rename one of them and try again", item.Name)
	}
	binding, err := crewWorkflowByID(destination.CrewID, id)
	if err != nil {
		binding = &crewExecutableBinding{ID: id, CrewID: destination.CrewID, CrewRoot: canonicalCrewWorkspaceRoot(destination.Path), Root: "Workflow/" + id, Kind: "script"}
		if err = registerCrewExecutable(*binding); err != nil {
			return teamOnlineItemMapping{}, err
		}
		registerWorkflowOwner(binding.Root, destination.CrewOwner)
	}
	codeRoot := path.Join(binding.Root, "code")
	writeCtx := context.WithValue(ctx, crewScriptCreationKey{}, binding.Root)
	wanted := map[string]bool{}
	// script.json goes first: the workspace writer checks every other code file
	// against the saved script, which a new online workflow does not have yet.
	if err = api.writeTeamOnlineWorkflowFile(writeCtx, p, codeRoot, "code/script.json", "script.json", id, wanted); err != nil {
		return teamOnlineItemMapping{}, err
	}
	err = fs.WalkDir(p.root.FS(), ".", func(name string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == ".chunks" {
			return fs.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		var rel string
		switch {
		case strings.HasPrefix(name, "code/"):
			rel = strings.TrimPrefix(name, "code/")
		case strings.HasPrefix(name, "skills/"):
			rel = name
		default:
			return nil
		}
		if rel == "script.json" {
			return nil
		}
		return api.writeTeamOnlineWorkflowFile(writeCtx, p, codeRoot, name, rel, id, wanted)
	})
	if err != nil {
		return teamOnlineItemMapping{}, err
	}
	if listed, err := listWorkspaceFilesRecursive(ctx, codeRoot); err == nil {
		for _, name := range listed {
			rel := strings.TrimPrefix(strings.TrimPrefix(name, codeRoot), "/")
			if rel != "" && teamOnlineWorkflowFileMoves(rel) && !wanted[rel] {
				_ = deleteWorkspaceFile(writeCtx, path.Join(codeRoot, rel))
			}
		}
	}
	if api.scheduler != nil {
		api.scheduler.InvalidateWorkflowManifestCache()
	}
	log.Printf("[TEAM_ONLINE_ITEM] step=workflow_installed destination=%s workflow=%s revision=%s files=%d", destination.CrewID, id, item.Revision, len(wanted))
	return teamOnlineItemMapping{SourceProjectID: source.CrewID, Kind: "workflow", ItemID: item.ID, DestinationProjectID: destination.CrewID, DestinationItemID: id, Revision: item.Revision, Name: item.Name}, nil
}

// writeTeamOnlineWorkflowFile copies one package file into the online
// workflow; script.json takes the binding's identity.
func (api *StreamingAPI) writeTeamOnlineWorkflowFile(ctx context.Context, p *teamOnlinePackage, codeRoot, name, rel, id string, wanted map[string]bool) error {
	raw, err := fs.ReadFile(p.root.FS(), name)
	if err != nil {
		return err
	}
	content := string(raw)
	if rel == "script.json" {
		var definition map[string]any
		if err := json.Unmarshal(raw, &definition); err != nil {
			return fmt.Errorf("this workflow's script.json is not valid")
		}
		definition["id"] = id
		encoded, _ := json.MarshalIndent(definition, "", "  ")
		content = string(encoded)
	}
	wanted[rel] = true
	return writeFileToWorkspace(ctx, path.Join(codeRoot, rel), content)
}

// removeMovedWorkflowSource runs at the source after the online copy is
// installed: back up the workflow and the schedules that start it, note where
// it went, then remove the local copy with the ordinary delete.
func (api *StreamingAPI) removeMovedWorkflowSource(ctx context.Context, source triggerTarget, p *teamOnlinePackage, moved teamOnlineMovedWorkflow) error {
	if p.request.Kind != "workflow" {
		return nil
	}
	binding, item, files, err := api.readTeamOnlineWorkflowSource(ctx, source, p.request.ItemID)
	if err != nil {
		return err
	}
	if item.Revision != p.request.Revision {
		return fmt.Errorf("the workflow changed on this computer while it moved; the online copy is in place, delete this one yourself")
	}
	stamp := time.Now().UTC().Format("2006-01-02-150405")
	backup := path.Join(source.Path, "backups", "moved-online", stamp+"-"+binding.ID)
	for name, content := range files {
		if err = writeFileToWorkspace(ctx, path.Join(backup, name), content); err != nil {
			return fmt.Errorf("the backup could not be saved, so this copy was kept: %w", err)
		}
	}
	schedules, err := readProjectSchedules(ctx, source.Path)
	if err != nil {
		return err
	}
	kept := []projectactions.Schedule{}
	removed := []projectactions.Schedule{}
	for _, s := range schedules {
		if s.Action.WorkflowID == binding.ID {
			removed = append(removed, s)
		} else {
			kept = append(kept, s)
		}
	}
	raw, _ := json.MarshalIndent(removed, "", "  ")
	if err = writeFileToWorkspace(ctx, path.Join(backup, "schedules.json"), string(raw)); err != nil {
		return fmt.Errorf("the backup could not be saved, so this copy was kept: %w", err)
	}
	moved.ID, moved.Name, moved.Revision, moved.Backup = binding.ID, item.Name, item.Revision, backup
	moved.MovedAt = time.Now().UTC().Format(time.RFC3339)
	notes, _ := readTeamOnlineMovedWorkflows(ctx, source)
	notes = slices.DeleteFunc(notes, func(m teamOnlineMovedWorkflow) bool { return m.DestinationItemID == moved.DestinationItemID })
	notes = append(notes, moved)
	if err = writeTeamOnlineMovedWorkflows(ctx, source, notes); err != nil {
		return err
	}
	if len(removed) > 0 {
		if err = writeProjectSchedules(ctx, source.Path, kept); err != nil {
			return err
		}
	}
	if _, err = api.crewWorkflowRenameOrDelete(ctx, source.Path, "delete", binding, map[string]any{"confirm": true}); err != nil {
		return fmt.Errorf("the online copy is in place, but this copy could not be removed: %w", err)
	}
	log.Printf("[TEAM_ONLINE] event=workflow_moved step=source_removed source_project=%s workflow=%s destination=%s backup_files=%d", source.CrewID, binding.ID, moved.DestinationProjectID, len(files))
	return nil
}

func readTeamOnlineMovedWorkflows(ctx context.Context, source triggerTarget) ([]teamOnlineMovedWorkflow, error) {
	raw, found, err := readFileFromWorkspace(ctx, path.Join(source.Path, teamOnlineMovedFile))
	if err != nil || !found {
		return []teamOnlineMovedWorkflow{}, err
	}
	var out []teamOnlineMovedWorkflow
	if err = json.Unmarshal([]byte(raw), &out); err != nil {
		return []teamOnlineMovedWorkflow{}, nil
	}
	return out, nil
}

// moveWorkflowOnline is the one entry for the button and the chat tool: preview
// for the current revision, then apply. The browser names only the workflow.
// A draft opened with Edit on Mac updates its Team workflow instead, refused if
// the Team copy changed since; keepMine replaces it anyway.
func (api *StreamingAPI) moveWorkflowOnline(ctx context.Context, crewID, workflowID string, keepMine bool) (teamOnlineResult, error) {
	var result teamOnlineResult
	claims := GetUserFromContext(ctx)
	if claims == nil || claims.Scope != "" && (claims.AccessToken == nil || !claims.AccessToken.Allows("crews:write")) {
		return result, fmt.Errorf("use your own Builder connection to move this workflow")
	}
	source, err := resolveTriggerTarget(ctx, claims, "workspace:work:"+crewID)
	if err != nil || source.CrewOwner != claims.UserID {
		return result, fmt.Errorf("move a workflow from your own Crew")
	}
	teamProject, connection, err := teamOnlineConnection(ctx, source)
	if err != nil {
		return result, err
	}
	args := map[string]any{"source_project_id": source.CrewID, "kind": "workflow", "item_id": workflowID, "submission_id": uuid.NewString(), "preview": true}
	if note, ok := draftNoteFor(ctx, source, workflowID); ok {
		args["online_id"], args["base_revision"] = note.DestinationItemID, note.BaseRevision
		if keepMine {
			items, err := api.teamOnlineItems(ctx, teamProject, connection)
			if err != nil {
				return result, err
			}
			if i := slices.IndexFunc(items, func(m teamOnlineItemMapping) bool {
				return m.Kind == "workflow" && m.DestinationItemID == note.DestinationItemID
			}); i >= 0 {
				args["base_revision"] = items[i].Revision
			}
		}
	}
	raw, err := api.teamOnlineChat(ctx, claims, connection, teamProject, args)
	if err != nil {
		return result, err
	}
	if err = json.Unmarshal([]byte(raw), &result); err != nil {
		return result, err
	}
	args["preview"], args["item_revision"] = false, result.Revision
	if raw, err = api.teamOnlineChat(ctx, claims, connection, teamProject, args); err != nil {
		return result, err
	}
	err = json.Unmarshal([]byte(raw), &result)
	log.Printf("[TEAM_ONLINE] event=workflow_moved person=%s source=%s team=%s workflow=%s", claims.UserID, source.CrewID, teamProject, workflowID)
	return result, err
}

// POST /api/crew-workflow-move-online?workspace_path=
func (api *StreamingAPI) handleCrewWorkflowMoveOnline(w http.ResponseWriter, r *http.Request) {
	binding := api.crewScriptAuthoringBinding(w, r)
	if binding == nil {
		return
	}
	result, err := api.moveWorkflowOnline(r.Context(), binding.CrewID, binding.ID, r.URL.Query().Get("keep_mine") == "true")
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		// The server says whether it is a conflict; the page only offers the choices.
		_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error(), "conflict": strings.Contains(err.Error(), teamOnlineChangedMarker)})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
