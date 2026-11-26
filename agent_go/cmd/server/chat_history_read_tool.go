package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// chat_history_read lets an agent read its own chat's saved history by line,
// or search it, without opening the stored file (#3140, after T3 Code's
// t3_thread_read). Handoffs list the lines they leave out; this is how the
// agent reads one. The tool is bound to the chat it runs in, so it reads
// nothing the chat could not already see.
const (
	chatHistoryReadTool      = "chat_history_read"
	chatHistoryReadPageBytes = 16 * 1024
	chatHistoryReadMatches   = 20
	chatHistoryReadSnippet   = 240
)

// chatHistoryReadInstruction is the one rule the handoff notices carry.
const chatHistoryReadInstruction = "Read any line of the saved history with the chat_history_read tool (line=N), or search it for words (query=...)."

// continuityArchives remembers, per chat, the saved conversation its last
// handoff came from, so the tool reads the same history the lines point at.
var continuityArchives sync.Map // sessionID -> conversation path

func rememberContinuityArchive(sessionID, conversationPath string) {
	if sessionID = strings.TrimSpace(sessionID); sessionID != "" && strings.TrimSpace(conversationPath) != "" {
		continuityArchives.Store(sessionID, strings.TrimSpace(conversationPath))
	}
}

func chatHistoryReadSchema() map[string]interface{} {
	return map[string]interface{}{"type": "object", "additionalProperties": false, "properties": map[string]interface{}{
		"line":   map[string]interface{}{"type": "integer", "minimum": 1, "description": "Line to read, as named in the handoff (\"line 12\", \"Left out ... by line: 1-40\")."},
		"offset": map[string]interface{}{"type": "integer", "minimum": 0, "description": "With line: where to continue a long line, from next_offset of the previous read."},
		"query":  map[string]interface{}{"type": "string", "description": "Words to find in the saved history (2 to 200 characters). Returns the newest matching lines first."},
	}}
}

func (api *StreamingAPI) registerChatHistoryReadTool(registrar definitionToolRegistrar, userID, sessionID string) error {
	return registrar.RegisterCustomTool(chatHistoryReadTool,
		"Read this chat's saved history: one line by its number (line), or search it for words (query). Use it when the user refers to something earlier that is not in your context, before asking them. Lines are numbered as in the handoff you were given.",
		chatHistoryReadSchema(), func(ctx context.Context, args map[string]interface{}) (string, error) {
			history, err := api.chatHistoryForRead(userID, sessionID)
			if err == nil {
				var out any
				if out, err = readChatHistory(history, args); err == nil {
					log.Printf("[CHAT_HISTORY_READ] session=%s line=%d query_len=%d messages=%d outcome=ok", sessionID, chatHistoryIntArg(args, "line"), len(chatHistoryQueryArg(args)), len(history))
					data, _ := json.Marshal(out)
					return string(data), nil
				}
			}
			log.Printf("[CHAT_HISTORY_READ] session=%s line=%d query_len=%d outcome=error err=%v", sessionID, chatHistoryIntArg(args, "line"), len(chatHistoryQueryArg(args)), err)
			return "", err
		}, "chat_history")
}

