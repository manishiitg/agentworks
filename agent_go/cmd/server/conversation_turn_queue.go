package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	unifiedevents "github.com/manishiitg/mcpagent/events"
)

const conversationTurnQueueVersion = 1

type conversationTurnQueueExecutionKey struct{}

func withConversationTurnQueueExecution(ctx context.Context) context.Context {
	return context.WithValue(ctx, conversationTurnQueueExecutionKey{}, true)
}

func conversationTurnQueueExecution(ctx context.Context) bool {
	value, _ := ctx.Value(conversationTurnQueueExecutionKey{}).(bool)
	return value
}

func shouldUseDurableConversationTurnQueue(req QueryRequest) bool {
	return !req.IsAutoNotification && shouldSerializeInteractiveQueryInput(req)
}

func sanitizedQueuedConversationRequest(req QueryRequest) QueryRequest {
	// Queue documents live in the workspace. Persist selectors and encrypted
	// secret names, never resolved values. handleQuery resolves current values
	// again after the turn is claimed, which also honors rotations and revokes.
	req.DecryptedSecrets = nil
	if req.LLMConfig != nil {
		config := *req.LLMConfig
		config.Primary = req.LLMConfig.Primary
		config.Primary.APIKey = nil
		config.APIKeys = nil
		req.LLMConfig = &config
	}
	return req
}

// queuedConversationPrincipal contains only the server-validated identity
// attributes needed to reconstruct and revalidate a delayed turn. It never
// stores a bearer token or an execution principal.
type queuedConversationPrincipal struct {
	ExternalBuilderOperationID string `json:"external_builder_operation_id,omitempty"`
	FeedbackOperationID        string `json:"feedback_operation_id,omitempty"`
	Provider                   string `json:"provider,omitempty"`
	AccessTokenID              string `json:"access_token_id,omitempty"`
	SlackTrustedApp            bool   `json:"slack_trusted_app,omitempty"`
	BotRouteGrant              string `json:"bot_route_grant,omitempty"`
	BotRouteWorkflowID         string `json:"bot_route_workflow_id,omitempty"`
	BotRouteProfileID          string `json:"bot_route_profile_id,omitempty"`
	BotRouteConversationKey    string `json:"bot_route_conversation_key,omitempty"`
	BotRouteWorkspacePath      string `json:"bot_route_workspace_path,omitempty"`
}

type queuedConversationTurn struct {
	ID           string                      `json:"id"`
	UserID       string                      `json:"user_id"`
	SessionID    string                      `json:"session_id"`
	Request      QueryRequest                `json:"request"`
	Principal    queuedConversationPrincipal `json:"principal,omitempty"`
	SubmissionID string                      `json:"submission_id,omitempty"`
	CreatedAt    time.Time                   `json:"created_at"`
	StartedAt    *time.Time                  `json:"started_at,omitempty"`
}

type conversationTurnQueueDocument struct {
	Version int                      `json:"version"`
	Turns   []queuedConversationTurn `json:"turns"`
}

type queuedConversationTurnResult struct {
	Result internalSessionTurnResult
	Err    error
}

func conversationTurnQueuePath(userID string) string {
	return filepath.ToSlash(filepath.Join(chatHistoryRoot(userID), "conversation-turn-dispatch.json"))
}

func queuedPrincipalFromContext(ctx context.Context) queuedConversationPrincipal {
	claims := GetUserFromContext(ctx)
	if claims == nil {
		return queuedConversationPrincipal{}
	}
	principal := queuedConversationPrincipal{
		ExternalBuilderOperationID: claims.ExternalBuilderOperationID,
		FeedbackOperationID:        virtualtools.FeedbackOperationFromContext(ctx),
		Provider:                   claims.Provider, SlackTrustedApp: claims.SlackTrustedApp,
		BotRouteGrant: claims.BotRouteGrant, BotRouteWorkflowID: claims.BotRouteWorkflowID,
		BotRouteProfileID: claims.BotRouteProfileID, BotRouteConversationKey: claims.BotRouteConversationKey,
		BotRouteWorkspacePath: claims.BotRouteWorkspacePath,
	}
	if claims.AccessToken != nil {
		principal.AccessTokenID = claims.AccessToken.ID
	}
	return principal
}

