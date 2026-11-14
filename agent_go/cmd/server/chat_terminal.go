package server

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chatterminal"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
)

// The browser and authenticated CLI use the same owner-only admission. A shared
// file grant, Team membership or the ability to read a chat is not ownership.
func (api *StreamingAPI) handleChatTerminal(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(204)
		return
	}
	key := r.URL.Query().Get("conversation_key")
	r = mux.SetURLVars(r, map[string]string{"id": "work"})
	user, slot, ok := api.agentProfileConversationSlot(w, r, key)
	if !ok {
		return
	}
	source, found, e := crewTabForKey(r.Context(), user, slot.profile.ID, slot.binding.ConversationKey, slot.binding.WorkspacePath)
	if e != nil || !found || source.OwnerID != user {
		externalError(w, 403, "tab_owner", "Continue one of your own chats in your terminal.")
		return
	}
	c := source.Record
	project := crewResourceProjectID(c.ConversationKey)
	claims := GetUserFromContext(r.Context())
	if t := claims.AccessToken; t != nil && (!t.Allows("crews:write") || !t.AllowsCrew(project)) {
		externalError(w, 403, "insufficient_scope", "This connection cannot move this chat.")
		return
	}
	op := r.URL.Query().Get("action")
	fail := func(code int, reason, message string) {
		log.Printf("[CHAT_TERMINAL_RESUME] chat=%s session=%s action=refused reason=%s", c.ConversationKey, c.SessionID, reason)
		externalError(w, code, reason, message)
	}
	if c.Provider != "claude-code" && (op == "fetch" || op == "command") {
		fail(422, "claude_only", "Continue in my terminal is available for Claude chats only.")
		return
	}
	state, e := readChatTerminal(c.SessionID)
	if op == "status" || op == "command" {
		if e != nil {
			state.Out = true
		}
		scheme := "https"
		if IsLocalMode() {
			scheme = "http"
		}
		ref := chatterminal.Reference{Server: scheme + "://" + r.Host, Key: c.ConversationKey, Project: project}
		localFolder := ""
		if IsLocalMode() {
			if roots, bound, err := crewStorageRoots(c.WorkspacePath); err == nil && bound {
				localFolder = roots.Files
			}
		}
		externalJSON(w, map[string]any{"out": state.Out, "command": "agentworks resume " + ref.String(), "notice": chatTerminalNotice, "local_folder": localFolder})
		return
	}
	if r.Method != http.MethodPost {
		fail(405, "use_post", "Use the chat's menu.")
		return
	}
	release, locked := api.tryLockSessionInputLane(c.SessionID, 0)
	if !locked {
		fail(409, "turn_running", "Wait for this chat's current turn to finish.")
		return
	}
	defer release()
	if api.crewAgentTurnRunning(c.SessionID) || op != "here" && api.conversationTurnHasBacklog(user, c.SessionID) {
		fail(409, "turn_running", "Wait for this chat's current turn and queued messages to finish.")
		return
	}
	state, e = readChatTerminal(c.SessionID)
	// Recovery must work even if an interrupted receipt cannot be decoded.
	if op == "here" {
		state = chatTerminalState{Owner: user, Chat: c.ConversationKey, Session: c.SessionID, Receipt: uuid.NewString()}
		if e = saveChatTerminal(state); e != nil {
			fail(500, "save_failed", "Could not return the chat here. Try again.")
			return
		}
		log.Printf("[CHAT_TERMINAL_RESUME] chat=%s session=%s action=resumed reason=continue_here", c.ConversationKey, c.SessionID)
		externalJSON(w, map[string]any{"out": false})
		return
	}
	if e != nil {
		fail(409, "receipt_unreadable", "Choose Continue here instead to recover this chat.")
		return
	}
	if op == "return" && (!state.Out || state.Owner != user) {
		fail(409, "handoff_superseded", "This terminal copy is no longer the live chat. Continue in AgentWorks, or import it as a new chat.")
		return
	}
	roots, bound, e := crewStorageRoots(c.WorkspacePath)
	if e != nil || !bound {
		fail(409, "folder_unavailable", "Open this chat's project folder first.")
		return
	}
	if op == "abort" {
		if !state.Out || state.Receipt != r.URL.Query().Get("receipt") || state.Owner != user {
			fail(409, "handoff_superseded", "This handoff is no longer live.")
			return
		}
		state.Out = false
		if e = saveChatTerminal(state); e != nil {
			fail(500, "save_failed", "Use Continue here instead to recover this chat.")
			return
		}
		log.Printf("[CHAT_TERMINAL_RESUME] chat=%s session=%s action=resumed reason=launch_not_started", c.ConversationKey, c.SessionID)
		externalJSON(w, map[string]any{"out": false})
		return
	}
	if op == "fetch" {
		if state.Out {
			fail(409, "already_in_terminal", chatTerminalNotice)
			return
		}
		if api.crewMacTwinForOwner(r, project) {
			fail(409, "use_mac", "Use Continue in my terminal in the Mac app where this chat runs.")
			return
		}
		conversation, err := api.captureCrewHandoffConversation(r.Context(), claims, roots, c, true)
		if err != nil || conversation == nil || conversation.Native == nil {
			fail(422, "native_unavailable", "This chat's saved Claude session is unavailable. Keep using it here.")
			return
		}
		b := conversation.Native
		// Only the selected root journal, never another session or provider home.
		b.Files = map[string]sessiontransfer.File{"session.jsonl": b.Files["session.jsonl"]}
		if e = b.ValidateFiles(); e != nil {
			fail(422, "native_invalid", "This chat's saved native session could not be verified.")
			return
		}
		state = chatTerminalState{Owner: user, Chat: c.ConversationKey, Session: c.SessionID, Native: b.SessionID, Receipt: uuid.NewString(), Out: true}
		if e = saveChatTerminal(state); e != nil {
			fail(500, "save_failed", "The chat could not be moved. Nothing was started.")
			return
		}
		closeWorkflowPolicyCLI(c.SessionID, c.Provider, "continued in personal terminal")
		transfer := chatterminal.Transfer{Receipt: state.Receipt, Session: c.SessionID, Bundle: b}
		transfer.Reference = chatterminal.Reference{Key: c.ConversationKey, Project: project}
		if IsLocalMode() {
			transfer.LocalFolder = roots.Files
		}
		log.Printf("[CHAT_TERMINAL_RESUME] chat=%s session=%s action=fetched reason=owner_handoff", c.ConversationKey, b.SessionID)
		w.Header().Set("Content-Type", "application/x-tar")
		if e = chatterminal.Write(w, transfer); e != nil {
			log.Printf("[CHAT_TERMINAL_RESUME] chat=%s session=%s action=refused reason=transfer_failed", c.ConversationKey, b.SessionID)
		}
		return
	}
	if op == "return" {
		if !state.Out || state.Owner != user {
			fail(409, "handoff_superseded", "This terminal copy is no longer the live chat. Continue in AgentWorks, or import it as a new chat.")
			return
		}
		dir, err := os.MkdirTemp(roots.Private, "terminal-return-")
		if err != nil {
			fail(500, "stage_unavailable", "Could not stage this chat's return.")
			return
		}
		defer os.RemoveAll(dir)
		t, err := chatterminal.Read(io.LimitReader(r.Body, sessiontransfer.MaxBytes()+128<<10), dir)
		if err != nil || t.Receipt != state.Receipt || t.Session != state.Session || t.Bundle == nil || t.Bundle.SessionID != state.Native {
			fail(409, "handoff_changed", "This terminal session no longer matches the live handoff. Continue here instead if you want to recover the chat.")
			return
		}
		runtime, err := api.importCrewNative(r.Context(), claims, roots, c, c.SessionID, t.Bundle, true)
		if err != nil || runtime == nil {
			fail(409, "import_failed", "Could not restore this native session. Both copies were kept; retry attach or choose Continue here instead.")
			return
		}
		raw, err := ReadChatHistoryConversation(user, c.SessionID, c.WorkspacePath)
		if err != nil {
			fail(500, "history_unavailable", "The saved chat could not be updated. Retry attach.")
			return
		}
		var doc map[string]json.RawMessage
		if json.Unmarshal(raw, &doc) != nil {
			fail(500, "history_invalid", "The saved chat could not be updated. Retry attach.")
			return
		}
		doc["runtime"], _ = json.Marshal(runtime)
		if err = api.saveTerminalReturnedHistory(r, roots, c, user, doc, t.Bundle.Root); err != nil {
			fail(500, "history_save_failed", "Could not save the returned conversation. Retry attach.")
			return
		}
		state.Out = false
		if e = saveChatTerminal(state); e != nil {
			fail(500, "save_failed", "Could not finish returning the chat. Retry attach.")
			return
		}
		log.Printf("[CHAT_TERMINAL_RESUME] chat=%s session=%s action=resumed reason=attached_back", c.ConversationKey, state.Native)
		externalJSON(w, map[string]any{"out": false})
		return
	}
	fail(400, "invalid_action", "Use this chat's Continue in my terminal command.")
}

