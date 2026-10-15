package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"strings"
)

// Per-workflow ownership and sharing (phase 3 of
// docs/design/user_accounts_and_workflow_sharing.md).
//
// A workflow carries an `access` block in its manifest with the same words
// as the account roles: owners, who may edit, run, share and delete it;
// editors, who may edit and run but not share; and readers (viewers), who
// get exactly the PLAT-262 read-only session (chat, run, watch, inspect;
// no mutating tools, no shell writes). The creator is the first owner.
// The stored `readers` key keeps its name for backward compatibility; it
// means the Viewer tier.
//
// Effective access is the lesser of the account role and the manifest
// grant: a Viewer account is never more than a reader even when listed as
// owner or editor; Editor and Creator accounts take the manifest grant
// as-is. Resolution order:
//   1. an account admin is owner of everything;
//   2. a manifest with an ownership record (an access block, or a created_by
//      stamp) answers from its lists, capped by the account role;
//   3. a legacy manifest with neither keeps today's behavior: the account's
//      own tier (creators and editors write, viewers read), so an existing
//      deployment sees no change until someone claims or shares a workflow.

// WorkflowAccess is the manifest's `access` block.
type WorkflowAccess struct {
	Owners  []string `json:"owners"`
	Editors []string `json:"editors,omitempty"`
	Readers []string `json:"readers"`
	// AllowedKBWriters lists workflow IDs explicitly permitted to write into
	// this workflow's knowledgebase/notes/ via a "write" knowledgebase_source
	// (see pkg/workflowkb). Distinct from Readers/Owners: audience overlap is
	// enough to read another workflow's KB, but a mutating grant needs this
	// workflow's own owner to explicitly name the consumer workflow.
	AllowedKBWriters []string `json:"allowed_kb_writers,omitempty"`
	// RunEveryone: the owner shared this workflow or Flow with every account that may use the product
	// (dashboard sharing, PLAT-750). Those accounts run it like readers; the named Readers stay as they are.
	RunEveryone bool `json:"run_everyone,omitempty"`
}

// WorkflowAccessNone is "may not see this workflow at all".
const WorkflowAccessNone WorkflowAccessLevel = "none"

func (m *WorkflowManifest) effectiveOwners() []string {
	if m == nil {
		return nil
	}
	if m.Team != nil {
		return []string{m.Team.LeaderID}
	}
	if m.Access != nil && len(m.Access.Owners) > 0 {
		return m.Access.Owners
	}
	if strings.TrimSpace(m.CreatedBy) != "" {
		return []string{m.CreatedBy}
	}
	return nil
}

func (m *WorkflowManifest) effectiveReaders() []string {
	if m == nil || m.Access == nil {
		return nil
	}
	return m.Access.Readers
}

func (m *WorkflowManifest) effectiveEditors() []string {
	if m == nil || m.Access == nil {
		return nil
	}
	return m.Access.Editors
}

// hasOwnershipRecord reports whether anyone has ever been recorded as owning
// this workflow; without one the legacy account-tier answer applies.
func (m *WorkflowManifest) hasOwnershipRecord() bool {
	return len(m.effectiveOwners()) > 0
}

// relayBuilder is the one person who builds a Flow (Builder mode, owner decision 2026-10-13: a Flow has a single owner, because
// an editor's runs leave journals in their own slot that the owner cannot clean). It is the creator when the creator is still a
// listed owner, else the first listed owner, so the answer is the same on every read. Everyone else is in Run mode.
func (m *WorkflowManifest) relayBuilder() string {
	owners := m.effectiveOwners()
	if len(owners) == 0 {
		return ""
	}
	if creator := strings.TrimSpace(m.CreatedBy); creator != "" && containsID(owners, creator) {
		return creator
	}
	return owners[0]
}

func containsID(list []string, id string) bool {
	for _, v := range list {
		if v == id {
			return true
		}
	}
	return false
}

