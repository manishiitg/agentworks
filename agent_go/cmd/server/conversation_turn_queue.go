package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costobserver"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	unifiedevents "github.com/manishiitg/mcpagent/events"
	llmproviders "github.com/manishiitg/multi-llm-provider-go"
)

const conversationTurnQueueVersion = 1

type conversationTurnQueueExecutionKey struct{}

// Internal callers install their result waiter before a durable turn becomes
// claimable. This callback is request-local and is never persisted in the queue.
type internalQueuedTurnAdmissionKey struct{}

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

// Only request-local validated input crosses the asynchronous retained send.
// It is not a new principal or a persistent runtime configuration cache.
type retainedQueueInputKey struct{}
type retainedQueueInput struct {
	Request   QueryRequest
	LiveInput bool
}

func (api *StreamingAPI) queueIdleRetainedInput(w http.ResponseWriter, r *http.Request, sessionID string, active bool) bool {
	input, known := r.Context().Value(retainedQueueInputKey{}).(retainedQueueInput)
	userID := GetUserIDFromContext(r.Context())
	if active || !known || isQueuedMessageSteer(r.Context()) || conversationTurnQueueExecution(r.Context()) || !shouldUseDurableConversationTurnQueue(input.Request) || !api.conversationTurnHasBacklog(userID, sessionID) {
		return false
	}
	// Recheck immediately before the provider Send. Any idle lane acquired
	// here stays held until durable admission completes, including errors.
	release, acquired := api.tryLockSessionInputLane(sessionID, 0)
	if acquired {
		defer release()
	}
	if input.LiveInput {
		return api.queueLiveInputTurnResponse(w, r, userID, sessionID, input.Request)
	}
	return api.queueConversationTurnResponse(w, r, userID, sessionID, input.Request)
}

func (api *StreamingAPI) queueLiveInputTurnResponse(w http.ResponseWriter, r *http.Request, userID, sessionID string, req QueryRequest) bool {
	turn, position, err := api.enqueueConversationTurn(r.Context(), userID, sessionID, req)
	if err != nil {
		http.Error(w, "Cannot durably queue conversation turn: "+err.Error(), http.StatusServiceUnavailable)
		return true
	}
	api.recordQueuedConversationUserMessage(sessionID, turn)
	api.kickConversationTurnQueue(sessionID)
	_ = json.NewEncoder(w).Encode(LiveInputResponse{Success: true, Message: "Queued behind an earlier conversation turn", DeliveryStatus: "queued_for_turn", MessageID: turn.ID, QueryID: turn.ID, QueuePosition: position})
	return true
}

type queuedBrowserInputKey struct{}

type queuedBrowserInput struct {
	Mode  string
	Port  *int
	Ports []int
}

func withQueuedBrowserInput(ctx context.Context, req QueryRequest) context.Context {
	input := queuedBrowserInput{Mode: req.BrowserMode, Ports: append([]int(nil), req.resolvedCDPPorts...)}
	if req.CdpPort != nil {
		port := *req.CdpPort
		input.Port = &port
	}
	return context.WithValue(ctx, queuedBrowserInputKey{}, input)
}

func sanitizedQueuedConversationRequest(ctx context.Context, req QueryRequest) QueryRequest {
	// Runtime enrichment is not caller input. Replay the validated browser
	// selection, then resolve current workspace settings in handleQuery again.
	input, captured := ctx.Value(queuedBrowserInputKey{}).(queuedBrowserInput)
	if !captured && req.callerBrowserInput != nil {
		input, captured = *req.callerBrowserInput, true
	}
	if captured {
		req.BrowserMode, req.CdpPort, req.resolvedCDPPorts = input.Mode, input.Port, input.Ports
	}
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
	CrewAskInput               *crewAskInputScope     `json:"crew_ask_input,omitempty"`
	CostAttribution            costledger.Attribution `json:"cost_attribution,omitempty"`
	ExternalBuilderOperationID string                 `json:"external_builder_operation_id,omitempty"`
	FeedbackOperationID        string                 `json:"feedback_operation_id,omitempty"`
	Provider                   string                 `json:"provider,omitempty"`
	AccessTokenID              string                 `json:"access_token_id,omitempty"`
	SlackTrustedApp            bool                   `json:"slack_trusted_app,omitempty"`
	BotRouteGrant              string                 `json:"bot_route_grant,omitempty"`
	BotRouteWorkflowID         string                 `json:"bot_route_workflow_id,omitempty"`
	BotRouteProfileID          string                 `json:"bot_route_profile_id,omitempty"`
	BotRouteConversationKey    string                 `json:"bot_route_conversation_key,omitempty"`
	BotRouteWorkspacePath      string                 `json:"bot_route_workspace_path,omitempty"`
}