func (api *StreamingAPI) saveTerminalReturnedHistory(r *http.Request, roots crewroot.Roots, c ProductConversationRecord, user string, doc map[string]json.RawMessage, stage string) error {
	messages, _, e := readNewClaudeTranscriptMessages(filepath.Join(stage, "session.jsonl"), time.Time{})
	if e != nil {
		return e
	}
	history := []llmtypes.MessageContent{}
	for _, m := range filterNativeContinuityMessagesUncapped(messages) {
		role := llmtypes.ChatMessageTypeHuman
		if m.Role == "ai" || m.Role == "assistant" {
			role = llmtypes.ChatMessageTypeAI
		}
		parts := []string{}
		for _, p := range m.Parts {
			if p.Text != "" {
				parts = append(parts, p.Text)
			}
		}
		if len(parts) > 0 {
			history = append(history, llmtypes.TextPart(role, strings.Join(parts, "\n\n")))
		}
	}
	if len(history) == 0 {
		return fmt.Errorf("returned Claude journal has no readable history")
	}
	doc["conversation_history"], _ = json.Marshal(history)
	path, found, e := FindChatHistoryConversationPathForSession(user, c.SessionID, c.WorkspacePath)
	if e != nil {
		return e
	}
	if !found {
		return fmt.Errorf("chat history unavailable")
	}
	data, e := json.Marshal(doc)
	if e != nil {
		return e
	}
	if e = writeRawFileToWorkspace(r.Context(), path, string(data)); e != nil {
		return e
	}
	write := func(p, content string) error { return writeRawFileToWorkspace(r.Context(), p, content) }
	if e = write(chatHistoryResumeSnapshotPath(path), string(boundChatHistoryResumeSnapshot(projectChatHistoryConversationForResumePage(data, chatHistoryResumeSnapshotTurns, 0)))); e != nil {
		return e
	}
	if e = updatePersistedChatHistoryIndexWithWriter(user, c.SessionID, "multi-agent", history, len(history), nil, path, int64(len(data)), time.Now(), nil, write); e != nil {
		return e
	}
	if api.eventStore == nil {
		return fmt.Errorf("conversation journal unavailable")
	}
	api.eventStore.SetSessionOwner(c.SessionID, user)
	if e = api.eventStore.SetSessionPersistenceClass(c.SessionID, storeevents.SessionPersistenceInteractiveChat); e != nil {
		return e
	}
	var conversation builderConversationLog
	if e = json.Unmarshal(data, &conversation); e != nil {
		return e
	}
	var dialogue []storeevents.Event
	for _, row := range builderConversationToRawEventsLimit(conversation, 0) {
		var event storeevents.Event
		if e = json.Unmarshal(row, &event); e != nil {
			return e
		}
		dialogue = append(dialogue, event)
	}
	return api.eventStore.ImportDurableChatEvents(c.SessionID, dialogue)
}
