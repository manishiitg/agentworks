package picli

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type piTranscriptSummary struct {
	Path                       string
	Messages                   []llmtypes.MessageContent
	ProgressMessages           []llmtypes.MessageContent
	StructuredProgressMessages []llmtypes.MessageContent
	Provider                   string
	Model                      string
	API                        string
	ResponseID                 string
	StopReason                 string
	InputTokens                int
	OutputTokens               int
	TotalTokens                int
	CacheReadTokens            int
	CacheWriteTokens           int
	InputCostUSD               float64
	OutputCostUSD              float64
	TotalCostUSD               float64
	CacheReadCostUSD           float64
	CacheWriteCostUSD          float64
	AssistantUsageCount        int
}

func (s *piTranscriptSummary) hasUsage() bool {
	return s != nil && s.AssistantUsageCount > 0 && s.InputTokens+s.OutputTokens+s.TotalTokens+s.CacheReadTokens+s.CacheWriteTokens > 0
}

func readPiTranscriptSummary(sessionID string, turnStart time.Time) *piTranscriptSummary {
	return readPiTranscriptSummaryInDir(piTranscriptSessionDir(), sessionID, turnStart)
}

func readPiTranscriptSummaryInDir(sessionDir, sessionID string, turnStart time.Time) *piTranscriptSummary {
	if strings.TrimSpace(sessionDir) == "" {
		sessionDir = piTranscriptSessionDir()
	}
	transcriptPath := latestPiTranscriptPathInDir(sessionDir, sessionID)
	if transcriptPath == "" {
		return nil
	}
	summary := readPiTranscriptSummaryFile(transcriptPath, turnStart)
	if summary == nil {
		return &piTranscriptSummary{Path: transcriptPath}
	}
	summary.Path = transcriptPath
	return summary
}

func latestPiTranscriptPath(sessionID string) string {
	return latestPiTranscriptPathInDir(piTranscriptSessionDir(), sessionID)
}

func latestPiTranscriptPathInDir(sessionDir, sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	sessionDir = strings.TrimSpace(sessionDir)
	if sessionDir == "" {
		return ""
	}
	type candidate struct {
		path string
		mod  time.Time
	}
	cands := []candidate{}
	suffix := "_" + sessionID + ".jsonl"
	if err := filepath.WalkDir(sessionDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry == nil || entry.IsDir() || !strings.HasSuffix(entry.Name(), suffix) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		cands = append(cands, candidate{path: path, mod: info.ModTime()})
		return nil
	}); err != nil {
		return ""
	}
	if len(cands) == 0 {
		return ""
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].mod.After(cands[j].mod) })
	return cands[0].path
}

func piTranscriptSessionDir() string {
	if dir := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_SESSION_DIR")); dir != "" {
		return dir
	}
	if dir := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR")); dir != "" {
		return filepath.Join(dir, "sessions")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".pi", "agent", "sessions")
}

func piConfiguredTranscriptSessionDir() string {
	if dir := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_SESSION_DIR")); dir != "" {
		return dir
	}
	if dir := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR")); dir != "" {
		return filepath.Join(dir, "sessions")
	}
	return ""
}

