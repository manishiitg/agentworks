package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/mcpagent/events"
)

// internalBotRequestContext gives bot-originated turns the same authenticated
// identity as an HTTP turn from the paired account. The user directory remains
// authoritative for permissions: we deliberately persist only the user ID on
// the connector, then resolve the current username/email/role for every turn so
// a promotion or demotion takes effect without re-pairing WhatsApp.
func internalBotRequestContext(ctx context.Context, userID string, reqMaps ...map[string]interface{}) context.Context {
	if userID == "" || GetUserFromContext(ctx) != nil {
		return ctx
	}
	claims := &UserClaims{UserID: userID, Username: userID}
	if record := directoryUserFor(userID, "", ""); record != nil {
		claims.Username = record.Username
		claims.Email = record.Email
	}
	if len(reqMaps) > 0 {
		applyBotRouteClaims(claims, reqMaps[0])
	}
	return context.WithValue(ctx, UserContextKey, claims)
}

func applyBotRouteClaims(claims *UserClaims, reqMap map[string]interface{}) {
	if claims == nil || reqMap == nil {
		return
	}
	platform, _ := reqMap["bot_platform"].(string)
	if strings.TrimSpace(platform) == "whatsapp" {
		// WhatsApp turns run as the paired owner on the owner's own data:
		// the bot is linked as the owner's number, and the sender was
		// authenticated at message ingress (pairing ownership, link codes,
		// per-slug workflow access). There is no channel grant to revalidate,
		// so these turns carry their own principal instead of bot_route — the
		// tool boundary admits them without Slack's channel revalidation.
		claims.Provider = "bot_owner"
		return
	}
	if strings.TrimSpace(platform) == "slack" {
		// A 1:1 DM whose sender the Slack service mapped to this account
		// (slack_dm.go): the turn runs as that user, not as the route.
		if dm, _ := reqMap["_trusted_slack_dm"].(bool); dm {
			claims.Provider = slackDMProvider
			return
		}
	}
	grant, _ := reqMap["bot_route_grant"].(string)
	grant = services.NormalizeBotRouteGrant(grant, "")
	if strings.TrimSpace(platform) != "slack" || stringFromRequestMap(reqMap, "bot_route_grant") == "" {
		return
	}
	claims.Provider = "bot_route"
	claims.BotRouteGrant = grant
	claims.BotRouteWorkflowID = strings.TrimSpace(stringFromRequestMap(reqMap, "preset_query_id"))
	claims.BotRouteProfileID = strings.TrimSpace(stringFromRequestMap(reqMap, "agent_profile_id"))
	claims.SlackTrustedApp, _ = reqMap["_trusted_slack_app"].(bool)
	claims.BotRouteConversationKey = strings.TrimSpace(stringFromRequestMap(reqMap, "agent_profile_conversation_key"))
	claims.BotRouteWorkspacePath = strings.TrimSpace(stringFromRequestMap(reqMap, "selected_folder"))
}

func stringFromRequestMap(reqMap map[string]interface{}, key string) string {
	value, _ := reqMap[key].(string)
	return strings.TrimSpace(value)
}

func botRouteUserClaims(userID string, route services.ChannelRoute) *UserClaims {
	userID = strings.TrimSpace(userID)
	return &UserClaims{
		UserID:                  userID,
		Username:                userID,
		Provider:                "bot_route",
		BotRouteGrant:           services.NormalizeBotRouteGrant(route.BotGrant, route.WorkshopMode),
		BotRouteWorkflowID:      strings.TrimSpace(route.WorkflowID),
		BotRouteProfileID:       strings.TrimSpace(route.ProfileID),
		BotRouteConversationKey: strings.TrimSpace(route.ConversationKey),
		BotRouteWorkspacePath:   strings.TrimSpace(route.WorkspacePath),
	}
}

