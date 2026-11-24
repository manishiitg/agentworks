package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
)

// Edit and resend (#3134): take one of the person's own chats back to just
// before one of their messages. The chat screen, the saved conversation and
// the coding agent all forget that message and everything after it; the
// screen then sends the edited text as a normal message.
//
// Claude Code and Codex continue from a copy of their own session cut at that
// point (the original native session is never written). Other agents (Cursor,
// Antigravity, ...) have no way back, so the next message starts a fresh
// native session that gets the shortened conversation as its handoff.
// Reverting files is the existing per-reply undo, run by the screen first.

const rewindNotInAgentMessage = "The agent no longer has this message, so the chat can't go back to it. Send it again as a new message instead."

type chatRewindRequest struct {
	ConversationKey string `json:"conversation_key"`
	EventID         string `json:"event_id"`
	DryRun          bool   `json:"dry_run"`
}

type chatRewindPoint struct {
	session  string
	sequence int64
	text     string
	fromEnd  int
	cut      int
	history  []llmtypes.MessageContent
	later    int
}

func (api *StreamingAPI) handleRewindCrewChat(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var input chatRewindRequest
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxAgentProfileRequestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || strings.TrimSpace(input.ConversationKey) == "" || strings.TrimSpace(input.EventID) == "" {
		externalError(w, 400, "invalid_request", "Choose one of your messages to edit.")
		return
	}
	user, slot, ok := api.agentProfileConversationSlot(w, r, input.ConversationKey)
	if !ok {
		return
	}
	if slot.profile.ID != crewProfileID || !slot.canBuild || !canUseCapLayerProfile(r.Context(), slot.profile.ID) {
		externalError(w, 403, "edit_unavailable", "Open one of your own project chats to edit its messages.")
		return
	}
	source, found, err := crewTabForKey(r.Context(), user, slot.profile.ID, slot.binding.ConversationKey, slot.binding.WorkspacePath)
	if err != nil {
		externalFailure(w, err)
		return
	}
	if !found || source.OwnerID != user {
		externalError(w, 403, "tab_owner", "Edit messages in one of your own chats.")
		return
	}
	if api.crewMacTwinForOwner(r, crewResourceProjectID(source.Record.ConversationKey)) {
		externalError(w, 409, "use_mac", "Edit this chat in the Mac app where it runs.")
		return
	}
	session := source.Record.SessionID
	if api.eventStore == nil {
		externalError(w, 503, "history_unavailable", "This chat's saved history is unavailable. Try again.")
		return
	}
	if api.sessionTurnRunning(session) {
		externalError(w, 409, "turn_running", "Edit is available when the agent finishes replying. Press Stop to end the reply now.")
		return
	}
	point, status, code, message := api.findChatRewindPoint(user, source.Record, strings.TrimSpace(input.EventID))
	if status != 0 {
		externalError(w, status, code, message)
		return
	}
	runtime, _, _ := ReadChatHistoryRuntimeForSession(user, session, source.Record.WorkspacePath)
	provider := source.Record.Provider
	if runtime != nil && runtime.Provider != "" {
		provider = runtime.Provider
	}
	native := chatRewindCanCut(runtime)
	if input.DryRun {
		writeAgentProfileJSON(w, 200, map[string]any{"ok": true, "later_messages": point.later, "provider": provider, "native": native, "team_copy": crewChatHasTeamCopy(source.Record)})
		return
	}

	// Hold the chat's input lane for the whole rewind: a message, schedule or
	// Team ask arriving now waits (or is told the chat is busy) instead of
	// starting a turn that is cut off or saves the removed turns back.
	// (sessionTurnRunning was checked above; holding the lane itself counts as a turn.)
	release, free := api.tryLockSessionInputLane(session, 0)
	if !free {
		externalError(w, 409, "turn_running", "Edit is available when the agent finishes replying. Press Stop to end the reply now.")
		return
	}
	defer release()

	started := time.Now()
	// Nothing may keep the old conversation alive: close the running CLI and
	// drop the in-memory agent so the next message rebuilds from disk.
	api.interruptWorkflowPolicySession(session, provider)
	api.removeSessionAgent(session)

	next, mode := api.rewindNativeRuntime(r, source.Record, runtime, point)
	path, found, err := FindChatHistoryConversationPathForSession(user, session, source.Record.WorkspacePath)
	if err != nil || !found {
		externalError(w, 500, "rewind_failed", "Couldn't go back: the saved conversation could not be found. Nothing was changed.")
		return
	}
	if err := api.writeRewoundConversation(user, session, path, point, next); err != nil {
		log.Printf("[CHAT_REWIND] session=%s status=failed phase=save error=%v", session, err)
		externalError(w, 500, "rewind_failed", "Couldn't go back: the saved conversation could not be updated. Nothing was changed.")
		return
	}
	removed, err := api.eventStore.RewindDurableChat(session, point.sequence)
	if err != nil {
		log.Printf("[CHAT_REWIND] session=%s status=partial phase=journal error=%v", session, err)
		externalError(w, 500, "rewind_failed", "The agent went back, but the chat screen could not be updated. Reload the chat.")
		return
	}
	log.Printf("[CHAT_REWIND] session=%s provider=%s mode=%s kept_rows=%d removed_events=%d later_messages=%d elapsed_ms=%d", session, provider, mode, point.cut, removed, point.later, time.Since(started).Milliseconds())
	writeAgentProfileJSON(w, 200, map[string]any{"ok": true, "later_messages": point.later, "provider": provider, "native": mode == "native", "mode": mode})
}