func (api *StreamingAPI) readConversationTurnQueue(ctx context.Context, userID string) ([]queuedConversationTurn, error) {
	read := readFileFromWorkspace
	if api != nil && api.internalTurnQueueRead != nil {
		read = api.internalTurnQueueRead
	}
	raw, exists, err := read(ctx, conversationTurnQueuePath(userID))
	if err != nil || !exists || strings.TrimSpace(raw) == "" {
		return nil, err
	}
	var document conversationTurnQueueDocument
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		return nil, fmt.Errorf("decode conversation turn queue: %w", err)
	}
	if document.Version != 0 && document.Version != conversationTurnQueueVersion {
		return nil, fmt.Errorf("unsupported conversation turn queue version %d", document.Version)
	}
	return document.Turns, nil
}

func (api *StreamingAPI) writeConversationTurnQueue(ctx context.Context, userID string, turns []queuedConversationTurn) error {
	write := writeRawFileToWorkspace
	if api != nil && api.internalTurnQueueWrite != nil {
		write = api.internalTurnQueueWrite
	}
	encoded, err := json.MarshalIndent(conversationTurnQueueDocument{Version: conversationTurnQueueVersion, Turns: turns}, "", "  ")
	if err != nil {
		return err
	}
	return write(ctx, conversationTurnQueuePath(userID), string(encoded))
}

func (api *StreamingAPI) enqueueConversationTurn(ctx context.Context, userID, sessionID string, req QueryRequest) (queuedConversationTurn, int, error) {
	userID, sessionID = strings.TrimSpace(userID), strings.TrimSpace(sessionID)
	if userID == "" || sessionID == "" {
		return queuedConversationTurn{}, 0, errors.New("conversation owner and session are required")
	}
	turn := queuedConversationTurn{
		ID: uuid.NewString(), UserID: userID, SessionID: sessionID, Request: sanitizedQueuedConversationRequest(req),
		Principal: queuedPrincipalFromContext(ctx), CreatedAt: time.Now().UTC(),
	}
	if submission, ok := ctx.Value(chatSubmissionContextKey{}).(chatSubmissionContext); ok {
		turn.SubmissionID = submission.ID
	}
	path := conversationTurnQueuePath(userID)
	lock := productConversationRegistryMutex(path)
	lock.Lock()
	defer lock.Unlock()
	storageCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	turns, err := api.readConversationTurnQueue(storageCtx, userID)
	if err != nil {
		return queuedConversationTurn{}, 0, err
	}
	turns = append(turns, turn)
	if err := api.writeConversationTurnQueue(storageCtx, userID, turns); err != nil {
		return queuedConversationTurn{}, 0, err
	}
	position := 0
	for _, pending := range turns {
		if pending.SessionID == sessionID && pending.StartedAt == nil {
			position++
		}
	}
	api.conversationTurnQueueMu.Lock()
	if api.conversationTurnQueueOwners == nil {
		api.conversationTurnQueueOwners = make(map[string]string)
	}
	api.conversationTurnQueueOwners[sessionID] = userID
	api.conversationTurnQueueMu.Unlock()
	return turn, position, nil
}

func (api *StreamingAPI) conversationTurnDispatchPending(sessionID string) bool {
	api.conversationTurnQueueMu.Lock()
	defer api.conversationTurnQueueMu.Unlock()
	return api.conversationTurnQueueDraining[sessionID]
}

func (api *StreamingAPI) conversationTurnOccupied(sessionID string) bool {
	if api == nil || strings.TrimSpace(sessionID) == "" {
		return false
	}
	if api.sessionTurnInProgress(sessionID) || api.conversationTurnDispatchPending(sessionID) ||
		api.hasActiveTurnCancel(sessionID) || api.storedAgentTurnInProgress(sessionID) {
		return true
	}
	if retained, ok := mcpagent.LookupSession(sessionID); ok && retained.ActiveTurnID() != "" {
		return true
	}
	api.retainedMainTurnsMu.Lock()
	_, retainedRunning := api.retainedMainTurns[sessionID]
	api.retainedMainTurnsMu.Unlock()
	return retainedRunning
}

