package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	storeEvents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/livefeed"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/redaction"
	agentevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type crewTeamMessagePreview struct {
	Typing        bool   `json:"typing,omitempty"`
	LastMessage   string `json:"last_message,omitempty"`
	LastMessageAt string `json:"last_message_at,omitempty"`
}

// Redact the complete visible text before whitespace normalization or shortening.
func crewTeamMessageLine(text string) string {
	text = redaction.Text(text)
	// Scheduled update receipts have a separate title/kind header. The full
	// chat keeps it; the one-line message preview shows only the body.
	if header, body, ok := strings.Cut(text, "\n\n"); ok && strings.HasSuffix(header, " · update") && strings.TrimSpace(body) != "" {
		text = body
	}
	runes := []rune(strings.Join(strings.Fields(text), " "))
	if len(runes) > 80 {
		return string(runes[:79]) + "…"
	}
	return string(runes)
}

func crewTeamLatestVisibleText(history []llmtypes.MessageContent) string {
	for i := len(history) - 1; i >= 0; i-- {
		message := history[i]
		role := strings.ToLower(string(message.Role))
		if role != "human" && role != "user" && role != "ai" && role != "assistant" {
			continue
		}
		parts := []string{}
		for _, part := range message.Parts {
			if text := chatHistoryPartText(part); text != "" {
				parts = append(parts, text)
			}
		}
		text := cleanChatHistoryQuery(strings.Join(parts, "\n"))
		if strings.TrimSpace(text) == "" || shouldSkipChatHistoryPreviewText(text) || isClaudeLocalCommandMessage(role, text) {
			continue
		}
		return crewTeamMessageLine(text)
	}
	return ""
}

// An index rewrite, title change or tool-only update must not invent message recency.
func crewTeamMessagePreviewUpdate(previous ChatHistorySession, preview []ChatHistoryPreviewMessage, history []llmtypes.MessageContent, now time.Time) crewTeamMessagePreview {
	out := crewTeamMessagePreview{LastMessage: crewTeamLatestVisibleText(history), LastMessageAt: previous.LastMessageAt}
	if !slices.Equal(previous.PreviewMessages, preview) && out.LastMessage != "" {
		out.LastMessageAt = now.UTC().Format(time.RFC3339Nano)
	}
	if out.LastMessage == "" {
		out.LastMessageAt = ""
	}
	return out
}

// Request-scoped reads: one owner registry and one read per distinct history index,
// never a full transcript scan per row. Unknown old timestamps remain unknown.
func crewTeamMessagePreviews(ctx context.Context, owner, project string) map[string]crewTeamMessagePreview {
	return crewTeamMessagePreviewsFor(ctx, owner, project, func(string) bool { return true })
}

