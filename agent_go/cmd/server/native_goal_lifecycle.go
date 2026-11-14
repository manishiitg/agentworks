package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	unifiedevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"
)

type nativeGoalRestoreRecord struct {
	Recovered       bool   `json:"recovered,omitempty"`
	ID              string `json:"id"`
	NativeID        string `json:"native_id"`
	ObjectiveHash   string `json:"objective_hash"`
	Claimed         bool   `json:"claimed"`
	WakeExecutionID string `json:"wake_execution_id,omitempty"`
}
type nativeGoalRestoreContextKey struct{}

// The native RPC is the external boundary; all projection and restore decisions
// below operate on real accepted chat state, not model output or UI hints.
var controlNativeGoal = codexcli.ControlCodexNativeGoal
var runNativeGoalResume = func(api *StreamingAPI, ctx context.Context, req map[string]interface{}, sessionID, owner string) error {
	_, err := api.startSessionInternalWithResult(ctx, req, sessionID, owner, nil)
	return err
}

func nativeGoalUpdateFromEvent(event *storeevents.Event) (*llmtypes.NativeGoalUpdate, bool) {
	if event == nil || event.Type != "native_goal_updated" || event.Data == nil {
		return nil, false
	}
	raw, err := json.Marshal(event.Data.Data)
	if err != nil {
		return nil, false
	}
	for i := 0; i < 3; i++ {
		var envelope struct {
			Update *llmtypes.NativeGoalUpdate `json:"update"`
			Data   json.RawMessage            `json:"data"`
		}
		if json.Unmarshal(raw, &envelope) != nil {
			return nil, false
		}
		if envelope.Update != nil {
			return envelope.Update, true
		}
		if len(envelope.Data) == 0 {
			return nil, false
		}
		raw = envelope.Data
	}
	return nil, false
}
func (api *StreamingAPI) latestNativeGoal(ctx context.Context, sessionID string) (*llmtypes.NativeGoalUpdate, error) {
	if api.eventStore == nil {
		return nil, nil
	}
	event, err := api.eventStore.LatestNativeGoal(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	update, _ := nativeGoalUpdateFromEvent(event)
	return update, nil
}
func (api *StreamingAPI) projectNativeGoal(sessionID string, update llmtypes.NativeGoalUpdate, reason string) error {
	if api.eventStore == nil {
		return fmt.Errorf("goal history unavailable")
	}
	update.Reason = reason
	event := storeevents.Event{ID: fmt.Sprintf("native-goal-control:%d", time.Now().UnixNano()), Type: "native_goal_updated", SessionID: sessionID, Timestamp: time.Now(), ExecutionKind: "main_agent", ExecutionID: "main:" + sessionID, ParentExecutionID: "session:" + sessionID, Data: unifiedevents.NewAgentEvent(&unifiedevents.NativeGoalEvent{Update: update})}
	if err := api.eventStore.AddEventChecked(sessionID, event); err != nil {
		log.Printf("[AGENT_GOAL] session=%s action=failed reason=projection_write_failed", sessionID)
		return err
	}
	status := "cleared"
	if update.Goal != nil {
		status = update.Goal.Status
	}
	log.Printf("[AGENT_GOAL] session=%s native=%s event=%s action=done status=%s reason=%s", sessionID, update.NativeSessionID, event.ID, status, reason)
	return nil
}

// Stop/clear are durable intent: late native frames cannot revive a stopped
// goal. An explicitly admitted set/resume releases this guard before launch.
func (api *StreamingAPI) prepareNativeGoalEvent(sessionID string, event *storeevents.Event) {
	if event.Type != "native_goal_updated" || event.Data == nil {
		return
	}
	update, ok := nativeGoalUpdateFromEvent(event)
	if !ok {
		return
	}
	if update.Reason == "person_stop" || update.Reason == "person_clear" || update.Reason == "intent_set" || update.Reason == "intent_resume" || update.Reason == "intent_message" {
		return
	}
	previous, err := api.latestNativeGoal(context.Background(), sessionID)
	if err != nil || previous == nil {
		return
	}
	if previous.Reason == "background_wake" && previous.Goal != nil && update.Goal != nil && previous.NativeSessionID == update.NativeSessionID && previous.Goal.Objective == update.Goal.Objective && (update.Goal.Status == "paused" || update.Goal.Status == "active") {
		goal := *update.Goal
		goal.Status = "paused"
		update.Goal = &goal
		update.Reason = "background_wake"
		event.Data = unifiedevents.NewAgentEvent(&unifiedevents.NativeGoalEvent{Update: *update})
		return
	}
	if previous.Reason != "person_stop" && previous.Reason != "person_clear" {
		return
	}
	// Keep the confirmed counters when they still describe this exact goal.
	if previous.Goal != nil && update.Goal != nil && previous.NativeSessionID == update.NativeSessionID && previous.Goal.Objective == update.Goal.Objective {
		goal := *update.Goal
		goal.Status = previous.Goal.Status
		previous.Goal = &goal
	}
	log.Printf("[AGENT_GOAL] session=%s native=%s event=%s action=skipped reason=late_after_person_control", sessionID, update.NativeSessionID, event.ID)
	event.Data = unifiedevents.NewAgentEvent(&unifiedevents.NativeGoalEvent{Update: *previous})
}

func (api *StreamingAPI) cancelNativeGoalRestore(sessionID, reason string) {
	if api.eventStore == nil {
		return
	}
	owner := api.eventStore.GetSessionOwner(sessionID)
	if owner == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	lock := productConversationRegistryMutex(restartContinuePath(owner))
	lock.Lock()
	defer lock.Unlock()
	document, err := api.readRestartContinueRecords(ctx, owner)
	if err != nil {
		log.Printf("[AGENT_GOAL] session=%s action=failed reason=restore_cancel_read", sessionID)
		return
	}
	record := document.Chats[sessionID]
	if record.NativeGoalRestore == nil {
		return
	}
	record.NativeGoalRestore = nil
	document.Chats[sessionID] = record
	if err = api.writeRestartContinueRecords(ctx, owner, document); err != nil {
		log.Printf("[AGENT_GOAL] session=%s action=failed reason=restore_cancel_write", sessionID)
		return
	}
	log.Printf("[AGENT_GOAL] session=%s action=skipped reason=%s", sessionID, reason)
}
func (api *StreamingAPI) pauseNativeGoalForStop(sessionID string) {
	api.cancelNativeGoalRestore(sessionID, "person_stop")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	previous, err := api.latestNativeGoal(ctx, sessionID)
	if err != nil || previous == nil || previous.Goal == nil || previous.Provider != "codex-cli" {
		return
	}
	update, err := controlNativeGoal(ctx, sessionID, "pause")
	if err != nil {
		// Stop still cancels the owning turn/process below. Retain pause intent
		// across restart; the next ordinary turn enforces it before any model work.
		update = previous
		if update.Goal.Status == "active" {
			goal := *update.Goal
			goal.Status = "paused"
			update.Goal = &goal
		}
		log.Printf("[AGENT_GOAL] session=%s native=%s action=paused status=%s reason=stop_pending_native_reconnect", sessionID, update.NativeSessionID, update.Goal.Status)
	}
	_ = api.projectNativeGoal(sessionID, *update, "person_stop")
}
func nativeGoalObjectiveHash(objective string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(objective)))
}