func (api *StreamingAPI) claimNextConversationTurn(ctx context.Context, userID, sessionID string) (queuedConversationTurn, bool) {
	path := conversationTurnQueuePath(userID)
	lock := productConversationRegistryMutex(path)
	lock.Lock()
	defer lock.Unlock()
	turns, err := api.readConversationTurnQueue(ctx, userID)
	if err != nil {
		logTurnQueue("cannot read queue for %s: %v", userID, err)
		return queuedConversationTurn{}, false
	}
	for i := range turns {
		if turns[i].SessionID != sessionID || turns[i].StartedAt != nil {
			continue
		}
		now := time.Now().UTC()
		turns[i].StartedAt = &now
		if err := api.writeConversationTurnQueue(ctx, userID, turns); err != nil {
			logTurnQueue("cannot claim turn %s: %v", turns[i].ID, err)
			return queuedConversationTurn{}, false
		}
		return turns[i], true
	}
	return queuedConversationTurn{}, false
}

func (api *StreamingAPI) removeConversationTurn(ctx context.Context, turn queuedConversationTurn) error {
	path := conversationTurnQueuePath(turn.UserID)
	lock := productConversationRegistryMutex(path)
	lock.Lock()
	defer lock.Unlock()
	turns, err := api.readConversationTurnQueue(ctx, turn.UserID)
	if err != nil {
		return err
	}
	kept := turns[:0]
	for _, pending := range turns {
		if pending.ID != turn.ID {
			kept = append(kept, pending)
		}
	}
	return api.writeConversationTurnQueue(ctx, turn.UserID, kept)
}

// dropStartedConversationTurns removes the session's queue entries that were claimed for
// execution (StartedAt set): the turn they belong to has been stopped and will never remove
// them. Unstarted entries stay queued. Returns how many were removed.
func (api *StreamingAPI) dropStartedConversationTurns(sessionID string) int {
	api.conversationTurnQueueMu.Lock()
	userID := api.conversationTurnQueueOwners[sessionID]
	api.conversationTurnQueueMu.Unlock()
	if userID == "" {
		return 0
	}
	ctx := context.Background()
	path := conversationTurnQueuePath(userID)
	lock := productConversationRegistryMutex(path)
	lock.Lock()
	defer lock.Unlock()
	turns, err := api.readConversationTurnQueue(ctx, userID)
	if err != nil || len(turns) == 0 {
		return 0
	}
	kept := make([]queuedConversationTurn, 0, len(turns))
	dropped := 0
	for _, turn := range turns {
		if turn.SessionID == sessionID && turn.StartedAt != nil {
			dropped++
			continue
		}
		kept = append(kept, turn)
	}
	if dropped == 0 {
		return 0
	}
	if err := api.writeConversationTurnQueue(ctx, userID, kept); err != nil {
		logTurnQueue("cannot drop stopped turns for %s: %v", sessionID, err)
		return 0
	}
	return dropped
}

func (api *StreamingAPI) kickConversationTurnQueue(sessionID string) {
	if api == nil || strings.TrimSpace(sessionID) == "" || api.conversationTurnOccupied(sessionID) {
		return
	}
	api.conversationTurnQueueMu.Lock()
	if api.conversationTurnQueueDraining == nil {
		api.conversationTurnQueueDraining = make(map[string]bool)
	}
	if api.conversationTurnQueueDraining[sessionID] {
		api.conversationTurnQueueMu.Unlock()
		return
	}
	userID := api.conversationTurnQueueOwners[sessionID]
	if userID == "" {
		api.conversationTurnQueueMu.Unlock()
		return
	}
	api.conversationTurnQueueDraining[sessionID] = true
	api.conversationTurnQueueMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	turn, ok := api.claimNextConversationTurn(ctx, userID, sessionID)
	cancel()
	if !ok {
		api.conversationTurnQueueMu.Lock()
		delete(api.conversationTurnQueueDraining, sessionID)
		delete(api.conversationTurnQueueOwners, sessionID)
		api.conversationTurnQueueMu.Unlock()
		return
	}
	go api.executeQueuedConversationTurn(turn)
}

