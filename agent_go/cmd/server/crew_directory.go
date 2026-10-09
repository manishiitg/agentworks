package server

import (
	"context"
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/gorilla/mux"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productschedule"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// Crew directory (Crew Run mode): every crew has a single owner and is
// read-only for everyone else, so the Crew UI lists other owners' crews
// from here and reads their files through the mediated endpoints below.
// The browser proxy refuses raw cross-user workspace traffic, which makes
// these endpoints the only path to another owner's crew tree.
//
// Visibility is universal, but workflow names stay access-controlled: the
// crew's workflow references are filtered to workflows the caller may
// open, so a crew reused across workflows never names one the caller
// cannot see. Private crew areas (the owner's chat transcripts under
// builder/, run databases under db/) and the raw owner manifests
// (product.json, workflow.json) are excluded from the tree and the file
// reader, and trigger endpoint material, secret values, and LLM
// connection IDs are never serialized.

type sharedProjectSummary struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Description   string `json:"description,omitempty"`
	Icon          string `json:"icon,omitempty"`
	Name          string `json:"name,omitempty"`
	OwnerID       string `json:"owner_id"`
	OwnerUsername string `json:"owner_username,omitempty"`
	// WorkspacePath is the owner's physical crew root. The UI needs it
	// to scope chat history; file reads for shared crews go through the
	// mediated endpoints, never through the proxy with this path.
	WorkspacePath        string                     `json:"workspace_path"`
	CreatedAt            string                     `json:"created_at,omitempty"`
	UpdatedAt            string                     `json:"updated_at,omitempty"`
	LLM                  *sharedProjectLLM          `json:"llm,omitempty"`
	SelectedServers      []string                   `json:"selected_servers,omitempty"`
	SelectedSkills       []string                   `json:"selected_skills,omitempty"`
	SelectedSecrets      []string                   `json:"selected_secrets,omitempty"`
	SelectedGlobalSecret []string                   `json:"selected_global_secrets,omitempty"`
	WorkflowContextPaths []string                   `json:"workflow_context_paths,omitempty"`
	Triggers             []sharedProjectTrigger     `json:"triggers,omitempty"`
	Schedules            []productschedule.Schedule `json:"schedules,omitempty"`
}

type sharedProjectLLM struct {
	Provider        string `json:"provider,omitempty"`
	ModelID         string `json:"model_id,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

type sharedProjectTrigger struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	Enabled        bool           `json:"enabled"`
	Kind           string         `json:"kind,omitempty"`
	Message        string         `json:"message,omitempty"`
	RunDestination string         `json:"run_destination,omitempty"`
	AuthMode       string         `json:"auth_mode,omitempty"`
	Caller         *triggerCaller `json:"caller,omitempty"`
}

type sharedProjectFileEntry struct {
	Path string `json:"path"`
	Type string `json:"type"`
}

type sharedProjectFileResponse struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated,omitempty"`
}

// sharedProjectExcludedTopSegments are crew-root subtrees no reader may
// list or open: the owner's private chat transcripts (builder/) and run
// databases (db/).
var sharedProjectExcludedTopSegments = map[string]bool{"builder": true, "db": true}

// sharedProjectExcludedRootFiles are crew-root files no reader may list
// or open raw: the owner's manifests carry LLM connection IDs, encrypted
// webhook secrets, and unfiltered workflow references. Readers still see
// the same configuration through the listing endpoint's sanitized
// summary, so raw access adds nothing but the secrets.
var sharedProjectExcludedRootFiles = map[string]bool{"product.json": true, "workflow.json": true}

// sharedProjectHiddenPath reports a crew-relative path readers may never
// list or open: the private subtrees, the raw manifests, and anything under
// a dot-segment at any depth. Dot-folders hold credentials and tool state:
// .sandbox-cache/home is the project's HOME (git credentials, ssh keys, CLI
// logins), plus .git (remote URLs with tokens), .env files, .ssh, .claude.
// Before this, any reader could open .sandbox-cache/home/.git-credentials
// of a Crew (RTS 2026-09-28).
func sharedProjectHiddenPath(rel string) bool {
	rel = strings.Trim(rel, "/")
	if sharedProjectExcludedRootFiles[rel] {
		return true
	}
	parts := strings.Split(rel, "/")
	if sharedProjectExcludedTopSegments[parts[0]] {
		return true
	}
	for _, part := range parts {
		if strings.HasPrefix(part, ".") && part != "." {
			return true
		}
	}
	return false
}