func botRouteProfileAccessForRequest(claims *UserClaims, req QueryRequest) (WorkflowAccessLevel, bool) {
	if claims == nil || claims.Provider != "bot_route" || strings.TrimSpace(claims.BotRouteProfileID) == "" {
		return "", false
	}
	if !strings.EqualFold(strings.TrimSpace(claims.BotRouteProfileID), strings.TrimSpace(req.AgentProfileID)) {
		return WorkflowAccessNone, true
	}
	if !strings.EqualFold(strings.TrimSpace(claims.BotRouteConversationKey), strings.TrimSpace(req.AgentProfileConversationKey)) {
		return WorkflowAccessNone, true
	}
	if strings.TrimSpace(claims.BotRouteWorkflowID) != "" {
		return WorkflowAccessNone, true
	}
	// A crew route saved before paths were made physical holds the logical
	// form; the turn runs in the physical folder.
	if strings.TrimSpace(claims.BotRouteWorkspacePath) == "" ||
		!workspacePathsMatchForUser(claims.UserID, claims.BotRouteWorkspacePath, req.SelectedFolder) {
		return WorkflowAccessNone, true
	}
	if strings.EqualFold(strings.TrimSpace(claims.BotRouteGrant), "owner") {
		return WorkflowAccessOwner, true
	}
	return WorkflowAccessRead, true
}

// checkBotWorkflowAccess resolves connector access. Slack routes are explicit
// channel grants, while WhatsApp passes its paired workspace user and must
// retain that user's current workflow access.
func (api *StreamingAPI) checkBotWorkflowAccess(ctx context.Context, workspaceUserID, userEmail string, route services.ChannelRoute) (string, bool, error) {
	fallbackID := strings.TrimSpace(workspaceUserID)
	if fallbackID == "" {
		return "", false, nil
	}

	var manifest *WorkflowManifest
	var exists bool
	var err error
	workspacePath := strings.TrimSpace(route.WorkspacePath)
	if workspacePath != "" {
		manifest, exists, err = ReadWorkflowManifest(ctx, workspacePath)
		if err != nil {
			return fallbackID, false, err
		}
	}
	if !exists || manifest == nil {
		manifest, exists, err = api.workflowManifestByBotRoute(ctx, route)
		if err != nil {
			return fallbackID, false, err
		}
		if !exists || manifest == nil {
			log.Printf("[BOT_ACCESS] Denied: no manifest for route workflow=%q workspace=%q", strings.TrimSpace(route.WorkflowID), workspacePath)
			return fallbackID, false, nil
		}
	}
	if routeID := strings.TrimSpace(route.WorkflowID); routeID != "" &&
		strings.TrimSpace(manifest.ID) != "" && !strings.EqualFold(routeID, strings.TrimSpace(manifest.ID)) {
		log.Printf("[BOT_ACCESS] Denied: route workflow=%q does not match manifest=%q", routeID, manifest.ID)
		return fallbackID, false, nil
	}
	if manifest.Kind == "relay" {
		log.Printf("[BOT_ACCESS] Denied: Relay %q only accepts API function triggers", manifest.ID)
		return fallbackID, false, nil
	}

	claims := botWorkflowAccessClaims(fallbackID, userEmail, route)
	if workflowAccessForManifest(claims, manifest) == WorkflowAccessNone {
		log.Printf("[BOT_ACCESS] Denied: route principal %s has no access to workflow %s", claims.UserID, manifest.ID)
		return fallbackID, false, nil
	}
	return claims.UserID, true, nil
}

func botWorkflowAccessClaims(userID, userEmail string, route services.ChannelRoute) *UserClaims {
	if strings.HasPrefix(strings.TrimSpace(userID), "bot-") {
		// Route creation already requires destination management authority. A
		// Slack Run grant also applies to legacy manifests without ownership.
		return botRouteUserClaims(userID, route)
	}
	claims := &UserClaims{UserID: strings.TrimSpace(userID), Username: strings.TrimSpace(userID), Email: strings.TrimSpace(userEmail)}
	if record := directoryUserFor(userID, "", userEmail); record != nil {
		claims.Username = record.Username
		claims.Email = record.Email
	}
	return claims
}