// crewTeamMessagePreviewsFor is crewTeamMessagePreviews for the chats include admits, keyed by chat key.
func crewTeamMessagePreviewsFor(ctx context.Context, owner, project string, include func(key string) bool) map[string]crewTeamMessagePreview {
	out := map[string]crewTeamMessagePreview{}
	path := productConversationRegistryPath(owner)
	mu := productConversationRegistryMutex(path)
	mu.Lock()
	doc, err := defaultProductConversationRegistryStore().loadDocument(ctx, path)
	mu.Unlock()
	if err != nil {
		return out
	}
	indexes := map[string]chatHistoryIndex{}
	for _, record := range doc.Entries {
		if record.ProfileID != crewProfileID || crewResourceProjectID(record.ConversationKey) != project || !include(record.ConversationKey) || record.SessionID == "" {
			continue
		}
		conv, ok := workProjectChatHistoryConversationPath(owner, record.WorkspacePath, record.SessionID, time.Now())
		if !ok {
			conv = chatHistoryConversationPath(owner, record.SessionID, time.Now())
		}
		indexPath := chatHistoryIndexWorkspacePath(owner, conv)
		index, loaded := indexes[indexPath]
		if !loaded {
			index = newChatHistoryIndex()
			data, exists, e := readFileFromWorkspace(ctx, indexPath)
			if e == nil && exists {
				_ = json.Unmarshal([]byte(data), &index)
			}
			indexes[indexPath] = index
		}
		for _, entry := range index.Entries {
			session := entry.Session
			if session.SessionID != record.SessionID {
				continue
			}
			preview := crewTeamMessagePreview{LastMessage: crewTeamMessageLine(session.LastMessage), LastMessageAt: session.LastMessageAt}
			if old, found := out[record.ConversationKey]; !found || crewTeamMessageTimeAfter(preview.LastMessageAt, old.LastMessageAt) {
				out[record.ConversationKey] = preview
			}
		}
		// A first turn can stream before its first history-index entry exists.
		if api := runningServerAPI; api != nil && api.eventStore != nil && api.sessionTurnRunning(record.SessionID) {
			last, _ := api.eventStore.GetLatestEventIndex(record.SessionID)
			current := crewTeamStreamingPreview(api.eventStore.GetEvents(record.SessionID, storeEvents.GetEventsOptions{SinceIndex: max(-1, last-200), IncludeStreaming: true}).Events)
			if crewTeamMessageTimeAfter(current.LastMessageAt, out[record.ConversationKey].LastMessageAt) {
				out[record.ConversationKey] = current
			}
		}
	}
	return out
}

// The paired Mac applies the current server-owned rule before sending metadata.
// A failed policy read sends no excerpts, never a last-good permission grant.
func (api *StreamingAPI) desktopCrewTeamMessagePreviews(ctx context.Context, owner, localProject, serverProject string, connection desktopConnection, rows []AgentProfileSideChat) map[string]crewTeamMessagePreview {
	out := map[string]crewTeamMessagePreview{}
	var team crewTeam
	if err := crewTeamRemoteJSON(ctx, owner, connection, http.MethodGet, "/api/crew-teams/"+url.PathEscape(serverProject), nil, &team); err != nil || !team.On || !team.member(connection.User.ID) {
		return out
	}
	var previews map[string]crewTeamMessagePreview
	for _, row := range rows {
		tab := strings.TrimPrefix(row.ConversationKey, localProject+codeChatSideMarker)
		if row.IsMain {
			tab = macMainTab
		}
		allowed := false
		for _, agent := range team.Agents {
			if agent.OwnerID == connection.User.ID && agent.Tab == tab && agent.InTeam && agent.Rule.ChatShare == crewChatShareLive {
				allowed = true
				break
			}
		}
		if !allowed {
			continue
		}
		if previews == nil {
			previews = crewTeamMessagePreviews(ctx, owner, localProject)
		}
		out[row.ConversationKey] = previews[row.ConversationKey]
	}
	return out
}

func crewTeamMessageTimeAfter(a, b string) bool {
	left, e := time.Parse(time.RFC3339Nano, a)
	if e != nil {
		return false
	}
	right, _ := time.Parse(time.RFC3339Nano, b)
	return left.After(right)
}

// The same visible assistant stream used by chat, not tools, reasoning, terminal
// frames or child agents. Call only after the current Team ChatShare gate.
func crewTeamStreamingPreview(rows []storeEvents.Event) crewTeamMessagePreview {
	var out crewTeamMessagePreview
	text := ""
	delta := false
	for _, event := range rows {
		if event.Data == nil || event.Data.HierarchyLevel != 0 {
			continue
		}
		var fields struct {
			Content    string `json:"content"`
			Source     string `json:"source"`
			IsDelta    bool   `json:"is_delta"`
			IsToolCall bool   `json:"is_tool_call"`
		}
		switch event.Type {
		case "user_message", "llm_generation_start":
			text, delta = "", false
			out.Typing = false
		case "streaming_chunk":
			data, err := json.Marshal(event.Data.Data)
			if err != nil || json.Unmarshal(data, &fields) != nil || fields.IsToolCall || (fields.Source != "" && fields.Source != "content" && fields.Source != "transcript") || fields.Content == "" {
				continue
			}
			if fields.IsDelta && delta {
				text += fields.Content
			} else {
				text = fields.Content
			}
			delta = fields.IsDelta
			out = crewTeamMessagePreview{LastMessage: crewTeamMessageLine(text), LastMessageAt: event.Timestamp.UTC().Format(time.RFC3339Nano), Typing: true}
		case "llm_generation_end":
			data, err := json.Marshal(event.Data.Data)
			if err == nil && json.Unmarshal(data, &fields) == nil && fields.Content != "" {
				out = crewTeamMessagePreview{LastMessage: crewTeamMessageLine(fields.Content), LastMessageAt: event.Timestamp.UTC().Format(time.RFC3339Nano)}
			}
			delta = false
			out.Typing = false
		case "tool_call_start", "streaming_end", "agent_end", "unified_completion", "agent_error":
			out.Typing = false
		}
	}
	return out
}

