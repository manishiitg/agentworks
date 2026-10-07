package server

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/mcpagent/events"
)

// botDryRunTimeout bounds one dry run. Route resolution and turn building
// read a few workspace files; nothing calls a model.
const botDryRunTimeout = 20 * time.Second

// dryRunSlackMention feeds a synthetic mention of a Slack app in a channel
// through the real inbound path (docs/design/bot_destination_scope.md):
// the app's route resolution and mention message, the bot manager's routing
// and turn building, then the query boundary's revalidation and target
// access. It stops before the model and returns what the user would have
// seen. Nothing is posted to Slack and no session starts.
func (api *StreamingAPI) dryRunSlackMention(ctx context.Context, connectionID, channelID, senderEmail, text string) (services.BotDryRunOutcome, error) {
	if api == nil || api.botManager == nil {
		return services.BotDryRunOutcome{}, fmt.Errorf("bot manager unavailable")
	}
	svc, err := ensureSlackService()
	if err != nil {
		return services.BotDryRunOutcome{}, err
	}
	app, err := svc.ServiceForConnection(connectionID)
	if err != nil {
		return services.BotDryRunOutcome{}, err
	}
	if strings.TrimSpace(text) == "" {
		text = "dry run"
	}
	msg, blocked, reply := app.DryRunMention(ctx, senderEmail, channelID, text)
	if blocked {
		return services.BotDryRunOutcome{Reason: "this channel's route blocks the sender's email"}, nil
	}
	if reply != "" {
		// The bot answers instead of a turn (the target buttons, a list).
		return services.BotDryRunOutcome{Reason: reply, Replies: []string{reply}}, nil
	}
	return api.botManager.RunDryRun(ctx, msg, api.admitBotTurn, botDryRunTimeout)
}

// admitBotTurn runs the query-boundary checks handleQuery applies to a bot
// turn, on the request the bot manager built, and stops there. It returns
// services.ErrBotDryRunAdmitted when the turn would start. Refusals carry
// handleQuery's status form so the bot's user-facing message is the real one.
func (api *StreamingAPI) admitBotTurn(ctx context.Context, reqMap map[string]interface{}, sessionID, userID string, _ func(*events.AgentEvent)) error {
	if failure, ok := reqMap["_bot_prepare_error"].(string); ok {
		return fmt.Errorf("prepare conversation: %s", failure)
	}
	wire := maps.Clone(reqMap)
	delete(wire, "_trusted_resume_target")
	delete(wire, "_trusted_slack_app")
	body, err := json.Marshal(wire)
	if err != nil {
		return err
	}
	var req QueryRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return fmt.Errorf("handleQuery returned status %d: %w", http.StatusBadRequest, err)
	}
	turnCtx := internalBotRequestContext(ctx, userID, reqMap)
	if target, ok := reqMap["_trusted_resume_target"].(*resolvedResumeTarget); ok {
		turnCtx = context.WithValue(turnCtx, resolvedResumeTargetContextKey{}, target)
		req.resolvedResumeTarget = target
	}
	principalCtx, err := api.revalidateExecutionPrincipal(turnCtx, req)
	if err != nil {
		return fmt.Errorf("handleQuery returned status %d: %w", http.StatusForbidden, err)
	}
	// handleQuery also binds a Slack workflow trigger's invocation here; that
	// allocates a run folder, so a dry run does not.
	if err := admitTurnContextPaths(principalCtx, &req); err != nil {
		return fmt.Errorf("handleQuery returned status %d: %w", http.StatusForbidden, err)
	}
	req.AgentMode = normalizeAgentMode(req.AgentMode)
	_, access, admitErr := api.admitQueryTarget(principalCtx, &req, GetUserIDFromContext(principalCtx), sessionID)
	if admitErr != nil {
		status := http.StatusForbidden
		if admitErr.invalidProfile {
			status = http.StatusBadRequest
		}
		return fmt.Errorf("handleQuery returned status %d: %w", status, admitErr)
	}
	mode := "full"
	if readOnlyForRequest(access, req) {
		mode = "run"
	}
	return &services.BotDryRunAdmission{Mode: mode}
}

// dryRunSlackDM is dryRunSlackMention for a 1:1 DM from senderSlackID: the
// Slack service's proof of the sender (the app's own lookup, which tests
// replace), then the same bot manager and query boundary.
func (api *StreamingAPI) dryRunSlackDM(ctx context.Context, connectionID, senderSlackID, channelID, text string) (services.BotDryRunOutcome, error) {
	if api == nil || api.botManager == nil {
		return services.BotDryRunOutcome{}, fmt.Errorf("bot manager unavailable")
	}
	svc, err := ensureSlackService()
	if err != nil {
		return services.BotDryRunOutcome{}, err
	}
	app, err := svc.ServiceForConnection(connectionID)
	if err != nil {
		return services.BotDryRunOutcome{}, err
	}
	if strings.TrimSpace(text) == "" {
		text = "dry run"
	}
	msg, refusal := app.DryRunDirectMessage(ctx, senderSlackID, channelID, text)
	if refusal != "" {
		return services.BotDryRunOutcome{Reason: refusal, Replies: []string{refusal}}, nil
	}
	return api.botManager.RunDryRun(ctx, msg, api.admitBotTurn, botDryRunTimeout)
}

// slackConnectionDryRunHandler is POST /connections/{id}/dry-run: what a
// mention of this app in a channel would do, without posting or running.
// Body: {"channel_id": "C...", "sender_email": "...", "text": "...", "slug": "..."}.
func slackConnectionDryRunHandler(api *StreamingAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		svc, err := ensureSlackService()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		connectionID := strings.TrimSpace(mux.Vars(r)["id"])
		conn, found := svc.GetConnection(connectionID)
		if !found {
			http.Error(w, fmt.Sprintf("slack connection %q not found", connectionID), http.StatusNotFound)
			return
		}
		if err := requireSlackConnectionAccess(r, api, conn); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		var body struct {
			ChannelID   string `json:"channel_id"`
			SenderEmail string `json:"sender_email"`
			Text        string `json:"text"`
			// Slug picks a target as "@bot <slug> ..." does (PLAT-668).
			Slug string `json:"slug"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !slackChannelIDPattern.MatchString(services.NormalizeSlackChannelID(body.ChannelID)) {
			http.Error(w, "an exact Slack channel ID is required (e.g. C1234567890)", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(body.SenderEmail) == "" {
			if claims := GetUserFromContext(r.Context()); claims != nil {
				body.SenderEmail = claims.Email
			}
		}
		if slug := strings.TrimSpace(body.Slug); slug != "" {
			body.Text = strings.TrimSpace(slug + " " + firstNonBlank(strings.TrimSpace(body.Text), "dry run"))
		}
		outcome, err := api.dryRunSlackMention(r.Context(), connectionID, body.ChannelID, body.SenderEmail, body.Text)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(outcome)
	}
}