func readPiTranscriptSummaryFile(path string, turnStart time.Time) *piTranscriptSummary {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	summary := &piTranscriptSummary{Path: path}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var ev piTranscriptRecord
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil || ev.Type != "message" || ev.Message == nil {
			continue
		}
		if !piTranscriptRecordInTurn(ev, turnStart) {
			continue
		}
		role, ok := piTranscriptRole(ev.Message.Role)
		if !ok {
			continue
		}
		if parts := piTranscriptParts(ev.Message.Content); len(parts) > 0 {
			summary.Messages = append(summary.Messages, llmtypes.MessageContent{Role: role, Parts: parts})
		}
		if role == llmtypes.ChatMessageTypeAI {
			summary.ProgressMessages = append(summary.ProgressMessages, piTranscriptProgressMessages(ev.Message.Content)...)
			summary.StructuredProgressMessages = append(summary.StructuredProgressMessages, piTranscriptProgressMessages(ev.Message.Content)...)
			for _, part := range piTranscriptParts(ev.Message.Content) {
				if _, ok := part.(llmtypes.ToolCall); ok {
					summary.StructuredProgressMessages = append(summary.StructuredProgressMessages, llmtypes.MessageContent{Role: role, Parts: []llmtypes.ContentPart{part}})
				}
			}
		} else if role == llmtypes.ChatMessageTypeTool {
			var texts []string
			for _, part := range ev.Message.Content {
				if part.Type == "text" {
					texts = append(texts, part.Text)
				}
			}
			summary.StructuredProgressMessages = append(summary.StructuredProgressMessages, llmtypes.MessageContent{Role: role, Parts: []llmtypes.ContentPart{llmtypes.ToolCallResponse{ToolCallID: ev.Message.ToolCallID, Name: ev.Message.ToolName, IsError: ev.Message.IsError, Content: strings.Join(texts, "\n")}}})
		}
		if role != llmtypes.ChatMessageTypeAI || ev.Message.Usage == nil {
			continue
		}
		summary.AssistantUsageCount++
		piMarkUsageReported(path, piUsageKey(ev))
		usage := ev.Message.Usage
		summary.InputTokens += usage.Input
		summary.OutputTokens += usage.Output
		summary.CacheReadTokens += usage.CacheRead
		summary.CacheWriteTokens += usage.CacheWrite
		if usage.TotalTokens > 0 {
			summary.TotalTokens += usage.TotalTokens
		}
		summary.InputCostUSD += usage.Cost.Input
		summary.OutputCostUSD += usage.Cost.Output
		summary.TotalCostUSD += usage.Cost.Total
		summary.CacheReadCostUSD += usage.Cost.CacheRead
		summary.CacheWriteCostUSD += usage.Cost.CacheWrite
		if ev.Message.Provider != "" {
			summary.Provider = ev.Message.Provider
		}
		if ev.Message.Model != "" {
			summary.Model = ev.Message.Model
		}
		if ev.Message.API != "" {
			summary.API = ev.Message.API
		}
		if ev.Message.ResponseID != "" {
			summary.ResponseID = ev.Message.ResponseID
		}
		if ev.Message.StopReason != "" {
			summary.StopReason = ev.Message.StopReason
		}
	}
	if summary.TotalTokens == 0 {
		summary.TotalTokens = summary.InputTokens + summary.OutputTokens
	}
	return summary
}

type piTranscriptRecord struct {
	Type      string               `json:"type"`
	Timestamp string               `json:"timestamp"`
	Message   *piTranscriptMessage `json:"message"`
}

type piTranscriptMessage struct {
	ToolCallID string                `json:"toolCallId"`
	ToolName   string                `json:"toolName"`
	IsError    bool                  `json:"isError"`
	Role       string                `json:"role"`
	Content    []piTranscriptContent `json:"content"`
	API        string                `json:"api"`
	Provider   string                `json:"provider"`
	Model      string                `json:"model"`
	Usage      *piTranscriptUsage    `json:"usage"`
	StopReason string                `json:"stopReason"`
	ResponseID string                `json:"responseId"`
}

type piTranscriptContent struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Thinking string `json:"thinking"`
	// ID/Name are only present on a "toolCall" content block. Captured so a
	// message can be recognized as still having a pending tool call attached
	// (PLAT-179).
	ID   string `json:"id"`
	Name string `json:"name"`
	// Arguments is the raw arguments object pi's own transcript records for
	// the tool call. Not replayed -- captured only so piTranscriptParts can
	// recover the real tool name when Name is pi's generic MCP-bridge
	// wrapper label ("mcp"): confirmed live against a real session
	// transcript, a bridge-routed call's arguments look like
	// {"tool": "api_bridge_execute_shell_command", "args": "{...}"}, the
	// same shape piGenericBridgeToolName/piRealToolNameFromBridgeArgs
	// already handle for the live marker-stream path.
	Arguments json.RawMessage `json:"arguments"`
}

