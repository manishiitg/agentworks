package server

import (
	"log"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chatterminal"
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
	if c.Provider != "claude-code" {
		fail(422, "claude_only", "Continue in my terminal is available for Claude chats only.")
		return
	}
	roots, bound, e := crewStorageRoots(c.WorkspacePath)
	switch op {
	case "command":
		scheme := "https"
		if IsLocalMode() {
			scheme = "http"
		}
		ref := chatterminal.Reference{Server: scheme + "://" + r.Host, Key: c.ConversationKey, Project: project}
		localFolder := ""
		if IsLocalMode() && e == nil && bound {
			localFolder = roots.Files
		}
		externalJSON(w, map[string]any{"command": "agentworks resume " + ref.String(), "local_folder": localFolder})
		return
	case "fetch":
	default:
		fail(400, "invalid_action", "Use this chat's Continue in my terminal command.")
		return
	}
	if r.Method != http.MethodPost {
		fail(405, "use_post", "Use the chat's menu.")
		return
	}
	// A personal terminal cannot borrow another person's private runtime.
	if e == nil && bound && roots.Owner != user && !IsLocalMode() {
		fail(403, "not_your_runtime", "Continue in terminal from your own project on your computer.")
		return
	}
	if e != nil || !bound {
		fail(409, "folder_unavailable", "Open this chat's project folder first.")
		return
	}
	if api.crewMacTwinForOwner(r, project) {
		fail(409, "use_mac", "Use Continue in my terminal in the Mac app where this chat runs.")
		return
	}
	// The terminal gets its own copy; this chat keeps working here unchanged.
	conversation, err := api.captureCrewHandoffConversation(r.Context(), claims, roots, c, true)
	if err != nil || conversation == nil || conversation.Native == nil {
		fail(422, "native_unavailable", "This chat's saved Claude session is unavailable. Send it a message, wait for the reply, then try again.")
		return
	}
	b := conversation.Native
	// Only the selected root journal, never another session or provider home.
	b.Files = map[string]sessiontransfer.File{"session.jsonl": b.Files["session.jsonl"]}
	if e = b.ValidateFiles(); e != nil {
		fail(422, "native_invalid", "This chat's saved session could not be read. Wait for its current reply to finish, then try again.")
		return
	}
	transfer := chatterminal.Transfer{Session: c.SessionID, Bundle: b, Reference: chatterminal.Reference{Key: c.ConversationKey, Project: project}}
	if IsLocalMode() {
		transfer.LocalFolder = roots.Files
	}
	log.Printf("[CHAT_TERMINAL_RESUME] chat=%s session=%s action=fetched reason=terminal_copy", c.ConversationKey, b.SessionID)
	w.Header().Set("Content-Type", "application/x-tar")
	if e = chatterminal.Write(w, transfer); e != nil {
		log.Printf("[CHAT_TERMINAL_RESUME] chat=%s session=%s action=refused reason=transfer_failed", c.ConversationKey, b.SessionID)
	}
}
