package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/redaction"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type crewTeamMessagePreview struct {
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
		if record.ProfileID != crewProfileID || crewResourceProjectID(record.ConversationKey) != project || isSharedThreadKey(record.ConversationKey) || record.SessionID == "" {
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
