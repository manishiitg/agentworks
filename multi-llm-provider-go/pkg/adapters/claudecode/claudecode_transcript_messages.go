package claudecode

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/nativereplies"
)

func parseClaudeTranscriptMessages(reader io.Reader, turnStart time.Time, includeInputs bool) []llmtypes.MessageContent {
	lineage := &nativereplies.ClaudeLineage{}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)

	type ev struct {
		Type      string          `json:"type"`
		Timestamp string          `json:"timestamp"`
		Message   json.RawMessage `json:"message"`
	}
	type assistantMessage struct {
		ID      string                        `json:"id"`
		Content []claudeAssistantContentBlock `json:"content"`
	}
	type userContentBlock struct {
		Type      string          `json:"type"`
		Text      string          `json:"text"`
		ToolUseID string          `json:"tool_use_id"`
		Content   json.RawMessage `json:"content"`
	}
	type userMessage struct {
		Content json.RawMessage `json:"content"`
	}

	// Claude-code writes ONE row per content block of an LLM call.
	// All rows from the same call share message.id; each row carries
	// exactly ONE content block (text, thinking, tool_use, ...). To
	// reconstruct the call as a single AI MessageContent we group by
	// message.id, accumulate blocks across rows, and emit a combined
	// MessageContent at the first sighting (preserving chronological
	// position in `out`).
	var out []llmtypes.MessageContent
	type pendingGroup struct {
		index int // position in out where this group's MessageContent lives
	}
	groups := make(map[string]*pendingGroup)

	for scanner.Scan() {
		var e ev
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			continue
		}
		root := lineage.Observe(scanner.Bytes())
		if !turnStart.IsZero() && e.Timestamp != "" {
			if ts, err := time.Parse(time.RFC3339Nano, e.Timestamp); err == nil && ts.Before(turnStart) {
				continue
			}
		}

		if !includeInputs && !nativereplies.ForegroundMayPublish(root) {
			continue
		}
		switch e.Type {
		case "assistant":
			var am assistantMessage
			if err := json.Unmarshal(e.Message, &am); err != nil {
				continue
			}
			parts := assistantBlocksToParts(am.Content)
			if len(parts) == 0 {
				// thinking blocks, redacted_reasoning, etc. — skip
				// without registering the group; if a later row of
				// the same message.id carries usable content, it
				// still creates the group then.
				continue
			}
			if am.ID == "" {
				// No id to group by: emit standalone.
				out = append(out, llmtypes.MessageContent{
					Role:  llmtypes.ChatMessageTypeAI,
					Parts: parts,
				})
				continue
			}
			if g, ok := groups[am.ID]; ok {
				// Append additional blocks from later rows of the
				// same LLM call to the already-emitted group.
				combined := append(out[g.index].Parts, parts...) //nolint:gocritic
				out[g.index].Parts = combined
				continue
			}
			groups[am.ID] = &pendingGroup{index: len(out)}
			out = append(out, llmtypes.MessageContent{
				Role:  llmtypes.ChatMessageTypeAI,
				Parts: parts,
			})

		case "user":
			var um userMessage
			if err := json.Unmarshal(e.Message, &um); err != nil {
				continue
			}
			// content is either a string (typed user text) or an array
			// of content blocks (tool_results when the CLI feeds tool
			// outputs back into the model). We only care about
			// tool_results here — typed user text is already in the
			// outer conversation_history.
			var blocks []userContentBlock
			if err := json.Unmarshal(um.Content, &blocks); err != nil {
				var text string
				if includeInputs && json.Unmarshal(um.Content, &text) == nil && text != "" {
					out = append(out, llmtypes.MessageContent{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: text}}})
				}
				continue
			}
			for _, b := range blocks {
				if includeInputs && b.Type == "text" && b.Text != "" {
					out = append(out, llmtypes.MessageContent{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: b.Text}}})
				}
				if b.Type != "tool_result" {
					continue
				}
				out = append(out, llmtypes.MessageContent{
					Role: llmtypes.ChatMessageTypeTool,
					Parts: []llmtypes.ContentPart{
						llmtypes.ToolCallResponse{
							ToolCallID: b.ToolUseID,
							Content:    flattenToolResultContent(b.Content),
						},
					},
				})
			}
		}
	}
	return out
}