// findChatRewindPoint locates the message in the chat journal and in the saved
// conversation. Messages with the same text are told apart by how many such
// messages come after them.
func (api *StreamingAPI) findChatRewindPoint(user string, record ProductConversationRecord, eventID string) (chatRewindPoint, int, string, string) {
	point := chatRewindPoint{session: record.SessionID}
	sequence, found, err := api.eventStore.DurableChatEventSequence(record.SessionID, eventID)
	if err != nil {
		return point, 503, "history_unavailable", "This chat's saved history is unavailable. Try again."
	}
	if !found {
		return point, 404, "message_unavailable", "This message is no longer in the chat. Reload the chat."
	}
	users, err := api.eventStore.DurableUserMessagesThrough(record.SessionID, sequence)
	if err != nil || len(users) == 0 || users[len(users)-1].ID != eventID {
		return point, 404, "message_unavailable", "Only your own messages can be edited."
	}
	text := storeevents.UserMessageText(users[len(users)-1])
	if text == "" {
		return point, 422, "message_empty", "This message has no text to edit."
	}
	raw, err := ReadChatHistoryConversation(user, record.SessionID, record.WorkspacePath)
	if err != nil {
		return point, 422, "not_in_agent", rewindNotInAgentMessage
	}
	var saved struct {
		History []llmtypes.MessageContent `json:"conversation_history"`
	}
	if err := json.Unmarshal(raw, &saved); err != nil {
		return point, 422, "not_in_agent", rewindNotInAgentMessage
	}
	// Later messages carrying the same text decide which saved row is meant.
	laterSame, err := api.laterUserMessagesContaining(record.SessionID, sequence, text)
	if err != nil {
		return point, 503, "history_unavailable", "This chat's saved history is unavailable. Try again."
	}
	cut, later := rewindHistoryCut(saved.History, text, laterSame+1)
	if cut < 0 {
		return point, 422, "not_in_agent", rewindNotInAgentMessage
	}
	point.sequence, point.text, point.fromEnd, point.cut, point.history, point.later = sequence, text, laterSame+1, cut, saved.History, later
	return point, 0, "", ""
}

func (api *StreamingAPI) laterUserMessagesContaining(session string, sequence int64, text string) (int, error) {
	all, err := api.eventStore.DurableUserMessagesThrough(session, 1<<62)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, event := range all {
		if event.Sequence > sequence && strings.Contains(storeevents.UserMessageText(event), text) {
			count++
		}
	}
	return count, nil
}

