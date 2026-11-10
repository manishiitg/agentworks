package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
)

// Pending attachments never grant file access: only a plan admitted for this
// caller's exact Crew conversation can be referenced by its subsequent send.
type pendingChatAttachment struct {
	Hash string `json:"hash"`
	Name string `json:"name"`
}
type pendingChatUploadKey struct{ User, Project, Conversation, Session, Hash string }
type pendingChatUpload struct {
	Ready   chan struct{}
	Path    string
	Expires time.Time
}

const pendingChatUploadTTL = 15 * time.Minute
const pendingChatAttachmentWait = 60 * time.Second

func (api *StreamingAPI) planPendingChatUpload(key pendingChatUploadKey) {
	api.pendingChatUploadsMu.Lock()
	defer api.pendingChatUploadsMu.Unlock()
	if api.pendingChatUploads == nil {
		api.pendingChatUploads = make(map[pendingChatUploadKey]*pendingChatUpload)
	}
	now := time.Now()
	for k, item := range api.pendingChatUploads {
		if now.After(item.Expires) {
			delete(api.pendingChatUploads, k)
		}
	}
	if item := api.pendingChatUploads[key]; item != nil {
		return
	}
	api.pendingChatUploads[key] = &pendingChatUpload{Ready: make(chan struct{}), Expires: now.Add(pendingChatUploadTTL)}
}
func (api *StreamingAPI) pendingChatUpload(key pendingChatUploadKey) (*pendingChatUpload, error) {
	api.pendingChatUploadsMu.Lock()
	defer api.pendingChatUploadsMu.Unlock()
	item := api.pendingChatUploads[key]
	if item == nil || time.Now().After(item.Expires) {
		return nil, errors.New("The attachment is unavailable. Attach it again, then send your message.")
	}
	return item, nil
}
func (api *StreamingAPI) finishPendingChatUpload(key pendingChatUploadKey, path string) {
	api.pendingChatUploadsMu.Lock()
	defer api.pendingChatUploadsMu.Unlock()
	if item := api.pendingChatUploads[key]; item != nil && item.Path == "" {
		item.Path = path
		close(item.Ready)
	}
}

// Upload endpoints already authorize the Crew and file writer. Bind optional
// send-while-uploading plans to an admitted, saved conversation, not a label.
func (api *StreamingAPI) admitPendingChatUpload(w http.ResponseWriter, r *http.Request, project, conversation, session string) bool {
	if conversation == "" || session == "" || crewResourceProjectID(conversation) != project {
		writeAgentProfileError(w, 400, "Open this Crew chat, then attach the file again.")
		return false
	}
	user, slot, ok := api.agentProfileConversationSlot(w, r, conversation)
	if !ok {
		return false
	}
	if slot.binding.ResourceID != project {
		writeAgentProfileError(w, 403, "Attach files in their own Crew chat.")
		return false
	}
	current, has, previous, err := defaultProductConversationRegistryStore().history(r.Context(), user, slot.profile, slot.binding)
	if err != nil {
		writeAgentProfileError(w, 503, "Chat attachments are unavailable. Try again.")
		return false
	}
	found := has && current.SessionID == session
	for _, record := range previous {
		if record.SessionID == session {
			found = true
		}
	}
	if !found {
		writeAgentProfileError(w, 403, "Open this chat again before attaching files.")
		return false
	}
	return true
}
func pendingAttachmentKey(user, session string, req QueryRequest, hash string) pendingChatUploadKey {
	return pendingChatUploadKey{user, crewResourceProjectID(req.AgentProfileConversationKey), req.AgentProfileConversationKey, session, hash}
}
func (api *StreamingAPI) validatePendingChatAttachments(user, session string, req QueryRequest) error {
	if len(req.PendingAttachments) == 0 {
		return nil
	}
	if req.AgentProfileID != crewProfileID || len(req.PendingAttachments) > 16 || req.AgentProfileConversationKey == "" {
		return errors.New("Attach these files in a Crew chat before sending.")
	}
	for _, file := range req.PendingAttachments {
		if len(file.Hash) != 64 || strings.Trim(file.Hash, "0123456789abcdef") != "" {
			return errors.New("The attachment was not understood. Attach it again.")
		}
		if _, err := api.pendingChatUpload(pendingAttachmentKey(user, session, req, file.Hash)); err != nil {
			return err
		}
	}
	return nil
}
func (api *StreamingAPI) resolvePendingChatAttachments(ctx context.Context, user, session string, req *QueryRequest) error {
	if len(req.PendingAttachments) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, pendingChatAttachmentWait)
	defer cancel()
	log.Printf("[CHAT_ATTACHMENTS] caller=%q project=%q session=%q count=%d outcome=waiting", user, crewResourceProjectID(req.AgentProfileConversationKey), session, len(req.PendingAttachments))
	paths := make([]string, 0, len(req.PendingAttachments))
	for _, file := range req.PendingAttachments {
		item, err := api.pendingChatUpload(pendingAttachmentKey(user, session, *req, file.Hash))
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return errors.New("The file did not finish uploading. Send again.")
		case <-item.Ready:
			// The writer supplied this path after a successful authorized commit.
			paths = append(paths, item.Path)
		}
	}
	req.Query += "\n\n📁 Files in context: " + strings.Join(paths, ", ")
	req.PendingAttachments = nil
	log.Printf("[CHAT_ATTACHMENTS] caller=%q session=%q count=%d outcome=ready", user, session, len(paths))
	return nil
}
func pendingAttachmentSubmissionQuery(req QueryRequest) string {
	if len(req.PendingAttachments) == 0 {
		return req.Query
	}
	data, _ := json.Marshal(req.PendingAttachments)
	return req.Query + "\n[pending attachment identity]" + string(data)
}
