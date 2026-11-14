package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
)

type chatSearchInput struct {
	Query  string `json:"query"`
	Cursor string `json:"cursor"`
}
type chatSearchHit struct {
	Session  string `json:"session_id"`
	ID       string `json:"id"`
	Sequence int64  `json:"sequence"`
	Title    string `json:"title"`
	Snippet  string `json:"snippet"`
	At       string `json:"at"`
	URL      string `json:"url"`
}

// Search is a read surface, not a new grant. General sign-ins can search their
// accessible history; scoped tools must first select an admitted Crew.
func chatSearchGeneralCaller(r *http.Request) bool {
	claims := GetUserFromContext(r.Context())
	return claims != nil && claims.UserID != "" && !userAccessForClaims(claims).Disabled && claims.Scope == "" && claims.Provider != "bot_route" && claims.AccessToken == nil
}

func (api *StreamingAPI) handleChatMessageSearch(w http.ResponseWriter, r *http.Request) {
	if !chatSearchGeneralCaller(r) {
		externalError(w, 403, "forbidden", "Open your workspace to search its chats.")
		return
	}
	var input chatSearchInput
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil {
		externalError(w, 400, "invalid_arguments", "Enter a short message search.")
		return
	}
	api.searchChatMessages(w, r, input, "")
}

func (api *StreamingAPI) externalSearchChatMessages(w http.ResponseWriter, r *http.Request, args map[string]any) {
	project, _, ok := api.readExternalWorkspace(r.Context(), GetUserFromContext(r.Context()), externalArg(args, "workspace_id"))
	if !ok {
		externalError(w, 404, "not_found", "Choose a Crew you can use before searching its messages.")
		return
	}
	api.searchChatMessages(w, r, chatSearchInput{Query: externalArg(args, "query"), Cursor: externalArg(args, "cursor")}, project.Binding.WorkspacePath)
}

