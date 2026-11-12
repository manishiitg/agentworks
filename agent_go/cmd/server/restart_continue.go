package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"
)

const restartContinueMessage = "[App restarted] The app restarted while you were working on this. Check what you already did, then continue the task."

// The last admitted chat's runtime selectors, not its original prompt or a live
// agent pointer. Restart recovery must re-enter normal query admission.
type restartContinueRecord struct {
	SessionID        string                      `json:"session_id"`
	QueryID          string                      `json:"query_id"`
	Request          QueryRequest                `json:"request"`
	Principal        queuedConversationPrincipal `json:"principal"`
	FollowUps        int                         `json:"follow_ups"`
	RecoveredQueryID string                      `json:"recovered_query_id,omitempty"`
	SavedAt          time.Time                   `json:"saved_at"`
}

type restartContinueDocument struct {
	Chats map[string]restartContinueRecord `json:"chats"`
}

func restartContinuePath(userID string) string {
	return filepath.ToSlash(filepath.Join(chatHistoryRoot(userID), "restart-continue.json"))
}

func restartContinueRequest(ctx context.Context, request QueryRequest) QueryRequest {
	request = sanitizedQueuedConversationRequest(ctx, request)
	// Do not retain the person's prompt, attachments, invocation parameters, or
	// delivery identities for replay. The saved request is only a continuation.
	request.Query, request.Message = restartContinueMessage, ""
	request.PendingAttachments = nil
	request.ExecutionOptions = nil
	request.AgentMessageInbox = ""
	request.IsAutoNotification = true
	request.TriggeredBy = "restart_continue"
	request.DisableLiveInputDelivery = true
	request.DeliveryMode = "queue"
	request.RestartCodingCLI = false
	return request
}

func (api *StreamingAPI) readRestartContinueRecords(ctx context.Context, userID string) (restartContinueDocument, error) {
	read := readFileFromWorkspace
	if api.internalTurnQueueRead != nil {
		read = api.internalTurnQueueRead
	}
	raw, exists, err := read(ctx, restartContinuePath(userID))
	document := restartContinueDocument{Chats: map[string]restartContinueRecord{}}
	if err != nil || !exists {
		return document, err
	}
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		return document, fmt.Errorf("decode restart continuation: %w", err)
	}
	if document.Chats == nil {
		document.Chats = map[string]restartContinueRecord{}
	}
	return document, nil
}

func (api *StreamingAPI) saveRestartContinueRequest(ctx context.Context, userID, sessionID, queryID string, req QueryRequest) error {
	if api == nil || api.eventStore == nil || strings.TrimSpace(userID) == "" || sessionID == "" || req.ParentSessionID != "" {
		return nil
	}
	// Child executions and Python/workflow runs do not become chat continuations.
	if !shouldSerializeInteractiveQueryInput(req) {
		return nil
	}
	storageCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	lock := productConversationRegistryMutex(restartContinuePath(userID))
	lock.Lock()
	defer lock.Unlock()
	document, err := api.readRestartContinueRecords(storageCtx, userID)
	if err != nil {
		return err
	}
	principal := queuedPrincipalFromContext(ctx)
	if GetUserFromContext(ctx) == nil {
		principal = document.Chats[sessionID].Principal
	}
	// Continue in the same admitted caller scope, but never re-present the
	// original incoming message or its Ask/submission correlation as new input.
	principal.DeliveredMessage = nil
	principal.CrewAskInput = nil
	principal.FeedbackOperationID = ""
	document.Chats[sessionID] = restartContinueRecord{SessionID: sessionID, QueryID: queryID, Request: restartContinueRequest(ctx, req), Principal: principal, SavedAt: time.Now().UTC(), FollowUps: backgroundFollowUps.value(sessionID)}
	raw, err := json.Marshal(document)
	if err != nil {
		return err
	}
	write := writeRawFileToWorkspace
	if api.internalTurnQueueWrite != nil {
		write = api.internalTurnQueueWrite
	}
	return write(storageCtx, restartContinuePath(userID), string(raw))
}