const (
	sharedProjectFileTreeDepth  = 4
	sharedProjectFileTreeCap    = 1000
	sharedProjectFileContentCap = 256 << 10
)

// GET /api/agent-profiles/{id}/shared-projects — crews owned by other
// users. The caller's own crews are omitted: the Crew UI already lists
// those from the caller's own projects root.
func (api *StreamingAPI) handleListSharedProjects(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	claims := GetUserFromContext(r.Context())
	profileID := strings.TrimSpace(mux.Vars(r)["id"])
	if claims == nil || strings.TrimSpace(claims.UserID) == "" || profileID == "" || api == nil || api.agentProfiles == nil {
		writeAgentProfileError(w, http.StatusNotFound, "shared projects not found")
		return
	}
	profile, err := api.agentProfiles.Resolve(profileID, 0, claims.UserID)
	if err != nil || !userAllowedProduct(claims, profile.Product) {
		writeAgentProfileError(w, http.StatusNotFound, "shared projects not found")
		return
	}
	if !strings.EqualFold(strings.TrimSpace(profile.Runtime.Conversation.Mode), agentprofiles.ConversationModeKeyed) ||
		!strings.EqualFold(strings.TrimSpace(profile.Runtime.Conversation.KeyType), agentprofiles.ConversationKeyTypeProject) {
		writeAgentProfileError(w, http.StatusNotFound, "shared projects not found")
		return
	}
	// Code has no shared directory, even if legacy share records exist.
	if strings.EqualFold(strings.TrimSpace(profile.ID), codeproduct.ProfileID) {
		writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{"projects": []sharedProjectSummary{}})
		return
	}
	if !strings.EqualFold(strings.TrimSpace(profile.ID), crewProfileID) {
		writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{"projects": []sharedProjectSummary{}})
		return
	}
	if !projectSharingEnabled() {
		writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{"projects": []sharedProjectSummary{}})
		return
	}
	rows := []sharedProjectSummary{}
	for _, ownerID := range crewProjectOwnerCandidates(claims.UserID) {
		for _, row := range listSharedProjectsForOwner(r.Context(), claims, profile, ownerID) {
			rows = append(rows, row)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].UpdatedAt != rows[j].UpdatedAt {
			return rows[i].UpdatedAt > rows[j].UpdatedAt
		}
		return rows[i].Title < rows[j].Title
	})
	writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{"projects": rows})
}

// GET /api/agent-profiles/{id}/own-shared-projects: the Crews the caller owns that live at the shared root
// (Crew/<folder>, PLAT-442 step 4). The Crew UI lists the caller's own Crews from their projects root, which a moved
// Crew has left; this is how it finds them. Only the caller's own, by the server's owner registry; only for the Crew
// product; the response names where each lives, nothing of its content.
func (api *StreamingAPI) handleListOwnSharedProjects(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	claims := GetUserFromContext(r.Context())
	profileID := strings.TrimSpace(mux.Vars(r)["id"])
	if claims == nil || strings.TrimSpace(claims.UserID) == "" || profileID == "" || api == nil || api.agentProfiles == nil {
		writeAgentProfileError(w, http.StatusNotFound, "projects not found")
		return
	}
	profile, err := api.agentProfiles.Resolve(profileID, 0, claims.UserID)
	if err != nil || !userAllowedProduct(claims, profile.Product) || !strings.EqualFold(strings.TrimSpace(profile.ID), crewProfileID) {
		writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{"projects": []map[string]string{}})
		return
	}
	type ownProject struct {
		ID            string `json:"id"`
		Title         string `json:"title,omitempty"`
		WorkspacePath string `json:"workspace_path"`
	}
	rows := []ownProject{}
	manifests, err := listSharedCrewManifestPaths(r.Context(), defaultProductProjectStore(), claims.UserID)
	if err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, "list projects: "+err.Error())
		return
	}
	for _, manifestPath := range manifests {
		root := strings.TrimSuffix(manifestPath, "/product.json")
		manifest, err := readCrewProjectManifests(r.Context(), profile.ID, root)
		if err != nil {
			continue
		}
		rows = append(rows, ownProject{ID: manifest.ID, Title: manifest.Title, WorkspacePath: root})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].WorkspacePath < rows[j].WorkspacePath })
	writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{"projects": rows})
}