// workflowAccessForManifest is the one answer to "what may this user do with
// this workflow".
// roleForClaims returns the caller's standardized account role for API
// responses. Directory members use their record; anyone else maps from the
// legacy/compat access answer.
func roleForClaims(claims *UserClaims) string {
	if claims != nil {
		if rec := directoryUserFor(claims.UserID, claims.Username, claims.Email); rec != nil {
			return roleForRecord(rec)
		}
	}
	acc := userAccessForClaims(claims)
	switch {
	case acc.Admin:
		return UserRoleAdmin
	case acc.CanCreate:
		return UserRoleCreator
	case acc.CanEdit:
		return UserRoleEditor
	default:
		return UserRoleViewer
	}
}

// effectiveWorkflowAccessMap resolves the caller's effective access for
// every visible workflow, keyed by workflow id. Workflows the caller may
// not see are omitted rather than listed as none, so the map doubles as
// the visible-workflow list.
func effectiveWorkflowAccessMap(ctx context.Context, claims *UserClaims) (map[string]string, error) {
	discovered, err := DiscoverWorkflowManifests(ctx)
	if err != nil {
		return nil, err
	}
	access := make(map[string]string, len(discovered))
	for _, item := range discovered {
		if item.Manifest == nil || strings.TrimSpace(item.Manifest.ID) == "" {
			continue
		}
		if level := workflowAccessForManifest(claims, item.Manifest); level != WorkflowAccessNone {
			access[item.Manifest.ID] = string(level)
		}
	}
	return access, nil
}

func workflowAccessForManifest(claims *UserClaims, m *WorkflowManifest) WorkflowAccessLevel {
	product := "agentworks"
	if m != nil && m.Kind == "relay" {
		product = "relays"
	}
	// Only a local installation narrows workflows to its product set. On a
	// server, AGENT_PRODUCTS chooses product homes, not project access: a
	// workflow reader or bot route may have access without a product-home grant.
	if localProductInstallation() && !productEnabled(product) {
		return WorkflowAccessNone
	}
	account := workflowAccessForClaims(claims)
	if claims == nil {
		return account
	}
	if level, ok := workflowAccessForBotRouteClaims(claims, m); ok {
		return level
	}
	if m != nil && m.Team != nil {
		if a := userAccessForClaims(claims); a.Disabled || IsMultiUserMode() && !a.Known {
			return WorkflowAccessNone
		}
		if userAccessForClaims(claims).Admin {
			return WorkflowAccessOwner
		}
		if !userAccessForClaims(claims).CanEdit {
			if claims.UserID == m.Team.LeaderID || m.Team.member(claims.UserID) != nil {
				return WorkflowAccessRead
			}
			return WorkflowAccessNone
		}
		if claims.UserID == m.Team.LeaderID {
			return WorkflowAccessOwner
		}
		if m.Team.member(claims.UserID) != nil {
			return WorkflowAccessWrite
		}
		return WorkflowAccessNone
	}
	if userAccessForClaims(claims).Admin {
		return WorkflowAccessOwner
	}
	if m == nil || !m.hasOwnershipRecord() {
		return account
	}
	if m.Kind == "relay" {
		// A Flow has one Builder; any other listed owner or editor runs published versions like a reader.
		if claims.UserID == m.relayBuilder() {
			if !userAccessForClaims(claims).CanEdit {
				return WorkflowAccessRead
			}
			return WorkflowAccessOwner
		}
		if containsID(m.effectiveOwners(), claims.UserID) || containsID(m.effectiveEditors(), claims.UserID) || containsID(m.effectiveReaders(), claims.UserID) || workflowRunEveryone(claims, m, product) {
			return WorkflowAccessRead
		}
		return WorkflowAccessNone
	}
	if containsID(m.effectiveOwners(), claims.UserID) {
		if !userAccessForClaims(claims).CanEdit {
			return WorkflowAccessRead
		}
		return WorkflowAccessOwner
	}
	if containsID(m.effectiveEditors(), claims.UserID) {
		if !userAccessForClaims(claims).CanEdit {
			return WorkflowAccessRead
		}
		return WorkflowAccessWrite
	}
	if containsID(m.effectiveReaders(), claims.UserID) || workflowRunEveryone(claims, m, product) {
		return WorkflowAccessRead
	}
	return WorkflowAccessNone
}