// noteAutomaticFollowUpStarted shares the same counter as Codex/Claude and
// checkpoints it with the last admitted chat config for the next restart.
func (api *StreamingAPI) noteAutomaticFollowUpStarted(sessionID string) {
	backgroundFollowUps.started(sessionID)
	api.lastQueryMu.RLock()
	req, ok := api.lastQueryRequests[sessionID]
	api.lastQueryMu.RUnlock()
	if !ok || req.userID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	lock := productConversationRegistryMutex(restartContinuePath(req.userID))
	lock.Lock()
	defer lock.Unlock()
	document, err := api.readRestartContinueRecords(ctx, req.userID)
	if err != nil {
		log.Printf("[RESTART_CONTINUE] session=%s action=skip reason=count_read_failed", sessionID)
		return
	}
	record, ok := document.Chats[sessionID]
	if !ok {
		return
	}
	record.FollowUps = backgroundFollowUps.value(sessionID)
	document.Chats[sessionID] = record
	if err := api.writeRestartContinueRecords(ctx, req.userID, document); err != nil {
		log.Printf("[RESTART_CONTINUE] session=%s action=skip reason=count_write_failed", sessionID)
	}
}

func (c *backgroundFollowUpChain) value(sessionID string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.count[sessionID]
}

func (c *backgroundFollowUpChain) restore(sessionID string, count int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.count[sessionID] = count
}

func (api *StreamingAPI) writeRestartContinueRecords(ctx context.Context, userID string, document restartContinueDocument) error {
	raw, err := json.Marshal(document)
	if err != nil {
		return err
	}
	write := writeRawFileToWorkspace
	if api.internalTurnQueueWrite != nil {
		write = api.internalTurnQueueWrite
	}
	return write(ctx, restartContinuePath(userID), string(raw))
}

func waitingPersonTurn(turns []queuedConversationTurn, sessionID string) bool {
	for _, turn := range turns {
		if turn.SessionID == sessionID && turn.StartedAt == nil && turn.DeliveryError == "" && personQueuedRequest(turn) {
			return true
		}
	}
	return false
}

// Called while the owner's queue document is locked, before any startup kick.
// Never reset StartedAt and replay an original request that may have acted.
func (api *StreamingAPI) prepareRestartContinues(ctx context.Context, userID string, turns []queuedConversationTurn) ([]queuedConversationTurn, error) {
	lock := productConversationRegistryMutex(restartContinuePath(userID))
	lock.Lock()
	defer lock.Unlock()
	document, err := api.readRestartContinueRecords(ctx, userID)
	if err != nil {
		return nil, err
	}
	kept := make([]queuedConversationTurn, 0, len(turns))
	for _, turn := range turns {
		if turn.StartedAt == nil {
			kept = append(kept, turn)
			continue
		}
		// First rollout can recover config from an already-admitted queue row.
		if _, ok := document.Chats[turn.SessionID]; !ok {
			principal := turn.Principal
			principal.DeliveredMessage, principal.CrewAskInput = nil, nil
			principal.FeedbackOperationID = ""
			document.Chats[turn.SessionID] = restartContinueRecord{SessionID: turn.SessionID, QueryID: turn.ID, Request: restartContinueRequest(ctx, turn.Request), Principal: principal}
		}
		log.Printf("[RESTART_CONTINUE] session=%s turn=%s action=skip reason=started_original_not_replayed", turn.SessionID, turn.ID)
	}
	for sessionID, record := range document.Chats {
		backgroundFollowUps.restore(sessionID, record.FollowUps)
		reason := ""
		switch {
		case api.eventStore == nil || !api.eventStore.PersistedTurnOpen(sessionID):
			reason = "turn_not_open"
		case api.isSessionMarkedStopped(sessionID):
			reason = "stopped_by_person"
		case api.sessionTurnRunning(sessionID):
			reason = "runtime_still_running"
		case record.QueryID == "" || record.RecoveredQueryID == record.QueryID:
			reason = "already_considered"
		}
		if reason != "" {
			log.Printf("[RESTART_CONTINUE] session=%s turn=%s action=skip reason=%s", sessionID, record.QueryID, reason)
			continue
		}
		// Record the old turn's interruption, never a successful completion or Stop.
		api.emitInterruptedTurnEnd(sessionID, "app_restart")
		record.RecoveredQueryID = record.QueryID
		if waitingPersonTurn(kept, sessionID) {
			reason = "person_message_queued"
		} else if record.FollowUps >= backgroundFollowUpChainLimit {
			reason = "chain_limit"
		}
		for _, turn := range kept {
			if turn.SessionID == sessionID && turn.Request.TriggeredBy == "restart_continue" {
				reason = "continue_already_queued"
			}
		}
		if reason == "" {
			record.FollowUps++
			backgroundFollowUps.restore(sessionID, record.FollowUps)
			kept = append(kept, queuedConversationTurn{ID: "restart:" + record.QueryID, UserID: userID, SessionID: sessionID, Request: record.Request, Principal: record.Principal, CreatedAt: time.Now().UTC()})
			log.Printf("[RESTART_CONTINUE] session=%s turn=%s action=queued reason=interrupted_by_restart", sessionID, record.QueryID)
		} else {
			log.Printf("[RESTART_CONTINUE] session=%s turn=%s action=skip reason=%s", sessionID, record.QueryID, reason)
		}
		document.Chats[sessionID] = record
	}
	if err := api.writeRestartContinueRecords(ctx, userID, document); err != nil {
		return nil, err
	}
	return kept, nil
}