func (api *StreamingAPI) checkWhatsAppWorkflowAccess(ctx context.Context, userID string, route services.ChannelRoute) (bool, error) {
	if strings.TrimSpace(route.ProfileID) != "" {
		// Crew slugs are product-conversation routes, not workflow manifests.
		// Resolve the project on every use so a deleted project, or a slug
		// whose folder no longer holds that crew, stops authorizing messages.
		if !strings.EqualFold(strings.TrimSpace(route.ProfileID), "work") ||
			strings.TrimSpace(route.WorkflowID) != "" ||
			strings.TrimSpace(route.ConversationKey) == "" ||
			strings.TrimSpace(route.WorkspacePath) == "" || api.agentProfiles == nil {
			return false, nil
		}
		profile, err := api.agentProfiles.Resolve("work", 0, strings.TrimSpace(userID))
		if err != nil {
			return false, err
		}
		if !userAllowedProduct(botWorkflowAccessClaims(strings.TrimSpace(userID), "", route), profile.Product) {
			return false, nil
		}
		// Crews are read-only for everyone but their owner: resolve the
		// project under whichever owner holds it, as the web chat does.
		binding, _, err := resolveConversationBindingForUser(ctx, strings.TrimSpace(userID), profile, route.ConversationKey)
		if err != nil {
			return false, nil
		}
		return workspacePathsMatchForUser(userID, route.WorkspacePath, binding.WorkspacePath), nil
	}
	_, allowed, err := api.checkBotWorkflowAccess(ctx, strings.TrimSpace(userID), "", route)
	return allowed, err
}

func (api *StreamingAPI) workflowManifestByBotRoute(ctx context.Context, route services.ChannelRoute) (*WorkflowManifest, bool, error) {
	workflowID := strings.TrimSpace(route.WorkflowID)
	if workflowID == "" {
		return nil, false, nil
	}
	discovered, err := DiscoverWorkflowManifests(ctx)
	if err != nil {
		return nil, false, err
	}
	for _, workflow := range discovered {
		if workflow.Manifest != nil && strings.EqualFold(strings.TrimSpace(workflow.Manifest.ID), workflowID) {
			return workflow.Manifest, true, nil
		}
	}
	return nil, false, nil
}

// startSessionInternal starts an agent session programmatically (used by bot connector).
// It constructs a QueryRequest from the provided map and invokes handleQuery internally.
// This blocks until the exact query execution and all descendants complete.
func (api *StreamingAPI) startSessionInternal(
	ctx context.Context,
	reqMap map[string]interface{},
	sessionID string,
	userID string,
	eventCallback func(event *events.AgentEvent),
) error {
	_, err := api.startSessionInternalWithResult(ctx, reqMap, sessionID, userID, eventCallback)
	return err
}

type internalSessionTurnResult struct {
	QueryID       string
	FinalResponse string
}

