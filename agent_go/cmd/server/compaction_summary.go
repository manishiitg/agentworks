package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// A chat's "Chat summarized" row opens what the agent kept when its conversation was compacted (#3236). The
// coding CLI writes it into its own saved session: Claude Code a written summary, Codex the person's earlier
// messages (its own summary is sealed for the model). The server reads it from there; the screen only shows it.

type compactionSummary struct {
	At           time.Time `json:"at"`
	Provider     string    `json:"provider"`
	Kind         string    `json:"kind"` // summary | kept_messages
	Text         string    `json:"text,omitempty"`
	Messages     []string  `json:"messages,omitempty"`
	TokensBefore int       `json:"tokens_before,omitempty"`
	TokensAfter  int       `json:"tokens_after,omitempty"`
}

const (
	compactionSummaryMaxText     = 200_000
	compactionSummaryMaxMessages = 200
	// A row's time and the session's own record of the same compaction differ by the CLI's write; anything
	// further apart is another compaction.
	compactionSummaryMatchWindow = 30 * time.Minute
)

var errCompactionSummaryUnavailable = errors.New("compaction summary unavailable")

// compactionSummariesFromFile lists the compactions recorded in one Claude Code or Codex session file, oldest
// first. Only the few lines that record a compaction are decoded.
func compactionSummariesFromFile(path, provider string) ([]compactionSummary, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader := bufio.NewReaderSize(file, 1<<20)
	var out []compactionSummary
	var before, after int
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			switch {
			case bytes.Contains(line, []byte(`"compact_boundary"`)):
				var entry struct {
					Subtype  string `json:"subtype"`
					Metadata struct {
						PreTokens  int `json:"preTokens"`
						PostTokens int `json:"postTokens"`
					} `json:"compactMetadata"`
				}
				if json.Unmarshal(line, &entry) == nil && entry.Subtype == "compact_boundary" {
					before, after = entry.Metadata.PreTokens, entry.Metadata.PostTokens
				}
			case bytes.Contains(line, []byte(`"isCompactSummary":true`)):
				if summary, ok := claudeCompactionSummary(line); ok {
					summary.Provider, summary.TokensBefore, summary.TokensAfter = provider, before, after
					out = append(out, summary)
				}
				before, after = 0, 0
			case bytes.Contains(line, []byte(`"type":"compacted"`)):
				if summary, ok := codexCompactionSummary(line); ok {
					summary.Provider = provider
					out = append(out, summary)
				}
			}
		}
		if readErr != nil {
			break
		}
	}
	return out, nil
}

func claudeCompactionSummary(line []byte) (compactionSummary, bool) {
	var entry struct {
		Timestamp        string                  `json:"timestamp"`
		IsCompactSummary bool                    `json:"isCompactSummary"`
		Message          claudeTranscriptMessage `json:"message"`
	}
	if json.Unmarshal(line, &entry) != nil || !entry.IsCompactSummary {
		return compactionSummary{}, false
	}
	text := cleanClaudeCompactionSummary(extractClaudeTranscriptText(entry.Message.Content))
	if text == "" {
		return compactionSummary{}, false
	}
	at, _ := time.Parse(time.RFC3339Nano, entry.Timestamp)
	return compactionSummary{At: at, Kind: "summary", Text: boundCompactionText(text)}, true
}

// cleanClaudeCompactionSummary keeps the summary itself: Claude wraps it in a "this session is being
// continued" opening, a tool list and a closing instruction to the agent, none of which a person needs.
func cleanClaudeCompactionSummary(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if idx := strings.Index(text, "\nSummary:\n"); idx >= 0 {
		text = text[idx+len("\nSummary:\n"):]
	} else if strings.HasPrefix(text, "Summary:\n") {
		text = strings.TrimPrefix(text, "Summary:\n")
	}
	for _, tail := range []string{
		"\nIf you need specific details from before compaction",
		"\nContinue the conversation from where it left off",
		"\nPlease continue the conversation from where we left it off",
	} {
		if idx := strings.Index(text, tail); idx >= 0 {
			text = text[:idx]
		}
	}
	return strings.TrimSpace(text)
}

