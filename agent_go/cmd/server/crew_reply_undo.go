package server

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/filesnapshots"
	unifiedevents "github.com/manishiitg/mcpagent/events"
)

// PLAT-746-158: "Undo this reply's changes". The preview and the apply use one
// plan: files this reply changed go back to their saved "before" state unless a
// later change touched them. Only the Crew's owner may undo; Run people never.
// No Git operation touches the user's repositories.
func (api *StreamingAPI) serveCrewReplyUndo(w http.ResponseWriter, r *http.Request, root, prefix, op, id string, store filesnapshots.Store, allowRead, allowWrite func(string) bool) {
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if sessionID == "" || id == "" || api.eventStore == nil {
		externalError(w, 400, "invalid_arguments", "Choose a chat and one of its saved replies to undo.")
		return
	}
	apply := op == "snapshot_undo"
	if apply != (r.Method == http.MethodPost) {
		externalError(w, 405, "method_not_allowed", "Preview with GET, then undo with POST.")
		return
	}
	if claims := GetUserFromContext(r.Context()); claims != nil && claims.AccessToken != nil && !claims.AccessToken.Allows("crews:write") {
		externalError(w, 403, "insufficient_scope", "Undoing a reply's file changes needs crews:write.")
		return
	}
	if !crewProjectOwnedByCaller(GetUserIDFromContext(r.Context()), root) {
		externalError(w, 403, "forbidden", "Only the Crew's owner can undo a reply's file changes.")
		return
	}
	if status, message := api.authorizeDurableChatRead(r, sessionID, root); status != 0 {
		externalError(w, status, "not_found", message)
		return
	}
	if !api.crewSessionCanControl(r, sessionID) {
		externalError(w, 403, "forbidden", "Undo changes from a chat you can write in.")
		return
	}
	if apply && api.sessionTurnRunning(sessionID) {
		externalError(w, http.StatusConflict, "turn_running", "Wait until this reply finishes, or press Stop, then Undo.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), crewSnapshotTimeout*2)
	defer cancel()
	history, err := api.chatFileReviewHistory(ctx, store, sessionID, root)
	if err != nil {
		externalError(w, 404, "not_found", "This chat has no saved file history.")
		return
	}
	known := false
	for _, ref := range history.Replies {
		known = known || ref.ID == id
	}
	if !known {
		// A reply of this chat whose copies were cleared or never saved is listed
		// in the chat but not among the saved replies.
		if _, ok := history.replyData[id]; ok {
			externalError(w, 404, "snapshot_unavailable", "This reply's saved files are unavailable, so it cannot be undone.")
			return
		}
		externalError(w, 404, "not_found", "This saved reply does not belong to this chat.")
		return
	}
	plan, err := store.PlanUndo(ctx, id, allowRead, allowWrite)
	if err != nil {
		externalError(w, 404, "snapshot_unavailable", "This reply's saved files are unavailable, so it cannot be undone.")
		return
	}
	if apply && len(plan.Restore) > 0 {
		undoExecution := "undo-" + id + "-" + uuid.NewString()
		started := api.crewSnapshotSequence(sessionID)
		if _, err := store.Begin(ctx, undoExecution); err != nil {
			externalError(w, 500, "undo_failed", "Could not save the current files before undoing. Nothing was changed.")
			return
		}
		plan, err = store.ApplyUndo(ctx, id, allowRead, allowWrite)
		undoRecord, finishErr := store.Finish(ctx, undoExecution)
		if err != nil {
			externalError(w, 500, "undo_failed", "Undo could not finish. Check the files and try again.")
			return
		}
		api.recordCrewReplyUndo(sessionID, root, id, undoExecution, undoRecord, finishErr, started, plan)
	}
	prefixItems := func(items []filesnapshots.UndoItem) {
		for i := range items {
			items[i].Path = prefix + items[i].Path
		}
	}
	prefixItems(plan.Restore)
	prefixItems(plan.Skipped)
	externalJSON(w, map[string]any{"applied": apply, "undo": plan})
}

// The notice is a chat event; the second event is the usual reply_file_changes
// reference so the undo appears as its own reviewable change.
func (api *StreamingAPI) recordCrewReplyUndo(sessionID, root, undone, execution string, record filesnapshots.Record, finishErr error, start int64, plan filesnapshots.UndoPlan) {
	now := time.Now()
	paths := func(items []filesnapshots.UndoItem) []string {
		out := []string{}
		for i, item := range items {
			if i == 100 {
				break
			}
			out = append(out, item.Path)
		}
		return out
	}
	notice := unifiedevents.NewAgentEvent(storeevents.NewGenericEventData("reply_changes_undone", map[string]interface{}{
		"snapshot_id": undone, "undo_snapshot_id": record.ID, "restored": len(plan.Restore), "skipped": len(plan.Skipped),
		"restored_paths": paths(plan.Restore), "skipped_paths": paths(plan.Skipped),
	}))
	notice.SessionID = sessionID
	if err := api.eventStore.AddEventChecked(sessionID, storeevents.Event{ID: "file-undo:" + record.ID, Type: "reply_changes_undone", Timestamp: now, SessionID: sessionID, Data: notice}); err != nil {
		log.Printf("[FILE_UNDO] session=%s snapshot=%s status=notice_not_saved error=%v", sessionID, undone, err)
		return
	}
	end := api.crewSnapshotSequence(sessionID)
	log.Printf("[FILE_UNDO] session=%s snapshot=%s undo_snapshot=%s restored=%d skipped=%d status=applied", sessionID, undone, record.ID, len(plan.Restore), len(plan.Skipped))
	if finishErr != nil || end <= start || (record.Status != "complete" && record.Status != "partial") {
		return
	}
	data := storeevents.NewGenericEventData("reply_file_changes", map[string]interface{}{
		"snapshot_id": record.ID, "workspace_path": root, "execution_id": execution, "start_sequence": start, "end_sequence": end,
	})
	event := unifiedevents.NewAgentEvent(data)
	event.SessionID = sessionID
	api.eventStore.AddEvent(sessionID, storeevents.Event{
		ID: "file-snapshot:" + record.ID, Type: "reply_file_changes", Timestamp: time.Now(), SessionID: sessionID,
		ExecutionID: execution, ExecutionKind: "main_agent", TerminalOwnerID: "main:" + sessionID, Data: event,
	})
}