// startSessionInternalWithResult starts one exact turn and returns the final
// assistant response emitted for that execution. Callers that record durable
// automation history use this instead of later inspecting a shared chat whose
// newest message may belong to another turn.
func (api *StreamingAPI) startSessionInternalWithResult(
	ctx context.Context,
	reqMap map[string]interface{},
	sessionID string,
	userID string,
	eventCallback func(event *events.AgentEvent),
) (internalSessionTurnResult, error) {
	if failure, ok := reqMap["_bot_prepare_error"].(string); ok {
		return internalSessionTurnResult{}, fmt.Errorf("prepare conversation: %s", failure)
	}
	// Marshal the request map to JSON
	wireRequest := maps.Clone(reqMap)
	delete(wireRequest, "_trusted_resume_target")
	delete(wireRequest, "_trusted_slack_app")
	body, err := json.Marshal(wireRequest)
	if err != nil {
		return internalSessionTurnResult{}, fmt.Errorf("failed to marshal query request: %w", err)
	}
	// Schedules, triggers and bots use the same durable dispatcher as browser
	// chat. Internal callers wait for their exact queued result; the HTTP path
	// acknowledges immediately. A claimed worker carries the bypass marker.
	if !conversationTurnQueueExecution(ctx) {
		var queuedRequest QueryRequest
		if decodeErr := json.Unmarshal(body, &queuedRequest); decodeErr == nil &&
			shouldUseDurableConversationTurnQueue(queuedRequest) &&
			api.conversationTurnOccupied(sessionID) {
			queueCtx := internalBotRequestContext(ctx, userID, reqMap)
			turn, _, queueErr := api.enqueueConversationTurn(queueCtx, userID, sessionID, queuedRequest)
			if queueErr != nil {
				return internalSessionTurnResult{}, queueErr
			}
			waiter := api.registerQueuedTurnWaiter(turn.ID, eventCallback)
			api.recordQueuedConversationUserMessage(sessionID, turn)
			api.kickConversationTurnQueue(sessionID)
			select {
			case queuedResult := <-waiter:
				return queuedResult.Result, queuedResult.Err
			case <-ctx.Done():
				return internalSessionTurnResult{}, ctx.Err()
			}
		}
	}

	// Create a fake HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", "/api/query", io.NopCloser(bytes.NewReader(body)))
	if err != nil {
		return internalSessionTurnResult{}, fmt.Errorf("failed to create internal request: %w", err)
	}
	httpReq = httpReq.WithContext(internalBotRequestContext(httpReq.Context(), userID, reqMap))
	if target, ok := reqMap["_trusted_resume_target"].(*resolvedResumeTarget); ok {
		httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), resolvedResumeTargetContextKey{}, target))
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Session-ID", sessionID)
	if userID != "" {
		httpReq.Header.Set("X-User-ID", userID)
	}

	// Use a ResponseRecorder to capture the response
	recorder := httptest.NewRecorder()

	// Call handleQuery synchronously — but it starts processing async and returns immediately
	api.handleQuery(recorder, httpReq)

	resp := recorder.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return internalSessionTurnResult{}, fmt.Errorf("handleQuery returned status %d: %s", resp.StatusCode, string(respBody))
	}

	// Parse the response to get the actual queryID
	var queryResp QueryResponse
	if err := json.NewDecoder(resp.Body).Decode(&queryResp); err != nil {
		if isScheduledSession(sessionID) {
			scheduleLogfWithContext(newServerLogContext("", "", "", userID, "", sessionID), "[BOT_SESSION] Failed to parse handleQuery response: %v", err)
		}
	}

	if isScheduledSession(sessionID) {
		scheduleLogfWithContext(newServerLogContext("", "", "", userID, "", sessionID), "[BOT_SESSION] Internal session started: sessionID=%s queryID=%s", sessionID, queryResp.QueryID)
	}

	if queryResp.Status == queryStatusLiveInputDelivered {
		if isScheduledSession(sessionID) {
			scheduleLogfWithContext(newServerLogContext("", "", "", userID, "", sessionID), "[BOT_SESSION] Internal session delivered as live input; waiting for exact execution %s", queryResp.QueryID)
		}
	}
	if strings.TrimSpace(queryResp.QueryID) == "" {
		return internalSessionTurnResult{}, fmt.Errorf("handleQuery did not return a query execution id")
	}
	err = api.waitForConversationTurnTree(ctx, sessionID, queryResp.QueryID, schedulerWorkshopMaxInactivity)
	if execution, ok := api.botExecutionForSession(sessionID); ok && execution.Request.PresetQueryID != "" {
		if pruneErr := api.pruneSlackRuns(execution.Request.SelectedFolder); pruneErr != nil {
			log.Printf("[SLACK_RETENTION] %v", pruneErr)
		}
	}
	result := internalSessionTurnResult{QueryID: queryResp.QueryID}
	if err == nil {
		result.FinalResponse = api.finalResponseForExecution(sessionID, queryResp.QueryID)
	}
	return result, err
}

