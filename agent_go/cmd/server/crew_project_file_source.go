package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
)

type projectCaptureSource struct {
	Label     string `json:"label"`
	AgentName string `json:"agent_name,omitempty"`
	CanOpen   bool   `json:"can_open"`
}

// Names belong to the caller's existing agent slots. No other person's
// registry, prompts or credentials are read to decorate file history.
func projectHistoryAgentNames(ctx context.Context, user string) map[string]string {
	path := productConversationRegistryPath(user)
	mu := productConversationRegistryMutex(path)
	mu.Lock()
	defer mu.Unlock()
	doc, err := defaultProductConversationRegistryStore().loadDocument(ctx, path)
	out := map[string]string{}
	if err != nil {
		return out
	}
	add := func(key string, record ProductConversationRecord) {
		if record.SessionID != "" {
			out[record.SessionID] = firstNonEmptyString(doc.Agents[key].Name, record.Title)
		}
	}
	for key, record := range doc.Entries {
		add(key, record)
	}
	for key, records := range doc.Previous {
		for _, record := range records {
			add(key, record)
		}
	}
	return out
}

type projectSourceReference struct {
	chatSnapshotReference
	Label string `json:"source_label"`
	Agent string `json:"source_agent"`
}

func projectSourceRef(event storeevents.Event) projectSourceReference {
	var ref projectSourceReference
	if event.Data != nil {
		raw, _ := json.Marshal(event.Data.Data)
		_ = json.Unmarshal(raw, &ref)
	}
	return ref
}

func (api *StreamingAPI) readableProjectSource(r *http.Request, root string, event storeevents.Event, names map[string]string) *projectCaptureSource {
	if status, _ := api.authorizeDurableChatRead(r, event.SessionID, root); status != 0 {
		return nil
	}
	ref := projectSourceRef(event)
	if ref.Workspace != root || ref.End <= ref.Start {
		return nil
	}
	message, err := api.eventStore.ChatUserMessageBetween(r.Context(), event.SessionID, ref.Start, ref.End)
	label := ref.Label
	if label == "" {
		label = message.Text
	}
	text := []rune(strings.Join(strings.Fields(label), " "))
	if len(text) > 80 {
		text = append(text[:79], '…')
	}
	if len(text) == 0 {
		return nil
	}
	return &projectCaptureSource{Label: string(text), AgentName: firstNonEmptyString(ref.Agent, names[event.SessionID]), CanOpen: err == nil && message.ID != ""}
}

func (api *StreamingAPI) decorateProjectHistory(r *http.Request, root string, history *chatFileReview) {
	if api.eventStore == nil {
		return
	}
	ids := []string{}
	seen := map[string]bool{}
	for _, checkout := range history.Checkouts {
		for _, capture := range checkout.Captures {
			if !seen[capture.SnapshotID] {
				ids = append(ids, capture.SnapshotID)
				seen[capture.SnapshotID] = true
			}
		}
	}
	refs, err := api.eventStore.FileSnapshotReferences(r.Context(), root, ids)
	if err != nil || len(refs) == 0 {
		return
	}
	names := map[string]string{}
	for _, event := range refs {
		if projectSourceRef(event).Agent == "" {
			names = projectHistoryAgentNames(r.Context(), GetUserIDFromContext(r.Context()))
			break
		}
	}
	sources := map[string]*projectCaptureSource{}
	for id, event := range refs {
		sources[id] = api.readableProjectSource(r, root, event, names)
	}
	for i := range history.Checkouts {
		for j := range history.Checkouts[i].Captures {
			capture := &history.Checkouts[i].Captures[j]
			capture.Source = sources[capture.SnapshotID]
		}
	}
}

// Opening a source is a read, never a resume or a change to the active chat.
// Resolve its exact journal boundary and recheck chat access on every page.
func (api *StreamingAPI) serveProjectSnapshotTurn(w http.ResponseWriter, r *http.Request, root, id string) {
	if api.eventStore == nil {
		externalError(w, 404, "source_unavailable", "The original turn is no longer available.")
		return
	}
	refs, err := api.eventStore.FileSnapshotReferences(r.Context(), root, []string{id})
	event, ok := refs[id]
	if err != nil || !ok {
		externalError(w, 404, "source_unavailable", "The original turn is no longer available.")
		return
	}
	names := map[string]string{}
	if projectSourceRef(event).Agent == "" {
		names = projectHistoryAgentNames(r.Context(), GetUserIDFromContext(r.Context()))
	}
	source := api.readableProjectSource(r, root, event, names)
	if source == nil || !source.CanOpen {
		externalError(w, 404, "source_unavailable", "The original turn is unavailable or you cannot open its chat.")
		return
	}
	ref := projectSourceRef(event)
	after := ref.Start
	if value := r.URL.Query().Get("after_sequence"); value != "" {
		after, err = strconv.ParseInt(value, 10, 64)
		if err != nil || after < ref.Start || after >= ref.End {
			externalError(w, 400, "invalid_cursor", "Open this turn again.")
			return
		}
	}
	page, err := api.eventStore.ReadDurableChatPage(event.SessionID, storeevents.DurableEventPageOptions{AfterSequence: after, FromStart: after == 0, Limit: 300})
	if err != nil {
		externalError(w, 503, "source_unavailable", "This turn could not load. Try again.")
		return
	}
	shown := []storeevents.Event{}
	var next int64
	for _, item := range page.Events {
		if item.Sequence > ref.End {
			next = 0
			break
		}
		if item.Sequence > ref.Start {
			shown = append(shown, item)
			next = item.Sequence
		}
	}
	if !page.HasNewer || next >= ref.End {
		next = 0
	}
	externalJSON(w, map[string]any{"source": source, "events": shown, "next_sequence": next})
}