// A wake first pauses the current goal, before waiting for the full-turn lane.
// Its one restore receipt contains identities/hash, never another prompt copy.
func (api *StreamingAPI) pauseNativeGoalForWake(sessionID string) *nativeGoalRestoreRecord {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	previous, err := api.latestNativeGoal(ctx, sessionID)
	if err != nil || previous == nil || previous.Provider != "codex-cli" || previous.Goal == nil {
		return nil
	}
	if previous.Goal.Status == "paused" && previous.Reason == "background_wake" {
		receipt := api.pendingNativeGoalRestore(ctx, sessionID)
		if receipt != nil && receipt.NativeID == previous.NativeSessionID && receipt.ObjectiveHash == nativeGoalObjectiveHash(previous.Goal.Objective) {
			return receipt
		}
	}
	if previous.Goal.Status != "active" {
		return nil
	}
	update, err := controlNativeGoal(ctx, sessionID, "wake_pause")
	if err != nil {
		log.Printf("[AGENT_GOAL] session=%s action=failed reason=wake_pause_failed", sessionID)
		return nil
	}
	if err = api.projectNativeGoal(sessionID, *update, "background_wake"); err != nil {
		return nil
	}
	if update.Goal == nil || update.Goal.Status != "paused" || update.NativeSessionID != previous.NativeSessionID || update.Goal.Objective != previous.Goal.Objective {
		return nil
	}
	owner := api.eventStore.GetSessionOwner(sessionID)
	if owner == "" {
		return nil
	}
	lock := productConversationRegistryMutex(restartContinuePath(owner))
	lock.Lock()
	defer lock.Unlock()
	document, err := api.readRestartContinueRecords(ctx, owner)
	if err != nil {
		return nil
	}
	record, known := document.Chats[sessionID]
	currentGoal, currentErr := api.latestNativeGoal(ctx, sessionID)
	if !known || api.isSessionMarkedStopped(sessionID) || currentErr != nil || currentGoal == nil || currentGoal.Reason != "background_wake" {
		log.Printf("[AGENT_GOAL] session=%s action=skipped reason=wake_pause_superseded", sessionID)
		return nil
	}
	receipt := &nativeGoalRestoreRecord{ID: fmt.Sprintf("goal-wake:%d", time.Now().UnixNano()), NativeID: update.NativeSessionID, ObjectiveHash: nativeGoalObjectiveHash(update.Goal.Objective)}
	record.NativeGoalRestore = receipt
	document.Chats[sessionID] = record
	if err = api.writeRestartContinueRecords(ctx, owner, document); err != nil {
		log.Printf("[AGENT_GOAL] session=%s action=failed reason=restore_arm_write", sessionID)
		return nil
	}
	log.Printf("[AGENT_GOAL] session=%s native=%s restore=%s action=paused status=paused reason=background_wake", sessionID, receipt.NativeID, receipt.ID)
	return receipt
}
func (api *StreamingAPI) nativeGoalRestoreCurrent(ctx context.Context, sessionID, receiptID string) bool {
	if api.eventStore == nil || api.isSessionMarkedStopped(sessionID) {
		return false
	}
	owner := api.eventStore.GetSessionOwner(sessionID)
	if owner == "" {
		return false
	}
	lock := productConversationRegistryMutex(restartContinuePath(owner))
	lock.Lock()
	defer lock.Unlock()
	document, err := api.readRestartContinueRecords(ctx, owner)
	if err != nil {
		return false
	}
	receipt := document.Chats[sessionID].NativeGoalRestore
	return receipt != nil && receipt.Claimed && receipt.ID == receiptID
}
func (api *StreamingAPI) restoreNativeGoalAfterWake(sessionID string, receipt *nativeGoalRestoreRecord, turnErr error) {
	if receipt == nil {
		return
	}
	if turnErr != nil {
		api.markNativeGoalWakeFailed(sessionID, receipt)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	owner := api.eventStore.GetSessionOwner(sessionID)
	lock := productConversationRegistryMutex(restartContinuePath(owner))
	lock.Lock()
	document, err := api.readRestartContinueRecords(ctx, owner)
	record := document.Chats[sessionID]
	current := record.NativeGoalRestore
	update, readErr := api.latestNativeGoal(ctx, sessionID)
	allowed := err == nil && readErr == nil && current != nil && current.ID == receipt.ID && current.WakeExecutionID != "" && current.WakeExecutionID == receipt.WakeExecutionID && !current.Claimed && !api.isSessionMarkedStopped(sessionID) && update != nil && update.Goal != nil && update.Goal.Status == "paused" && update.Reason == "background_wake" && update.NativeSessionID == receipt.NativeID && nativeGoalObjectiveHash(update.Goal.Objective) == receipt.ObjectiveHash
	if !allowed {
		lock.Unlock()
		log.Printf("[AGENT_GOAL] session=%s restore=%s action=skipped reason=restore_superseded", sessionID, receipt.ID)
		return
	}
	current.Claimed = true
	record.NativeGoalRestore = current
	document.Chats[sessionID] = record
	err = api.writeRestartContinueRecords(ctx, owner, document)
	lock.Unlock()
	if err != nil {
		log.Printf("[AGENT_GOAL] session=%s restore=%s action=failed reason=restore_claim_write", sessionID, receipt.ID)
		return
	}
	// This starts exactly one native resume through normal project/account
	// admission and the existing lane. Native Codex owns any later turns.
	req := record.Request
	req.Query = "Resume the current native goal after the background wake."
	req.Message = ""
	req.TriggeredBy = "native_goal_restore"
	req.IsAutoNotification = true
	req.DisableLiveInputDelivery = true
	reqMap, err := queryRequestToMap(req)
	if err != nil {
		return
	}
	turn := queuedConversationTurn{ID: receipt.ID, UserID: owner, SessionID: sessionID, Request: req, Principal: record.Principal}
	resumeCtx, err := api.queuedConversationTurnContext(turn, reqMap)
	if err != nil {
		log.Printf("[AGENT_GOAL] session=%s action=failed reason=restore_principal", sessionID)
		return
	}
	resumeCtx = context.WithValue(resumeCtx, nativeGoalRestoreContextKey{}, receipt.ID)
	resumeCtx = llmtypes.WithNativeGoalCommand(resumeCtx, llmtypes.NativeGoalCommand{SessionID: sessionID, Action: "resume", Objective: update.Goal.Objective})
	log.Printf("[AGENT_GOAL] session=%s native=%s restore=%s action=started status=paused reason=wake_restore", sessionID, receipt.NativeID, receipt.ID)
	err = runNativeGoalResume(api, resumeCtx, reqMap, sessionID, owner)
	if err != nil {
		log.Printf("[AGENT_GOAL] session=%s restore=%s action=failed reason=restore_turn_failed", sessionID, receipt.ID)
	} else {
		log.Printf("[AGENT_GOAL] session=%s restore=%s action=done reason=restore_turn_finished", sessionID, receipt.ID)
	}
}

func (api *StreamingAPI) markNativeGoalWakeStarted(sessionID string, receipt *nativeGoalRestoreRecord, executionID string) bool {
	if receipt == nil || api.eventStore == nil {
		return false
	}
	owner := api.eventStore.GetSessionOwner(sessionID)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	lock := productConversationRegistryMutex(restartContinuePath(owner))
	lock.Lock()
	defer lock.Unlock()
	document, err := api.readRestartContinueRecords(ctx, owner)
	if err != nil {
		return false
	}
	record := document.Chats[sessionID]
	current := record.NativeGoalRestore
	if current == nil || current.ID != receipt.ID || current.Claimed || api.isSessionMarkedStopped(sessionID) {
		return false
	}
	current.WakeExecutionID = executionID
	current.Recovered = false
	record.NativeGoalRestore = current
	document.Chats[sessionID] = record
	if err = api.writeRestartContinueRecords(ctx, owner, document); err != nil {
		log.Printf("[AGENT_GOAL] session=%s action=failed reason=wake_identity_write", sessionID)
		return false
	}
	receipt.WakeExecutionID = executionID
	return true
}

// Recovered wake turns also restore through their own accepted completion.
// An older parent/child completion cannot claim a newly armed wake receipt.
func (api *StreamingAPI) observeNativeGoalWakeCompletion(sessionID string, event storeevents.Event) {
	if event.Type != "unified_completion" || event.Data == nil || (event.ParentExecutionID != "" && event.ParentExecutionID != "session:"+sessionID) || (event.ExecutionKind != "" && event.ExecutionKind != "main_agent") {
		return
	}
	owner := api.eventStore.GetSessionOwner(sessionID)
	if owner == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	lock := productConversationRegistryMutex(restartContinuePath(owner))
	lock.Lock()
	document, err := api.readRestartContinueRecords(ctx, owner)
	lock.Unlock()
	if err != nil {
		return
	}
	record := document.Chats[sessionID]
	receipt := record.NativeGoalRestore
	if receipt == nil || !receipt.Recovered || receipt.Claimed || receipt.WakeExecutionID == "" || receipt.WakeExecutionID != record.QueryID || event.Timestamp.Before(record.SavedAt) {
		return
	}
	var payload struct {
		Status   string `json:"status"`
		Metadata struct {
			Source string `json:"source"`
		} `json:"metadata"`
	}
	raw, _ := json.Marshal(event.Data.Data)
	if json.Unmarshal(raw, &payload) != nil {
		return
	}
	owned := event.ExecutionID == record.QueryID
	if !owned && payload.Metadata.Source == "mcpagent_session" {
		api.retainedMainTurnsMu.Lock()
		expected, tracked := api.retainedMainTurnCanonicalIDs[api.retainedMainTurnExecutionIDs[sessionID]]
		api.retainedMainTurnsMu.Unlock()
		owned = tracked && expected.matchesCompletion(event)
	}
	if !owned {
		return
	}
	if payload.Status != "completed" {
		api.markNativeGoalWakeFailed(sessionID, receipt)
		return
	}
	go api.restoreNativeGoalAfterWake(sessionID, receipt, nil)
}

func (api *StreamingAPI) pendingNativeGoalRestore(ctx context.Context, sessionID string) *nativeGoalRestoreRecord {
	if api.eventStore == nil || api.isSessionMarkedStopped(sessionID) {
		return nil
	}
	owner := api.eventStore.GetSessionOwner(sessionID)
	if owner == "" {
		return nil
	}
	lock := productConversationRegistryMutex(restartContinuePath(owner))
	lock.Lock()
	defer lock.Unlock()
	document, err := api.readRestartContinueRecords(ctx, owner)
	if err != nil {
		return nil
	}
	receipt := document.Chats[sessionID].NativeGoalRestore
	if receipt == nil || receipt.Claimed {
		return nil
	}
	return receipt
}
func (api *StreamingAPI) markNativeGoalWakeFailed(sessionID string, receipt *nativeGoalRestoreRecord) {
	if api.eventStore == nil || receipt == nil {
		return
	}
	owner := api.eventStore.GetSessionOwner(sessionID)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	lock := productConversationRegistryMutex(restartContinuePath(owner))
	lock.Lock()
	defer lock.Unlock()
	document, err := api.readRestartContinueRecords(ctx, owner)
	if err != nil {
		return
	}
	record := document.Chats[sessionID]
	current := record.NativeGoalRestore
	if current == nil || current.ID != receipt.ID || current.Claimed || current.WakeExecutionID != receipt.WakeExecutionID {
		return
	}
	current.WakeExecutionID = ""
	current.Recovered = false
	record.NativeGoalRestore = current
	document.Chats[sessionID] = record
	if err = api.writeRestartContinueRecords(ctx, owner, document); err != nil {
		log.Printf("[AGENT_GOAL] session=%s action=failed reason=wake_failure_write", sessionID)
		return
	}
	log.Printf("[AGENT_GOAL] session=%s restore=%s action=failed status=paused reason=wake_failed_receipt_retained", sessionID, receipt.ID)
}