func (api *StreamingAPI) finalResponseForExecution(sessionID, executionID string) string {
	if api == nil || api.eventStore == nil {
		return ""
	}
	fallback := ""
	// Main-agent turns are scoped to the session ("main:<session>"),
	// not the query execution ID: match both, newest first.
	wantMain := "main:" + strings.TrimSpace(sessionID)
	wantQuery := strings.TrimSpace(executionID)
	all := api.eventStore.GetAllEventsRaw(sessionID)
	for i := len(all) - 1; i >= 0; i-- {
		event := all[i]
		execID := strings.TrimSpace(event.ExecutionID)
		matched := execID == wantMain || (wantQuery != "" && execID == wantQuery)
		if !matched || event.Data == nil {
			continue
		}
		switch event.Type {
		case "unified_completion":
			if text := endEventText(event.Data.Data); text != "" {
				return text
			}
		case "llm_generation_end":
			// Turns may end without a unified completion (the waiter
			// accepts several terminal types); the newest generation
			// of a completed turn is its final assistant text.
			if fallback == "" {
				fallback = endEventText(event.Data.Data)
			}
		}
	}
	return fallback
}

// endEventText extracts the final assistant text from a turn-end event
// payload, typed or generic. It mirrors scheduledTurnProducedResponse:
// a unified completion carries FinalResult, a generation end its Content.
func endEventText(data interface{}) string {
	switch completion := data.(type) {
	case *events.UnifiedCompletionEvent:
		if completion != nil {
			return strings.TrimSpace(completion.FinalResult)
		}
	case *events.LLMGenerationEndEvent:
		if completion != nil {
			return strings.TrimSpace(completion.Content)
		}
	}
	// Persisted/reloaded event data can be represented by a generic payload.
	if raw, marshalErr := json.Marshal(data); marshalErr == nil {
		var payload struct {
			FinalResult string `json:"final_result"`
			Content     string `json:"content"`
		}
		if json.Unmarshal(raw, &payload) == nil {
			return strings.TrimSpace(firstNonEmptyTrimmed(payload.FinalResult, payload.Content))
		}
	}
	return ""
}

// sendFollowUpInternal injects a follow-up message into an existing session.
// It reuses the handleQuery path with the same session ID but does NOT block on completion.
// Events flow via EventStore → BotEventFilter → thread automatically.
//
// The reqMap is built by BotConversationManager.buildQueryRequest() so the follow-up agent
// gets the exact same config (servers, skills, delegation mode, API keys, etc.) as the initial session.
func (api *StreamingAPI) sendFollowUpInternal(
	ctx context.Context,
	reqMap map[string]interface{},
	sessionID string,
	userID string,
) error {
	if failure, ok := reqMap["_bot_prepare_error"].(string); ok {
		return fmt.Errorf("prepare conversation: %s", failure)
	}
	wireRequest := maps.Clone(reqMap)
	delete(wireRequest, "_trusted_resume_target")
	delete(wireRequest, "_trusted_slack_app")
	body, err := json.Marshal(wireRequest)
	if err != nil {
		return fmt.Errorf("failed to marshal follow-up request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", "/api/query", io.NopCloser(bytes.NewReader(body)))
	if err != nil {
		return fmt.Errorf("failed to create follow-up request: %w", err)
	}
	httpReq = httpReq.WithContext(internalBotRequestContext(httpReq.Context(), userID, reqMap))
	if target, ok := reqMap["_trusted_resume_target"].(*resolvedResumeTarget); ok {
		httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), resolvedResumeTargetContextKey{}, target))
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Session-ID", sessionID)
	if userID != "" {
		httpReq.Header.Set("X-User-ID", userID)
	}

	recorder := httptest.NewRecorder()
	api.handleQuery(recorder, httpReq)

	resp := recorder.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return services.NewBotSubmissionError(resp.StatusCode, respBody)
	}

	if execution, ok := api.botExecutionForSession(sessionID); ok && execution.Request.PresetQueryID != "" {
		var result QueryResponse
		if json.NewDecoder(resp.Body).Decode(&result) == nil && result.QueryID != "" {
			go func() {
				_ = api.waitForConversationTurnTree(context.Background(), sessionID, result.QueryID, schedulerWorkshopMaxInactivity)
				if err := api.pruneSlackRuns(execution.Request.SelectedFolder); err != nil {
					log.Printf("[SLACK_RETENTION] %v", err)
				}
			}()
		}
	}

	if isScheduledSession(sessionID) {
		scheduleLogfWithContext(newServerLogContext("", "", "", userID, "", sessionID), "[BOT_SESSION] Follow-up injected into session %s", sessionID)
	}
	return nil
}

