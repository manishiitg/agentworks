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
func browserProjectKey(userID, workspace string) string {
	ref, ok := workspaceref.Parse(filepath.ToSlash(strings.TrimSpace(workspace)))
	if !ok || ref.IsEmpty() {
		return ""
	}
	return ref.PhysicalKeepOwner(userID)
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