// Registry records supply names and workspace context, NEVER authority. Owner
// and sharing checks below run independently on every page and on every open.
func (api *StreamingAPI) chatSearchRecords(ctx context.Context, owner string) map[string]ProductConversationRecord {
	out := map[string]ProductConversationRecord{}
	doc, err := defaultProductConversationRegistryStore().loadDocument(ctx, productConversationRegistryPath(owner))
	if err != nil {
		return out
	}
	for _, rows := range doc.Previous {
		for _, record := range rows {
			out[record.SessionID] = record
		}
	}
	for _, record := range doc.Entries {
		out[record.SessionID] = record
	}
	return out
}
func (api *StreamingAPI) chatSearchReadable(r *http.Request, session, workspace string) bool {
	if status, _ := api.authorizeDurableChatRead(r, session, workspace); status != 0 {
		return false
	}
	return api.crewSessionChatPrivate(r, session) == nil
}
func chatMessageURL(session, id string, sequence int64) string {
	params := url.Values{"chat": {session}, "id": {id}, "sequence": {fmtChatSequence(sequence)}}
	return "/message?" + params.Encode()
}
func fmtChatSequence(sequence int64) string { raw, _ := json.Marshal(sequence); return string(raw) }
func messageSearchSnippet(content, query string) string {
	lower := strings.ToLower(content)
	at := strings.Index(lower, strings.ToLower(query))
	// Translate the match to rune positions before clipping: Unicode case
	// folding can change byte lengths, but must never split a character.
	start := 0
	if at >= 0 {
		start = len([]rune(lower[:at])) - 80
	}
	if start < 0 {
		start = 0
	}
	text := []rune(content)
	if start > len(text) {
		start = 0
	}
	text = text[start:]
	prefix := ""
	if start > 0 {
		prefix = "…"
	}
	if len(text) > 300 {
		return prefix + strings.TrimSpace(string(text[:300])) + "…"
	}
	return prefix + strings.TrimSpace(string(text))
}
func (api *StreamingAPI) searchChatMessages(w http.ResponseWriter, r *http.Request, input chatSearchInput, workspaceFilter string) {
	started := time.Now()
	result := []chatSearchHit{}
	defer func() {
		log.Printf("[CHAT_SEARCH] user=%s results=%d elapsed_ms=%d", GetUserIDFromContext(r.Context()), len(result), time.Since(started).Milliseconds())
	}()
	w.Header().Set("Cache-Control", "private, no-store")
	query := strings.TrimSpace(input.Query)
	if utf8.RuneCountInString(query) < 2 || utf8.RuneCountInString(query) > 200 {
		externalError(w, 400, "invalid_arguments", "Use 2 to 200 characters to search messages.")
		return
	}
	// Only a numeric store position is returned, not a private chat identity.
	// A caller can choose another position; every result still needs admission.
	var cursor *events.ChatMessageCursor
	if input.Cursor != "" {
		position, err := strconv.ParseInt(input.Cursor, 10, 64)
		if err != nil || position < 1 {
			externalError(w, 400, "invalid_cursor", "Start the search again.")
			return
		}
		boundary := events.ChatMessageCursor(position)
		cursor = &boundary
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	page, err := api.eventStore.ScanChatMessages(ctx, cursor)
	if err != nil {
		externalError(w, 503, "unavailable", "Message search is unavailable. Try again from your workspace.")
		return
	}
	ownerRecords := map[string]map[string]ProductConversationRecord{}
	type admission struct {
		allowed bool
		record  ProductConversationRecord
	}
	admissions := map[string]admission{}
	for _, message := range page.Messages {
		if !strings.Contains(strings.ToLower(message.Text), strings.ToLower(query)) {
			continue
		}
		entry, found := admissions[message.SessionID]
		if !found {
			owner, ownerErr := api.eventStore.DurableChatOwner(message.SessionID)
			if ownerErr != nil || owner == "" {
				continue
			}
			records, loaded := ownerRecords[owner]
			if !loaded {
				records = api.chatSearchRecords(ctx, owner)
				ownerRecords[owner] = records
			}
			record := records[message.SessionID]
			entry = admission{record: record, allowed: (workspaceFilter == "" || record.WorkspacePath == workspaceFilter) && api.chatSearchReadable(r, message.SessionID, record.WorkspacePath)}
			admissions[message.SessionID] = entry
		}
		if !entry.allowed {
			continue
		}
		title := strings.TrimSpace(entry.record.Title)
		if title == "" {
			title = "Saved chat · " + message.SessionID
		}
		result = append(result, chatSearchHit{Session: message.SessionID, ID: message.ID, Sequence: message.Sequence, Title: title, Snippet: messageSearchSnippet(message.Text, query), At: message.At, URL: chatMessageURL(message.SessionID, message.ID, message.Sequence)})
	}
	next := ""
	if page.Next != nil {
		next = strconv.FormatInt(int64(*page.Next), 10)
	}
	externalJSON(w, map[string]any{"results": result, "next_cursor": next})
}

func (api *StreamingAPI) handleChatMessageOpen(w http.ResponseWriter, r *http.Request) {
	if !chatSearchGeneralCaller(r) {
		externalError(w, 403, "forbidden", "Open your workspace to read this chat.")
		return
	}
	var input struct {
		Session  string `json:"chat"`
		ID       string `json:"id"`
		Sequence int64  `json:"sequence"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input) != nil || input.Sequence < 1 || len(input.Session) > 512 || len(input.ID) > 512 {
		externalError(w, 400, "invalid_arguments", "Choose a message from search.")
		return
	}
	owner, err := api.eventStore.DurableChatOwner(input.Session)
	if err != nil || owner == "" {
		externalError(w, 404, "not_found", "This chat is unavailable. Return to your workspace or ask its owner for access.")
		return
	}
	record := api.chatSearchRecords(r.Context(), owner)[input.Session]
	if !api.chatSearchReadable(r, input.Session, record.WorkspacePath) {
		externalError(w, 404, "not_found", "This chat is unavailable. Return to your workspace or ask its owner for access.")
		return
	}
	messages, err := api.eventStore.ChatMessageWindow(r.Context(), input.Session, input.Sequence)
	if err != nil {
		externalError(w, 503, "unavailable", "Could not load this message. Try again.")
		return
	}
	found := false
	for _, m := range messages {
		if m.ID == input.ID && m.Sequence == input.Sequence {
			found = true
		}
	}
	if !found {
		externalError(w, 404, "not_found", "This saved message is no longer available.")
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	externalJSON(w, map[string]any{"title": firstNonEmptyTrimmed(record.Title, "Saved chat"), "messages": messages, "selected_id": input.ID})
}