func (api *StreamingAPI) executeQueuedConversationTurn(turn queuedConversationTurn) {
	reqMap, err := queryRequestToMap(turn.Request)
	var result internalSessionTurnResult
	var callback func(event *unifiedevents.AgentEvent)
	api.conversationTurnQueueMu.Lock()
	callback = api.conversationTurnQueueCallbacks[turn.ID]
	api.conversationTurnQueueMu.Unlock()
	if err == nil {
		ctx, principalErr := api.queuedConversationTurnContext(turn, reqMap)
		if principalErr != nil {
			err = principalErr
		} else {
			ctx = withConversationTurnQueueExecution(ctx)
			if turn.Principal.ExternalBuilderOperationID != "" {
				result, err = api.runQueuedExternalBuilder(ctx, turn, reqMap)
			} else {
				result, err = api.startSessionInternalWithResult(ctx, reqMap, turn.SessionID, turn.UserID, callback)
			}
		}
	}
	if err != nil && turn.Principal.ExternalBuilderOperationID != "" {
		_ = settleExternalBuilder(turn.Principal.ExternalBuilderOperationID, "failed", "", err.Error())
	}
	outcome, proof := "confirmed", "conversation_turn_completed"
	if err != nil {
		outcome, proof = "failed", "conversation_turn_failed"
		logTurnQueue("turn %s failed: %v", turn.ID, err)
	}
	api.recordLiveInputConfirmed(turn.SessionID, turn.ID, outcome, proof, turn.Request.Provider, time.Since(turn.CreatedAt).Milliseconds(), sanitizeClientMessageID(turn.SubmissionID))
	storageCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	if removeErr := api.removeConversationTurn(storageCtx, turn); removeErr != nil {
		logTurnQueue("cannot persist completion for %s: %v", turn.ID, removeErr)
		if err == nil {
			err = removeErr
		}
	}
	cancel()
	api.conversationTurnQueueMu.Lock()
	waiter := api.conversationTurnQueueWaiters[turn.ID]
	delete(api.conversationTurnQueueWaiters, turn.ID)
	delete(api.conversationTurnQueueCallbacks, turn.ID)
	delete(api.conversationTurnQueueDraining, turn.SessionID)
	api.conversationTurnQueueMu.Unlock()
	if waiter != nil {
		waiter <- queuedConversationTurnResult{Result: result, Err: err}
		close(waiter)
	}
	api.kickConversationTurnQueue(turn.SessionID)
}

func (api *StreamingAPI) queuedConversationTurnContext(turn queuedConversationTurn, reqMap map[string]interface{}) (context.Context, error) {
	ctx := internalBotRequestContext(context.Background(), turn.UserID, reqMap)
	claims := GetUserFromContext(ctx)
	if claims == nil {
		return nil, errors.New("queued conversation principal is unavailable")
	}
	copy := *claims
	copy.Provider = turn.Principal.Provider
	copy.SlackTrustedApp = turn.Principal.SlackTrustedApp
	copy.BotRouteGrant = turn.Principal.BotRouteGrant
	copy.BotRouteWorkflowID = turn.Principal.BotRouteWorkflowID
	copy.BotRouteProfileID = turn.Principal.BotRouteProfileID
	copy.BotRouteConversationKey = turn.Principal.BotRouteConversationKey
	copy.BotRouteWorkspacePath = turn.Principal.BotRouteWorkspacePath
	if turn.Principal.AccessTokenID != "" {
		tokenClaims, claimsErr := activeExternalGrantClaims(ctx, turn.Principal.AccessTokenID)
		if claimsErr != nil {
			return nil, claimsErr
		}
		if tokenClaims.UserID != turn.UserID {
			return nil, errors.New("queued grant owner does not match the conversation")
		}
		copy = *tokenClaims
	}
	copy.ExternalBuilderOperationID = turn.Principal.ExternalBuilderOperationID
	ctx = context.WithValue(ctx, UserContextKey, &copy)
	if turn.Principal.FeedbackOperationID != "" {
		ctx = virtualtools.WithFeedbackOperation(ctx, turn.Principal.FeedbackOperationID)
	}
	if turn.SubmissionID != "" {
		ctx = context.WithValue(ctx, chatSubmissionContextKey{}, chatSubmissionContext{
			ID: turn.SubmissionID, Owner: turn.UserID, Session: turn.SessionID, Message: turn.Request.Query,
		})
	}
	return ctx, nil
}

