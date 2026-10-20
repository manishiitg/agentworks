package server

import (
	"net/http"
	"net/url"
	"strings"
)

// Slack and WhatsApp for a workflow, Flow or Crew over MCP, through the Slack
// tab's own handlers and their owner check. Seeing the setup, routing
// channels, and choosing
// which existing bot answers. Connecting a new Slack bot (a token or the app
// install) and pairing WhatsApp (a QR code scanned with the phone) stay in the
// app: whatsapp_link only says where (owner decision 2026-10-09: a paired
// phone acts as its owner, so pairing stays behind a browser sign-in).

func externalMessagingDefinitions(add func(string, string, bool, bool, map[string]any, ...string)) {
	add("manage_messaging", "Slack and WhatsApp for a workflow, Flow or Crew. status: the Slack bot it uses, the channels routed to it and the bots you may choose. set_target: name, description and slug shown in Slack's \"who should answer\" picker (pass only what you want to change; an empty value goes back to the product's). add_channel / remove_channel: route a Slack channel (exact ID like C0123ABCD) to it. use_bot: answer through one of the listed bots (connection_id, or \"shared\" for the shared bot). whatsapp_link: where to pair your WhatsApp in the app (scan the QR code with your phone; pairing is never done over MCP). Connecting a new Slack bot also stays in the app. Changes need the owner.", true, false, map[string]any{
		"workflow_id":   externalString("Workflow or Flow ID from list_workflows. Pass this or crew_id."),
		"crew_id":       externalString("Crew ID from list_crews. Pass this or workflow_id."),
		"action":        map[string]any{"type": "string", "enum": []any{"status", "set_target", "add_channel", "remove_channel", "use_bot", "whatsapp_link"}},
		"channel_id":    externalString("Exact Slack channel ID, e.g. C0123ABCD."),
		"connection_id": externalString("For use_bot: a bot id from status, or \"shared\"."),
		"name":          map[string]any{"type": "string", "maxLength": 60, "description": "For set_target: the name shown in Slack's \"who should answer\" picker. Empty goes back to the product's name."},
		"description":   map[string]any{"type": "string", "maxLength": 75, "description": "For set_target: one short line shown under the name in the picker. Empty goes back to the product's description."},
		"slug":          map[string]any{"type": "string", "maxLength": 40, "description": "For set_target: the word people type to pick this target (lowercase letters, digits, hyphens)."},
	}, "action")
}

type externalMessagingTarget struct {
	kind, id, label, path, profile string
	workflow                       *DiscoveredWorkflow
	manifest                       *WorkflowManifest
}

func (api *StreamingAPI) externalMessagingCall(w http.ResponseWriter, r *http.Request, args map[string]any, target externalMessagingTarget) {
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
	switch action {
	case "status":
		out := map[string]any{"kind": target.kind, "id": target.id}
		out["whatsapp"] = "Pair your own WhatsApp in the app (manage_messaging whatsapp_link says where); it runs as you."
		if target.manifest != nil && target.manifest.Kind == "relay" {
			out["whatsapp"] = "Flows offer Act in Slack only."
		}
		if _, err := personalConnectionItemScope(r, target.path, true, "Slack", "Slack"); err != nil {
			out["status"] = "Ask this item's Builder to configure Slack. Your existing Run access is unchanged."
			externalJSON(w, out)
			return
		}
		accounts, err := api.personalSlackAccounts(r, target.path)
		if err != nil {
			externalError(w, 403, "account_unavailable", err.Error())
			return
		}
		out["act_in_slack"] = accounts
		state, err := api.slackVaultBotState(r, target.path)
		if err != nil {
			externalError(w, 403, "bot_unavailable", err.Error())
			return
		}
		out["slack_bot"], out["bots"], out["allow_bots"], out["slack_routing"] = state["selected_id"], state["bots"], state["allow_bots"], state["settings"]
		externalJSON(w, out)
	case "set_target":
		payload := map[string]any{"workspace_path": target.path, "profile_id": target.profile}
		for arg, key := range map[string]string{"name": "display_name", "description": "description", "slug": "slug"} {
			if v, ok := args[arg].(string); ok {
				payload[key] = v
			}
		}
		if len(payload) == 2 {
			externalError(w, 400, "invalid_arguments", "set_target needs name, description or slug.")
			return
		}
		respond(scheduleHandler(r, slackTargetSettingsHandler(api), http.MethodPut, "/api/human-feedback/slack/targets/settings", nil, nil, payload))
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
		externalJSON(w, map[string]any{"routed": channel})
	case "remove_channel":
		if !needChannel() {
			return
		}
		respond(scheduleHandler(r, removeSlackTargetChannelHandler(api), http.MethodDelete, "/api/human-feedback/slack/targets/channels/"+url.PathEscape(channel), map[string]string{"channel": channel}, scope, nil))
	case "use_bot":
		id := strings.TrimSpace(externalArg(args, "connection_id"))
		if id == "shared" {
			if svc, err := ensureSlackService(); err == nil {
				id = svc.DefaultConnectionID()
			}
		}
		result, err := api.slackVaultBotOperation(r, target.path, map[string]any{"action": "select", "connection_id": id})
		if err != nil {
			externalError(w, 403, "bot_unavailable", err.Error())
			return
		}
		externalJSON(w, result)
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
