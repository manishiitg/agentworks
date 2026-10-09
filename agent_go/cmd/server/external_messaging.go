package server

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

// Slack and WhatsApp for a workflow, Relay or Crew over MCP, through the Slack
// tab's own handlers and their owner check. Seeing the setup, routing
// channels (each new route is tested with the app's dry run), and choosing
// which existing bot answers. Connecting a new Slack bot (a token or the app
// install) and pairing WhatsApp (a QR code scanned with the phone) stay in the
// app: whatsapp_link only says where (owner decision 2026-10-09: a paired
// phone acts as its owner, so pairing stays behind a browser sign-in).

func externalMessagingDefinitions(add func(string, string, bool, bool, map[string]any, ...string)) {
	add("manage_messaging", "Slack and WhatsApp for a workflow, Relay or Crew. status: the Slack bot it uses, the channels routed to it and the bots you may choose. add_channel / remove_channel: route a Slack channel (exact ID like C0123ABCD) to it; a new route is tested with the app's dry run (nothing is posted). test_channel: run that dry run again. use_bot: answer through one of the listed bots (connection_id, or \"shared\" for the shared bot). whatsapp_link: where to pair your WhatsApp in the app (scan the QR code with your phone; pairing is never done over MCP). Connecting a new Slack bot also stays in the app. Changes need the owner.", true, false, map[string]any{
		"workflow_id":   externalString("Workflow or Relay ID from list_workflows. Pass this or crew_id."),
		"crew_id":       externalString("Crew ID from list_crews. Pass this or workflow_id."),
		"action":        map[string]any{"type": "string", "enum": []any{"status", "add_channel", "remove_channel", "test_channel", "use_bot", "whatsapp_link"}},
		"channel_id":    externalString("Exact Slack channel ID, e.g. C0123ABCD."),
		"connection_id": externalString("For use_bot: a bot id from status, or \"shared\"."),
		"text":          map[string]any{"type": "string", "maxLength": 500, "description": "Optional message text for the dry run."},
	}, "action")
}

type externalMessagingTarget struct {
	kind, id, label, path, profile string
	workflow                       *DiscoveredWorkflow
	manifest                       *WorkflowManifest
}

