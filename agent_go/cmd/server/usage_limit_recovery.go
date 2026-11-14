package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	unifiedevents "github.com/manishiitg/mcpagent/events"
)

const usageLimitContinueTrigger = "usage_limit_continue"
const usageLimitContinueMessage = "Continue where you left off."
const usageLimitMaxOverdue = 24 * time.Hour

type usageLimitRecoveryRecord struct {
	EventID  string    `json:"event_id"`
	FailedAt time.Time `json:"failed_at"`
	ResetAt  time.Time `json:"reset_at"`
	Canceled bool      `json:"canceled,omitempty"`
	Consumed bool      `json:"consumed"`
}

func usageLimitRecoveryID(record restartContinueRecord) string {
	return "usage-limit:" + record.QueryID + ":" + record.UsageLimit.EventID
}

func usageLimitResetReason(recovery usageLimitRecoveryRecord, now time.Time) string {
	if !recovery.ResetAt.After(recovery.FailedAt) {
		return "reset_not_after_failure"
	}
	if now.Sub(recovery.ResetAt) > usageLimitMaxOverdue {
		return "reset_overdue"
	}
	return ""
}

// Only structured main-turn failures can arm recovery. Tool/child errors and
// diagnostic prose cannot supply a reset time or a new execution authority.
func (api *StreamingAPI) observeUsageLimitFailure(sessionID string, event storeevents.Event) {
	if event.Type != "unified_completion" || event.Data == nil || event.ID == "" {
		return
	}
	var payload struct {
		Status   string `json:"status"`
		Metadata struct {
			Code    string `json:"error_code"`
			RetryAt string `json:"retry_at"`
			Source  string `json:"source"`
		} `json:"metadata"`
	}
	raw, err := json.Marshal(event.Data.Data)
	if err != nil || json.Unmarshal(raw, &payload) != nil || payload.Metadata.Code != "quota_exhausted" || payload.Status == "completed" {
		return
	}
	reset, err := time.Parse(time.RFC3339, payload.Metadata.RetryAt)
	if err != nil {
		log.Printf("[USAGE_LIMIT_RECOVERY] session=%s event=%s action=skipped reason=reset_missing", sessionID, event.ID)
		return
	}
	userID := api.eventStore.GetSessionOwner(sessionID)
	if userID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	// Queue -> restart is the same lock order used at startup and claim.
	queueLock := productConversationRegistryMutex(conversationTurnQueuePath(userID))
	queueLock.Lock()
	restartLock := productConversationRegistryMutex(restartContinuePath(userID))
	restartLock.Lock()
	document, err := api.readRestartContinueRecords(ctx, userID)
	var turns []queuedConversationTurn
	armed := false
	if err == nil {
		record, known := document.Chats[sessionID]
		reason := ""
		switch {
		case !known || record.QueryID == "":
			reason = "runtime_config_missing"
		case event.Timestamp.IsZero() || event.Timestamp.Before(record.SavedAt):
			reason = "older_failure"
		case event.ParentExecutionID != "" || (event.ExecutionKind != "" && event.ExecutionKind != "main_agent"):
			reason = "not_main_turn"
		case event.ExecutionID != record.QueryID && payload.Metadata.Source != "mcpagent_session":
			reason = "different_turn"
		case api.isSessionMarkedStopped(sessionID):
			reason = "stopped"
		case record.UsageLimit != nil && record.UsageLimit.EventID == event.ID:
			reason = "already_considered"
		}
		// An older native completion cannot arm the newer accepted retained input.
		api.retainedMainTurnsMu.Lock()
		expected, tracked := api.retainedMainTurnCanonicalIDs[api.retainedMainTurnExecutionIDs[sessionID]]
		api.retainedMainTurnsMu.Unlock()
		if reason == "" && tracked && !expected.matchesCompletion(event) {
			reason = "different_native_turn"
		}
		recovery := usageLimitRecoveryRecord{EventID: event.ID, FailedAt: event.Timestamp, ResetAt: reset}
		if reason == "" {
			reason = usageLimitResetReason(recovery, time.Now())
		}
		if reason == "" {
			turns, err = api.readConversationTurnQueue(ctx, userID)
			if err == nil && waitingPersonTurn(turns, sessionID) {
				reason = "person_message_queued"
			}
		}
		if reason == "" && err == nil {
			record.UsageLimit = &recovery
			document.Chats[sessionID] = record
			err = api.writeRestartContinueRecords(ctx, userID, document)
			if err == nil {
				turns = append(turns, usageLimitQueuedTurn(userID, record))
				err = api.writeConversationTurnQueue(ctx, userID, turns)
				armed = err == nil
			}
		} else if reason != "" {
			log.Printf("[USAGE_LIMIT_RECOVERY] session=%s turn=%s event=%s action=skipped reason=%s", sessionID, record.QueryID, event.ID, reason)
		}
	}
	restartLock.Unlock()
	queueLock.Unlock()
	if err != nil {
		log.Printf("[USAGE_LIMIT_RECOVERY] session=%s event=%s action=failed reason=arm_storage_failed", sessionID, event.ID)
		return
	}
	if !armed {
		return
	}
	api.conversationTurnQueueMu.Lock()
	api.registerConversationQueueOwnerLocked(userID, sessionID)
	api.conversationTurnQueueMu.Unlock()
	api.eventStore.AddEvent(sessionID, storeevents.Event{
		ID: "usage-limit-notice:" + event.ID, Type: "usage_limit_notice", SessionID: sessionID, Timestamp: time.Now(),
		Data: unifiedevents.NewAgentEvent(&unifiedevents.GenericEventData{
			BaseEventData: unifiedevents.BaseEventData{Timestamp: time.Now()},
			Data:          map[string]interface{}{"retry_at": reset.UTC().Format(time.RFC3339)},
		}),
	})
	log.Printf("[USAGE_LIMIT_RECOVERY] session=%s event=%s action=armed reset_at=%s", sessionID, event.ID, reset.UTC().Format(time.RFC3339))
	api.kickConversationTurnQueue(sessionID)
}

