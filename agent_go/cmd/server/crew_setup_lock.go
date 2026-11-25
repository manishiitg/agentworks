package server

import (
	"context"
	"log"
	"path"
	"strings"
)

// Users of a server project (owner, 25 Nov: "co-owners can always change everything... we should define a user role
// for this type of user who have limited permission"; replacing the 24 Nov per-co-owner switch). The main owner adds a
// User from the Access panel or crew_owners. A User works in the project with a co-owner's working power (shell,
// files, tools, functions; never Run mode), but:
//   - the screen shows only their own chats, nothing beside them; the dashboard opens from its own link (crew-sync);
//   - the project's setup files are locked for their turns: the project instructions, skills, settings and any extra
//     paths the owner chose (crewSetupLockedWritePaths, enforced by the folder guard in every file layer);
//   - the setup tools are refused on every path (crewSetupLockedTools and the checks below);
//   - every chat runs on the project's default model; they cannot pick another (crew_chat_provider_accounts.go).
// Co-owners always change everything.

// crewSetupLockedDefaultPaths are always locked for a User, relative to the project folder.
var crewSetupLockedDefaultPaths = []string{"PROJECT_INSTRUCTIONS.md", "WELCOME.md", "AGENTS.md", "CLAUDE.md", "skills", ".agentworks/skills", ".agentworks/settings", "workflow.json", "product.json"}

// crewIsProjectUser says whether userID is one of the Users of the server Crew at root.
func crewIsProjectUser(root, userID string) bool {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return false
	}
	for _, id := range crewUserIDs(root) {
		if sanitizeUserIDForPath(id) == sanitizeUserIDForPath(userID) {
			return true
		}
	}
	return false
}

// crewSetupLockFor says whether userID is a User of the Crew at root, and the extra paths the owner locked for them.
// The main owner, co-owners and everyone else are never locked.
func crewSetupLockFor(root, userID string) (bool, []string) {
	if !crewIsProjectUser(root, userID) {
		return false, nil
	}
	rec, _ := crewOwnerRecordAt(root)
	for id, paths := range rec.UserLockedPaths {
		if sanitizeUserIDForPath(id) == sanitizeUserIDForPath(userID) {
			return true, paths
		}
	}
	return true, nil
}

// crewSetupLocked is crewSetupLockFor without the paths.
func crewSetupLocked(root, userID string) bool {
	locked, _ := crewSetupLockFor(root, userID)
	return locked
}

// cleanCrewLockedPaths keeps project-relative paths only: no absolute paths, no "..", no duplicates.
func cleanCrewLockedPaths(paths []string) []string {
	out := []string{}
	for _, p := range paths {
		p = strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
		p = strings.Trim(path.Clean("/"+p), "/")
		if p == "" || p == "." || strings.HasPrefix(p, "..") {
			continue
		}
		if !containsID(out, p) {
			out = append(out, p)
		}
	}
	return out
}

// crewSetupLockedWritePaths are the blocked-write paths for a User's turn in the Crew at root (the project
// folder as the turn names it), or nil for everyone else.
func crewSetupLockedWritePaths(root, userID string) []string {
	locked, extra := crewSetupLockFor(root, userID)
	if !locked {
		return nil
	}
	base := strings.TrimSuffix(strings.TrimSpace(root), "/")
	out := []string{}
	for _, rel := range cleanCrewLockedPaths(append(append([]string{}, crewSetupLockedDefaultPaths...), extra...)) {
		out = append(out, base+"/"+rel)
	}
	return out
}

// crewSetupLockedTools change the project's setup. A User's turn never gets them; the folder guard keeps
// the setup files themselves.
var crewSetupLockedTools = map[string]bool{
	// Selections: apps, secrets, skills.
	"update_project_mcp_server_selection":    true,
	"update_project_global_secret_selection": true,
	"update_project_skill_selection":         true,
	// Apps and keys.
	"install_mcp_server":     true,
	"add_mcp_server":         true,
	"edit_mcp_server":        true,
	"remove_mcp_server":      true,
	"manage_vault_access":    true,
	"manage_my_vaults":       true,
	"set_workflow_secret":    true,
	"delete_workflow_secret": true,
	// Identity, sharing and models.
	"set_work_identity": true,
	"manage_crew_sync":  true,
	"manage_crew_model": true,
	"set_tab_model":     true,
	"crew_owners":       true,
	// Automations.
	"create_project_schedule":   true,
	"update_project_schedule":   true,
	"delete_project_schedule":   true,
	"manage_condition_triggers": true,
	"manage_schedules":          true,
	"attach_workflow_reference": true,
	"detach_workflow_reference": true,
}