func workflowAccessForBotRouteClaims(claims *UserClaims, m *WorkflowManifest) (WorkflowAccessLevel, bool) {
	if claims == nil || claims.Provider != "bot_route" {
		return "", false
	}
	if m == nil {
		return WorkflowAccessRead, true
	}
	target := strings.TrimSpace(claims.BotRouteWorkflowID)
	if target == "" || !strings.EqualFold(target, strings.TrimSpace(m.ID)) {
		return WorkflowAccessNone, true
	}
	if strings.EqualFold(strings.TrimSpace(claims.BotRouteGrant), "owner") {
		if !m.hasOwnershipRecord() {
			return WorkflowAccessNone, true
		}
		return WorkflowAccessOwner, true
	}
	return WorkflowAccessRead, true
}

// workflowAccessForWorkspacePath reads the manifest at workspacePath and
// resolves access. A folder without a manifest (a workflow being created)
// answers with the account tier and a nil manifest.
func workflowAccessForWorkspacePath(ctx context.Context, claims *UserClaims, workspacePath string) (WorkflowAccessLevel, *WorkflowManifest) {
	manifest, exists, err := ReadWorkflowManifest(ctx, workspacePath)
	if err != nil || !exists {
		return workflowAccessForManifest(claims, nil), nil
	}
	return workflowAccessForManifest(claims, manifest), manifest
}

func currentUserWorkflowAccess(r *http.Request, workspacePath string) WorkflowAccessLevel {
	level, _ := workflowAccessForWorkspacePath(r.Context(), GetUserFromContext(r.Context()), workspacePath)
	return level
}

// requireWorkflowOwner writes a 403 and returns false unless the caller may
// edit the workflow at workspacePath.
func requireWorkflowOwner(w http.ResponseWriter, r *http.Request, workspacePath string) bool {
	level := currentUserWorkflowAccess(r, workspacePath)
	if level == WorkflowAccessOwner || level == WorkflowAccessWrite {
		return true
	}
	writeWorkflowPermissionDenied(w, "owner")
	return false
}

// requireWorkflowVisible writes a 403 and returns false unless the caller
// may at least see the workflow at workspacePath.
func requireWorkflowVisible(w http.ResponseWriter, r *http.Request, workspacePath string) bool {
	clean, ok := cleanWorkspaceReadPath(workspacePath, true)
	if !ok {
		writeWorkflowPermissionDenied(w, "read")
		return false
	}
	workspacePath = clean
	if clean == "Workflow/.relay_releases" || strings.HasPrefix(clean, "Workflow/.relay_releases/") {
		return requireRelayReleaseVisible(w, r, workspacePath)
	}

	claims := GetUserFromContext(r.Context())
	level, manifest := workflowAccessForWorkspacePath(r.Context(), claims, workspacePath)
	if level != WorkflowAccessNone && (manifest == nil || userAllowedWorkflowID(claims, manifest.ID)) {
		return true
	}
	writeWorkflowPermissionDenied(w, "read")
	return false
}

// Release files have a separate execution path but inherit access from the
// live Flow. Never authorize them through the manifest-less release parent.
func requireRelayReleaseVisible(w http.ResponseWriter, r *http.Request, workspacePath string) bool {
	clean, ok := cleanWorkspaceReadPath(workspacePath, true)
	if !ok {
		writeWorkflowPermissionDenied(w, "read")
		return false
	}
	if clean != "Workflow/.relay_releases" && !strings.HasPrefix(clean, "Workflow/.relay_releases/") {
		return true
	}
	if !relayReleaseReadAllowed(r.Context(), GetUserFromContext(r.Context()), clean) {
		writeWorkflowPermissionDenied(w, "read")
		return false
	}
	return true
}