// POST /api/agent-profiles/{id}/projects/reserve: where the UI should create a new Crew (PLAT-442). With the shared-root
// switch on, the server picks Crew/<slug>-<id8>, registers the caller as owner and gives the folder its owner's slot
// group, so a Crew the browser then fills never needs a later move. Switch off, or another product: shared=false and
// the browser creates in its own tree as before.
func (api *StreamingAPI) handleReserveAgentProfileProject(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	claims := GetUserFromContext(r.Context())
	profileID := strings.TrimSpace(mux.Vars(r)["id"])
	if claims == nil || strings.TrimSpace(claims.UserID) == "" || profileID == "" || api == nil || api.agentProfiles == nil {
		writeAgentProfileError(w, http.StatusNotFound, "profile not found")
		return
	}
	profile, err := api.agentProfiles.Resolve(profileID, 0, claims.UserID)
	if err != nil || !userAllowedProduct(claims, profile.Product) {
		writeAgentProfileError(w, http.StatusNotFound, "profile not found")
		return
	}
	isCrew := strings.EqualFold(strings.TrimSpace(profile.ID), crewProfileID)
	// The app reserves every new Crew here first, so this refuses an account without the create permission in both
	// layouts (shared root or the owner's own tree).
	if isCrew {
		if status, msg := crewCreateGate(claims); status != 0 {
			writeAgentProfileError(w, status, msg)
			return
		}
	}
	if !isCrew || !crewSharedRootEnabled() {
		writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{"shared": false})
		return
	}
	var body struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil || strings.TrimSpace(body.Title) == "" {
		writeAgentProfileError(w, http.StatusBadRequest, "title is required")
		return
	}
	id := uuid.NewString()
	folder := slugifyCrewTitle(body.Title) + "-" + id[:8]
	workspacePath := workspaceref.SharedCrewRoot + "/" + folder
	if err := defaultProjectOwners().Register(projectOwnerRecord{
		Product: profile.ID, Folder: folder, OwnerID: claims.UserID, ProjectID: id, Shared: true,
	}); err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, "register crew owner: "+err.Error())
		return
	}
	if err := createWorkspaceFolder(r.Context(), workspacePath); err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, "create crew folder: "+err.Error())
		return
	}
	ensureSharedCrewFolderAccess(folder, claims.UserID)
	writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{"shared": true, "id": id, "workspace_path": workspacePath})
}

func listSharedProjectsForOwner(ctx context.Context, claims *UserClaims, profile agentprofiles.Profile, ownerID string) []sharedProjectSummary {
	projectsRoot, err := cleanAgentProfileWorkspace(profile.Runtime.Workspace.ProjectsRoot, ownerID)
	if err != nil {
		return nil
	}
	listing, exists, err := listWorkspaceFolder(ctx, agentProfileRuntimeWorkspace(ownerID, projectsRoot), 3)
	var paths []string
	if err == nil && exists {
		collectWorkspaceFilePaths(listing, &paths)
	}
	// Crews that moved to the shared root belong to their registered owner (PLAT-442 step 4).
	if strings.EqualFold(strings.TrimSpace(profile.ID), crewProfileID) {
		if shared, sharedErr := listSharedCrewManifestPaths(ctx, defaultProductProjectStore(), ownerID); sharedErr == nil {
			paths = append(paths, shared...)
		}
	}
	if len(paths) == 0 {
		return nil
	}
	rows := []sharedProjectSummary{}
	seenManifests := make(map[string]bool)
	for _, candidate := range paths {
		if !strings.HasSuffix(candidate, "/product.json") || seenManifests[candidate] {
			continue
		}
		// The workspace API can include a folder both beneath the requested
		// root and as a top-level item, yielding the same manifest twice.
		seenManifests[candidate] = true
		projectRoot := strings.TrimSuffix(candidate, "/product.json")
		if !crewSharedWithOthers(projectRoot) {
			continue
		}
		manifest, err := readCrewProjectManifests(ctx, profile.ID, projectRoot)
		if err != nil {
			continue
		}
		rows = append(rows, summarizeSharedProject(ctx, claims, ownerID, projectRoot, manifest))
	}
	return rows
}

