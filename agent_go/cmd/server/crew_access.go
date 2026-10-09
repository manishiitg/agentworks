package server

import (
	"context"
	"errors"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	"log"
	"path/filepath"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// Crew Run mode (issue #205, BUG_ID_001): every crew has a single owner and
// is read-only for everyone else. There is no per-crew share list and no
// workflow derivation: the owner gets full mode, any other signed-in user
// with the Crew product gets Run — chat with a read-only tool surface, a
// read-only system prompt, and no writes anywhere.
//
// This file holds the shared access primitives: cross-owner project
// binding, ownership tests, and the reader-turn detector. Enforcement
// itself stays at the existing PLAT-262 seams (conversationTargetAccess,
// folder guards, tool registration, prompt assembly), which the rest of
// the change wires to these primitives.

// crewProjectBinding is a verified binding of one crew project for one
// caller: the owning user, whether the caller owns it, and the project
// binding. Non-owned bindings carry no ManifestPath and no authoritative
// session: the conversation registry then stays strictly per-reader (its
// manifest writes are all guarded on a non-empty ManifestPath), so opening
// or chatting with someone else's crew never touches the owner's manifest
// and never adopts the owner's live session.
type crewProjectBinding struct {
	OwnerID       string
	OwnedByCaller bool
	Binding       productConversationBinding
}

// crewProjectOwnerID returns the owning user ID of a crew workspace path.
//
//   - Crew/<id> (shared root): the owner is the server's registry entry (PLAT-449), never the path or the manifest;
//     "" when there is none, so a crew nobody registered is nobody's (PLAT-442 step 4).
//   - _users/<owner>/Chats/Work/projects/<id> (physical, per-user): the path names the owner, unless the crew has
//     been migrated, in which case this old spelling is an alias of Crew/<id> and the registry answers.
//   - anything else (a logical path names no owner): no crew owner.
//
// Every caller that used to take the owner out of the path goes through here, so none of them says "no owner" for
// Crew/<id> or follows an old path to an empty folder.
func crewProjectOwnerID(workspacePath string) (string, bool) {
	ref, ok := workspaceref.Parse(filepath.ToSlash(strings.TrimSpace(workspacePath)))
	if !ok {
		return "", false
	}
	if project, shared := ref.SharedProject(); shared {
		owner := crewOwners.owner(context.Background(), workspaceref.SharedProjectPath(project))
		return owner, owner != ""
	}
	if !ref.HasOwner() || ref.Logical() == "" {
		return "", false
	}
	if project, _, isCrew := ref.AnyCrewProject(); isCrew {
		if moved := crewPathAliases.lookup(context.Background(), legacyCrewRoot(ref.Owner(), project)); moved != "" {
			owner := crewOwners.owner(context.Background(), moved)
			return owner, owner != ""
		}
	}
	return ref.Owner(), true
}

// canonicalCrewWorkspaceRoot normalizes a crew workspace root for exact
// comparison: slash separators, trimmed whitespace, no leading or
// trailing slashes.
//
// A moved Crew (PLAT-442 step 4) has one root under every spelling: an old spelling is folded to its Crew/<folder>
// root, so a root stored before the move compares equal to the same Crew's root after it.
func canonicalCrewWorkspaceRoot(workspacePath string) string {
	return foldCrewRootForStoredReference(strings.Trim(filepath.ToSlash(strings.TrimSpace(workspacePath)), "/"))
}

// isCrewProjectPath reports whether a workspace path addresses a crew
// project of any owner: a shared Crew/<id> path, a physical per-user path,
// or the caller's own logical path, under Chats/Work/projects/<project>.
func isCrewProjectPath(workspacePath string) bool {
	_, _, ok := workspaceref.MustParse(workspacePath).AnyCrewProject()
	return ok
}

// crewProjectOwnedByCaller reports whether the caller owns the crew project
// at workspacePath. Logical (prefix-less) project paths address the
// caller's own tree; physical paths name their owner explicitly; a shared
// Crew/<id> path (or an old spelling of a migrated crew) is owned by the
// owner in the server's registry.
func crewProjectOwnedByCaller(callerID, workspacePath string) bool {
	ref := workspaceref.MustParse(filepath.ToSlash(strings.TrimSpace(workspacePath)))
	if _, shared := ref.SharedProject(); shared {
		owner, ok := crewProjectOwnerID(workspacePath)
		return ok && owner == sanitizeUserIDForPath(callerID)
	}
	if !ref.HasOwner() {
		return ref.IsProject()
	}
	if _, _, isCrew := ref.AnyCrewProject(); isCrew {
		owner, ok := crewProjectOwnerID(workspacePath)
		return ok && owner == sanitizeUserIDForPath(callerID)
	}
	return ref.OwnedBy(callerID) && ref.Logical() != ""
}

// resolveConversationBindingForUser binds one conversation for one caller.
// Work projects resolve under any owner (Crew Run mode); every other
// profile keeps strict caller-scoped resolution. The owned flag reports
// whether the caller owns the bound project: reader bindings carry no
// manifest path, so registry operations on them never touch the owner's
// manifest.
func resolveConversationBindingForUser(ctx context.Context, userID string, profile agentprofiles.Profile, requestedKey string) (productConversationBinding, bool, error) {
	if strings.EqualFold(strings.TrimSpace(profile.ID), codeproduct.ProfileID) {
		// A Code conversation belongs only to its owner.
		project, err := resolveCrewProjectBinding(ctx, userID, profile, requestedKey, "")
		if err != nil {
			return productConversationBinding{}, true, err
		}
		if !project.OwnedByCaller {
			return productConversationBinding{}, true, fmt.Errorf("Code access denied")
		}
		return project.Binding, project.OwnedByCaller, nil
	}
	if !strings.EqualFold(strings.TrimSpace(profile.ID), "work") {
		binding, err := resolveProductConversationBinding(ctx, userID, profile, requestedKey)
		if err != nil {
			return productConversationBinding{}, true, err
		}
		return binding, true, nil
	}
	crew, err := resolveCrewProjectBinding(ctx, userID, profile, requestedKey, "")
	if err != nil {
		return productConversationBinding{}, true, err
	}
	return crew.Binding, crew.OwnedByCaller, nil
}

// crewBuilderDiskHiddenFromUser reports whether project-local transcript
// scans must be skipped: a reader's history for someone else's crew comes
// from the reader's own central store only, never from the owner's
// builder/conversation transcripts on disk.
func crewBuilderDiskHiddenFromUser(userID, workflowPath string) bool {
	return isProjectWorkspacePath(workflowPath) && !crewProjectOwnedByCaller(userID, workflowPath)
}

// isCrewReaderTurn reports whether a turn addresses someone else's crew
// project: a work-profile turn whose folder is a crew project the caller
// does not own. Owner turns, landing chats, and non-crew turns are false.
func isCrewReaderTurn(req QueryRequest, callerID string) bool {
	if !strings.EqualFold(strings.TrimSpace(req.AgentProfileID), "work") {
		return false
	}
	folder := strings.TrimSpace(req.SelectedFolder)
	if !isCrewProjectPath(folder) {
		return false
	}
	return !crewProjectOwnedByCaller(callerID, folder)
}

// resolveCrewProjectBinding binds one crew project for one caller,
// regardless of who owns it. The caller's own tree is tried first, so
// owner turns cost exactly what they cost today; otherwise the owner is
// taken from the selected folder's explicit segment when it names one,
// and finally every known owner is scanned. A project ID present under
// two owners is ambiguous and fails closed.
func resolveCrewProjectBinding(ctx context.Context, callerID string, profile agentprofiles.Profile, projectID, selectedFolder string) (crewProjectBinding, error) {
	denied := func() (crewProjectBinding, error) {
		return crewProjectBinding{}, fmt.Errorf("crew project %q is unavailable", strings.TrimSpace(projectID))
	}
	if strings.TrimSpace(projectID) == "" {
		return denied()
	}
	store := defaultProductProjectStore()
	if binding, err := resolveProductProjectBindingWithStore(ctx, callerID, profile, projectID, store); err == nil {
		// PLAT-449: found in the caller's own tree is only an ADMISSION by path. A folder registered to someone else
		// (a copy or a planted project) is not the caller's: refused, never resolved on a manifest's say-so.
		if id, ok := projectIdentityOf(binding.WorkspacePath); ok {
			if registered, conflict := registeredOwnerConflict(id, callerID); conflict {
				log.Printf("[OWNER_MISMATCH] %s: %s/%s is registered to %q, not %q; refusing to open it as theirs", binding.WorkspacePath, id.Product, id.Folder, registered, callerID)
				return denied()
			}
		}
		// The owner opening a project registers it once (PLAT-449) and leaves the owner as information in its manifest.
		ensureProjectOwnerID(ctx, binding.WorkspacePath, callerID)
		return crewProjectBinding{OwnerID: sanitizeUserIDForPath(callerID), OwnedByCaller: true, Binding: binding}, nil
	}
	// Code never resolves under another owner, including legacy shares.
	if strings.EqualFold(strings.TrimSpace(profile.ID), codeproduct.ProfileID) {
		return denied()
	}
	// Otherwise only Crews resolve under other owners; every other profile
	// is the caller's own or nothing.
	if !strings.EqualFold(strings.TrimSpace(profile.ID), crewProfileID) {
		return denied()
	}
	// Projects are private to their owner when project sharing is switched off (project_sharing.go).
	if !projectSharingEnabled() {
		return denied()
	}
	// A Crew its owner made private resolves for nobody else.
	reader := func(ownerID string, binding productConversationBinding) (crewProjectBinding, error) {
		if !crewSharedWithOthers(binding.WorkspacePath) {
			return denied()
		}
		return readerCrewProjectBinding(ownerID, binding), nil
	}
	// A Crew at the shared root (PLAT-442 step 4) has its owner in the server's registry: one listing of Crew/ finds
	// it whoever the owner is (not the caller, whose own were tried above).
	var sharedOwner string
	if shared, err := resolveProductProjectBindingInRoot(ctx, profile, workspaceref.SharedCrewRoot, strings.TrimSpace(projectID), crewResourceProjectID(projectID), store, func(folder string) bool {
		owner := sharedCrewOwner(folder)
		if owner == "" || owner == sanitizeUserIDForPath(callerID) {
			return false
		}
		sharedOwner = owner
		return true
	}); err == nil {
		return reader(sharedOwner, shared)
	} else if !errors.Is(err, errProjectNotFound) {
		return denied()
	}
	// The query path already carries the verified physical root: resolve
	// directly under its owner instead of scanning every user.
	if ownerID, ok := crewProjectOwnerID(selectedFolder); ok && ownerID != sanitizeUserIDForPath(callerID) {
		binding, err := resolveProductProjectBindingWithStore(ctx, ownerID, profile, projectID, store)
		if err != nil {
			return denied()
		}
		return reader(ownerID, binding)
	}
	ownerID, binding, err := scanCrewProjectOwners(ctx, callerID, profile, projectID, store)
	if err != nil {
		return denied()
	}
	return reader(ownerID, binding)
}

// readerCrewProjectBinding strips the manifest coupling from a verified
// non-owned binding (see crewProjectBinding).
func readerCrewProjectBinding(ownerID string, binding productConversationBinding) crewProjectBinding {
	binding.ManifestPath = ""
	binding.AuthoritativeSessionID = ""
	return crewProjectBinding{OwnerID: ownerID, OwnedByCaller: false, Binding: binding}
}

// scanCrewProjectOwners resolves a crew project under every known owner
// except the caller. Exactly one match wins; zero, duplicates, and
// directory failures all fail closed.
func scanCrewProjectOwners(ctx context.Context, callerID string, profile agentprofiles.Profile, projectID string, store productProjectStore) (string, productConversationBinding, error) {
	owners := crewProjectOwnerCandidates(callerID)
	var matchOwner string
	var matchBinding productConversationBinding
	matched := false
	for _, ownerID := range owners {
		binding, err := resolveProductProjectBindingWithStore(ctx, ownerID, profile, projectID, store)
		if err != nil {
			continue
		}
		rootOwner, ok := crewProjectOwnerID(binding.WorkspacePath)
		if !ok {
			continue
		}
		if matched {
			return "", productConversationBinding{}, fmt.Errorf("crew project %q is ambiguous", strings.TrimSpace(projectID))
		}
		matchOwner, matchBinding, matched = rootOwner, binding, true
	}
	if !matched {
		return "", productConversationBinding{}, fmt.Errorf("crew project %q is unavailable", strings.TrimSpace(projectID))
	}
	return matchOwner, matchBinding, nil
}

// crewProjectOwnerCandidates lists every user ID whose crew tree may hold
// the project: the account directory plus the single-user default,
// sanitized to path segments and excluding the caller.
func crewProjectOwnerCandidates(callerID string) []string {
	seen := map[string]bool{sanitizeUserIDForPath(callerID): true}
	owners := []string{}
	add := func(raw string) {
		segment := sanitizeUserIDForPath(strings.TrimSpace(raw))
		if segment == "" || seen[segment] {
			return
		}
		seen[segment] = true
		owners = append(owners, segment)
	}
	if dir, err := loadUserDirectory(); err == nil && dir != nil {
		for _, rec := range dir.Users {
			if rec.Disabled {
				continue
			}
			add(rec.ID)
		}
	}
	add(GetDefaultUserID())
	sort.Strings(owners)
	return owners
}

// crewReaderWorkspaceRoots splits the project root of a crew turn into the
// folder-guard inputs: owners keep the project writable, readers get it
// read-only with an explicit blocked-write entry (mirroring crew
// attachment roots, which enter ReadPaths and BlockedWritePaths but never
// WritePaths).
func crewReaderWorkspaceRoots(profileRoot string, reader bool) (readRoots, writeRoots, blockedWriteRoots []string) {
	root := strings.TrimSuffix(strings.TrimSpace(profileRoot), "/") + "/"
	if !reader {
		return []string{root}, []string{root}, nil
	}
	return []string{root}, nil, []string{root}
}

// crewGuestCallerForTurn is the user a Crew turn works for as a guest: set
// on the owner's own Crew turn when a function call or ask came from someone
// else (product_webhooks.go). Empty on every other turn, so the field can
// only ever narrow what an owner's turn may do.
func crewGuestCallerForTurn(req QueryRequest, currentUserID string) string {
	guest := strings.TrimSpace(req.CrewGuestCaller)
	if guest == "" || !strings.EqualFold(strings.TrimSpace(req.AgentProfileID), "work") {
		return ""
	}
	folder := strings.TrimSpace(req.SelectedFolder)
	if !isCrewProjectPath(folder) || !crewProjectOwnedByCaller(currentUserID, folder) {
		return ""
	}
	if sanitizeUserIDForPath(guest) == sanitizeUserIDForPath(currentUserID) {
		return ""
	}
	return guest
}

// crewGuestCaller is the calling user when it is not the Crew's owner, else
// "": a non-owner's call runs as their guest.
func crewGuestCaller(callerID, ownerID string) string {
	caller := strings.TrimSpace(callerID)
	if caller == "" || sanitizeUserIDForPath(caller) == sanitizeUserIDForPath(ownerID) {
		return ""
	}
	return caller
}

// applyCrewGuestCaller marks a Crew turn request as working for a guest:
// pinned read-only, with the guest named for attribution.
func applyCrewGuestCaller(reqMap map[string]interface{}, guestID string) {
	if strings.TrimSpace(guestID) == "" {
		return
	}
	reqMap["pin_run_mode"] = true
	reqMap["crew_guest_caller"] = strings.TrimSpace(guestID)
}

// crewOwnerDisplayName resolves a crew owner's path segment to a username
// for prompts and listings, falling back to the segment itself.
func crewOwnerDisplayName(ownerID string) string {
	if dir, err := loadUserDirectory(); err == nil && dir != nil {
		for _, rec := range dir.Users {
			if sanitizeUserIDForPath(rec.ID) == ownerID {
				if strings.TrimSpace(rec.Username) != "" {
					return strings.TrimSpace(rec.Username)
				}
				return ownerID
			}
		}
	}
	return ownerID
}

// crewReaderDeniedTools is the Crew Run mode deny-list: mutating tools a
// reader turn must never receive, dropped at the product tool gate even
// when a registration path admits them. Reader-safe subsets (list/get/run
// operations, caller-scoped tools) are kept by their registrars instead;
// this list is the backstop for the generic surface (file patching,
// media creation) plus every crew-management tool by its public name.
func crewReaderDeniedTools() []string {
	return []string{
		// Generic mutating surface.
		"diff_patch_workspace_file",
		"image_gen",
		"image_edit",
		// Crew configuration: references, schedules, triggers (lists stay).
		"attach_workflow_reference",
		"detach_workflow_reference",
		"create_project_schedule",
		"update_project_schedule",
		"delete_project_schedule",
		"trigger_project_schedule",
		"create_project_trigger",
		"update_project_trigger",
		"delete_project_trigger",
		// MCP connections (readers may list them).
		"install_mcp_server",
		"add_mcp_server",
		"edit_mcp_server",
		"remove_mcp_server",
		"trigger_mcp_discovery",
		"manage_vault_access",
		"manage_my_vaults",
		// Crew selections (servers, secrets, skills).
		"update_project_mcp_server_selection",
		"update_project_global_secret_selection",
		"update_project_skill_selection",
		// Secrets (already skipped when read-only; denied here too).
		"set_workflow_secret",
		"delete_workflow_secret",
		// Crew identity and UI actions (reads stay).
		"set_work_identity",
		"create_crew",
		"perform_ui_action",
	}
}

// crewResourceProjectID is the project id of a conversation key ("<id>" or "<id>:<tab>").
func crewResourceProjectID(conversationKey string) string {
	key := strings.TrimSpace(conversationKey)
	if base, _, found := strings.Cut(key, ":"); found {
		return strings.TrimSpace(base)
	}
	return key
}
