package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/mux"
)

// chatLinkedHelper is a task this chat handed to another of its owner's chat
// tabs (messages ask/queue), shown as a helper in the chat's agents list
// (#3245). The other tab may run any coding app, so this is how a Claude chat
// gets a Codex or Cursor helper.
type chatLinkedHelper struct {
	MessageID string    `json:"message_id"`
	InboxID   string    `json:"inbox_id"`
	Title     string    `json:"title"`
	Task      string    `json:"task"`
	State     string    `json:"state"` // open, in_progress, stalled, blocked, needs_you, done
	Reply     string    `json:"reply,omitempty"`
	ReplyAt   time.Time `json:"reply_at,omitempty"`
	SentAt    time.Time `json:"sent_at"`
	Agent     string    `json:"agent"`
	ChatKey   string    `json:"chat_key"`
	SessionID string    `json:"session_id,omitempty"`
	Provider  string    `json:"provider,omitempty"`
	Model     string    `json:"model,omitempty"`
	Running   bool      `json:"running"`
}

// maxChatLinkedHelpers bounds the list to the newest tasks.
const maxChatLinkedHelpers = 50

// chatLinkedHelpers lists the requests the owner's chat (sessionID) sent to
// the owner's other chat tabs, newest first. Requests to other people's
// agents, the Team and external agents are not helpers and are left out.
func (api *StreamingAPI) chatLinkedHelpers(ctx context.Context, owner, sessionID string) ([]chatLinkedHelper, error) {
	out := []chatLinkedHelper{}
	if owner == "" || sessionID == "" {
		return out, nil
	}
	store, err := readAgentMessageSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	doc, err := defaultProductConversationRegistryStore().loadDocument(ctx, productConversationRegistryPath(owner))
	if err != nil {
		return nil, err
	}
	tabs := map[string]chatContextTab{} // by session and by conversation key
	ownKey := ""
	for key, record := range doc.Entries {
		tab := chatContextTab{ProfileID: record.ProfileID, Record: record, Identity: doc.Agents[key]}
		if record.SessionID != "" {
			tabs["session:"+record.SessionID] = tab
		}
		if record.ConversationKey != "" {
			tabs["key:"+record.ConversationKey] = tab
		}
		if record.SessionID == sessionID {
			ownKey = record.ConversationKey
		}
	}
	asker := func(e agentMessageEndpoint) bool {
		return e.UserID == owner && !e.External && e.Guest == "" && e.ShareID == "" &&
			(e.Session == sessionID || ownKey != "" && e.ChatKey == ownKey)
	}
	now := time.Now().UTC()
	for _, c := range store.Conversations {
		for _, m := range c.Messages {
			if m.MessageType != "request" || m.Kind != "" || m.From < 0 || m.From > 1 || !asker(c.Endpoints[m.From]) {
				continue
			}
			doer := c.Endpoints[1-m.From]
			if doer.UserID != owner || doer.External || doer.ShareID != "" || doer.Kind == agentSelfKind || agentTeamEndpoint(doer) || doer.ChatKey == "" || doer.ChatKey == ownKey {
				continue
			}
			state, _ := agentTaskState(c, m, now)
			if state == "" {
				state = "open" // not delivered yet
			}
			helper := chatLinkedHelper{
				MessageID: m.ID, InboxID: c.ID, Title: firstNonEmptyTrimmed(m.Title, m.Text), Task: m.Text, State: state,
				SentAt: m.SentAt, Agent: doer.Label, ChatKey: doer.ChatKey, SessionID: firstNonEmptyTrimmed(doer.Session, m.TaskProgress.Session),
			}
			if reply, ok := agentTaskLatestReply(c, m); ok {
				helper.Reply, helper.ReplyAt = reply.Text, reply.SentAt
			}
			tab, ok := tabs["key:"+doer.ChatKey]
			if !ok && helper.SessionID != "" {
				tab, ok = tabs["session:"+helper.SessionID]
			}
			if ok {
				if tab.Record.SessionID != "" {
					helper.SessionID = tab.Record.SessionID
				}
				if next, known := api.chatNextModel(owner, tab); known {
					helper.Provider, helper.Model = next.Provider, next.ModelID
				}
			}
			helper.Running = helper.SessionID != "" && api.isSessionBusy(helper.SessionID)
			out = append(out, helper)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].SentAt.After(out[j].SentAt) })
	if len(out) > maxChatLinkedHelpers {
		out = out[:maxChatLinkedHelpers]
	}
	return out, nil
}

// GET /api/sessions/{session_id}/linked-helpers: the tasks this chat handed
// to its owner's other tabs, for the chat's agents list. The caller's own
// chats only.
func (api *StreamingAPI) handleGetSessionLinkedHelpers(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(mux.Vars(r)["session_id"])
	owner := GetUserIDFromContext(r.Context())
	if sessionID == "" {
		http.Error(w, "session is required", http.StatusBadRequest)
		return
	}
	if _, ok := chatContextTabForSession(r.Context(), owner, sessionID); !ok {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}
	helpers, err := api.chatLinkedHelpers(r.Context(), owner, sessionID)
	if err != nil {
		log.Printf("[CHAT_HELPERS] session=%s action=linked_read_failed error=%v", sessionID, err)
		http.Error(w, "failed to read the chat's helpers", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"session_id": sessionID, "helpers": helpers})
}
