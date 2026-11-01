package server

import (
	"context"
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
	// Owners build and change it; Readers ("Can use") use its dashboard and ask for work (one sharing model, 2026-10-27).
	Owners  []string `json:"owners"`
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
		owners := []string{m.Team.LeaderID}
		if m.Access != nil {
			for _, id := range m.Access.Owners {
				if id != "" && !containsID(owners, id) {
					owners = append(owners, id)
				}
			}
		}
		return owners
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

// hasOwnershipRecord reports whether anyone has ever been recorded as owning
// this workflow; without one the legacy account-tier answer applies.
func (m *WorkflowManifest) hasOwnershipRecord() bool {
	return len(m.effectiveOwners()) > 0
}

// relayBuilder is the MAIN owner of a Flow: the one it runs as (owner decision 2026-10-13: runs leave journals in one slot, the
// owner's). It is the creator when the creator is still a listed owner, else the first listed owner, so the answer is the same on
// every read. Other listed owners are co-owners: they build it too (2026-10-16) but runs still happen as this person.
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
	if m != nil && m.crewExecutable != nil {
		if claims == nil || userAccessForClaims(claims).Disabled {
			return WorkflowAccessNone
		}
		ref, ok := resolveCrewPath(context.Background(), claims.UserID, m.crewExecutable.CrewRoot)
		if !ok {
			return WorkflowAccessNone
		}
		switch crewAccessFor(claims, ref) {
		case crewAccessOwner:
			return WorkflowAccessOwner
		case crewAccessReader:
			return WorkflowAccessRead
		default:
			return WorkflowAccessNone
		}
	}

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
			if containsID(m.effectiveOwners(), claims.UserID) || m.Team.member(claims.UserID) != nil {
				return WorkflowAccessRead
			}
			return WorkflowAccessNone
		}
		if containsID(m.effectiveOwners(), claims.UserID) {
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
		// Every owner of a Flow builds it (owner, 2026-10-16: co-owners); runs happen as the main owner, relayBuilder().
		// Listed editors and readers run published versions.
		if containsID(m.effectiveOwners(), claims.UserID) {
			if !userAccessForClaims(claims).CanEdit {
				return WorkflowAccessRead
			}
			return WorkflowAccessOwner
		}
		if containsID(m.effectiveReaders(), claims.UserID) || workflowRunEveryone(claims, m, product) {
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