// chatHistoryForRead loads the full saved history of the tool's own chat.
func (api *StreamingAPI) chatHistoryForRead(userID, sessionID string) ([]llmtypes.MessageContent, error) {
	var data []byte
	exists := false
	var err error
	if remembered, ok := continuityArchives.Load(sessionID); ok {
		if data, exists, err = readChatHistoryConversationDataFromPath(remembered.(string)); err != nil || !exists {
			continuityArchives.Delete(sessionID) // deleted or moved since the handoff
			data, exists, err = nil, false, nil
		}
	}
	if !exists {
		if path, ok, findErr := api.liveConversationPath(userID, sessionID, ""); findErr == nil && ok && strings.TrimSpace(path) != "" {
			data, exists, err = readChatHistoryConversationDataFromPath(path)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("the saved history could not be read: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("this chat has no saved history yet")
	}
	var record struct {
		History []llmtypes.MessageContent `json:"conversation_history"`
	}
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("the saved history could not be read: %w", err)
	}
	return record.History, nil
}

type chatHistoryLine struct {
	Line       int    `json:"line"`
	Role       string `json:"role"`
	Text       string `json:"text"`
	NextOffset int    `json:"next_offset,omitempty"`
}

type chatHistorySearchResult struct {
	Lines   int               `json:"lines"`
	Matches []chatHistoryLine `json:"matches"`
	More    bool              `json:"more,omitempty"`
}

func readChatHistory(history []llmtypes.MessageContent, args map[string]interface{}) (any, error) {
	return readChatHistoryLines(chatHistoryLines(history), args)
}

// readChatHistoryLines answers a line or query read over already numbered lines (a chat's own history, or a
// teammate's synced chat read by the messages tool's history action).
func readChatHistoryLines(lines []chatHistoryLine, args map[string]interface{}) (any, error) {
	if query := chatHistoryQueryArg(args); query != "" {
		if len(query) < 2 || len(query) > 200 {
			return nil, fmt.Errorf("query must be 2 to 200 characters")
		}
		return searchChatHistory(lines, query), nil
	}
	line := chatHistoryIntArg(args, "line")
	if line < 1 || line > len(lines) {
		return nil, fmt.Errorf("line must be between 1 and %d", len(lines))
	}
	out := lines[line-1]
	offset := chatHistoryIntArg(args, "offset")
	if offset < 0 || offset > len(out.Text) {
		offset = 0
	}
	text := out.Text[offset:]
	if len(text) > chatHistoryReadPageBytes {
		cut := chatHistoryReadPageBytes
		for cut > 0 && !utf8RuneStart(text[cut]) {
			cut--
		}
		text, out.NextOffset = text[:cut], offset+cut
	}
	out.Text = text
	return out, nil
}

// chatHistoryLines renders each saved (non-system) message in full: its text,
// each tool call with its arguments and the result it got.
func chatHistoryLines(history []llmtypes.MessageContent) []chatHistoryLine {
	results := map[string]llmtypes.ToolCallResponse{}
	calls := map[string]bool{}
	for _, message := range history {
		for _, part := range message.Parts {
			if r, ok := part.(llmtypes.ToolCallResponse); ok && r.ToolCallID != "" {
				results[r.ToolCallID] = r
			}
			if c, ok := part.(llmtypes.ToolCall); ok && c.ID != "" {
				calls[c.ID] = true
			}
		}
	}
	var lines []chatHistoryLine
	for _, message := range history {
		role := strings.ToLower(string(message.Role))
		if role == "system" {
			continue
		}
		switch role {
		case "human":
			role = "user"
		case "ai":
			role = "assistant"
		}
		var parts []string
		for _, part := range message.Parts {
			switch p := part.(type) {
			case llmtypes.TextContent:
				if text := strings.TrimSpace(p.Text); text != "" {
					parts = append(parts, text)
				}
			case llmtypes.ToolCall:
				if p.FunctionCall == nil {
					continue
				}
				call := "Called " + p.FunctionCall.Name + " " + strings.TrimSpace(p.FunctionCall.Arguments)
				if r, ok := results[p.ID]; ok {
					label := "Result"
					if r.IsError {
						label = "Error"
					}
					call += "\n" + label + ": " + strings.TrimSpace(r.Content)
				}
				parts = append(parts, call)
			case llmtypes.ToolCallResponse:
				// Shown with its call; a result whose call is missing still shows.
				if !calls[p.ToolCallID] {
					parts = append(parts, "Result: "+strings.TrimSpace(p.Content))
				}
			}
		}
		lines = append(lines, chatHistoryLine{Line: len(lines) + 1, Role: role, Text: strings.Join(parts, "\n\n")})
	}
	return lines
}

func searchChatHistory(lines []chatHistoryLine, query string) chatHistorySearchResult {
	out := chatHistorySearchResult{Lines: len(lines), Matches: []chatHistoryLine{}}
	needle := strings.ToLower(query)
	for i := len(lines) - 1; i >= 0; i-- {
		at := strings.Index(strings.ToLower(lines[i].Text), needle)
		if at < 0 {
			continue
		}
		if len(out.Matches) == chatHistoryReadMatches {
			out.More = true
			break
		}
		start := at - chatHistoryReadSnippet/2
		if start < 0 {
			start = 0
		}
		for start > 0 && !utf8RuneStart(lines[i].Text[start]) {
			start--
		}
		end := start + chatHistoryReadSnippet
		if end > len(lines[i].Text) {
			end = len(lines[i].Text)
		}
		for end < len(lines[i].Text) && !utf8RuneStart(lines[i].Text[end]) {
			end++
		}
		snippet := lines[i].Text[start:end]
		if start > 0 {
			snippet = "…" + snippet
		}
		if end < len(lines[i].Text) {
			snippet += "…"
		}
		out.Matches = append(out.Matches, chatHistoryLine{Line: lines[i].Line, Role: lines[i].Role, Text: snippet})
	}
	return out
}

func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }

func chatHistoryIntArg(args map[string]interface{}, name string) int {
	switch v := args[name].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	}
	return 0
}

func chatHistoryQueryArg(args map[string]interface{}) string {
	query, _ := args["query"].(string)
	return strings.TrimSpace(query)
}
