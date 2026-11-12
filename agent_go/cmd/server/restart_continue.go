package server

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

const restartContinueMessage = "[App restarted] The app restarted while you were working on this. Check what you already did, then continue the task."

// The last admitted chat's runtime selectors, not its original prompt or a live
// agent pointer. Restart recovery must re-enter normal query admission.
type restartContinueRecord struct {
	SessionID string                      `json:"session_id"`
	QueryID   string                      `json:"query_id"`
	Request   QueryRequest                `json:"request"`
	Principal queuedConversationPrincipal `json:"principal"`
	SavedAt   time.Time                   `json:"saved_at"`
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
	// Continue in the same admitted caller scope, but never re-present the
	// original incoming message or its Ask/submission correlation as new input.
	principal.DeliveredMessage = nil
	principal.CrewAskInput = nil
	principal.FeedbackOperationID = ""
	document.Chats[sessionID] = restartContinueRecord{SessionID: sessionID, QueryID: queryID, Request: restartContinueRequest(ctx, req), Principal: principal, SavedAt: time.Now().UTC()}
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