// rewindHistoryCut returns the index of the fromEnd-th user row (counting from
// the newest) whose text contains text, and how many user rows come after it.
func rewindHistoryCut(history []llmtypes.MessageContent, text string, fromEnd int) (int, int) {
	matches := []int{}
	for i, message := range history {
		if message.Role == llmtypes.ChatMessageTypeHuman && strings.Contains(historyText(message), text) {
			matches = append(matches, i)
		}
	}
	if fromEnd < 1 || fromEnd > len(matches) {
		return -1, 0
	}
	cut := matches[len(matches)-fromEnd]
	later := 0
	for _, message := range history[cut+1:] {
		if message.Role == llmtypes.ChatMessageTypeHuman {
			later++
		}
	}
	return cut, later
}

func chatRewindCanCut(runtime *ChatHistoryAgentRuntime) bool {
	if runtime == nil || runtime.AgentSessionHandle == nil {
		return false
	}
	handle := runtime.AgentSessionHandle.Provider
	return handle.NativeSessionID != "" && (handle.Provider == "claude-code" || handle.Provider == "codex-cli")
}

// rewindNativeRuntime returns the runtime the chat continues with: a cut copy
// of the agent's own session, or none so the next message starts fresh.
func (api *StreamingAPI) rewindNativeRuntime(r *http.Request, record ProductConversationRecord, runtime *ChatHistoryAgentRuntime, point chatRewindPoint) (*ChatHistoryAgentRuntime, string) {
	if runtime == nil {
		return nil, "fresh"
	}
	fresh := *runtime
	fresh.ExternalSessionID, fresh.AgentSessionHandle, fresh.ResumeSupported = "", nil, false
	// Another agent's saved session may hold the removed turns too.
	fresh.OtherAgentSessions, fresh.SeenLine = nil, 0
	if !chatRewindCanCut(runtime) {
		return &fresh, "fresh"
	}
	roots, found, err := crewStorageRoots(record.WorkspacePath)
	if err != nil || !found {
		log.Printf("[CHAT_REWIND] session=%s native=false reason=%q", record.SessionID, "project runtime unavailable")
		return &fresh, "fresh"
	}
	destination := record
	destination.Provider = runtime.AgentSessionHandle.Provider.Provider
	kept := 0
	cut := func(bundle *sessiontransfer.Bundle) error {
		var err error
		kept, err = sessiontransfer.CutBefore(bundle, point.text, point.fromEnd)
		if err == nil && kept == 0 {
			return errRewindToStart
		}
		return err
	}
	copied, err := api.copyCrewNativeForkCut(r, roots, record, destination, runtime, cut)
	if err != nil || copied == nil {
		reason := "native copy failed"
		if err != nil {
			reason = err.Error()
		}
		log.Printf("[CHAT_REWIND] session=%s native=false reason=%q", record.SessionID, reason)
		return &fresh, "fresh"
	}
	next := fresh
	next.ExternalSessionID, next.AgentSessionHandle, next.ResumeSupported = copied.ExternalSessionID, copied.AgentSessionHandle, true
	log.Printf("[CHAT_REWIND] session=%s native=true provider=%s kept_prompts=%d", record.SessionID, destination.Provider, kept)
	return &next, "native"
}

var errRewindToStart = errors.New("the edited message was the first; starting fresh")

// rewindBackupSuffix names the one copy kept of a conversation before its
// latest edit; the next edit replaces it.
const rewindBackupSuffix = ".before-edit"

// crewChatHasTeamCopy reports whether this chat has been copied for the Team
// (its private source journal exists). That copy only grows, so an edit does
// not remove the earlier messages from it.
func crewChatHasTeamCopy(record ProductConversationRecord) bool {
	roots, found, err := crewStorageRoots(record.WorkspacePath)
	if err != nil || !found || record.ConversationID == "" {
		return false
	}
	_, err = os.Stat(filepath.Join(roots.Private, "offline-source-journals", record.ConversationID))
	return err == nil
}

