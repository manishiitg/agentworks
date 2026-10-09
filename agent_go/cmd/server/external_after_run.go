package server

import "net/http"

// Backup or publish now over MCP: the same Pulse basic pass the scheduler
// starts after a run, for owners and editors with run access. Setting them
// up stays in the app's Builder (/backup, /publish).

func externalAfterRunDefinitions(add func(string, string, bool, bool, map[string]any, ...string)) {
	add("run_after_run", "Back up or publish a workflow now, the same pass that runs after a run. Works only when that option is set up (see get_settings backup/publish; set up in the app's Builder with /backup or /publish) and something changed since the last time. Returns started, or why nothing started. Owners and editors with run access. To test notifications, use notify_user.", true, true, map[string]any{
		"option": map[string]any{"type": "string", "enum": []any{"backup", "publish"}},
	}, "option")
}

func (api *StreamingAPI) externalRunAfterRun(w http.ResponseWriter, r *http.Request, args map[string]any, workflow DiscoveredWorkflow) {
	if t := GetUserFromContext(r.Context()).AccessToken; t != nil && !t.Allows("runs:execute") {
		externalError(w, 403, "insufficient_scope", "Running backup or publish needs run access.")
		return
	}
	started, notes, err := api.scheduler.runAfterRunNow(workflow.WorkspacePath, externalArg(args, "option"))
	if err != nil {
		externalError(w, 409, "not_started", err.Error())
		return
	}
	externalJSON(w, map[string]any{"workflow_id": workflow.Manifest.ID, "option": externalArg(args, "option"), "started": started, "notes": notes})
}