// readCrewProjectManifests loads a crew's product and runtime manifests
// read-only. It never migrates or writes: a crew missing its runtime
// manifest lists with whatever product.json carries.
func readCrewProjectManifests(ctx context.Context, profileID, projectRoot string) (productProjectManifest, error) {
	raw, found, err := readFileFromWorkspace(ctx, projectRoot+"/product.json")
	if err != nil || !found {
		return productProjectManifest{}, errSharedProjectNotFound
	}
	var manifest productProjectManifest
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		return productProjectManifest{}, errSharedProjectNotFound
	}
	if !strings.EqualFold(strings.TrimSpace(manifest.Product), strings.TrimSpace(profileID)) || strings.TrimSpace(manifest.ID) == "" {
		return productProjectManifest{}, errSharedProjectNotFound
	}
	if isProjectProfileID(profileID) {
		// Falls back to product.json when workflow.json is absent;
		// merging that over itself is a no-op.
		if runtimeRaw, runtimeFound, runtimeErr := readProjectRuntimeManifest(ctx, profileID, projectRoot); runtimeErr == nil && runtimeFound {
			var runtime productProjectManifest
			if err := json.Unmarshal([]byte(runtimeRaw), &runtime); err == nil {
				manifest.Capabilities = runtime.Capabilities
				manifest.Schedules = runtime.Schedules
				manifest.Triggers = runtime.Triggers
				manifest.WorkflowContextPaths = runtime.WorkflowContextPaths
			}
		}
	}
	return manifest, nil
}

type sharedProjectNotFoundError struct{}

func (sharedProjectNotFoundError) Error() string { return "shared project not found" }

var errSharedProjectNotFound error = sharedProjectNotFoundError{}

func summarizeSharedProject(ctx context.Context, claims *UserClaims, ownerID, projectRoot string, manifest productProjectManifest) sharedProjectSummary {
	row := sharedProjectSummary{
		ID:              manifest.ID,
		Title:           manifest.Title,
		Description:     strings.TrimSpace(manifest.Description),
		Icon:            strings.TrimSpace(manifest.Identity.Icon),
		Name:            strings.TrimSpace(manifest.Identity.Name),
		OwnerID:         ownerID,
		OwnerUsername:   crewOwnerDisplayName(ownerID),
		WorkspacePath:   projectRoot,
		CreatedAt:       strings.TrimSpace(manifest.CreatedAt),
		UpdatedAt:       strings.TrimSpace(manifest.UpdatedAt),
		SelectedServers: append([]string(nil), manifest.Capabilities.SelectedServers...),
		SelectedSkills:  append([]string(nil), manifest.Capabilities.SelectedSkills...),
	}
	row.SelectedSecrets = derefSharedProjectSelection(manifest.Capabilities.SelectedSecrets)
	row.SelectedGlobalSecret = derefSharedProjectSelection(manifest.Capabilities.SelectedGlobalSecretNames)
	row.LLM = summarizeSharedProjectLLM(manifest.Capabilities.LLMConfig)
	refs := append([]string(nil), manifest.WorkflowContextPaths...)
	refs = append(refs, manifest.Capabilities.WorkflowContextPaths...)
	row.WorkflowContextPaths = visibleWorkflowContextPathsForCaller(ctx, claims, refs)
	for _, trigger := range manifest.Triggers {
		row.Triggers = append(row.Triggers, summarizeSharedProjectTrigger(trigger))
	}
	row.Schedules = append([]productschedule.Schedule(nil), manifest.Schedules...)
	return row
}

func summarizeSharedProjectLLM(config *workflowtypes.PresetLLMConfig) *sharedProjectLLM {
	builder := presetPrimaryLLMForChat(config)
	if builder == nil {
		return nil
	}
	out := &sharedProjectLLM{
		Provider: strings.TrimSpace(builder.Provider),
		ModelID:  strings.TrimSpace(builder.ModelID),
	}
	if effort, ok := builder.Options["reasoning_effort"].(string); ok {
		out.ReasoningEffort = strings.TrimSpace(effort)
	}
	if out.Provider == "" && out.ModelID == "" {
		return nil
	}
	return out
}