// claudeCompletedTranscriptResponse describes whether Claude's session JSONL
// exists and whether the current turn contains a committed end_turn message.
// Found and Completed are intentionally separate: an existing but incomplete
// transcript means the turn was interrupted and must never fall back to a pane
// scrape, while a genuinely unavailable transcript retains legacy compatibility.
type claudeCompletedTranscriptResponse struct {
	IncludedRoots []string
	FinalRoot     string
	// Text is the committed end_turn message: the turn's answer, and what
	// callers treat as the final result.
	Text string
	// TurnText is every assistant text block of the turn in transcript order,
	// narration before tool calls included, ending with Text. Chat surfaces
	// show this; Claude Code's own UI does the same. Empty until Completed.
	TurnText  string
	Found     bool
	Completed bool
	// LastIsEndTurn is stricter than Completed: the current turn's latest
	// assistant or user record is an end_turn message with text. Completed
	// stays true once any end_turn appears, even if Claude then continued
	// (a queued message or a background result delivered as a new user row),
	// so only LastIsEndTurn may end a turn.
	LastIsEndTurn bool
	// PendingBackgroundAgents is Claude's own count of background agents
	// still running when the latest turn ended (system turn_duration row's
	// pendingBackgroundAgentCount). While it is non-zero the answer is interim:
	// each agent's report arrives later as a new user row and Claude continues.
	PendingBackgroundAgents int
	// PendingBackgroundTasks counts tool calls Claude moved to the background
	// during the turn ("MCP tool ... is still running after 120s. It was moved
	// to the background as task <id>") whose <task-notification> has not
	// arrived yet. Claude ends its turn right away and starts a new one on its
	// own when the task finishes; without this gate the platform closed the
	// chat turn and never showed that continuation (RTS 2026-09-24).
	PendingBackgroundTasks int
}

var (
	claudeBackgroundedTaskPattern = regexp.MustCompile(`moved to the background as task ([A-Za-z0-9_-]+)`)
	claudeTaskNotificationPattern = regexp.MustCompile(`<task-notification>\s*<task-id>([A-Za-z0-9_-]+)</task-id>`)
)

// claudeUserMessageText is the plain text of a user row: its string content, or
// the text of its tool results.
func claudeUserMessageText(raw json.RawMessage) string {
	var message struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &message) != nil || len(message.Content) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(message.Content, &text) == nil {
		return text
	}
	var blocks []struct {
		Type    string          `json:"type"`
		Text    string          `json:"text"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(message.Content, &blocks) != nil {
		return ""
	}
	parts := []string{}
	for _, block := range blocks {
		if block.Text != "" {
			parts = append(parts, block.Text)
		}
		if len(block.Content) == 0 {
			continue
		}
		var inner string
		if json.Unmarshal(block.Content, &inner) == nil {
			parts = append(parts, inner)
			continue
		}
		var innerBlocks []struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(block.Content, &innerBlocks) == nil {
			for _, ib := range innerBlocks {
				parts = append(parts, ib.Text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

// completedAssistantResponseFromTranscript returns the final assistant message
// that Claude Code explicitly committed with stop_reason=end_turn during the
// current turn. It deliberately skips narration attached to tool_use calls:
// those blocks are progress within the turn, not the final response callers
// should persist.
//
// Claude writes one JSONL row per content block and may repeat a message ID, so
// text is accumulated by message ID and the last completed message wins.
func completedAssistantResponseFromTranscript(sessionID, workingDir string, turnStart time.Time, accountHome ...string) claudeCompletedTranscriptResponse {
	if !isClaudeTranscriptSessionID(sessionID) {
		return claudeCompletedTranscriptResponse{}
	}
	f, _, err := openClaudeTranscript(sessionID, workingDir, accountHome...)
	if err != nil {
		return claudeCompletedTranscriptResponse{}
	}
	defer f.Close()
	result := claudeCompletedTranscriptResponse{Found: true}

	type assistantMessage struct {
		ID         string                        `json:"id"`
		StopReason string                        `json:"stop_reason"`
		Content    []claudeAssistantContentBlock `json:"content"`
	}
	type event struct {
		Type                        string          `json:"type"`
		Subtype                     string          `json:"subtype"`
		Timestamp                   string          `json:"timestamp"`
		Message                     json.RawMessage `json:"message"`
		PendingBackgroundAgentCount int             `json:"pendingBackgroundAgentCount"`
	}
	type group struct {
		text          []string
		completed     bool
		observerOwned bool
		nativeRoot    string
	}

	groups := make(map[string]*group)
	order := make([]string, 0)
	standalone := 0
	lastAssistantID := ""
	userAfterLastAssistant := false
	pendingTasks := map[string]bool{}
	lineage := &nativereplies.ClaudeLineage{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var e event
		if json.Unmarshal(scanner.Bytes(), &e) != nil {
			continue
		}
		root := lineage.Observe(scanner.Bytes())
		if e.Type == "system" && e.Subtype == "turn_duration" {
			result.PendingBackgroundAgents = e.PendingBackgroundAgentCount
			continue
		}
		if e.Type != "assistant" && e.Type != "user" {
			continue
		}
		if !turnStart.IsZero() && e.Timestamp != "" {
			if ts, parseErr := time.Parse(time.RFC3339Nano, e.Timestamp); parseErr == nil && ts.Before(turnStart) {
				continue
			}
		}
		if e.Type == "user" {
			// A tool result or a newly delivered message: Claude is working again.
			if lastAssistantID != "" {
				userAfterLastAssistant = true
			}
			if text := claudeUserMessageText(e.Message); text != "" {
				for _, match := range claudeBackgroundedTaskPattern.FindAllStringSubmatch(text, -1) {
					pendingTasks[match[1]] = true
				}
				for _, match := range claudeTaskNotificationPattern.FindAllStringSubmatch(text, -1) {
					delete(pendingTasks, match[1])
				}
			}
			continue
		}
		var am assistantMessage
		if json.Unmarshal(e.Message, &am) != nil {
			continue
		}
		id := am.ID
		if id == "" {
			standalone++
			id = "__standalone_" + strconv.Itoa(standalone)
		}
		g, ok := groups[id]
		if !ok {
			g = &group{}
			groups[id] = g
			order = append(order, id)
		}
		g.observerOwned = !nativereplies.ForegroundMayPublish(root)
		if root.Helper {
			g.nativeRoot = root.Root
		}
		g.text = append(g.text, textFromClaudeBlocks(am.Content)...)
		if am.StopReason == "end_turn" {
			g.completed = true
			result.Completed = true
		}
		lastAssistantID = id
		userAfterLastAssistant = false
		// A new assistant record belongs to a turn whose turn_duration row
		// (if any) has not been written yet.
		result.PendingBackgroundAgents = 0
	}
	result.PendingBackgroundTasks = len(pendingTasks)
	if last := groups[lastAssistantID]; last != nil && last.completed && !userAfterLastAssistant && len(last.text) > 0 && result.PendingBackgroundAgents == 0 && result.PendingBackgroundTasks == 0 {
		result.LastIsEndTurn = true
	}

	for i := len(order) - 1; i >= 0; i-- {
		g := groups[order[i]]
		if g.completed && len(g.text) > 0 && !g.observerOwned {
			result.FinalRoot = g.nativeRoot
			result.Text = strings.TrimSpace(strings.Join(g.text, "\n\n"))
			parts := make([]string, 0, len(order))
			for _, id := range order[:i+1] {
				if groups[id].observerOwned {
					continue
				}
				if groups[id].completed && groups[id].nativeRoot != "" && len(groups[id].text) > 0 {
					result.IncludedRoots = append(result.IncludedRoots, groups[id].nativeRoot)
				}
				if t := strings.TrimSpace(strings.Join(groups[id].text, "\n\n")); t != "" {
					parts = append(parts, t)
				}
			}
			result.TurnText = strings.Join(parts, "\n\n")
			return result
		}
	}
	return result
}

func textFromClaudeBlocks(blocks []claudeAssistantContentBlock) []string {
	var text []string
	for _, block := range blocks {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			text = append(text, strings.TrimSpace(block.Text))
		}
	}
	return text
}

// claudeAssistantContentBlock is one block inside an assistant
// message in claude-code's JSONL transcript. Hoisted to package scope
// so the helper below can take it as a typed parameter.
type claudeAssistantContentBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text"`
	Thinking string          `json:"thinking"`
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Input    json.RawMessage `json:"input"`
}