// Shared projection for Monitor and the reachable-agents panel. Even cached Mac
// metadata must pass TODAY's registry rule; answers-only returns a Doing post,
// never a chat excerpt or a typing bit. Unknown rules fail closed.
func crewTeamAgentMessagePreviews(ctx context.Context, team crewTeam, posts crewTeamActivityPosts) func(string, crewTeamAgent) crewTeamMessagePreview {
	cache := map[string]map[string]crewTeamMessagePreview{}
	return func(person string, agent crewTeamAgent) crewTeamMessagePreview {
		registered, ok := team.Agents[agent.ID]
		if !team.On || !ok || !registered.InTeam || registered.OwnerID != person || registered.ChatKey != agent.ChatKey || !team.member(person) {
			return crewTeamMessagePreview{}
		}
		// Terminal native text/typing is never a shared preview, even with Live.
		if registered.Kind == "terminal" {
			return crewTeamTerminalPostPreview(registered, posts)
		}
		if registered.Rule.ChatShare == crewChatShareAgent {
			post, found := posts.Doing[agent.ID]
			if !found || post.OwnerID != person || post.AgentID != agent.ID || post.Line == "" {
				return crewTeamMessagePreview{}
			}
			return crewTeamMessagePreview{LastMessage: crewTeamMessageLine(post.Line), LastMessageAt: post.At.UTC().Format(time.RFC3339Nano)}
		}
		if registered.Rule.ChatShare != crewChatShareLive {
			return crewTeamMessagePreview{}
		}
		preview := agent.lastMessage
		if agent.Place != "mac" {
			if cache[person] == nil {
				cache[person] = crewTeamMessagePreviews(ctx, person, team.ProjectID)
			}
			preview = cache[person][agent.ChatKey]
		}
		preview.LastMessage = crewTeamMessageLine(preview.LastMessage)
		preview.Typing = preview.Typing && agent.Activity == "running"
		if preview.LastMessage == "" {
			return crewTeamMessagePreview{}
		}
		return preview
	}
}

// Existing tab-wide monitor feed only: a notice has IDs, never excerpts. The
// admitted /tab read applies the current audience and ChatShare every time.
func (api *StreamingAPI) noteCrewTeamPreviewEvent(session string, event storeEvents.Event) {
	if event.Data == nil || event.Data.HierarchyLevel != 0 {
		return
	}
	switch event.Type {
	case "streaming_chunk":
		chunk, ok := event.Data.Data.(*agentevents.StreamingChunkEvent)
		if !ok || chunk.IsToolCall || (chunk.Source != "" && chunk.Source != "content" && chunk.Source != "transcript") {
			return
		}
	case "llm_generation_end", "tool_call_start", "streaming_end":
	default:
		return
	}
	api.activeSessionsMux.RLock()
	workspace := ""
	if active := api.activeSessions[session]; active != nil {
		workspace = active.WorkspacePath
	}
	api.activeSessionsMux.RUnlock()
	if project := crewPanelProjectForPath(workspace); project != "" {
		livefeed.Default.PublishProject(livefeed.Sessions, project)
	}
}