func (api *StreamingAPI) externalMessagingCall(w http.ResponseWriter, r *http.Request, args map[string]any, target externalMessagingTarget) {
	ctx := r.Context()
	action := externalArg(args, "action")
	respond := func(status int, body []byte) { externalScheduleRespond(w, status, body) }
	scope := url.Values{"workspace_path": {target.path}, "profile_id": {target.profile}}
	channel := strings.TrimSpace(externalArg(args, "channel_id"))
	needChannel := func() bool {
		if channel == "" {
			externalError(w, 400, "invalid_arguments", "channel_id is required (an exact Slack channel ID, e.g. C0123ABCD).")
			return false
		}
		return true
	}
	dryRun := func() (int, []byte) {
		return scheduleHandler(r, slackTargetDryRunHandler(api), http.MethodPost, "/api/human-feedback/slack/targets/dry-run", nil, nil,
			map[string]any{"workspace_path": target.path, "profile_id": target.profile, "channel_id": channel, "text": externalArg(args, "text")})
	}
	switch action {
	case "status":
		out := map[string]any{"kind": target.kind, "id": target.id}
		if status, body := scheduleHandler(r, slackTargetSettingsHandler(api), http.MethodGet, "/api/human-feedback/slack/targets/settings", nil, scope, nil); status == http.StatusOK && json.Valid(body) {
			out["slack_routing"] = json.RawMessage(body)
		}
		selected := ""
		if target.manifest != nil {
			selected = target.manifest.Capabilities.SlackConnectionID
		} else if status, body := scheduleHandler(r, projectSlackConnectionHandler(api), http.MethodGet, "/api/human-feedback/slack/connections/project/selection", nil, scope, nil); status == http.StatusOK {
			var picked struct {
				SlackConnectionID string `json:"slack_connection_id"`
			}
			_ = json.Unmarshal(body, &picked)
			selected = picked.SlackConnectionID
		}
		// Only bots that can answer here: the shared one and this project's own.
		bots := []map[string]any{}
		if svc, err := ensureSlackService(); err == nil {
			defaultID := svc.DefaultConnectionID()
			for _, conn := range svc.ListConnections() {
				own := strings.Trim(conn.WorkspacePath, "/") != "" && strings.Trim(conn.WorkspacePath, "/") == strings.Trim(target.path, "/")
				if conn.WorkspacePath != "" && !own {
					continue
				}
				bots = append(bots, map[string]any{"connection_id": conn.ID, "name": conn.DisplayName, "enabled": conn.Enabled, "shared": conn.WorkspacePath == "", "default": conn.ID == defaultID})
			}
		}
		if selected == "" {
			selected = "shared"
		}
		out["slack_bot"], out["bots"] = selected, bots
		out["whatsapp"] = "Pair your own WhatsApp in the app (manage_messaging whatsapp_link says where); it runs as you."
		externalJSON(w, out)
	case "add_channel":
		if !needChannel() {
			return
		}
		status, body := scheduleHandler(r, addSlackTargetChannelHandler(api), http.MethodPost, "/api/human-feedback/slack/targets/channels/"+url.PathEscape(channel), map[string]string{"channel": channel}, nil,
			map[string]any{"workspace_path": target.path, "profile_id": target.profile})
		if status >= 400 {
			respond(status, body)
			return
		}
		out := map[string]any{"routed": channel}
		if testStatus, testBody := dryRun(); testStatus == http.StatusOK && json.Valid(testBody) {
			out["dry_run"] = json.RawMessage(testBody)
		} else {
			out["dry_run_error"] = strings.TrimSpace(string(testBody))
		}
		externalJSON(w, out)
	case "remove_channel":
		if !needChannel() {
			return
		}
		respond(scheduleHandler(r, removeSlackTargetChannelHandler(api), http.MethodDelete, "/api/human-feedback/slack/targets/channels/"+url.PathEscape(channel), map[string]string{"channel": channel}, scope, nil))
	case "test_channel":
		if !needChannel() {
			return
		}
		respond(dryRun())
	case "use_bot":
		id := strings.TrimSpace(externalArg(args, "connection_id"))
		if id == "" {
			externalError(w, 400, "invalid_arguments", "connection_id is required: a bot id from status, or \"shared\".")
			return
		}
		if id == "shared" {
			id = ""
		}
		if target.manifest != nil {
			if err := requireSlackTargetOwner(ctx, api, slackTargetFromRequest(ctx, target.path, target.profile)); err != nil {
				externalError(w, 403, "forbidden", err.Error())
				return
			}
			caps := target.manifest.Capabilities
			caps.SlackConnectionID = id
			respond(scheduleHandler(r, requireWorkflowWriteAccess(api.handleUpdateWorkflowManifest), http.MethodPut, "/api/workflows/manifest", nil, nil,
				UpdateWorkflowManifestRequest{WorkspacePath: target.path, Capabilities: &caps}))
			return
		}
		respond(scheduleHandler(r, projectSlackConnectionHandler(api), http.MethodPut, "/api/human-feedback/slack/connections/project/selection", nil, scope, map[string]any{"slack_connection_id": id}))
	case "whatsapp_link":
		place := "the workflow"
		if target.kind == "crew" {
			place = "the Crew"
		}
		externalJSON(w, map[string]any{"open": getBaseURL(r), "steps": "Sign in, open " + place + " \"" + target.label + "\", go to Integrations, open WhatsApp and scan the QR code with your phone (WhatsApp > Settings > Linked devices > Link a device).",
			"note": "Pairing is done in the app, never over MCP: a paired phone acts as you until it is unpaired."})
	default:
		externalError(w, 400, "invalid_arguments", "Unknown action.")
	}
}