func relayReleaseReadAllowed(ctx context.Context, claims *UserClaims, clean string) bool {
	parts := strings.Split(clean, "/")
	if len(parts) < 4 {
		return false
	}
	draft, err := relayDraftWorkspaceForRelease(ctx, strings.Join(parts[:4], "/"))
	if err != nil {
		return false
	}
	level, manifest := workflowAccessForWorkspacePath(ctx, claims, draft)
	return manifest != nil && level != WorkflowAccessNone && userAllowedWorkflowID(claims, manifest.ID)
}

// workflowFolderFromWorkspaceProxyPath extracts "Workflow/<folder>" from a
// workspace-API path such as "api/documents/Workflow/<folder>/plan.json" or
// "api/folders/Workflow/<folder>". Empty when the path is the Workflow root
// itself (creating a new folder).
func workflowFolderFromWorkspaceProxyPath(path string) string {
	for _, prefix := range []string{"api/documents/Workflow/", "api/folders/Workflow/"} {
		if strings.HasPrefix(path, prefix) {
			rest := strings.TrimPrefix(path, prefix)
			folder := rest
			if i := strings.Index(rest, "/"); i >= 0 {
				folder = rest[:i]
			}
			if folder != "" {
				return "Workflow/" + folder
			}
		}
	}
	return ""
}

// ---- share API ---------------------------------------------------------

type workflowAccessUser struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email,omitempty"`
}

type workflowAccessResponse struct {
	WorkspacePath string               `json:"workspace_path"`
	Owners        []workflowAccessUser `json:"owners"`
	Editors       []workflowAccessUser `json:"editors"`
	Readers       []workflowAccessUser `json:"readers"`
	MyAccess      WorkflowAccessLevel  `json:"my_access"`
	// Legacy is true when nothing has been recorded yet and the workflow is
	// open to every creator and editor; saving any grant ends that.
	Legacy bool `json:"legacy"`
}

func describeUsers(ids []string, dir *userDirectory) []workflowAccessUser {
	out := make([]workflowAccessUser, 0, len(ids))
	for _, id := range ids {
		u := workflowAccessUser{ID: id, Username: id}
		if dir != nil {
			if rec := dir.byID(id); rec != nil {
				u.Username, u.Email = rec.Username, rec.Email
			}
		}
		out = append(out, u)
	}
	return out
}

// GET /api/workflow/access?workspace_path=Workflow/<folder>
func (api *StreamingAPI) handleGetWorkflowAccess(w http.ResponseWriter, r *http.Request) {
	workspacePath := strings.TrimSpace(r.URL.Query().Get("workspace_path"))
	if workspacePath == "" {
		writeUsersError(w, http.StatusBadRequest, "workspace_path is required")
		return
	}
	claims := GetUserFromContext(r.Context())
	level, manifest := workflowAccessForWorkspacePath(r.Context(), claims, workspacePath)
	if manifest == nil {
		writeUsersError(w, http.StatusNotFound, "no workflow at that path")
		return
	}
	if level == WorkflowAccessNone {
		writeWorkflowPermissionDenied(w, "read")
		return
	}
	dir, _ := loadUserDirectory()
	owners, editors, readers := manifest.effectiveOwners(), manifest.effectiveEditors(), manifest.effectiveReaders()
	if manifest.Kind == "relay" {
		owners, editors, readers = relayAccessLists(manifest)
	}
	writeUsersJSON(w, http.StatusOK, workflowAccessResponse{
		WorkspacePath: workspacePath,
		Owners:        describeUsers(owners, dir),
		Editors:       describeUsers(editors, dir),
		Readers:       describeUsers(readers, dir),
		MyAccess:      level,
		Legacy:        !manifest.hasOwnershipRecord(),
	})
}