// writeRewoundConversation replaces the saved conversation with the rows
// before the edited message. This is the one deliberate shortening: the usual
// writer refuses to lose turns.
func (api *StreamingAPI) writeRewoundConversation(user, session, path string, point chatRewindPoint, runtime *ChatHistoryAgentRuntime) error {
	lock := chatConversationMutex(path)
	lock.Lock()
	defer lock.Unlock()
	existing, exists, err := readFileFromWorkspace(context.Background(), path)
	if err != nil || !exists {
		return fmt.Errorf("read conversation: %v", err)
	}
	var record map[string]interface{}
	if err := json.Unmarshal([]byte(existing), &record); err != nil {
		return err
	}
	if owner := stringFromRecord(record, "user_id"); owner != "" && owner != user {
		return fmt.Errorf("conversation belongs to another person")
	}
	// One copy of the conversation as it was before the latest edit, beside it.
	// Agents that start fresh (Cursor, Antigravity) keep the cut turns nowhere else.
	if err := writeRawFileToWorkspace(context.Background(), path+rewindBackupSuffix, existing); err != nil {
		return fmt.Errorf("save the conversation before the edit: %w", err)
	}
	kept := append([]llmtypes.MessageContent(nil), point.history[:point.cut]...)
	for _, key := range []string{"history_log", "history_count", "history_system", "history_tail", "history_total"} {
		delete(record, key)
	}
	record["conversation_history"] = kept
	if runtime != nil {
		record["runtime"] = runtime
	} else {
		delete(record, "runtime")
	}
	delete(record, "terminal_snapshots")
	delete(record, "terminal_snapshot")
	cutoff, _ := api.rewindCutoffTime(session, point.sequence)
	record["ui_events"] = uiEventsBefore(record["ui_events"], point.sequence, cutoff)
	record["updated_at"] = time.Now().Format(time.RFC3339)
	advanceChatConversationRevision(record)
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	if err := writeRawFileToWorkspace(context.Background(), path, string(data)); err != nil {
		return err
	}
	if err := writeChatHistoryResumeSnapshot(context.Background(), path, data); err != nil {
		log.Printf("[CHAT_REWIND] session=%s resume_snapshot_error=%v", session, err)
	}
	mode, _ := record["agent_mode"].(string)
	if err := updatePersistedChatHistoryIndex(user, session, firstNonEmptyTrimmed(mode, "multi-agent"), kept, runtime, path, int64(len(data)), time.Now(), nil); err != nil {
		log.Printf("[CHAT_REWIND] session=%s index_error=%v", session, err)
	}
	// The next message must load the shortened conversation from disk.
	api.conversationMux.Lock()
	delete(api.conversationHistory, session)
	delete(api.resumeHistorySources, session)
	api.conversationMux.Unlock()
	return nil
}

// The edited message's own time: saved screen events from then on go too.
func (api *StreamingAPI) rewindCutoffTime(session string, sequence int64) (time.Time, bool) {
	users, err := api.eventStore.DurableUserMessagesThrough(session, sequence)
	if err != nil || len(users) == 0 || users[len(users)-1].Sequence != sequence {
		return time.Time{}, false
	}
	return users[len(users)-1].Timestamp, !users[len(users)-1].Timestamp.IsZero()
}

// uiEventsBefore keeps saved screen events from before the edited message: by
// journal sequence when an event carries one, else by its time.
func uiEventsBefore(value interface{}, sequence int64, cutoff time.Time) interface{} {
	raw, err := json.Marshal(value)
	if err != nil || value == nil {
		return value
	}
	var events []storeevents.Event
	if json.Unmarshal(raw, &events) != nil {
		return value
	}
	kept := make([]storeevents.Event, 0, len(events))
	for _, event := range events {
		switch {
		case event.Sequence > 0 && sequence > 0:
			if event.Sequence < sequence {
				kept = append(kept, event)
			}
		case cutoff.IsZero() || event.Timestamp.IsZero() || event.Timestamp.Before(cutoff):
			kept = append(kept, event)
		}
	}
	return kept
}