// Only our own restart settlement may precede an unstarted continuation.
// A later Stop, failure or completed person turn supersedes that note.
func (api *StreamingAPI) restartContinueSuperseded(sessionID string) bool {
	if api.isSessionMarkedStopped(sessionID) {
		return true
	}
	if api.eventStore == nil {
		return false
	}
	rows := api.eventStore.LastEventsRaw(sessionID, 300)
	for i := len(rows) - 1; i >= 0; i-- {
		switch rows[i].Type {
		case "unified_completion":
			if rows[i].Data != nil {
				// Journal replay restores typed payloads as GenericEventData.
				var payload struct {
					Metadata map[string]interface{} `json:"metadata"`
				}
				raw, err := json.Marshal(rows[i].Data.Data)
				if err == nil && json.Unmarshal(raw, &payload) == nil {
					return payload.Metadata["source"] != "app_restart"
				}
			}
			return true
		case "conversation_error", "agent_error", "context_cancelled", "conversation_end":
			return true
		}
	}
	return false
}

func (api *StreamingAPI) pruneRestartContinues(turns []queuedConversationTurn) ([]queuedConversationTurn, bool) {
	kept := make([]queuedConversationTurn, 0, len(turns))
	for _, turn := range turns {
		if turn.StartedAt == nil && turn.Request.TriggeredBy == "restart_continue" && (waitingPersonTurn(turns, turn.SessionID) || api.restartContinueSuperseded(turn.SessionID)) {
			log.Printf("[RESTART_CONTINUE] session=%s turn=%s action=skip reason=superseded_before_start", turn.SessionID, turn.ID)
			continue
		}
		kept = append(kept, turn)
	}
	return kept, len(kept) != len(turns)
}

// Retained live input bypasses handleQuery's full bootstrap. Accepted person
// input still resets the shared chain and refreshes the restart identity.
func (api *StreamingAPI) noteRestartLivePersonInput(sessionID, messageID string) {
	api.lastQueryMu.RLock()
	req, known := api.lastQueryRequests[sessionID]
	api.lastQueryMu.RUnlock()
	if !known || req.userID == "" {
		return
	}
	backgroundFollowUps.personWrote(sessionID)
	if err := api.saveRestartContinueRequest(context.Background(), req.userID, sessionID, messageID, req); err != nil {
		log.Printf("[RESTART_CONTINUE] session=%s turn=%s action=skip reason=live_config_write_failed", sessionID, messageID)
	}
}