// PUT /api/workflow/access {workspace_path, owners:[...], editors:[...], readers:[...]}
// Entries may be user ids, usernames, or emails; they are stored as ids. The
// caller must be an owner (or admin) and cannot remove the last owner.
// Editors may edit and run but not share; readers are view-only.
func (api *StreamingAPI) handleSetWorkflowAccess(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkspacePath string    `json:"workspace_path"`
		Owners        []string  `json:"owners"`
		Editors       *[]string `json:"editors"`
		Readers       []string  `json:"readers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeUsersError(w, http.StatusBadRequest, "invalid body")
		return
	}
	req.WorkspacePath = strings.TrimSpace(req.WorkspacePath)
	if req.WorkspacePath == "" {
		writeUsersError(w, http.StatusBadRequest, "workspace_path is required")
		return
	}
	claims := GetUserFromContext(r.Context())
	level, manifest := workflowAccessForWorkspacePath(r.Context(), claims, req.WorkspacePath)
	if manifest == nil {
		writeUsersError(w, http.StatusNotFound, "no workflow at that path")
		return
	}
	// A Flow has one owner and is shared by sharing its dashboard (PLAT-750).
	if manifest.Kind == "relay" {
		writeUsersError(w, http.StatusConflict, "Share this Flow from its dashboard: open the dashboard and press Share.")
		return
	}
	// Only owners share a claimed workflow. Account-tier writers may still
	// share a legacy manifest with no ownership record — that save is what
	// claims it and ends the legacy openness.
	if level != WorkflowAccessOwner && (level != WorkflowAccessWrite || manifest.hasOwnershipRecord()) {
		writeWorkflowPermissionDenied(w, "owner")
		return
	}
	dir, err := loadUserDirectory()
	if err != nil {
		writeUsersError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resolve := func(entries []string) ([]string, string) {
		seen := map[string]bool{}
		var ids []string
		for _, raw := range entries {
			key := strings.TrimSpace(raw)
			if key == "" {
				continue
			}
			rec := dir.find(key, key, key)
			if rec == nil {
				return nil, key
			}
			if !seen[rec.ID] {
				seen[rec.ID] = true
				ids = append(ids, rec.ID)
			}
		}
		sort.Strings(ids)
		return ids, ""
	}
	owners, unknown := resolve(req.Owners)
	if unknown != "" {
		writeUsersError(w, http.StatusBadRequest, "unknown user: "+unknown)
		return
	}
	// An absent editors key (older clients) preserves the stored list,
	// quietly dropping deleted accounts; an explicit empty list clears it.
	var editors []string
	if req.Editors == nil {
		for _, id := range manifest.effectiveEditors() {
			if dir.byID(id) != nil {
				editors = append(editors, id)
			}
		}
	} else {
		var unknown string
		if editors, unknown = resolve(*req.Editors); unknown != "" {
			writeUsersError(w, http.StatusBadRequest, "unknown user: "+unknown)
			return
		}
	}
	readers, unknown := resolve(req.Readers)
	if unknown != "" {
		writeUsersError(w, http.StatusBadRequest, "unknown user: "+unknown)
		return
	}
	if len(owners) == 0 {
		writeUsersError(w, http.StatusBadRequest, "a workflow needs at least one owner")
		return
	}
	if manifest.Kind == "relay" {
		if len(owners) != 1 {
			writeUsersError(w, http.StatusBadRequest, "A Flow has one owner; others can run its published versions")
			return
		}
		// Everyone who is not the owner runs the Flow: the editor tier does not exist for a Flow.
		seen := map[string]bool{}
		var runners []string
		for _, id := range append(append([]string{}, readers...), editors...) {
			if !seen[id] {
				seen[id] = true
				runners = append(runners, id)
			}
		}
		sort.Strings(runners)
		editors, readers = nil, runners
	}
	// A user holds exactly one tier; higher tiers win overlaps.
	keptEditors := editors[:0]
	for _, id := range editors {
		if !containsID(owners, id) {
			keptEditors = append(keptEditors, id)
		}
	}
	filtered := readers[:0]
	for _, id := range readers {
		if !containsID(owners, id) && !containsID(keptEditors, id) {
			filtered = append(filtered, id)
		}
	}
	if manifest.Team != nil {
		writeUsersError(w, http.StatusConflict, "use team settings to manage the single Leader and Contributors")
		return
	}
	manifest.Access = &WorkflowAccess{Owners: owners, Editors: keptEditors, Readers: filtered}
	if err := WriteWorkflowManifest(r.Context(), req.WorkspacePath, manifest); err != nil {
		writeUsersError(w, http.StatusInternalServerError, err.Error())
		return
	}
	log.Printf("[ACCESS] %s set access on %s: owners=%v editors=%v readers=%v", GetUserIDFromContext(r.Context()), req.WorkspacePath, owners, keptEditors, filtered)
	writeUsersJSON(w, http.StatusOK, workflowAccessResponse{
		WorkspacePath: req.WorkspacePath,
		Owners:        describeUsers(owners, dir),
		Editors:       describeUsers(keptEditors, dir),
		Readers:       describeUsers(filtered, dir),
		MyAccess:      workflowAccessForManifest(claims, manifest),
	})
}

// GET /api/users/directory — the picker behind sharing: every enabled
// account's id, username and email, nothing else. Any signed-in user may
// call it (an owner needs it to share their own workflow).
func (api *StreamingAPI) handleUserDirectory(w http.ResponseWriter, r *http.Request) {
	dir, err := loadUserDirectory()
	if err != nil {
		writeUsersError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]workflowAccessUser, 0, len(dir.Users))
	for _, u := range dir.Users {
		if u.Disabled {
			continue
		}
		out = append(out, workflowAccessUser{ID: u.ID, Username: u.Username, Email: u.Email})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Username < out[j].Username })
	writeUsersJSON(w, http.StatusOK, map[string]any{"users": out})
}

// ---- phase 4: session visibility ----------------------------------------

// sessionVisibleTo replaces the old "empty owner means everyone" rule. A
// session that recorded no owner (system-started: scheduler, bots) is visible
// to the machine's single user, or to admins in multi-user mode — never to
// every signed-in account.
func sessionVisibleTo(ownerID string, claims *UserClaims) bool {
	currentUserID := GetDefaultUserID()
	if claims != nil && claims.UserID != "" {
		currentUserID = claims.UserID
	}
	if ownerID == currentUserID {
		return true
	}
	if ownerID != "" {
		return false
	}
	if !IsMultiUserMode() {
		return true
	}
	return userAccessForClaims(claims).Admin
}

// workflowManifestBlockedWriteForNonOwner is the workflow.json path a
// Workshop session must not write when its user is not an owner of the
// workflow at folder (editors included), or "" when they may. The manifest
// holds the access record and created_by, so only owners change it.
func workflowManifestBlockedWriteForNonOwner(ctx context.Context, claims *UserClaims, folder string) string {
	folder = strings.TrimSuffix(strings.TrimSpace(folder), "/")
	if folder == "" {
		return ""
	}
	level, manifest := workflowAccessForWorkspacePath(ctx, claims, folder)
	if manifest == nil || level == WorkflowAccessOwner {
		return ""
	}
	return folder + "/workflow.json"
}

// relayAccessLists is how a Flow's people are shown: its one Builder as the owner, nobody as an editor, and everyone else
// (other listed owners, editors, readers) as running it.
func relayAccessLists(m *WorkflowManifest) (owners, editors, readers []string) {
	builder := m.relayBuilder()
	if builder != "" {
		owners = []string{builder}
	}
	seen := map[string]bool{builder: true}
	for _, list := range [][]string{m.effectiveOwners(), m.effectiveEditors(), m.effectiveReaders()} {
		for _, id := range list {
			if !seen[id] {
				seen[id] = true
				readers = append(readers, id)
			}
		}
	}
	return owners, nil, readers
}