// assistantBlocksToParts maps claude content blocks into llmtypes parts.
func assistantBlocksToParts(blocks []claudeAssistantContentBlock) []llmtypes.ContentPart {
	var parts []llmtypes.ContentPart
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if b.Text == "" {
				continue
			}
			parts = append(parts, llmtypes.TextContent{Text: b.Text})
		case "tool_use":
			args := "{}"
			if len(b.Input) > 0 {
				args = string(b.Input)
			}
			parts = append(parts, llmtypes.ToolCall{
				ID:   b.ID,
				Type: "function",
				FunctionCall: &llmtypes.FunctionCall{
					Name:      b.Name,
					Arguments: args,
				},
			})
		}
	}
	return parts
}

// flattenToolResultContent collapses a tool_result's `content` field
// (either a raw string or an array of {type:"text", text}) into a
// single string suitable for llmtypes.ToolCallResponse.Content.
func flattenToolResultContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// Try string first.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	// Then array of {type, text}.
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		joined := ""
		for i, b := range blocks {
			if b.Type != "text" {
				continue
			}
			if i > 0 && joined != "" {
				joined += "\n"
			}
			joined += b.Text
		}
		return joined
	}
	// Last resort: return raw JSON so nothing is silently dropped.
	return string(raw)
}

// ReadTranscriptFile returns a whole Claude Code session file as messages,
// the person's inputs, replies, tool calls and tool results included. A copy
// of a chat handed to another program uses it as the chat's history.
func ReadTranscriptFile(path string) ([]llmtypes.MessageContent, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parseClaudeTranscriptMessages(f, time.Time{}, true), nil
}
