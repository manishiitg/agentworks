package server

import (
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
)

// workUIControlContract is what the agent may show in a Crew's side pane: only the project Dashboard (owner,
// 3 Nov: other panels are reached from the menu or chat).
var workUIControlContract = uiContract{
	Version: uiControlContract.Version,
	Product: "work",
	Views:   []uiViewCapability{{ID: "report", Label: "Dashboard", Actions: []string{"open", "refresh"}}},
}

// projectUIControlContract returns the UI contract for a project workspace. Builders and Run people get the same
// Dashboard-only contract; both the bound page and the agent's actions use it.
func projectUIControlContract(workspace string, builder bool) uiContract {
	return workUIControlContract
}

// restoredWorkUIScope reconstructs the presentation-only browser scope from
// active-session state. A restored Crew surface can mount before another model
// turn rebuilds its tool catalog, so relying only on tool registration leaves
// the browser polling an empty broker scope. The workspace remains
// server-derived and owner-scoped; no path from the request body is trusted.
func restoredWorkUIScope(userID, session string, active *ActiveSessionInfo) string {
	if active == nil || !(strings.HasPrefix(session, "work:project:") || strings.HasPrefix(session, workproduct.ProfileID+":project:")) ||
		strings.TrimSpace(active.BotPlatform) != "" {
		return ""
	}
	if !isActiveWorkProjectWorkspace(userID, active.WorkspacePath) {
		return ""
	}
	return canonicalChatHistoryWorkspacePath(userID, active.WorkspacePath)
}

// registerWorkUIAllowed: every turn of the person's own Crew chat, automatic ones included, so the tool set is
// one per chat (owner, 5 Nov); never a Slack reply or another person's agent's message.
func registerWorkUIAllowed(req QueryRequest) bool {
	return isProjectProfileID(req.AgentProfileID) && strings.TrimSpace(req.BotPlatform) == "" && !req.AgentMessageFromOtherPerson
}

func (api *StreamingAPI) registerOpenWorkWorkspaceViewTool(registrar definitionToolRegistrar, userID, session, workspace string, builder bool) error {
	workspace = canonicalChatHistoryWorkspacePath(userID, workspace)
	contract := projectUIControlContract(workspace, builder)
	return api.registerUIControlToolsForContract(registrar, session, workspace, contract)
}