func summarizeSharedProjectTrigger(trigger productWebhookTrigger) sharedProjectTrigger {
	out := sharedProjectTrigger{
		ID:             strings.TrimSpace(trigger.ID),
		Name:           strings.TrimSpace(trigger.Name),
		Enabled:        trigger.Enabled,
		Kind:           strings.TrimSpace(trigger.Kind),
		Message:        trigger.Message,
		RunDestination: runDestination(trigger.ownConversation()),
	}
	if trigger.Webhook != nil {
		out.AuthMode = strings.TrimSpace(trigger.Webhook.AuthMode)
	}
	if trigger.Caller != nil {
		caller := *trigger.Caller
		out.Caller = &caller
	}
	return out
}

func derefSharedProjectSelection(selection *[]string) []string {
	if selection == nil {
		return nil
	}
	out := make([]string, 0, len(*selection))
	for _, name := range *selection {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// visibleWorkflowContextPathsForCaller keeps only the references the
// caller may still open. Detached, revoked, or deleted workflows drop out
// silently rather than failing the whole listing.
func visibleWorkflowContextPathsForCaller(ctx context.Context, claims *UserClaims, paths []string) []string {
	seen := map[string]bool{}
	kept := []string{}
	for _, raw := range paths {
		folder := strings.TrimSuffix(strings.TrimSpace(raw), "/")
		if folder == "" || seen[folder] {
			continue
		}
		seen[folder] = true
		manifest, exists, err := ReadWorkflowManifest(ctx, folder)
		if err != nil || !exists || manifest == nil {
			continue
		}
		if workflowAccessForManifest(claims, manifest) == WorkflowAccessNone || !userAllowedWorkflowID(claims, manifest.ID) {
			continue
		}
		kept = append(kept, folder)
	}
	return kept
}

// authorizeCrewProjectRead verifies the caller may read one crew project:
// any signed-in user with the product, under whichever owner holds it.
// Every denial is a 404 so project IDs are not an oracle.
func (api *StreamingAPI) authorizeCrewProjectRead(r *http.Request, projectID string) (productConversationBinding, productProjectManifest, bool) {
	claims := GetUserFromContext(r.Context())
	profileID := strings.TrimSpace(mux.Vars(r)["id"])
	if claims == nil || strings.TrimSpace(claims.UserID) == "" || profileID == "" || strings.TrimSpace(projectID) == "" || api == nil || api.agentProfiles == nil {
		return productConversationBinding{}, productProjectManifest{}, false
	}
	profile, err := api.agentProfiles.Resolve(profileID, 0, claims.UserID)
	if err != nil || !userAllowedProduct(claims, profile.Product) {
		return productConversationBinding{}, productProjectManifest{}, false
	}
	crew, err := resolveCrewProjectBinding(r.Context(), claims.UserID, profile, strings.TrimSpace(projectID), "")
	if err != nil {
		return productConversationBinding{}, productProjectManifest{}, false
	}
	manifest, err := readCrewProjectManifests(r.Context(), profile.ID, crew.Binding.WorkspacePath)
	if err != nil || strings.TrimSpace(manifest.ID) != strings.TrimSpace(projectID) {
		return productConversationBinding{}, productProjectManifest{}, false
	}
	return crew.Binding, manifest, true
}

// GET /api/agent-profiles/{id}/shared-projects/{project_id}/files — the
// crew's file tree, crew-relative, minus the excluded private subtrees
// and the raw owner manifests.
func (api *StreamingAPI) handleListSharedProjectFiles(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	binding, _, ok := api.authorizeCrewProjectRead(r, strings.TrimSpace(mux.Vars(r)["project_id"]))
	if !ok {
		writeAgentProfileError(w, http.StatusNotFound, "shared project not found")
		return
	}
	entries := []sharedProjectFileEntry{}
	truncated := false
	if listing, exists, err := listWorkspaceFolder(r.Context(), binding.WorkspacePath, sharedProjectFileTreeDepth); err == nil && exists {
		entries, truncated = flattenSharedProjectFiles(binding.WorkspacePath, listing)
	}
	writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{"files": entries, "truncated": truncated})
}

// GET /api/agent-profiles/{id}/shared-projects/{project_id}/file?path=<rel> —
// one text file from the verified crew root, truncated past the cap.
// The raw owner manifests are not servable here; their sanitized
// equivalent is the shared-projects listing summary.
func (api *StreamingAPI) handleGetSharedProjectFile(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	binding, _, ok := api.authorizeCrewProjectRead(r, strings.TrimSpace(mux.Vars(r)["project_id"]))
	if !ok {
		writeAgentProfileError(w, http.StatusNotFound, "shared project not found")
		return
	}
	full, confined := confineSharedProjectPath(binding.WorkspacePath, r.URL.Query().Get("path"))
	if !confined {
		writeAgentProfileError(w, http.StatusNotFound, "shared project file not found")
		return
	}
	raw, found, err := readFileFromWorkspace(r.Context(), full)
	if err != nil || !found {
		writeAgentProfileError(w, http.StatusNotFound, "shared project file not found")
		return
	}
	if isSharedProjectBinaryContent(raw) {
		writeAgentProfileError(w, http.StatusUnsupportedMediaType, "shared project file is not readable as text")
		return
	}
	response := sharedProjectFileResponse{Path: strings.TrimPrefix(workflowtypes.CanonicalCrewAttachmentRoot(full), workflowtypes.CanonicalCrewAttachmentRoot(binding.WorkspacePath)+"/"), Content: raw}
	if len(response.Content) > sharedProjectFileContentCap {
		response.Content = response.Content[:sharedProjectFileContentCap]
		response.Truncated = true
	}
	writeAgentProfileJSON(w, http.StatusOK, response)
}

// confineSharedProjectPath resolves a crew-relative file path against the
// verified root. Absolute paths, escapes, the excluded private subtrees,
// and the raw owner manifests fail closed. The exact-name match scopes
// the manifest exclusion to the crew root: a nested same-named file is
// ordinary project data, not a Crew manifest.
func confineSharedProjectPath(root, rel string) (string, bool) {
	rel = filepath.ToSlash(filepath.Clean("/" + strings.TrimSpace(rel)))
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" || rel == "." || strings.HasPrefix(rel, "../") || strings.Contains(rel, "/../") {
		return "", false
	}
	if sharedProjectHiddenPath(rel) {
		return "", false
	}
	return workflowtypes.CanonicalCrewAttachmentRoot(root) + "/" + rel, true
}

