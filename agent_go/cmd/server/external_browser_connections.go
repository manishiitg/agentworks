package server

import (
	"net/http"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browserrelay"
)

func externalBrowserConnectionsAllowed(c *UserClaims) bool {
	if c == nil {
		return false
	}
	if c.AccessToken == nil {
		return true
	}
	t := c.AccessToken
	return t.Allows("workflows:read") || t.Allows("runs:execute") || t.Allows("crews:read") || t.Allows("crews:write") || externalCodeRunAllowed(c)
}
func externalBrowserConnectionsDefinition(add func(string, string, bool, bool, map[string]any, ...string)) {
	add("browser_connections", "Your saved Chrome and Edge connections. List your own browsers or get a setup link; select or disconnect a browser for one project under existing Builder access. No account credentials or other projects' tabs are returned. Connect native browsers once from Account → Browser connections.", true, false, map[string]any{
		"action":      map[string]any{"type": "string", "enum": []any{"list", "setup", "select", "disconnect"}},
		"browser_id":  externalString("Browser ID returned by list; required for select."),
		"workflow_id": externalString("Workflow ID for a project selection."),
		"crew_id":     externalString("Crew ID for a project selection."),
		"project_id":  externalString("Your own Code project ID for a project selection."),
	}, "action")
}
func (api *StreamingAPI) externalBrowserConnectionsCall(w http.ResponseWriter, r *http.Request, args map[string]any) {
	c := GetUserFromContext(r.Context())
	action := externalArg(args, "action")
	if action == "list" || action == "setup" {
		externalJSON(w, map[string]any{"browsers": browserrelay.Default.Browsers(c.UserID), "setup_url": getBaseURL(r) + "/?browser_connections=1", "next": "Open Browser connections to connect Chrome or Edge once. Products can then use a saved browser."})
		return
	}
	wid, cid, pid := externalArg(args, "workflow_id"), externalArg(args, "crew_id"), externalArg(args, "project_id")
	count := 0
	for _, id := range []string{wid, cid, pid} {
		if id != "" {
			count++
		}
	}
	if count != 1 {
		externalError(w, 400, "invalid_arguments", "Choose one workflow, Crew or Code project.")
		return
	}
	root, profile := "", ""
	switch {
	case wid != "":
		if t := c.AccessToken; t != nil && (!t.BuilderAccess() || !t.AllowsWorkflow(wid)) {
			externalError(w, 403, "forbidden", "Use a Builder connection for this workflow, or choose its browser in the app.")
			return
		}
		var ok bool
		root, ok = dashboardWorkflowRoot(r, wid)
		if !ok {
			externalError(w, 404, "not_found", "Workflow unavailable.")
			return
		}
		profile = "workflow"
	case cid != "":
		if t := c.AccessToken; t != nil && (!t.Allows("crews:write") || !t.AllowsCrew(cid)) {
			externalError(w, 403, "forbidden", "Use this Crew's Builder connection, or choose its browser in the app.")
			return
		}
		if status, message := externalCrewAuthorGate(c); status != 0 {
			externalError(w, status, "forbidden", message)
			return
		}
		crew, _, _, ok := api.externalCrewResolve(r.Context(), c, cid)
		if !ok {
			externalError(w, 404, "not_found", "Crew unavailable.")
			return
		}
		root, profile = crew.Binding.WorkspacePath, "work"
	case pid != "":
		if !externalCodeRunAllowed(c) {
			externalError(w, 403, "forbidden", "Use your own Code connection, or choose its browser in the app.")
			return
		}
		binding, _, ok := api.externalCodeRunOwnProject(r.Context(), c.UserID, pid)
		if !ok {
			externalError(w, 404, "not_found", "Code project unavailable.")
			return
		}
		root, profile = binding.WorkspacePath, "code"
	}
	if _, err := api.projectExtensionAccess(r, root, profile); err != nil {
		externalError(w, 403, "forbidden", "This project cannot use your personal browser. Choose a project you build.")
		return
	}
	scope := browserSessionForWorkspace(c.UserID, root)
	if action == "disconnect" {
		if err := browserrelay.Default.Disconnect(c.UserID, scope); err != nil {
			externalError(w, 503, "unavailable", err.Error())
			return
		}
	} else {
		id := externalArg(args, "browser_id")
		if id == "" {
			externalError(w, 400, "invalid_arguments", "Choose browser_id from browser_connections action=list.")
			return
		}
		if _, err := browserrelay.Default.PairForProfile(c.UserID, scope, root, profile); err != nil {
			externalError(w, 503, "unavailable", "Could not prepare this project's browser.")
			return
		}
		if err := browserrelay.Default.SelectBrowser(c.UserID, scope, id); err != nil {
			externalError(w, 400, "invalid_arguments", err.Error())
			return
		}
		browserrelay.Default.RequestProjectConnection(c.UserID, scope)
	}
	externalJSON(w, browserrelay.Default.Status(c.UserID, scope))
}