func usageLimitQueuedTurn(userID string, record restartContinueRecord) queuedConversationTurn {
	req := record.Request
	req.Query, req.Message = usageLimitContinueMessage, ""
	req.TriggeredBy = usageLimitContinueTrigger
	at := record.UsageLimit.ResetAt
	return queuedConversationTurn{ID: usageLimitRecoveryID(record), UserID: userID, SessionID: record.SessionID, Request: req, Principal: record.Principal, RetryAt: &at, CreatedAt: record.UsageLimit.FailedAt}
}

// Startup runs under both document locks. Rebuild a missing queue row from its
// recovery receipt, never from the failed person's prompt.
func (api *StreamingAPI) prepareUsageLimitContinues(document *restartContinueDocument, userID string, turns []queuedConversationTurn) []queuedConversationTurn {
	for sid, record := range document.Chats {
		if record.UsageLimit == nil || record.UsageLimit.Consumed {
			continue
		}
		reason := usageLimitResetReason(*record.UsageLimit, time.Now())
		if api.isSessionMarkedStopped(sid) || waitingPersonTurn(turns, sid) {
			reason = "person_superseded"
		}
		if reason != "" {
			record.UsageLimit.Consumed = true
			document.Chats[sid] = record
			log.Printf("[USAGE_LIMIT_RECOVERY] session=%s turn=%s action=skipped reason=%s", sid, record.QueryID, reason)
			continue
		}
		found := false
		for _, turn := range turns {
			if turn.ID == usageLimitRecoveryID(record) {
				found = true
				break
			}
		}
		if !found {
			turns = append(turns, usageLimitQueuedTurn(userID, record))
		}
		log.Printf("[USAGE_LIMIT_RECOVERY] session=%s turn=%s action=restored reset_at=%s", sid, record.QueryID, record.UsageLimit.ResetAt.UTC().Format(time.RFC3339))
	}
	return turns
}

// Call after admitted person input or Stop. A stopped receipt survives restart;
// the existing queue timer may fire later but will find nothing claimable.
func (api *StreamingAPI) cancelUsageLimitRecovery(sessionID, reason string) {
	if api.eventStore == nil {
		return
	}
	userID := api.eventStore.GetSessionOwner(sessionID)
	if userID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	lock := productConversationRegistryMutex(restartContinuePath(userID))
	lock.Lock()
	defer lock.Unlock()
	document, err := api.readRestartContinueRecords(ctx, userID)
	if err == nil {
		record := document.Chats[sessionID]
		if record.UsageLimit == nil || record.UsageLimit.Canceled {
			return
		}
		record.UsageLimit.Consumed = true
		record.UsageLimit.Canceled = true
		document.Chats[sessionID] = record
		err = api.writeRestartContinueRecords(ctx, userID, document)
	}
	if err != nil {
		log.Printf("[USAGE_LIMIT_RECOVERY] session=%s action=failed reason=cancel_storage_failed", sessionID)
		return
	}
	log.Printf("[USAGE_LIMIT_RECOVERY] session=%s action=skipped reason=%s", sessionID, reason)
}