type queuedConversationTurn struct {
	ID            string                      `json:"id"`
	UserID        string                      `json:"user_id"`
	SessionID     string                      `json:"session_id"`
	Request       QueryRequest                `json:"request"`
	Principal     queuedConversationPrincipal `json:"principal,omitempty"`
	SubmissionID  string                      `json:"submission_id,omitempty"`
	CreatedAt     time.Time                   `json:"created_at"`
	StartedAt     *time.Time                  `json:"started_at,omitempty"`
	Steering      bool                        `json:"steering,omitempty"`
	DeliveryError string                      `json:"delivery_error,omitempty"`
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
		CrewAskInput:               crewAskInputFromContext(ctx),
		CostAttribution:            costobserver.AttributionFromContext(ctx),
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
	if idleCompactRunFromContext(ctx) != nil {
		// Never persist an idle compaction: its request is the last person
		// message, which the drainer would run again as a real turn.
		return queuedConversationTurn{}, 0, errors.New("idle compaction turns are never queued")
	}
	userID, sessionID = strings.TrimSpace(userID), strings.TrimSpace(sessionID)
	if userID == "" || sessionID == "" {
		return queuedConversationTurn{}, 0, errors.New("conversation owner and session are required")
	}
	turn := queuedConversationTurn{
		ID: uuid.NewString(), UserID: userID, SessionID: sessionID, Request: sanitizedQueuedConversationRequest(ctx, req),
		Principal: queuedPrincipalFromContext(ctx), CreatedAt: time.Now().UTC(),
	}
	if ask := crewAskInputFromContext(ctx); ask != nil {
		turn.ID = ask.Query
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
		if pending.SessionID == sessionID && pending.StartedAt == nil && pending.DeliveryError == "" {
			position++
		}
	}
	api.conversationTurnQueueMu.Lock()
	api.registerConversationQueueOwnerLocked(userID, sessionID)
	api.conversationTurnQueueMu.Unlock()
	// Keep the queue document lock through admission: an existing queue watcher
	// can claim immediately after unlock, even before this caller kicks it.
	if admitted, ok := ctx.Value(internalQueuedTurnAdmissionKey{}).(func(string)); ok {
		admitted(turn.ID)
	}
	return turn, position, nil
}

// This index is installed only after durable admission (and rebuilt on
// restart). A free input lane does not mean an older admitted turn vanished.
func (api *StreamingAPI) conversationTurnHasBacklog(userID, sessionID string) bool {
	if api == nil || userID == "" || sessionID == "" {
		return false
	}
	api.conversationTurnQueueMu.Lock()
	defer api.conversationTurnQueueMu.Unlock()
	return len(api.conversationTurnQueueOwners[sessionID]) > 0
}

// Caller holds conversationTurnQueueMu. Membership follows a successful
// durable write; two authorized callers never replace each other's owner.
func (api *StreamingAPI) registerConversationQueueOwnerLocked(userID, sessionID string) {
	if api.conversationTurnQueueOwners == nil {
		api.conversationTurnQueueOwners = make(map[string]map[string]bool)
	}
	if api.conversationTurnQueueOwners[sessionID] == nil {
		api.conversationTurnQueueOwners[sessionID] = make(map[string]bool)
	}
	api.conversationTurnQueueOwners[sessionID][userID] = true
}

func (api *StreamingAPI) conversationQueueOwners(sessionID string) []string {
	api.conversationTurnQueueMu.Lock()
	defer api.conversationTurnQueueMu.Unlock()
	owners := make([]string, 0, len(api.conversationTurnQueueOwners[sessionID]))
	for owner := range api.conversationTurnQueueOwners[sessionID] {
		owners = append(owners, owner)
	}
	sort.Strings(owners)
	return owners
}