func codexCompactionSummary(line []byte) (compactionSummary, bool) {
	var entry struct {
		Timestamp string `json:"timestamp"`
		Type      string `json:"type"`
		Payload   struct {
			Message            string                       `json:"message"`
			ReplacementHistory []codexRolloutMessagePayload `json:"replacement_history"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &entry) != nil || entry.Type != "compacted" {
		return compactionSummary{}, false
	}
	at, _ := time.Parse(time.RFC3339Nano, entry.Timestamp)
	if text := strings.TrimSpace(entry.Payload.Message); text != "" {
		return compactionSummary{At: at, Kind: "summary", Text: boundCompactionText(text)}, true
	}
	var messages []string
	for _, item := range entry.Payload.ReplacementHistory {
		if item.Type != "message" || item.Role != "user" {
			continue
		}
		text := extractCodexTranscriptText(item.Content)
		if text == "" || isCodexInjectedUserMessage(text) {
			continue
		}
		if idx := strings.LastIndex(text, "[Current User Message]\n"); idx >= 0 {
			text = text[idx+len("[Current User Message]\n"):]
		}
		if text = cleanChatHistoryQuery(text); text != "" && !strings.HasPrefix(text, "<") {
			messages = append(messages, boundedNativeText(text))
		}
	}
	if len(messages) > compactionSummaryMaxMessages {
		messages = messages[len(messages)-compactionSummaryMaxMessages:]
	}
	return compactionSummary{At: at, Kind: "kept_messages", Messages: messages}, true
}

func boundCompactionText(text string) string {
	if len(text) <= compactionSummaryMaxText {
		return text
	}
	return strings.ToValidUTF8(text[:compactionSummaryMaxText], "") + "\n\n…"
}

// pickCompactionSummary returns the compaction nearest to at (the chat row's time); with no time, the newest.
func pickCompactionSummary(summaries []compactionSummary, at time.Time) (compactionSummary, bool) {
	if len(summaries) == 0 {
		return compactionSummary{}, false
	}
	if at.IsZero() {
		return summaries[len(summaries)-1], true
	}
	best, bestGap := -1, compactionSummaryMatchWindow
	for i, summary := range summaries {
		gap := summary.At.Sub(at)
		if gap < 0 {
			gap = -gap
		}
		if gap <= bestGap {
			best, bestGap = i, gap
		}
	}
	if best < 0 {
		return compactionSummary{}, false
	}
	return summaries[best], true
}

// chatCompactionSummary reads the compaction of the owner's own chat nearest to at from the chat's current
// native session. A summary from an earlier conversation (fresh chat, another account) is not available.
func (api *StreamingAPI) chatCompactionSummary(ctx context.Context, owner string, tab chatContextTab, at time.Time) (compactionSummary, error) {
	sessionID := tab.Record.SessionID
	raw, err := ReadChatHistoryConversation(owner, sessionID, tab.Record.WorkspacePath)
	if err != nil || len(raw) == 0 {
		return compactionSummary{}, errCompactionSummaryUnavailable
	}
	var conversation struct {
		Runtime claudeNativeTranscriptRuntime `json:"runtime"`
	}
	if json.Unmarshal(raw, &conversation) != nil {
		return compactionSummary{}, errCompactionSummaryUnavailable
	}
	runtime := conversation.Runtime
	provider := strings.ToLower(strings.TrimSpace(runtime.Provider))
	nativeID := strings.TrimSpace(runtime.ExternalSessionID)
	workingDir, runtimeDir, home := "", "", ""
	if handle := runtime.AgentSessionHandle; handle != nil && handle.Provider != nil {
		if provider == "" {
			provider = strings.ToLower(strings.TrimSpace(handle.Provider.Provider))
		}
		if nativeID == "" {
			nativeID = strings.TrimSpace(handle.Provider.NativeSessionID)
		}
		workingDir, runtimeDir = strings.TrimSpace(handle.Provider.WorkingDir), strings.TrimSpace(handle.Provider.RuntimeDir)
		if handle.ConnectionID != "" {
			if keys, keyErr := api.connectionAPIKeys(ctx, providerAccountScope{Principal: owner, WorkspacePath: tab.Record.WorkspacePath}, provider, handle.ConnectionID); keyErr == nil {
				home = keys.RuntimeEnvironment["HOME"]
			}
		}
	}
	if provider != "claude-code" && provider != "codex-cli" {
		log.Printf("[COMPACTION_SUMMARY] session=%s provider=%s action=unavailable reason=provider_keeps_none", sessionID, provider)
		return compactionSummary{}, errCompactionSummaryUnavailable
	}
	if nativeID == "" {
		return compactionSummary{}, errCompactionSummaryUnavailable
	}
	_, _, path, found, err := nativeTranscriptMessagesForBinding(provider, nativeID, workingDir, runtimeDir, home)
	if err != nil || !found || path == "" {
		log.Printf("[COMPACTION_SUMMARY] session=%s provider=%s action=unavailable reason=no_session_file found=%t err=%v", sessionID, provider, found, err)
		return compactionSummary{}, errCompactionSummaryUnavailable
	}
	summaries, err := compactionSummariesFromFile(path, provider)
	if err != nil {
		log.Printf("[COMPACTION_SUMMARY] session=%s provider=%s action=unavailable reason=read_failed err=%v", sessionID, provider, err)
		return compactionSummary{}, errCompactionSummaryUnavailable
	}
	summary, ok := pickCompactionSummary(summaries, at)
	if !ok {
		log.Printf("[COMPACTION_SUMMARY] session=%s provider=%s action=unavailable reason=no_match compactions=%d at=%s", sessionID, provider, len(summaries), at.Format(time.RFC3339))
		return compactionSummary{}, errCompactionSummaryUnavailable
	}
	log.Printf("[COMPACTION_SUMMARY] session=%s provider=%s action=read kind=%s compactions=%d chars=%d messages=%d", sessionID, provider, summary.Kind, len(summaries), len(summary.Text), len(summary.Messages))
	return summary, nil
}

const compactionSummaryUnavailableMessage = "This summary is no longer saved. It belonged to an earlier conversation of this chat (for example before a fresh start or an account change). You can ask the agent what it remembers from earlier."

// GET /api/chat-context/summary?session_id=…&at=<RFC3339> returns the summary behind a "Chat summarized" row of
// the caller's own chat; without at, the newest.
func (api *StreamingAPI) handleChatCompactionSummary(w http.ResponseWriter, r *http.Request) {
	owner := productWorkspaceUserID(r.Context())
	tab, found := chatContextTabForSession(r.Context(), owner, r.URL.Query().Get("session_id"))
	if !found {
		writeAgentProfileError(w, http.StatusNotFound, "Only this chat's owner can see its summaries.")
		return
	}
	var at time.Time
	if raw := strings.TrimSpace(r.URL.Query().Get("at")); raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			writeAgentProfileError(w, http.StatusBadRequest, "at must be a time like 2026-01-02T15:04:05Z")
			return
		}
		at = parsed
	}
	summary, err := api.chatCompactionSummary(r.Context(), owner, tab, at)
	if err != nil {
		writeAgentProfileError(w, http.StatusNotFound, compactionSummaryUnavailableMessage)
		return
	}
	writeAgentProfileJSON(w, http.StatusOK, map[string]any{"summary": summary})
}
