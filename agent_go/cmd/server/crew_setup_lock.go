package server

import (
	"path"
	"strings"
)

// A co-owner's "Can change project setup" (owner, 24 Nov: "i setup a project for some one.. to do specific stuff with
// limited options to change"). It is on by default. When the main owner switches it off for a co-owner, that person
// still works in the project with a co-owner's full power (shell, files, tools, functions; never Run mode), but:
//   - the screen shows only the chat (crew-sync chat_only);
//   - the project's setup files are locked for their turns: the project instructions, skills, settings and any extra
//     paths the owner chose (crewSetupLockedWritePaths, enforced by the folder guard in every file layer);
//   - the setup tools are not offered (crewSetupLockedTools);
//   - every chat runs on the project's default model; they cannot pick another (crew_chat_provider_accounts.go).

// crewSetupLockedDefaultPaths are always locked for such a co-owner, relative to the project folder.
var crewSetupLockedDefaultPaths = []string{"PROJECT_INSTRUCTIONS.md", "AGENTS.md", "CLAUDE.md", "skills", ".agentworks/skills", ".agentworks/settings", "workflow.json", "product.json"}

// crewSetupLockFor says whether userID is a co-owner of the Crew at root whose setup the main owner locked, and the
// extra paths the owner chose. The main owner, other co-owners and everyone else are never locked.
func crewSetupLockFor(root, userID string) (bool, []string) {
	userID = strings.TrimSpace(userID)
	if userID == "" || !crewIsCoOwner(root, userID) {
		return false, nil
	}
	rec, found := crewOwnerRecordAt(root)
	if !found {
		return false, nil
	}
	for id, paths := range rec.SetupLocked {
		if sanitizeUserIDForPath(id) == sanitizeUserIDForPath(userID) {
			return true, paths
		}
	}
	return false, nil
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

// crewSetupLockedWritePaths are the blocked-write paths for a locked co-owner's turn in the Crew at root (the project
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

// crewSetupLockedTools change the project's setup. A locked co-owner's turn never gets them; the folder guard keeps
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

// crewSetupLockedRefusal is the one plain line a locked co-owner sees when something would change the setup.
func crewSetupLockedRefusal(root string) string {
	owner := "the project's owner"
	if id, ok := crewProjectOwnerID(root); ok {
		if name := crewOwnerDisplayName(id); name != "" {
			owner = name
		}
	}
	return "The project's setup (instructions, skills, settings, model and sharing) is set by " + owner + ". You can keep working here; ask them to change it."
}
