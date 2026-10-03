package server

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	mcpexecutor "github.com/manishiitg/mcpagent/executor"
)

var slackReadParameters = map[string][]string{
	"conversations.history": {"cursor", "limit", "oldest", "latest", "inclusive"},
	"conversations.replies": {"ts", "cursor", "limit", "oldest", "latest", "inclusive"},
	"conversations.info":    {"include_locale", "include_num_members"},
	"reactions.get":         {"timestamp", "full"}, "pins.list": {}, "bookmarks.list": {},
}

func validateSlackCLIRead(method, channel string, parameters map[string]interface{}) (map[string]interface{}, error) {
	keys, ok := slackReadParameters[method]
	if !ok {
		return nil, fmt.Errorf("Slack method %s is not supported by this tool; search requires separate search permissions and token support", method)
	}
	allowed := map[string]bool{"channel": true}
	for _, key := range keys {
		allowed[key] = true
	}
	args := map[string]interface{}{"channel": channel}
	for key, value := range parameters {
		if !allowed[key] {
			return nil, fmt.Errorf("unsupported Slack parameter %s", key)
		}
		if key == "channel" {
			if value != channel {
				return nil, fmt.Errorf("cross-channel Slack access denied")
			}
			continue
		}
		switch value.(type) {
		case string, bool, float64, int, json.Number:
		default:
			return nil, fmt.Errorf("Slack parameters must be scalar values")
		}
		args[key] = value
	}
	if required := map[string]string{"conversations.replies": "ts", "reactions.get": "timestamp"}[method]; required != "" {
		if value, ok := args[required].(string); !ok || value == "" {
			return nil, fmt.Errorf("%s requires %s", method, required)
		}
	}
	if method == "conversations.history" || method == "conversations.replies" {
		limit := 100.
		if raw, ok := args["limit"]; ok {
			switch v := raw.(type) {
			case float64:
				limit = v
			case int:
				limit = float64(v)
			default:
				return nil, fmt.Errorf("limit must be an integer")
			}
		}
		if limit < 1 || limit > 100 || limit != float64(int(limit)) {
			return nil, fmt.Errorf("limit must be an integer from 1 to 100")
		}
		args["limit"] = int(limit)
	}
	return args, nil
}
func (api *StreamingAPI) slackCLIFromTool(ctx context.Context, args map[string]interface{}) (string, error) {
	channel := stringFromRequestMap(args, "route_id")
	method := stringFromRequestMap(args, "method")
	if !slackChannelIDPattern.MatchString(channel) {
		return "", fmt.Errorf("an exact configured route_id is required")
	}
	parameters := map[string]interface{}{}
	if raw, ok := args["parameters"]; ok {
		var valid bool
		parameters, valid = raw.(map[string]interface{})
		if !valid {
			return "", fmt.Errorf("parameters must be a JSON object")
		}
	}
	if method == "chat.postMessage" {
		for key := range parameters {
			if key != "channel" && key != "text" {
				return "", fmt.Errorf("tracked sends support text only; use thread_ref for a thread")
			}
		}
		if supplied, ok := parameters["channel"]; ok && supplied != channel {
			return "", fmt.Errorf("cross-channel Slack send denied")
		}
		return api.sendSlackMessageFromTool(ctx, map[string]interface{}{"route_id": channel, "message": parameters["text"], "thread_ref": args["thread_ref"], "idempotency_key": args["idempotency_key"]})
	}
	parameters, err := validateSlackCLIRead(method, channel, parameters)
	if err != nil {
		return "", err
	}
	session, _ := ctx.Value(common.ChatSessionIDKey).(string)
	if session == "" {
		session = mcpexecutor.SessionIDFromContext(ctx)
	}
	if session == "" {
		return "", fmt.Errorf("authenticated session required")
	}
	cfg, routes, err := api.slackRoutes(ctx)
	if err != nil {
		return "", err
	}
	route, found, dedicated := api.slackToolRoute(ctx, session, channel, routes)
	if !slackTrafficAllowed(cfg, found, dedicated) || (route.BotGrant != "run" && route.BotGrant != "owner") {
		return "", fmt.Errorf("Slack route is inactive or revoked")
	}
	if _, err = api.authorizeSlackToolRoute(ctx, session, channel, route); err != nil {
		return "", err
	}
	return services.RunSlackCLIOnConnection(ctx, slackToolConnectionID(ctx, api, session, route), method, parameters)
}

// slackAPIMethodPattern accepts Slack Web API method names (views.publish,
// conversations.members, ...), never CLI flags or paths.
var slackAPIMethodPattern = regexp.MustCompile(`^[a-z]+(\.[a-zA-Z]+)+$`)

// slackCLIFullAccess is the Slack tool for a trusted full-mode turn: the
// destination's owner (or editor) in their web chat, WhatsApp or 1:1 DM. It
// acts for them like they would at the keyboard, so any Slack API method is
// open (views.publish for the bot's Home tab, users.list, ...), with any
// JSON parameters. Run-mode turns — Slack channels, where anyone who can
// post steers the agent, and read-only users — keep slackCLIFromTool's
// read-and-reply limits (user decision 2026-09-26). The bot token stays
// backend-owned: a caller-supplied token is refused and responses are
// redacted and size-capped by the runner.
func (api *StreamingAPI) slackCLIFullAccess(ctx context.Context, session, workspace, profile string, args map[string]interface{}) (string, error) {
	method := stringFromRequestMap(args, "method")
	if !slackAPIMethodPattern.MatchString(method) {
		return "", fmt.Errorf("method must be a Slack Web API method name, e.g. views.publish")
	}
	parameters := map[string]interface{}{}
	if raw, ok := args["parameters"]; ok && raw != nil {
		var valid bool
		if parameters, valid = raw.(map[string]interface{}); !valid {
			return "", fmt.Errorf("parameters must be a JSON object")
		}
	}
	for key := range parameters {
		if strings.EqualFold(strings.TrimSpace(key), "token") {
			return "", fmt.Errorf("the bot token is backend-owned; do not pass credentials")
		}
	}
	if target := strings.Trim(strings.TrimSpace(workspace), "/"); strings.HasPrefix(target, "Workflow/") {
		manifest, found, err := ReadWorkflowManifest(ctx, target)
		if err != nil {
			return "", fmt.Errorf("cannot verify Slack target: %w", err)
		}
		if found && manifest.Kind == "relay" {
			return "", fmt.Errorf("Relays do not support Slack")
		}
	}
	connID := ""
	if channel := stringFromRequestMap(args, "route_id"); channel != "" && slackChannelIDPattern.MatchString(channel) {
		if _, routes, err := api.slackRoutes(ctx); err == nil {
			if route, found, _ := api.slackToolRoute(ctx, session, channel, routes); found {
				connID = slackToolConnectionID(ctx, api, session, route)
			}
		}
		if _, set := parameters["channel"]; !set {
			parameters["channel"] = channel
		}
	}
	if connID == "" {
		if execution, ok := api.botExecutionForSession(session); ok {
			connID = strings.TrimSpace(execution.Request.BotConnectionID)
		}
	}
	if connID == "" {
		// The workflow's or crew's own bot, else the platform default.
		if target, err := api.slackToolTarget(ctx, workspace, profile); err == nil {
			connID = slackConnectionIDForRoute(ctx, target)
		}
	}
	return services.RunSlackCLIOnConnection(ctx, connID, method, parameters)
}