// Called while the owner's queue is locked, before claim. Both claim paths use
// the same receipt; consuming it BEFORE dispatch prevents a crash from replaying.
func (api *StreamingAPI) consumeUsageLimitContinue(ctx context.Context, userID string, turn queuedConversationTurn) (bool, error) {
	if turn.Request.TriggeredBy != usageLimitContinueTrigger {
		return true, nil
	}
	lock := productConversationRegistryMutex(restartContinuePath(userID))
	lock.Lock()
	defer lock.Unlock()
	document, err := api.readRestartContinueRecords(ctx, userID)
	if err != nil {
		return false, err
	}
	record := document.Chats[turn.SessionID]
	if record.UsageLimit == nil || record.UsageLimit.Consumed || usageLimitRecoveryID(record) != turn.ID || api.isSessionMarkedStopped(turn.SessionID) {
		return false, nil
	}
	if reason := usageLimitResetReason(*record.UsageLimit, time.Now()); reason != "" {
		return false, fmt.Errorf("usage recovery %s", reason)
	}
	record.UsageLimit.Consumed = true
	document.Chats[turn.SessionID] = record
	if err := api.writeRestartContinueRecords(ctx, userID, document); err != nil {
		return false, err
	}
	log.Printf("[USAGE_LIMIT_RECOVERY] session=%s turn=%s action=started reason=reset_due", turn.SessionID, turn.ID)
	return true, nil
}

func (api *StreamingAPI) pruneUsageLimitContinues(ctx context.Context, userID string, turns []queuedConversationTurn) ([]queuedConversationTurn, error) {
	has := false
	for _, turn := range turns {
		if turn.StartedAt == nil && turn.Request.TriggeredBy == usageLimitContinueTrigger {
			has = true
			break
		}
	}
	if !has {
		return turns, nil
	}
	lock := productConversationRegistryMutex(restartContinuePath(userID))
	lock.Lock()
	defer lock.Unlock()
	document, err := api.readRestartContinueRecords(ctx, userID)
	if err != nil {
		return nil, err
	}
	kept := make([]queuedConversationTurn, 0, len(turns))
	changed := false
	for _, turn := range turns {
		if turn.StartedAt == nil && turn.Request.TriggeredBy == usageLimitContinueTrigger {
			record := document.Chats[turn.SessionID]
			reason := ""
			switch {
			case record.UsageLimit == nil || record.UsageLimit.Consumed || usageLimitRecoveryID(record) != turn.ID:
				reason = "superseded"
			case api.isSessionMarkedStopped(turn.SessionID) || waitingPersonTurn(turns, turn.SessionID):
				reason = "person_superseded"
			default:
				reason = usageLimitResetReason(*record.UsageLimit, time.Now())
			}
			if reason != "" {
				if record.UsageLimit != nil && !record.UsageLimit.Consumed {
					record.UsageLimit.Consumed = true
					document.Chats[turn.SessionID] = record
					changed = true
				}
				log.Printf("[USAGE_LIMIT_RECOVERY] session=%s turn=%s action=skipped reason=%s", turn.SessionID, turn.ID, reason)
				continue
			}
		}
		kept = append(kept, turn)
	}
	if changed {
		if err := api.writeRestartContinueRecords(ctx, userID, document); err != nil {
			return nil, err
		}
	}
	return kept, nil
}

// Recheck the consumed receipt immediately before entering ordinary admission:
// Stop or a new person message may have arrived after the queue claim.
func (api *StreamingAPI) usageLimitContinuationCurrent(ctx context.Context, turn queuedConversationTurn) bool {
	if turn.Request.TriggeredBy != usageLimitContinueTrigger {
		return true
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	lock := productConversationRegistryMutex(restartContinuePath(turn.UserID))
	lock.Lock()
	defer lock.Unlock()
	document, err := api.readRestartContinueRecords(ctx, turn.UserID)
	if err != nil {
		log.Printf("[USAGE_LIMIT_RECOVERY] session=%s turn=%s action=failed reason=admission_storage_failed", turn.SessionID, turn.ID)
		return false
	}
	record := document.Chats[turn.SessionID]
	return record.UsageLimit != nil && record.UsageLimit.Consumed && !record.UsageLimit.Canceled && usageLimitRecoveryID(record) == turn.ID && !api.isSessionMarkedStopped(turn.SessionID)
}
