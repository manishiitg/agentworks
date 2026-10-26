package server

import "net/http"

func isExternalWorkflowAccountTool(name string) bool {
	return name == "workflow_google_account" || name == "workflow_slack_account" || name == "workflow_slack_bot"
}

// Reuse the native account schemas and operations rather than copying account
// settings or granting authority through a second connection model.
func externalWorkflowAccountDefinitions(add func(string, string, bool, bool, map[string]any, ...string)) error {
	api := &StreamingAPI{}
	reg := crewCapabilityRegistrar{}
	if err := api.registerPersonalGoogleAccountTool(reg, ""); err != nil {
		return err
	}
	if err := api.registerPersonalSlackAccountTool(reg, ""); err != nil {
		return err
	}
	for _, provider := range []string{"google", "slack"} {
		tool := reg["manage_"+provider+"_account"]
		props, err := externalSchemaProperties(tool.description, tool.schema)
		if err != nil {
			return err
		}
		add("workflow_"+provider+"_account", tool.description, true, true, props, "action")
	}
	if err := api.registerSlackVaultBotTool(reg, ""); err != nil {
		return err
	}
	tool := reg["manage_slack_bot"]
	add("workflow_slack_bot", "Choose a My Vault bot for this Goal with the same operation as its Slack page. Flows support Act in Slack only. "+tool.description, true, true, tool.schema["properties"].(map[string]any), "action")
	return nil
}

func (api *StreamingAPI) externalWorkflowAccountCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any, workflow DiscoveredWorkflow) {
	claims := GetUserFromContext(r.Context())
	if !externalFileAuthoringAllowed(claims, workflow.Manifest) {
		externalError(w, http.StatusForbidden, "forbidden", "Open an item you build, using an MCP connection with Builder access, to manage its accounts.")
		return
	}
	if err := personalAccountManagementScope(r, workflow.WorkspacePath, true); err != nil {
		externalError(w, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	account := args
	var result any
	var err error
	if name == "workflow_slack_bot" {
		bot := args
		result, err = api.slackVaultBotOperation(r, workflow.WorkspacePath, bot)
	} else if name == "workflow_google_account" {
		result, err = api.personalGoogleOperation(r, workflow.WorkspacePath, account)
	} else {
		result, err = api.personalSlackOperation(r, workflow.WorkspacePath, account)
	}
	if err != nil {
		externalError(w, http.StatusForbidden, "account_unavailable", err.Error())
		return
	}
	externalJSON(w, result)
}