func flattenSharedProjectFiles(root string, listing virtualtools.WorkspaceFolderListing) ([]sharedProjectFileEntry, bool) {
	prefix := workflowtypes.CanonicalCrewAttachmentRoot(root) + "/"
	entries := []sharedProjectFileEntry{}
	truncated := false
	var walk func(items []virtualtools.WorkspaceFolderItem) bool
	walk = func(items []virtualtools.WorkspaceFolderItem) bool {
		for _, item := range items {
			// The listing's own root (and any foreign path) has no
			// crew-relative form; still descend so children are visited.
			clean := workflowtypes.CanonicalCrewAttachmentRoot(item.FilePath)
			if rel := strings.TrimPrefix(clean, prefix); rel != clean && rel != "" {
				if !sharedProjectHiddenPath(rel) {
					kind := "file"
					if strings.EqualFold(strings.TrimSpace(item.Type), "folder") {
						kind = "folder"
					}
					if len(entries) >= sharedProjectFileTreeCap {
						return false
					}
					entries = append(entries, sharedProjectFileEntry{Path: rel, Type: kind})
				}
			}
			if len(item.Children) > 0 && !walk(item.Children) {
				return false
			}
		}
		return true
	}
	if !walk(listing) {
		truncated = true
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, truncated
}

func isSharedProjectBinaryContent(raw string) bool {
	head := raw
	if len(head) > 8192 {
		head = head[:8192]
	}
	return strings.IndexByte(head, 0) >= 0
}

// whatsappOtherCrews lists other owners' crews for a WhatsApp pairing's @list
// and @switch: the same crews the Crew UI lists for this user, read-only.
func (api *StreamingAPI) whatsappOtherCrews(ctx context.Context, userID string) []services.WhatsAppOtherCrew {
	userID = strings.TrimSpace(userID)
	if api == nil || api.agentProfiles == nil || userID == "" {
		return nil
	}
	claims := botWorkflowAccessClaims(userID, "", services.ChannelRoute{})
	profile, err := api.agentProfiles.Resolve("work", 0, userID)
	if err != nil || !userAllowedProduct(claims, profile.Product) {
		return nil
	}
	var crews []services.WhatsAppOtherCrew
	for _, ownerID := range crewProjectOwnerCandidates(userID) {
		for _, row := range listSharedProjectsForOwner(ctx, claims, profile, ownerID) {
			crews = append(crews, services.WhatsAppOtherCrew{ID: row.ID, Title: firstNonEmptyTrimmed(row.Title, row.Name), OwnerName: row.OwnerUsername, WorkspacePath: row.WorkspacePath})
		}
	}
	return crews
}

// GET /api/agent-profiles/{id}/project-sharing: the folders of the caller's own Crews they made private (every other
// Crew they own is shared with every user of the Crew product while project sharing is on).
func (api *StreamingAPI) handleGetProjectSharing(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	claims := GetUserFromContext(r.Context())
	if claims == nil || strings.TrimSpace(claims.UserID) == "" || !strings.EqualFold(strings.TrimSpace(mux.Vars(r)["id"]), crewProfileID) || !userAllowedProduct(claims, "work") {
		writeAgentProfileError(w, http.StatusNotFound, "projects not found")
		return
	}
	all, err := defaultProjectOwners().All()
	if err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, "project sharing is unavailable")
		return
	}
	owner := cleanManifestOwner(claims.UserID)
	private := []string{}
	for _, rec := range all {
		if rec.Product == "work" && rec.Private && rec.OwnerID == owner {
			private = append(private, rec.Folder)
		}
	}
	sort.Strings(private)
	writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{"sharing": projectSharingEnabled(), "private_folders": private})
}