// One session drainer picks the oldest eligible head from its already known
// callers. Documents and delayed principals remain isolated per caller.
func (api *StreamingAPI) claimOldestConversationTurn(ctx context.Context, sessionID string) (queuedConversationTurn, bool, error) {
	var oldest queuedConversationTurn
	var readErr error
	for _, owner := range api.conversationQueueOwners(sessionID) {
		lock := productConversationRegistryMutex(conversationTurnQueuePath(owner))
		lock.Lock()
		turns, err := api.readConversationTurnQueue(ctx, owner)
		if err != nil {
			lock.Unlock()
			api.noteConversationQueueReadState(sessionID, owner, err)
			readErr = errors.Join(readErr, err)
			continue // One caller's unreadable document must not block others.
		}
		api.noteConversationQueueReadState(sessionID, owner, nil)
		var head queuedConversationTurn
		for _, turn := range turns {
			if turn.UserID == owner && turn.SessionID == sessionID && turn.StartedAt == nil && turn.DeliveryError == "" {
				head = turn
				break
			}
		}
		if head.ID == "" {
			// Keep the file lock through index cleanup: a concurrent enqueue
			// must register again after we remove this empty membership.
			api.conversationTurnQueueMu.Lock()
			delete(api.conversationTurnQueueOwners[sessionID], owner)
			if len(api.conversationTurnQueueOwners[sessionID]) == 0 {
				delete(api.conversationTurnQueueOwners, sessionID)
			}
			api.conversationTurnQueueMu.Unlock()
		} else if oldest.ID == "" || head.CreatedAt.Before(oldest.CreatedAt) || (head.CreatedAt.Equal(oldest.CreatedAt) && head.ID < oldest.ID) {
			oldest = head
		}
		lock.Unlock()
	}
	if oldest.ID == "" {
		return queuedConversationTurn{}, false, readErr
	}
	lock := productConversationRegistryMutex(conversationTurnQueuePath(oldest.UserID))
	lock.Lock()
	defer lock.Unlock()
	turns, err := api.readConversationTurnQueue(ctx, oldest.UserID)
	if err != nil {
		api.noteConversationQueueReadState(sessionID, oldest.UserID, err)
		return queuedConversationTurn{}, false, err
	}
	for i := range turns {
		if turns[i].ID == oldest.ID && turns[i].UserID == oldest.UserID && turns[i].SessionID == sessionID && turns[i].StartedAt == nil && turns[i].DeliveryError == "" {
			now := time.Now().UTC()
			turns[i].StartedAt = &now
			if err := api.writeConversationTurnQueue(ctx, oldest.UserID, turns); err != nil {
				logTurnQueue("cannot persist queue claim for session %s: %v", sessionID, err)
				return queuedConversationTurn{}, false, err
			}
			return turns[i], true, nil
		}
	}
	// An explicit owner action took the selected head. Re-evaluate heads.
	return queuedConversationTurn{}, false, nil
}

// Compatible input may still steer a genuinely active turn. It must not
// start a new retained turn in the idle gap before a queued turn is claimed.
func (api *StreamingAPI) shouldWaitForQueuedConversation(ctx context.Context, userID, sessionID string, req QueryRequest) bool {
	if conversationTurnQueueExecution(ctx) || !shouldUseDurableConversationTurnQueue(req) || !api.conversationTurnHasBacklog(userID, sessionID) {
		return false
	}
	occupant := api.conversationTurnOccupiedBy(sessionID)
	return occupant == "" || occupant == "dispatch_pending"
}

func (api *StreamingAPI) conversationTurnDispatchPending(sessionID string) bool {
	api.conversationTurnQueueMu.Lock()
	defer api.conversationTurnQueueMu.Unlock()
	return api.conversationTurnQueueDraining[sessionID]
}

func (api *StreamingAPI) conversationTurnOccupied(sessionID string) bool {
	return api.conversationTurnOccupiedBy(sessionID) != ""
}

// sessionTurnRunning is the one answer to "is a turn running in this chat" for
// status and lifecycle checks: the queue's occupancy (the input lane is the
// authority) plus the display busy flag, which some turn kinds set without
// taking the lane. MCP turn_status used the flag alone and could say idle
// while a turn still held the lane.
func (api *StreamingAPI) sessionTurnRunning(sessionID string) bool {
	return api.conversationTurnOccupied(sessionID) || api.isSessionBusy(sessionID)
}