// crewSetupLockedRefusal is the one plain line a User sees when something would change the setup.
func crewSetupLockedRefusal(root string) string {
	owner := "The project's owner"
	if id, ok := crewProjectOwnerID(root); ok {
		if name := crewOwnerDisplayName(id); name != "" {
			owner = name
		}
	}
	return owner + " controls this project's setup (instructions, skills, settings, model, automations and sharing). You can keep working here; ask them to change it."
}

// The same lock on every path, not only in chat (#3796): the server's own file writes (settings, selections, skills,
// identity and schedules all land in the locked setup files), the website's file proxy, direct MCP tools, sharing and
// project schedules all ask crewSetupLockPathRefusal or crewSetupLockRefusalFor.

// crewSetupLockRootAndRel splits a workspace path inside a server Crew ("Crew/<folder>/<rel>").
func crewSetupLockRootAndRel(p string) (string, string, bool) {
	p = strings.Trim(path.Clean("/"+strings.ReplaceAll(strings.TrimSpace(p), "\\", "/")), "/")
	parts := strings.SplitN(p, "/", 3)
	if len(parts) < 2 || parts[0] != "Crew" || parts[1] == "" {
		return "", "", false
	}
	rel := ""
	if len(parts) == 3 {
		rel = parts[2]
	}
	return parts[0] + "/" + parts[1], rel, true
}

// crewSetupLockPathRefusal is the refusal when userID is a User of the Crew at root and rel (relative to
// the project folder) is one of its locked setup paths, else "".
func crewSetupLockPathRefusal(root, rel, userID, op string) string {
	locked, extra := crewSetupLockFor(root, userID)
	if !locked {
		return ""
	}
	rel = strings.Trim(path.Clean("/"+strings.ReplaceAll(rel, "\\", "/")), "/")
	for _, lockedPath := range cleanCrewLockedPaths(append(append([]string{}, crewSetupLockedDefaultPaths...), extra...)) {
		if rel == lockedPath || strings.HasPrefix(rel, lockedPath+"/") {
			logCrewSetupLockRefusal(root, userID, op+" "+lockedPath)
			return crewSetupLockedRefusal(root)
		}
	}
	return ""
}

// crewSetupLockWorkspaceRefusal checks a full workspace path for the signed-in caller of ctx.
func crewSetupLockWorkspaceRefusal(ctx context.Context, filePath, op string) string {
	userID := GetUserIDFromContext(ctx)
	if userID == "" {
		return ""
	}
	root, rel, ok := crewSetupLockRootAndRel(filePath)
	if !ok {
		return ""
	}
	return crewSetupLockPathRefusal(root, rel, userID, op)
}

// crewSetupLockRefusalFor refuses a whole setup operation (sharing, schedules, a setup tool) for a User.
func crewSetupLockRefusalFor(root, userID, op string) string {
	if !crewSetupLocked(root, userID) {
		return ""
	}
	logCrewSetupLockRefusal(root, userID, op)
	return crewSetupLockedRefusal(root)
}

func logCrewSetupLockRefusal(root, userID, op string) {
	project := root
	if id, ok := projectIdentityOf(root); ok {
		project = id.Folder
	}
	log.Printf("[CREW_SETUP_LOCK] event=refused op=%q project=%s user=%s", op, project, logUsernameForUserID(userID))
}

// crewSetupLockedExternalTools are direct MCP tools (after a merged tool's action resolves to its member) that change
// a project's setup, beyond crewSetupLockedTools. crew_execution runs the project's shell without a turn's folder
// guard, so only its read actions stay open.
var crewSetupLockedExternalTools = map[string]bool{
	"install_skill": true, "import_skill": true, "uninstall_skill": true, "import_native_skill": true,
	"attach_work_folder": true, "detach_work_folder": true,
	"update_crew": true, "share_dashboard": true,
	"manage_triggers": true, "manage_condition_triggers": true,
	"crew_execution": true,
}

// crewSetupReadActions stay open on any tool: they change nothing.
var crewSetupReadActions = map[string]bool{"": false, "list": true, "get": true, "search": true, "history": true, "status": true, "runs": true, "describe": true, "read": true}

// crewSetupLockExternalRefusal refuses a direct MCP call that would change the setup of a project the caller is a
// User of. The project comes from crew_id or project.
func crewSetupLockExternalRefusal(userID, toolName, action string, args map[string]any) string {
	if !crewSetupLockedTools[toolName] && !crewSetupLockedExternalTools[toolName] {
		return ""
	}
	if crewSetupReadActions[strings.TrimSpace(action)] {
		return ""
	}
	for _, key := range []string{"crew_id", "project"} {
		if id := strings.TrimSpace(externalArg(args, key)); id != "" {
			if root := crewProjectRootByID(id); root != "" {
				if refusal := crewSetupLockRefusalFor(root, userID, toolName+" "+action); refusal != "" {
					return refusal
				}
			}
		}
	}
	return ""
}