// piTranscriptProgressMessages preserves the human-readable narration Pi shows
// in its terminal. Thinking blocks are intentionally excluded from Messages,
// because they must never become the retained turn's final answer, but they are
// exactly the intermediate progress users expect to see while that turn runs.
func piTranscriptProgressMessages(content []piTranscriptContent) []llmtypes.MessageContent {
	var messages []llmtypes.MessageContent
	for _, part := range content {
		switch strings.TrimSpace(part.Type) {
		case "thinking":
			// Tagged as thinking: sent as plain text it showed as a reply in
			// the chat and reached Slack/WhatsApp (2026-09-25).
			if thinking := strings.TrimSpace(part.Thinking); thinking != "" {
				messages = append(messages, llmtypes.MessageContent{
					Role:  llmtypes.ChatMessageTypeAI,
					Parts: []llmtypes.ContentPart{llmtypes.ThinkingContent{Thinking: thinking}},
				})
			}
		case "", "text":
			if text := strings.TrimSpace(part.Text); text != "" {
				messages = append(messages, llmtypes.TextPart(llmtypes.ChatMessageTypeAI, text))
			}
		}
	}
	return messages
}

type piTranscriptUsage struct {
	Input       int              `json:"input"`
	Output      int              `json:"output"`
	TotalTokens int              `json:"totalTokens"`
	CacheRead   int              `json:"cacheRead"`
	CacheWrite  int              `json:"cacheWrite"`
	Cost        piTranscriptCost `json:"cost"`
}

type piTranscriptCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	Total      float64 `json:"total"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
}

func piTranscriptRecordInTurn(ev piTranscriptRecord, turnStart time.Time) bool {
	if turnStart.IsZero() || ev.Timestamp == "" {
		return true
	}
	ts, err := time.Parse(time.RFC3339Nano, ev.Timestamp)
	if err != nil {
		return true
	}
	return !ts.Before(turnStart)
}

func piTranscriptRole(role string) (llmtypes.ChatMessageType, bool) {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "user":
		return llmtypes.ChatMessageTypeHuman, true
	case "assistant":
		return llmtypes.ChatMessageTypeAI, true
	case "toolresult":
		return llmtypes.ChatMessageTypeTool, true
	case "system":
		return llmtypes.ChatMessageTypeSystem, true
	default:
		return "", false
	}
}

// piTranscriptParts converts one message's raw content blocks into
// llmtypes parts, preserving a "toolCall" block as a real llmtypes.ToolCall
// rather than silently dropping it the way pure text extraction used to.
//
// PLAT-179. mcpagent's shared retainedturn.finalResponse skips any AI
// message that has a ToolCall part alongside its text -- that is what
// correctly excludes intermediate commentary sent right before a tool call
// from being read as the turn's final answer. Before this, pi's own
// transcript reader threw the toolCall block away here and kept only the
// text, so by the time the message reached finalResponse there was nothing
// left to signal "this one still has a pending tool call" -- confirmed live:
// a message combining {"type":"text","text":"<progress update>"} and
// {"type":"toolCall",...} in the SAME content array came out as a
// text-only MessageContent, indistinguishable from a genuinely finished
// reply.
func piTranscriptParts(content []piTranscriptContent) []llmtypes.ContentPart {
	var texts []string
	var parts []llmtypes.ContentPart
	for _, part := range content {
		switch strings.TrimSpace(part.Type) {
		case "", "text":
			if strings.TrimSpace(part.Text) != "" {
				texts = append(texts, part.Text)
			}
		case "toolCall":
			name := part.Name
			if piGenericBridgeToolName(name) {
				if real := piRealToolNameFromBridgeArgs(part.Arguments); real != "" {
					name = real
				}
			}
			parts = append(parts, llmtypes.ToolCall{
				ID:           part.ID,
				Type:         "function",
				FunctionCall: &llmtypes.FunctionCall{Name: name},
			})
		}
	}
	if text := strings.TrimSpace(strings.Join(texts, "\n")); text != "" {
		// Text first, matching the order it appeared in the raw content
		// array in every observed transcript (commentary, then the call).
		parts = append([]llmtypes.ContentPart{llmtypes.TextContent{Text: text}}, parts...)
	}
	return parts
}

func lastPiAssistantText(messages []llmtypes.MessageContent) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != llmtypes.ChatMessageTypeAI {
			continue
		}
		texts := make([]string, 0, len(messages[i].Parts))
		for _, part := range messages[i].Parts {
			if text, ok := part.(llmtypes.TextContent); ok && strings.TrimSpace(text.Text) != "" {
				texts = append(texts, text.Text)
			}
		}
		if text := strings.TrimSpace(strings.Join(texts, "\n")); text != "" {
			return text
		}
	}
	return ""
}
