package server

import (
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
)

var workUIControlContract = uiContract{
	Version: uiControlContract.Version,
	Product: "work",
	Views: []uiViewCapability{
		{ID: "report", Label: "Dashboard", Actions: []string{"open", "refresh"}},
		{ID: "memory", Label: "Memory & skills", TargetKind: "view_section", Targets: []string{"memory", "skills"}, Actions: []string{"open", "refresh"}},
		{ID: "database", Label: "Database", Actions: []string{"open", "refresh"}},
		{ID: "browser", Label: "Browser", Actions: []string{"open", "refresh"}},
		{ID: "costs", Label: "Costs and usage", Actions: []string{"open", "refresh"}},
		{ID: "history", Label: "Chat history", Actions: []string{"open", "refresh"}},
		{ID: "workshop", Label: "Automations", TargetKind: "view_section", Actions: []string{"open", "refresh"}, Targets: []string{"schedules", "conditions", "triggers", "functions", "email"}},
		{ID: "schedules", Label: "Schedules", TargetKind: "view_section", Actions: []string{"open", "refresh"}, Targets: []string{"schedules", "webhooks"}},
		{ID: "files", Label: "Files", Actions: []string{"open", "refresh"}},
		{ID: "identity", Label: "Settings", Actions: []string{"open", "refresh"}},
		{ID: "mcp", Label: "Connections", TargetKind: "view_section", Actions: []string{"open", "refresh"}, Targets: []string{"connected", "available", "secrets", "brain", "folders", "slack", "gmail", "whatsapp", "cli", "agents"}},
		{ID: "llm", Label: "Models", Actions: []string{"open", "refresh"}},
		{ID: "folders", Label: "Attached folders", Actions: []string{"open", "refresh"}},
	},
}

// codeUIControlContract is Crew's project contract without the views Code
// does not have: identity, schedules, triggers and the automation section.
var codeUIControlContract = func() uiContract {
	contract := workUIControlContract
	contract.Product = codeproduct.ProfileID
	contract.Views = nil
	for _, view := range workUIControlContract.Views {
		switch view.ID {
		// Code has no identity, schedules or automation.
		case "identity", "schedules", "workshop":
			continue
		}
		contract.Views = append(contract.Views, view)
	}
	return contract
}()

// projectUIControlContract returns the UI contract for a project workspace.
func projectUIControlContract(workspace string) uiContract {
	if isCodeProjectPath(workspace) {
		return codeUIControlContract
	}
	return workUIControlContract
}

// restoredWorkUIScope reconstructs the presentation-only browser scope from
// active-session state. A restored Crew surface can mount before another model
// turn rebuilds its tool catalog, so relying only on tool registration leaves
// the browser polling an empty broker scope. The workspace remains
// server-derived and owner-scoped; no path from the request body is trusted.
func restoredWorkUIScope(userID, session string, active *ActiveSessionInfo) string {
	if active == nil || !(strings.HasPrefix(session, "work:project:") || strings.HasPrefix(session, codeproduct.ProfileID+":project:")) ||
		strings.TrimSpace(active.BotPlatform) != "" || strings.TrimSpace(active.ParentSessionID) != "" ||
		strings.TrimSpace(active.SessionKind) != "" || active.IsSyntheticTurn {
		return ""
	}
	trigger := strings.ToLower(strings.TrimSpace(active.TriggeredBy))
	if trigger != "" && trigger != "manual" {
		return ""
	}
	if !isActiveWorkProjectWorkspace(userID, active.WorkspacePath) {
		return ""
	}
	return canonicalChatHistoryWorkspacePath(userID, active.WorkspacePath)
}

func registerWorkUIAllowed(req QueryRequest) bool {
	trigger := strings.ToLower(strings.TrimSpace(req.TriggeredBy))
	return isProjectProfileID(req.AgentProfileID) &&
		(trigger == "" || trigger == "manual" || req.UserInteractiveContinuation) && strings.TrimSpace(req.BotPlatform) == "" &&
		strings.TrimSpace(req.ParentSessionID) == "" && strings.TrimSpace(req.SessionKind) == "" &&
		!req.IsAutoNotification
}

func (api *StreamingAPI) registerOpenWorkWorkspaceViewTool(registrar definitionToolRegistrar, userID, session, workspace string) error {
	workspace = canonicalChatHistoryWorkspacePath(userID, workspace)
	contract := projectUIControlContract(workspace)
	return api.registerUIControlToolsForContract(registrar, session, workspace, contract)
}
