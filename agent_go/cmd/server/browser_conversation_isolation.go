package server

import (
	"log"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// Browsers are one per workflow and one per project, never per user. A Crew
// (or any product project) shares its browser with every user who can open
// it, exactly as a workflow does.
func bindConversationBrowserIsolation(sessionID, userID, selectedWorkspace string, _ *resolvedAgentProfile) {
	workspace := normalizeConversationWorkspace(selectedWorkspace)
	if strings.HasPrefix(workspace, "Workflow/") {
		common.BindSessionBrowserIsolationForWorkflow(sessionID, workspace)
		return
	}
	if key := browserProjectKey(userID, selectedWorkspace); key != "" {
		common.BindSessionBrowserIsolationForProject(sessionID, key)
		return
	}
	log.Printf("[BROWSER] session %s has no workspace; using a session-scoped browser", sessionID)
	common.BindSessionBrowserIsolationForSession(sessionID)
}

// browserProjectKey is the owner-qualified physical path of a project
// workspace ("_users/<owner>/..."), so the owner (who may send the logical
// path) and other users (who send the physical path) name the same browser,
// while two owners' same-named projects stay distinct.
//
// A Crew that moved to the shared root (PLAT-442 step 4) keeps the key it always had (its first old physical path,
// from the server's registry), under every spelling: the browser's profile, tabs, cookies and logins are named by this
// key, and a new key would silently log the Crew out of everything. A Crew created at the shared root has no old
// path and is keyed by Crew/<folder>.
func browserProjectKey(userID, workspace string) string {
	ref, ok := workspaceref.Parse(filepath.ToSlash(strings.TrimSpace(workspace)))
	if !ok || ref.IsEmpty() {
		return ""
	}
	if folder, shared := ref.SharedProject(); shared {
		if legacy := crewLegacyKey(folder); legacy != "" {
			return legacy
		}
		return workspaceref.SharedProjectPath(folder)
	}
	// An old spelling of a migrated Crew already is the legacy key.
	return ref.PhysicalKeepOwner(userID)
}

// crewLegacyKey is the first old physical root a moved Crew had ("" when it never lived in an owner's tree or has not
// moved).
func crewLegacyKey(folder string) string {
	rec, ok := defaultProjectOwners().Lookup("work", folder)
	if !ok || !rec.Shared || len(rec.Aliases) == 0 {
		return ""
	}
	return rec.Aliases[0]
}

// browserSessionForWorkspace returns the managed browser session name a
// conversation at workspace uses, matching bindConversationBrowserIsolation.
func browserSessionForWorkspace(userID, workspace string) string {
	normalized := normalizeConversationWorkspace(workspace)
	if strings.HasPrefix(normalized, "Workflow/") {
		return common.PrefixBrowserSessionID(common.WorkflowBrowserSessionNamespace(normalized) + "--browser")
	}
	if key := browserProjectKey(userID, workspace); key != "" {
		return common.PrefixBrowserSessionID(common.ProjectBrowserSessionNamespace(key) + "--browser")
	}
	return ""
}