// PUT /api/agent-profiles/{id}/projects/{project_id}/sharing {"private": bool}: the owner keeps one Crew to themselves
// or shares it again. Only the registered owner; the choice is kept in the server-controlled owner registry.
func (api *StreamingAPI) handlePutProjectSharing(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	claims := GetUserFromContext(r.Context())
	profileID := strings.TrimSpace(mux.Vars(r)["id"])
	projectID := strings.TrimSpace(mux.Vars(r)["project_id"])
	if claims == nil || strings.TrimSpace(claims.UserID) == "" || !strings.EqualFold(profileID, crewProfileID) || projectID == "" || api == nil || api.agentProfiles == nil {
		writeAgentProfileError(w, http.StatusNotFound, "project not found")
		return
	}
	var body struct {
		Private *bool `json:"private"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil || body.Private == nil {
		writeAgentProfileError(w, http.StatusBadRequest, `body must be {"private": true|false}`)
		return
	}
	profile, err := api.agentProfiles.Resolve(profileID, 0, claims.UserID)
	if err != nil || !userAllowedProduct(claims, profile.Product) {
		writeAgentProfileError(w, http.StatusNotFound, "project not found")
		return
	}
	crew, err := resolveCrewProjectBinding(r.Context(), claims.UserID, profile, projectID, "")
	if err != nil || !crew.OwnedByCaller {
		writeAgentProfileError(w, http.StatusNotFound, "project not found")
		return
	}
	id, ok := projectIdentityOf(crew.Binding.WorkspacePath)
	if !ok || id.Product != "work" {
		writeAgentProfileError(w, http.StatusNotFound, "project not found")
		return
	}
	registry := defaultProjectOwners()
	if err := registry.Register(projectOwnerRecord{Product: id.Product, Folder: id.Folder, OwnerID: claims.UserID, ProjectID: projectID, Shared: id.Shared}); err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, "could not record the Crew's owner")
		return
	}
	if err := registry.SetPrivate(id.Product, id.Folder, *body.Private); err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, "could not change the Crew's sharing")
		return
	}
	log.Printf("[CREW_SHARING] %s set %s/%s private=%v", sanitizeUserIDForPath(claims.UserID), id.Product, id.Folder, *body.Private)
	writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{"private": *body.Private, "folder": id.Folder})
}