// botWorkflowTurn is the workflow adapter to the application-owned request
// builder. Connector code supplies only normalized text, target and thread.
func (api *StreamingAPI) botWorkflowTurn(ctx context.Context, query string, route services.ChannelRoute, thread services.ThreadID, dmUserID string) (map[string]interface{}, error) {
	manifest, found, err := ReadWorkflowManifest(ctx, route.WorkspacePath)
	if err != nil {
		return nil, err
	}
	if !found || manifest.ID != route.WorkflowID {
		return nil, fmt.Errorf("workflow route target is unavailable")
	}
	if manifest.Kind == "relay" {
		return nil, fmt.Errorf("Relay %q does not accept bot channel messages", manifest.ID)
	}
	if api.scheduler == nil {
		return nil, fmt.Errorf("shared conversation builder unavailable")
	}
	// A channel turn runs as the route's principal; a 1:1 DM as its sender.
	// Either way the model and its credentials come from the workflow's own
	// LLM settings (applyLLMAndSecretsToReqMap), so who asks never changes
	// which account pays; the cost ledger records the sender as the user.
	principalID := services.BotPrincipalIDForRoute(thread.Platform, route)
	if strings.TrimSpace(dmUserID) != "" {
		principalID = strings.TrimSpace(dmUserID)
	}
	req := api.scheduler.buildWorkshopRequest(ctx, &ScheduleContext{WorkspacePath: route.WorkspacePath, WorkflowID: route.WorkflowID, WorkflowLabel: manifest.Label, OwnerUserID: principalID, Capabilities: manifest.Capabilities, Schedule: WorkflowSchedule{Name: manifest.Label}, TriggerSource: "bot:" + thread.Platform})
	req["query"] = query
	services.ApplyBotThreadFields(req, thread.Platform, thread)
	req["bot_route_grant"] = route.BotGrant
	req["workshop_mode"] = services.WorkshopModeForBotGrant(route.BotGrant)
	req["execution_options"].(map[string]interface{})["workshop_mode"] = services.WorkshopModeForBotGrant(route.BotGrant)
	req["bot_send_full_details"] = route.SendFullDetails
	delete(req, "disable_live_input_delivery")
	return req, nil
}

// userWorkflowChat is the session a user's web Builder restores for the
// workflow (handleGetWorkflowBuilderSession: their live Builder session,
// else their latest saved Builder conversation), so a DM continues it.
func (api *StreamingAPI) userWorkflowChat(ctx context.Context, userID string, route services.ChannelRoute) string {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return ""
	}
	userCtx := internalBotRequestContext(ctx, userID)
	workspacePath := strings.Trim(strings.TrimSpace(route.WorkspacePath), "/")
	if live := api.findLiveWorkflowBuilderSession(userCtx, route.WorkflowID, workspacePath); live != nil {
		return live.SessionID
	}
	restored, err := api.restoreLatestBuilderConversationLimited(userCtx, route.WorkflowID, workspacePath, slackBuilderRefreshLimit)
	if err != nil {
		log.Printf("[BOT_ACCESS] user %s workflow chat lookup for %s: %v", userID, route.WorkflowID, err)
		return ""
	}
	if restored == nil {
		return ""
	}
	return restored.SessionID
}