// conversationTurnOccupiedBy names what occupies the session ("" when idle).
func (api *StreamingAPI) conversationTurnOccupiedBy(sessionID string) string {
	if api == nil || strings.TrimSpace(sessionID) == "" {
		return ""
	}
	switch {
	case api.sessionTurnInProgress(sessionID):
		return "input_lane"
	case api.conversationTurnDispatchPending(sessionID):
		return "dispatch_pending"
	case api.hasActiveTurnCancel(sessionID):
		return "active_turn_cancel"
	case api.storedAgentTurnInProgress(sessionID):
		return "stored_agent_turn"
	}
	if retained, ok := mcpagent.LookupSession(sessionID); ok && retained.ActiveTurnID() != "" {
		return "retained_session_turn"
	}
	api.retainedMainTurnsMu.Lock()
	_, retainedRunning := api.retainedMainTurns[sessionID]
	api.retainedMainTurnsMu.Unlock()
	if retainedRunning {
		return "retained_main_turn"
	}
	return ""
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
		if turns[i].SessionID != sessionID || turns[i].StartedAt != nil || turns[i].DeliveryError != "" {
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
	dropped := 0
	for _, owner := range api.conversationQueueOwners(sessionID) {
		dropped += api.dropStartedConversationTurnsForOwner(owner, sessionID)
	}
	return dropped
}

func (api *StreamingAPI) dropStartedConversationTurnsForOwner(userID, sessionID string) int {
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
	if api == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	if by := api.conversationTurnOccupiedBy(sessionID); by != "" {
		// A running claimed turn kicks again when it ends. Any other occupant
		// (lane, cancel handle, stored agent, retained turn) releases without a
		// kick of its own, or just after the kick that saw it: watch the session
		// so queued messages cannot wait forever (provider switch, 2026-10-05).
		if by != "dispatch_pending" {
			api.watchOccupiedConversationTurnQueue(sessionID, by)
		}
		return
	}
	api.conversationTurnQueueMu.Lock()
	// The session is free: the next claimed turn applies any runtime change itself.
	delete(api.conversationTurnQueueRuntimeChange, sessionID)
	if api.conversationTurnQueueDraining == nil {
		api.conversationTurnQueueDraining = make(map[string]bool)
	}
	if api.conversationTurnQueueDraining[sessionID] {
		api.conversationTurnQueueMu.Unlock()
		return
	}
	if len(api.conversationTurnQueueOwners[sessionID]) == 0 {
		api.conversationTurnQueueMu.Unlock()
		return
	}
	api.conversationTurnQueueDraining[sessionID] = true
	api.conversationTurnQueueMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	turn, ok, err := api.claimOldestConversationTurn(ctx, sessionID)
	cancel()
	if !ok {
		api.conversationTurnQueueMu.Lock()
		delete(api.conversationTurnQueueDraining, sessionID)
		pending := len(api.conversationTurnQueueOwners[sessionID]) > 0
		api.conversationTurnQueueMu.Unlock()
		if err != nil {
			api.retryUnreadableConversationQueue(sessionID)
		} else if pending {
			// Admission or an explicit owner action changed a head while
			// claiming. The next kick uses a fresh registered-owner snapshot.
			go api.kickConversationTurnQueue(sessionID)
		}
		return
	}
	// Paired with the turn's user row timestamp, this shows how long a message sat between the queue and the chat.
	logTurnQueue("handing queued message %s to its turn in session %s after %s in the queue", turn.ID, sessionID, time.Since(turn.CreatedAt).Round(time.Millisecond))
	go api.executeQueuedConversationTurn(turn)
}

type conversationQueueRetryState struct {
	Attempts    int
	OwnerErrors map[string]string
}

func (api *StreamingAPI) noteConversationQueueReadState(sessionID, owner string, err error) {
	api.conversationTurnQueueMu.Lock()
	if api.conversationTurnQueueRetries == nil {
		api.conversationTurnQueueRetries = make(map[string]*conversationQueueRetryState)
	}
	state := api.conversationTurnQueueRetries[sessionID]
	if state == nil {
		state = &conversationQueueRetryState{OwnerErrors: map[string]string{}}
		api.conversationTurnQueueRetries[sessionID] = state
	}
	previous := state.OwnerErrors[owner]
	current := ""
	if err != nil {
		current = err.Error()
		state.OwnerErrors[owner] = current
	} else {
		delete(state.OwnerErrors, owner)
	}
	if len(state.OwnerErrors) == 0 {
		delete(api.conversationTurnQueueRetries, sessionID)
	}
	api.conversationTurnQueueMu.Unlock()
	if current != previous {
		if err != nil {
			logTurnQueue("cannot read queue for owner %s in session %s: %v", owner, sessionID, err)
		} else {
			logTurnQueue("queue readable again for owner %s in session %s", owner, sessionID)
		}
	}
}

func conversationQueueRetryDelay(attempt int) time.Duration {
	if attempt == 0 {
		return 5 * time.Second
	}
	if attempt == 1 {
		return 30 * time.Second
	}
	return 5 * time.Minute
}

// Reuse the one-watcher-per-session guard. A failed queue read keeps its
// owner registered and gets another chance without spinning or blocking others.
func (api *StreamingAPI) retryUnreadableConversationQueue(sessionID string) {
	api.conversationTurnQueueMu.Lock()
	if api.conversationTurnQueueWatching == nil {
		api.conversationTurnQueueWatching = make(map[string]bool)
	}
	if api.conversationTurnQueueWatching[sessionID] || len(api.conversationTurnQueueOwners[sessionID]) == 0 {
		api.conversationTurnQueueMu.Unlock()
		return
	}
	api.conversationTurnQueueWatching[sessionID] = true
	if api.conversationTurnQueueRetries == nil {
		api.conversationTurnQueueRetries = make(map[string]*conversationQueueRetryState)
	}
	state := api.conversationTurnQueueRetries[sessionID]
	if state == nil {
		state = &conversationQueueRetryState{OwnerErrors: map[string]string{}}
		api.conversationTurnQueueRetries[sessionID] = state
	}
	delay := conversationQueueRetryDelay(state.Attempts)
	if state.Attempts < 2 {
		state.Attempts++
	}
	api.conversationTurnQueueMu.Unlock()
	time.AfterFunc(delay, func() {
		api.conversationTurnQueueMu.Lock()
		delete(api.conversationTurnQueueWatching, sessionID)
		api.conversationTurnQueueMu.Unlock()
		api.kickConversationTurnQueue(sessionID)
	})
}

const conversationTurnQueueWatchInterval = 2 * time.Second

// watchOccupiedConversationTurnQueue re-checks a session whose queued messages
// found it occupied, and kicks the queue once the occupant is gone. One watcher
// per session; it stops when nothing is queued for the session any more.
func (api *StreamingAPI) watchOccupiedConversationTurnQueue(sessionID, occupant string) {
	api.conversationTurnQueueMu.Lock()
	if api.conversationTurnQueueWatching == nil {
		api.conversationTurnQueueWatching = make(map[string]bool)
	}
	if api.conversationTurnQueueWatching[sessionID] || len(api.conversationTurnQueueOwners[sessionID]) == 0 {
		api.conversationTurnQueueMu.Unlock()
		return
	}
	api.conversationTurnQueueWatching[sessionID] = true
	api.conversationTurnQueueMu.Unlock()
	logTurnQueue("session %s occupied by %s; queued messages wait and the queue is re-checked every %s", sessionID, occupant, conversationTurnQueueWatchInterval)
	go func() {
		ticker := time.NewTicker(conversationTurnQueueWatchInterval)
		defer ticker.Stop()
		idleChecks := 0
		for range ticker.C {
			api.conversationTurnQueueMu.Lock()
			queued := len(api.conversationTurnQueueOwners[sessionID]) > 0
			api.conversationTurnQueueMu.Unlock()
			if !queued {
				break
			}
			if by := api.conversationTurnOccupiedBy(sessionID); by != "" {
				api.endIdleTurnForRuntimeChange(sessionID, by, &idleChecks)
				api.autoSteerQueuedConversationTurn(sessionID)
				continue
			}
			break
		}
		api.conversationTurnQueueMu.Lock()
		delete(api.conversationTurnQueueWatching, sessionID)
		api.conversationTurnQueueMu.Unlock()
		api.kickConversationTurnQueue(sessionID)
	}()
}

// queueOccupiedConversationTurnForRuntimeChange queues a message that must wait
// to relaunch the coding CLI with a changed runtime (definition or provider),
// and marks the session so the queue watcher may end an idle live turn for it.
func (api *StreamingAPI) queueOccupiedConversationTurnForRuntimeChange(w http.ResponseWriter, r *http.Request, userID, sessionID string, req QueryRequest) bool {
	api.setConversationTurnRuntimeChange(sessionID, true)
	if api.queueOccupiedConversationTurn(w, r, userID, sessionID, req) {
		return true
	}
	api.setConversationTurnRuntimeChange(sessionID, false)
	return false
}

func (api *StreamingAPI) setConversationTurnRuntimeChange(sessionID string, waiting bool) {
	api.conversationTurnQueueMu.Lock()
	defer api.conversationTurnQueueMu.Unlock()
	if !waiting {
		delete(api.conversationTurnQueueRuntimeChange, sessionID)
		return
	}
	if api.conversationTurnQueueRuntimeChange == nil {
		api.conversationTurnQueueRuntimeChange = make(map[string]bool)
	}
	api.conversationTurnQueueRuntimeChange[sessionID] = true
}

// runtimeChangeIdleChecks is how many consecutive watcher ticks (2 s apart) must
// see the retained CLI idle at its prompt before its live turn is ended, so a
// turn that is just finishing on its own is never cut short.
const runtimeChangeIdleChecks = 3

// retainedCLIAtPrompt reports whether the chat's main coding CLI is idle at its
// input prompt, using the provider adapter's own idle-composer check
// (CodingAgentPaneReady, the signal the retained-turn observer settles turns on).
// No live pane, a failed capture, or a provider without a pane check (Muse)
// counts as busy: fail closed. A var so tests can stub tmux.
var retainedCLIAtPrompt = func(api *StreamingAPI, sessionID string) bool {
	snapshot, live := api.liveMainCodingTmuxSnapshot(sessionID)
	provider := llmproviders.Provider(retainedCodingAgentProvider(snapshot))
	if !live || provider == "" {
		return false
	}
	// A Claude terminal running the AgentWorks mod says itself whether a turn runs.
	if idle, known := claudecode.ClaudeModIdle(snapshot.TmuxSession); known {
		return idle
	}
	ctx, cancel := context.WithTimeout(context.Background(), terminalTmuxActionTimeout)
	defer cancel()
	pane, err := runTerminalTmuxOutputCommand(ctx, "capture-pane", "-p", "-J", "-t", snapshot.TmuxSession)
	return err == nil && llmproviders.CodingAgentPaneReady(provider, pane)
}

// endIdleTurnForRuntimeChange applies the between-turns rule for a message queued
// only because the runtime changed. In tmux live-input mode one streaming turn
// stays open while the person keeps typing into the running CLI, so it holds the
// input lane long after the CLI finished answering. Code chat sde-private on server A,
// 2026-10-07: a turn open since 05:40 held the lane, a deploy at 06:39 changed the
// Code definition, and the message sent at 07:31 stayed "Queued" with "Working…"
// forever. When the CLI has been idle at its prompt for runtimeChangeIdleChecks
// ticks, end that turn the way a definition change always does (cancel the turn,
// close the CLI: interruptWorkflowPolicySession) and release its busy markers as
// Stop does; the watcher then kicks the queue and the message relaunches the CLI
// with the new runtime. A CLI that is mid-response keeps the turn: a relaunch
// mid-turn killed a running Muse turn (server B 2026-10-03).
func (api *StreamingAPI) endIdleTurnForRuntimeChange(sessionID, occupant string, idleChecks *int) bool {
	api.conversationTurnQueueMu.Lock()
	waiting := api.conversationTurnQueueRuntimeChange[sessionID]
	api.conversationTurnQueueMu.Unlock()
	// A claimed queued turn is already running the message (dispatch_pending).
	if !waiting || occupant == "dispatch_pending" || api.conversationTurnDispatchPending(sessionID) || !retainedCLIAtPrompt(api, sessionID) {
		*idleChecks = 0
		return false
	}
	*idleChecks++
	if *idleChecks < runtimeChangeIdleChecks {
		return false
	}
	*idleChecks = 0
	logTurnQueue("session %s: the coding CLI is idle at its prompt but its live turn (%s) is still open; ending it so the message queued for a runtime change runs", sessionID, occupant)
	api.interruptWorkflowPolicySession(sessionID, "")
	api.releaseStoppedSessionTurnMarkers(sessionID)
	return true
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
	if err == nil {
		api.recordLiveInputConfirmed(turn.SessionID, turn.ID, outcome, proof, turn.Request.Provider, time.Since(turn.CreatedAt).Milliseconds(), sanitizeClientMessageID(turn.SubmissionID))
	}
	storageCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	if err != nil {
		// A failure may precede the normal user echo, or follow partial work.
		// Keep its content and explicit owner retry; never replay it automatically.
		// Persist the no-replay state before publishing failure. A crash or
		// failed queue write must not publish a verdict recovery can contradict.
		if persistErr := api.failConversationTurn(storageCtx, turn); persistErr != nil {
			logTurnQueue("cannot persist failed turn %s: %v", turn.ID, persistErr)
		} else {
			api.recordFailedQueuedConversationUserMessage(turn)
		}
	} else if removeErr := api.removeConversationTurn(storageCtx, turn); removeErr != nil {
		logTurnQueue("cannot persist completion for %s: %v", turn.ID, removeErr)
		err = removeErr
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

const queuedTurnFailureMessage = "This queued message could not be completed. Check the chat before retrying."

func (api *StreamingAPI) failConversationTurn(ctx context.Context, turn queuedConversationTurn) error {
	lock := productConversationRegistryMutex(conversationTurnQueuePath(turn.UserID))
	lock.Lock()
	defer lock.Unlock()
	turns, err := api.readConversationTurnQueue(ctx, turn.UserID)
	if err != nil {
		return err
	}
	for i := range turns {
		if turns[i].ID == turn.ID {
			turns[i].StartedAt = nil
			turns[i].Steering = false
			turns[i].DeliveryError = queuedTurnFailureMessage
		}
	}
	return api.writeConversationTurnQueue(ctx, turn.UserID, turns)
}

func (api *StreamingAPI) recordFailedQueuedConversationUserMessage(turn queuedConversationTurn) {
	api.recordLiveCodingAgentUserMessage(turn.SessionID, turn.Request.Query, turn.Request.Provider, turn.ID, "failed", sanitizeClientMessageID(turn.SubmissionID))
	// The content must precede its receipt for live and restored consumers.
	api.recordLiveInputConfirmed(turn.SessionID, turn.ID, "failed", "conversation_turn_failed", turn.Request.Provider, time.Since(turn.CreatedAt).Milliseconds(), sanitizeClientMessageID(turn.SubmissionID))
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
	if ask := turn.Principal.CrewAskInput; ask != nil {
		if ask.User != turn.UserID || ask.Session != turn.SessionID || ask.Query != turn.ID {
			return nil, errors.New("queued Ask identity no longer matches")
		}
		ctx = context.WithValue(ctx, crewAskInputKey{}, ask)
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
	ctx = costobserver.ContextWithAttribution(ctx, turn.Principal.CostAttribution)
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
	sessions := map[string]bool{}
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
		turns = dropStaleWaitingTurns(turns, time.Now().UTC())
		for i := range turns {
			if turns[i].Steering {
				// An interrupted steer may have reached the CLI. Never replay it blindly.
				turns[i].DeliveryError = "Delivery unconfirmed after restart. Check the chat before resending."
				turns[i].Steering = false
			}
			turns[i].StartedAt = nil
		}
		if err := api.writeConversationTurnQueue(ctx, userID, turns); err != nil {
			lock.Unlock()
			logTurnQueue("cannot recover queue for %s: %v", userID, err)
			continue
		}
		lock.Unlock()
		api.conversationTurnQueueMu.Lock()
		for _, turn := range turns {
			api.registerConversationQueueOwnerLocked(userID, turn.SessionID)
			sessions[turn.SessionID] = true
		}
		api.conversationTurnQueueMu.Unlock()
	}
	// Discover every known caller before choosing a session's oldest head.
	for session := range sessions {
		api.kickConversationTurnQueue(session)
	}
}

// staleWaitingTurnAge: a message that has waited this long without ever starting was stuck behind
// a turn that never finished; the person has almost always resent it since. Running it after a
// restart would repeat work (server B, 2026-09-30: the same demo-data request queued three times).
const staleWaitingTurnAge = 30 * time.Minute

// dropStaleWaitingTurns removes turns that never started and are older than staleWaitingTurnAge.
// Turns that had started (they were running when the server stopped) are kept and run again.
func dropStaleWaitingTurns(turns []queuedConversationTurn, now time.Time) []queuedConversationTurn {
	kept := make([]queuedConversationTurn, 0, len(turns))
	for _, turn := range turns {
		if turn.Request.DeliveryMode != "queue" && turn.DeliveryError == "" && turn.StartedAt == nil && !turn.CreatedAt.IsZero() && now.Sub(turn.CreatedAt) > staleWaitingTurnAge {
			logTurnQueue("dropping a message that waited %s without starting (session %s)", now.Sub(turn.CreatedAt).Round(time.Minute), turn.SessionID)
			continue
		}
		kept = append(kept, turn)
	}
	return kept
}

func logTurnQueue(format string, args ...interface{}) {
	scheduleLogf("[CONVERSATION-TURN-QUEUE] "+format, args...)
}

// PendingQueuedMessage is a keyed message waiting in the durable turn queue.
type PendingQueuedMessage struct {
	MessageID       string    `json:"message_id"`
	Steering        bool      `json:"steering,omitempty"`
	DeliveryError   string    `json:"delivery_error,omitempty"`
	ClientMessageID string    `json:"client_message_id"`
	Content         string    `json:"content"`
	QueuedAt        time.Time `json:"queued_at"`
	QueuePosition   int       `json:"queue_position"`
	// Refreshing: the chat is being updated (new definition, model or settings) before this message can go in, so the person
	// sees that, and Steer now does not apply: steering into the outdated process is what the update replaces. Remove stays, so a long
	// update can still be withdrawn.
	Refreshing  bool   `json:"refreshing,omitempty"`
	QueueReason string `json:"queue_reason"`
	CanSteer    bool   `json:"can_steer"`
	// Started: the queue handed this message to its turn, which has not finished. The chat keeps showing it as being sent
	// until the turn's own user row arrives, instead of it vanishing between the queue and the chat.
	Started bool `json:"started,omitempty"`
}

// markRefreshingQueuedMessages flags a chat's waiting messages when its runtime is waiting to be replaced.
func (api *StreamingAPI) markRefreshingQueuedMessages(sessionID string, messages []PendingQueuedMessage) {
	if api == nil || len(messages) == 0 {
		return
	}
	api.conversationTurnQueueMu.Lock()
	waiting := api.conversationTurnQueueRuntimeChange[sessionID]
	api.conversationTurnQueueMu.Unlock()
	if !waiting {
		return
	}
	for i := range messages {
		messages[i].Refreshing = true
	}
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
	messages := pendingQueuedMessagesFromTurns(turns, userID, sessionID)
	api.projectQueuedMessageDelivery(sessionID, turns, messages)
	return messages
}

func pendingQueuedMessagesFromTurns(turns []queuedConversationTurn, userID, sessionID string) []PendingQueuedMessage {
	var pending []PendingQueuedMessage
	waiting := 0
	for _, turn := range turns {
		if turn.UserID != userID || turn.SessionID != sessionID {
			continue
		}
		clientMessageID := sanitizeClientMessageID(turn.SubmissionID)
		if clientMessageID == "" {
			continue
		}
		message := PendingQueuedMessage{
			MessageID:       turn.ID,
			Steering:        turn.Steering,
			DeliveryError:   turn.DeliveryError,
			ClientMessageID: clientMessageID,
			Content:         turn.Request.Query,
			QueuedAt:        turn.CreatedAt,
			Started:         turn.StartedAt != nil && !turn.Steering,
		}
		if !message.Started {
			waiting++
			message.QueuePosition = waiting
		}
		pending = append(pending, message)
	}
	return pending
}

// autoSteerQueuedConversationTurn hands a person's queued chat message to the running agent as soon as it can take live
// input. Such a message was queued only because the agent was starting or restarting when it was sent (after a deploy
// it relaunches); without this it waited for the whole reply even though "Steer now" would have worked. Only the oldest
// waiting message moves, so order is kept; a message the person chose to queue, or any non-person turn, stays queued.
func (api *StreamingAPI) autoSteerQueuedConversationTurn(sessionID string) {
	api.conversationTurnQueueMu.Lock()
	owners := make([]string, 0, len(api.conversationTurnQueueOwners[sessionID]))
	for owner := range api.conversationTurnQueueOwners[sessionID] {
		owners = append(owners, owner)
	}
	api.conversationTurnQueueMu.Unlock()
	for _, owner := range owners {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		turns, err := api.readConversationTurnQueue(ctx, owner)
		cancel()
		if err != nil {
			continue
		}
		var head *queuedConversationTurn
		for i := range turns {
			turn := &turns[i]
			if turn.UserID != owner || turn.SessionID != sessionID || turn.StartedAt != nil {
				continue
			}
			if head == nil || turn.CreatedAt.Before(head.CreatedAt) {
				head = turn
			}
		}
		if head == nil || !autoSteerablePersonTurn(*head) {
			continue
		}
		if _, canSteer := api.queuedMessageDeliveryState(sessionID, head.Request); !canSteer {
			continue
		}
		ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
		claimed, status, err := api.changeQueuedConversationMessage(ctx, owner, sessionID, head.ID, true)
		cancel()
		if err != nil || status != http.StatusOK {
			continue
		}
		logTurnQueue("handing queued message %s to the running agent in session %s (it was queued while the agent started)", claimed.ID, sessionID)
		go api.deliverQueuedMessageAsSteer(claimed)
	}
}

func autoSteerablePersonTurn(turn queuedConversationTurn) bool {
	req := turn.Request
	trigger := strings.ToLower(strings.TrimSpace(req.TriggeredBy))
	return sanitizeClientMessageID(turn.SubmissionID) != "" && turn.DeliveryError == "" && !turn.Steering &&
		req.DeliveryMode != "queue" && !req.DisableLiveInputDelivery && !req.IsAutoNotification &&
		strings.TrimSpace(req.BotPlatform) == "" && (trigger == "" || trigger == "manual" || trigger == "interactive")
}