func (api *StreamingAPI) recordQueuedConversationUserMessage(sessionID string, turn queuedConversationTurn) {
	// A keyed submission already shows as a provisional queued bubble in the
	// submitting browser. Its durable row is the turn's own user_message, which
	// the event store stamps with the same client id when the turn starts, so it
	// lands after the answer it waited behind instead of above it.
	if sanitizeClientMessageID(turn.SubmissionID) != "" {
		return
	}
	api.recordLiveCodingAgentUserMessage(sessionID, turn.Request.Query, turn.Request.Provider, turn.ID, "queued_for_turn", "")
}

func (api *StreamingAPI) registerQueuedTurnWaiter(turnID string, callback func(event *unifiedevents.AgentEvent)) <-chan queuedConversationTurnResult {
	waiter := make(chan queuedConversationTurnResult, 1)
	api.conversationTurnQueueMu.Lock()
	if api.conversationTurnQueueWaiters == nil {
		api.conversationTurnQueueWaiters = make(map[string]chan queuedConversationTurnResult)
	}
	if api.conversationTurnQueueCallbacks == nil {
		api.conversationTurnQueueCallbacks = make(map[string]func(event *unifiedevents.AgentEvent))
	}
	api.conversationTurnQueueWaiters[turnID] = waiter
	if callback != nil {
		api.conversationTurnQueueCallbacks[turnID] = callback
	}
	api.conversationTurnQueueMu.Unlock()
	return waiter
}

func (api *StreamingAPI) recoverConversationTurnQueue(ctx context.Context) {
	userIDs := map[string]bool{GetDefaultUserID(): true}
	if directory, err := loadUserDirectory(); err == nil && directory != nil {
		for _, user := range directory.Users {
			if !user.Disabled && strings.TrimSpace(user.ID) != "" {
				userIDs[user.ID] = true
			}
		}
	}
	users := make([]string, 0, len(userIDs))
	for userID := range userIDs {
		users = append(users, userID)
	}
	sort.Strings(users)
	for _, userID := range users {
		path := conversationTurnQueuePath(userID)
		lock := productConversationRegistryMutex(path)
		lock.Lock()
		turns, err := api.readConversationTurnQueue(ctx, userID)
		if err != nil || len(turns) == 0 {
			lock.Unlock()
			continue
		}
		for i := range turns {
			turns[i].StartedAt = nil
		}
		if err := api.writeConversationTurnQueue(ctx, userID, turns); err != nil {
			lock.Unlock()
			logTurnQueue("cannot recover queue for %s: %v", userID, err)
			continue
		}
		lock.Unlock()
		api.conversationTurnQueueMu.Lock()
		if api.conversationTurnQueueOwners == nil {
			api.conversationTurnQueueOwners = make(map[string]string)
		}
		for _, turn := range turns {
			api.conversationTurnQueueOwners[turn.SessionID] = userID
		}
		api.conversationTurnQueueMu.Unlock()
		for _, turn := range turns {
			api.kickConversationTurnQueue(turn.SessionID)
		}
	}
}

func logTurnQueue(format string, args ...interface{}) {
	scheduleLogf("[CONVERSATION-TURN-QUEUE] "+format, args...)
}

// PendingQueuedMessage is a keyed message waiting in the durable turn queue.
type PendingQueuedMessage struct {
	ClientMessageID string    `json:"client_message_id"`
	Content         string    `json:"content"`
	QueuedAt        time.Time `json:"queued_at"`
	QueuePosition   int       `json:"queue_position"`
}

// pendingQueuedMessages lists the viewer's not-yet-started keyed turns for a
// session so a reload or another tab can show them before the CLI takes them.
func (api *StreamingAPI) pendingQueuedMessages(ctx context.Context, userID, sessionID string) []PendingQueuedMessage {
	if api == nil || userID == "" || sessionID == "" {
		return nil
	}
	turns, err := api.readConversationTurnQueue(ctx, userID)
	if err != nil {
		return nil
	}
	var pending []PendingQueuedMessage
	for _, turn := range turns {
		if turn.SessionID != sessionID || turn.StartedAt != nil {
			continue
		}
		clientMessageID := sanitizeClientMessageID(turn.SubmissionID)
		if clientMessageID == "" {
			continue
		}
		pending = append(pending, PendingQueuedMessage{
			ClientMessageID: clientMessageID,
			Content:         turn.Request.Query,
			QueuedAt:        turn.CreatedAt,
			QueuePosition:   len(pending) + 1,
		})
	}
	return pending
}
