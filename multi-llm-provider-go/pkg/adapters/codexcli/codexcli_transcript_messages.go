package codexcli

import (
	"bufio"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// readCodexTranscriptMessages reconstructs the CLI's internal
// turn-by-turn trail (assistant text, function/custom tool calls,
// tool outputs) from the same rollout JSONL that readCodexTranscriptUsage
// reads tokens from. Returns []llmtypes.MessageContent suitable for
// splicing into a workflow conversation_history.
//
// Shape mapping (codex rollout → llmtypes):
//
//	response_item:message  role=assistant   → {Role: AI, Parts: [TextContent...]}
//	response_item:function_call             → {Role: AI, Parts: [ToolCall]}
//	response_item:function_call_output      → {Role: Tool, Parts: [ToolCallResponse]}
//	response_item:custom_tool_call          → {Role: AI, Parts: [ToolCall]}    (apply_patch etc.)
//	response_item:custom_tool_call_output   → {Role: Tool, Parts: [ToolCallResponse]}
//
// Skipped (noise / non-content):
//   - response_item:reasoning      (encrypted, no usable text)
//   - response_item:message role=developer|user (system prompts / outer user msg already in history)
//   - response_item:tool_search_*  (internal tool discovery)
//   - event_msg:*                  (UI/telemetry duplicates of response_items)
//
// File selection mirrors readCodexTranscriptUsage: freshest rollout
// whose mtime is at-or-after turnStart-30s and whose session_meta.cwd
// matches expectedWorkingDir. If expectedWorkingDir is set and no
// rollout matches, returns nil (we never want to leak cross-session
// messages).
//
// Best-effort: returns nil on any error or when no rollout is found.
func readCodexTranscriptMessages(turnStart time.Time, expectedWorkingDir string, accountRoot ...string) []llmtypes.MessageContent {
	root := codexSessionsRoot(accountRoot...)
	if root == "" {
		return nil
	}

	cutoff := turnStart.Add(-30 * time.Second)
	type cand struct {
		path string
		mod  time.Time
	}
	var cands []cand
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if filepath.Ext(p) != ".jsonl" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.ModTime().Before(cutoff) {
			return nil
		}
		cands = append(cands, cand{path: p, mod: info.ModTime()})
		return nil
	})
	if len(cands) == 0 {
		return nil
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].mod.After(cands[j].mod) })

	for _, candidate := range cands {
		msgs, cwd := readCodexTranscriptMessagesFile(candidate.path, turnStart)
		if strings.TrimSpace(expectedWorkingDir) != "" && !sameCodexWorkingDir(cwd, expectedWorkingDir) {
			continue
		}
		return msgs
	}
	return nil
}

// readCodexTranscriptFinalAssistantText returns Codex's authoritative final
// assistant message for the current turn. Prefer task_complete.last_agent_message
// because it is the exact text Codex committed as the turn result; fall back to
// the last assistant response_item explicitly marked final_answer for releases
// that omit last_agent_message. Commentary is never a valid turn result.
//
// This must remain separate from readCodexTranscriptMessages: that function
// intentionally returns the complete intermediate trail (assistant progress,
// MCP calls, and tool outputs) for conversation forensics. Using that trail as
// the workflow result is what caused MCP calls to leak into plan-step completion
// summaries and automatic parent notifications.
func readCodexTranscriptFinalAssistantText(turnStart time.Time, expectedWorkingDir string, accountRoot ...string) (string, string) {
	path := findCodexRolloutByWorkingDirUnsafe(turnStart, expectedWorkingDir, accountRoot...)
	if path == "" {
		return "", ""
	}
	return readCodexRolloutFinalAssistantText(path, turnStart)
}

// readCodexRolloutFinalAssistantText reads the committed final answer from ONE
// known rollout file. Callers that can identify the exact conversation (see
// resolveCodexRolloutPath) use this directly so no directory/recency guess is
// involved in choosing whose answer is returned. Returns the text and the
// payload shape it came from.
func readCodexRolloutFinalAssistantText(path string, turnStart time.Time) (string, string) {
	if strings.TrimSpace(path) == "" {
		return "", ""
	}

	f, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer f.Close()

	type contentBlock struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type event struct {
		Type      string `json:"type"`
		Timestamp string `json:"timestamp"`
		Payload   struct {
			Type             string         `json:"type"`
			Role             string         `json:"role"`
			Phase            string         `json:"phase"`
			Content          []contentBlock `json:"content"`
			LastAgentMessage string         `json:"last_agent_message"`
			TurnID           string         `json:"turn_id"`
		} `json:"payload"`
	}

	var lastAssistantMessage string
	var lastCompletedMessage string
	var startedTurnID string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 32*1024*1024)
	for scanner.Scan() {
		var e event
		if json.Unmarshal(scanner.Bytes(), &e) != nil {
			continue
		}
		if !turnStart.IsZero() {
			timestamp, parseErr := time.Parse(time.RFC3339Nano, e.Timestamp)
			if parseErr != nil || timestamp.Before(turnStart) {
				continue
			}
		}
		if e.Type == "event_msg" && e.Payload.Type == "task_started" {
			startedTurnID = strings.TrimSpace(e.Payload.TurnID)
			// A queued follow-up may start inside the same retained read window.
			// Its predecessor's final cannot close this newer native turn.
			lastAssistantMessage, lastCompletedMessage = "", ""
			continue
		}
		if e.Type == "response_item" && e.Payload.Type == "message" && e.Payload.Role == "assistant" &&
			(e.Payload.Phase == "final_answer" || e.Payload.Phase == "final") {
			var parts []string
			for _, block := range e.Payload.Content {
				if (block.Type == "output_text" || block.Type == "text") && strings.TrimSpace(block.Text) != "" {
					parts = append(parts, block.Text)
				}
			}
			if len(parts) > 0 {
				lastAssistantMessage = strings.TrimSpace(strings.Join(parts, "\n"))
			}
		}
		if e.Type == "event_msg" && e.Payload.Type == "task_complete" && strings.TrimSpace(e.Payload.LastAgentMessage) != "" &&
			codexTaskCompleteBelongsToTurn(startedTurnID, e.Payload.TurnID) {
			lastCompletedMessage = normalizeCodexFinalAssistantText(e.Payload.LastAgentMessage)
		}
	}
	if lastCompletedMessage != "" {
		return lastCompletedMessage, "rollout_task_complete"
	}
	if lastAssistantMessage != "" {
		return lastAssistantMessage, "rollout_assistant_message"
	}
	return "", ""
}

// readCodexRolloutTurnError returns the error Codex recorded on this turn's
// task_complete event, or "" when the turn ended normally. Codex reports a
// failed turn (an unsupported model for the account, a 4xx from the API, a
// refused request) exactly here: task_complete with last_agent_message null
// and an error object — and then shows the ready prompt again, so every other
// completion signal looks like success. Without reading it the adapter
// returned an empty reply that the runtime accepted as a launch-only
// response, and the family saw "No content generated" instead of the reason.
//
// The message is often a JSON-serialized API error; the nested
// error.message is returned when it parses, the raw text otherwise.
func readCodexRolloutTurnError(path string, turnStart time.Time) string {
	message, _ := readCodexRolloutTurnFailure(path, turnStart)
	return message
}

// readCodexRolloutTurnFailure is readCodexRolloutTurnError plus the machine-readable
// error.codex_error_info Codex writes next to the message ("server_overloaded" when the
// selected model is at capacity).
func readCodexRolloutTurnFailure(path string, turnStart time.Time) (string, string) {
	if strings.TrimSpace(path) == "" {
		return "", ""
	}
	f, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer f.Close()

	type event struct {
		Type      string `json:"type"`
		Timestamp string `json:"timestamp"`
		Payload   struct {
			Type             string          `json:"type"`
			LastAgentMessage string          `json:"last_agent_message"`
			Error            json.RawMessage `json:"error"`
			TurnID           string          `json:"turn_id"`
			Reason           string          `json:"reason"`
		} `json:"payload"`
	}
	var last, lastInfo string
	var startedTurnID string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 32*1024*1024)
	for scanner.Scan() {
		var e event
		if json.Unmarshal(scanner.Bytes(), &e) != nil {
			continue
		}
		if e.Type != "event_msg" {
			continue
		}
		switch e.Payload.Type {
		case "task_complete", "task_started", "turn_aborted":
		default:
			continue
		}
		if !turnStart.IsZero() {
			timestamp, parseErr := time.Parse(time.RFC3339Nano, e.Timestamp)
			if parseErr != nil || timestamp.Before(turnStart) {
				continue
			}
		}
		if e.Payload.Type == "task_started" {
			if startedTurnID == "" {
				startedTurnID = strings.TrimSpace(e.Payload.TurnID)
			}
			continue
		}
		if !codexTaskCompleteBelongsToTurn(startedTurnID, e.Payload.TurnID) {
			continue
		}
		if e.Payload.Type == "turn_aborted" {
			last, lastInfo = codexTurnAbortedMessage(e.Payload.Reason), ""
			continue
		}
		// A later successful completion in the same window supersedes an
		// earlier failure (Codex retried and the answer arrived).
		if strings.TrimSpace(e.Payload.LastAgentMessage) != "" {
			last, lastInfo = "", ""
			continue
		}
		last, lastInfo = codexTurnErrorMessage(e.Payload.Error), codexTurnErrorInfo(e.Payload.Error)
	}
	return last, lastInfo
}

// codexTurnErrorInfo returns error.codex_error_info from task_complete's error object.
func codexTurnErrorInfo(raw json.RawMessage) string {
	var asObject struct {
		Info string `json:"codex_error_info"`
	}
	if json.Unmarshal(raw, &asObject) == nil {
		return strings.TrimSpace(asObject.Info)
	}
	return ""
}

// codexTurnAbortedMessage is the failure reported for a turn Codex recorded as
// turn_aborted: nothing was answered, and the turn must not be mistaken for
// one still running or for whatever older text the pane still shows.
func codexTurnAbortedMessage(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" || reason == "interrupted" {
		return "the turn was interrupted before Codex answered"
	}
	return "the turn was aborted before Codex answered (" + reason + ")"
}

// codexTurnErrorMessage flattens task_complete's error field, which Codex
// writes as {"message": "..."} (the message itself frequently being a
// JSON-serialized API error), or occasionally as a bare string.
func codexTurnErrorMessage(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	var asString string
	if json.Unmarshal(raw, &asString) == nil {
		return unwrapCodexAPIErrorMessage(asString)
	}
	var asObject struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &asObject) == nil && strings.TrimSpace(asObject.Message) != "" {
		return unwrapCodexAPIErrorMessage(asObject.Message)
	}
	return trimmed
}

func unwrapCodexAPIErrorMessage(message string) string {
	trimmed := strings.TrimSpace(message)
	if !strings.HasPrefix(trimmed, "{") {
		return trimmed
	}
	var api struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(trimmed), &api) == nil {
		if m := strings.TrimSpace(api.Error.Message); m != "" {
			return m
		}
		if m := strings.TrimSpace(api.Message); m != "" {
			return m
		}
	}
	return trimmed
}

// normalizeCodexFinalAssistantText handles the two task_complete payload
// shapes emitted by Codex releases: plain text and a JSON-serialized content
// block array. The latter otherwise leaks transport JSON into workflow results
// and automatic parent notifications.
func normalizeCodexFinalAssistantText(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || !strings.HasPrefix(trimmed, "[") {
		return trimmed
	}

	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(trimmed), &blocks); err != nil || len(blocks) == 0 {
		return trimmed
	}

	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Type != "text" && block.Type != "output_text" {
			return trimmed
		}
		if text := strings.TrimSpace(block.Text); text != "" {
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		return trimmed
	}
	return strings.Join(parts, "\n")
}

func readCodexTranscriptMessagesFile(path string, turnStart time.Time, includeInputs ...bool) ([]llmtypes.MessageContent, string) {
	f, err := os.Open(path)
	if err != nil {
		return nil, ""
	}
	defer f.Close()

	type contentBlock struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type rolloutPayload struct {
		// shared
		Type string `json:"type"`
		// session_meta
		CWD              string `json:"cwd"`
		BaseInstructions struct {
			Text string `json:"text"`
		} `json:"base_instructions"`
		// message
		Role    string         `json:"role"`
		Content []contentBlock `json:"content"`
		// function_call / custom_tool_call
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
		Input     string `json:"input"`
		CallID    string `json:"call_id"`
		// function_call_output / custom_tool_call_output
		Output string `json:"output"`
	}
	type ev struct {
		Type      string         `json:"type"`
		Timestamp string         `json:"timestamp"`
		Payload   rolloutPayload `json:"payload"`
	}

	var sessionCWD string
	var out []llmtypes.MessageContent

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 32*1024*1024)
	for scanner.Scan() {
		var e ev
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			continue
		}
		if e.Type == "session_meta" && e.Payload.CWD != "" {
			sessionCWD = e.Payload.CWD
			if len(includeInputs) > 0 && includeInputs[0] && e.Payload.BaseInstructions.Text != "" {
				out = append(out, llmtypes.MessageContent{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: e.Payload.BaseInstructions.Text}}})
			}
		}
		if !turnStart.IsZero() && e.Timestamp != "" {
			if ts, err := time.Parse(time.RFC3339Nano, e.Timestamp); err == nil && ts.Before(turnStart) {
				continue
			}
		}
		if e.Type != "response_item" {
			continue
		}

		switch e.Payload.Type {
		case "message":
			if e.Payload.Role != "assistant" && !(len(includeInputs) > 0 && includeInputs[0]) {
				// skip user / developer / system rows; the outer
				// conversation already carries the user prompt.
				continue
			}
			var parts []llmtypes.ContentPart
			for _, b := range e.Payload.Content {
				// codex uses "output_text" for assistant content;
				// "input_text" appears on user / developer rows.
				if b.Type != "output_text" && b.Type != "text" && !(len(includeInputs) > 0 && includeInputs[0] && b.Type == "input_text") {
					continue
				}
				if b.Text == "" {
					continue
				}
				parts = append(parts, llmtypes.TextContent{Text: b.Text})
			}
			if len(parts) == 0 {
				continue
			}
			role := llmtypes.ChatMessageTypeAI
			if e.Payload.Role == "user" {
				role = llmtypes.ChatMessageTypeHuman
			}
			if e.Payload.Role == "system" || e.Payload.Role == "developer" {
				role = llmtypes.ChatMessageTypeSystem
			}
			out = append(out, llmtypes.MessageContent{Role: role, Parts: parts})

		case "function_call":
			args := e.Payload.Arguments
			if args == "" {
				args = "{}"
			}
			out = append(out, llmtypes.MessageContent{
				Role: llmtypes.ChatMessageTypeAI,
				Parts: []llmtypes.ContentPart{
					llmtypes.ToolCall{
						ID:   e.Payload.CallID,
						Type: "function",
						FunctionCall: &llmtypes.FunctionCall{
							Name:      e.Payload.Name,
							Arguments: args,
						},
					},
				},
			})

		case "custom_tool_call":
			// codex's custom tools (e.g., apply_patch) use a raw
			// `input` string instead of JSON arguments. Wrap it in a
			// single-key JSON object so downstream consumers still
			// see a parseable Arguments field.
			input := e.Payload.Input
			args := input
			if !looksLikeJSON(input) {
				wrapped, err := json.Marshal(map[string]string{"input": input})
				if err == nil {
					args = string(wrapped)
				}
			}
			if args == "" {
				args = "{}"
			}
			out = append(out, llmtypes.MessageContent{
				Role: llmtypes.ChatMessageTypeAI,
				Parts: []llmtypes.ContentPart{
					llmtypes.ToolCall{
						ID:   e.Payload.CallID,
						Type: "function",
						FunctionCall: &llmtypes.FunctionCall{
							Name:      e.Payload.Name,
							Arguments: args,
						},
					},
				},
			})

		case "function_call_output", "custom_tool_call_output":
			out = append(out, llmtypes.MessageContent{
				Role: llmtypes.ChatMessageTypeTool,
				Parts: []llmtypes.ContentPart{
					llmtypes.ToolCallResponse{
						ToolCallID: e.Payload.CallID,
						Content:    e.Payload.Output,
					},
				},
			})
		}
	}
	return out, sessionCWD
}

func looksLikeJSON(s string) bool {
	t := strings.TrimSpace(s)
	if t == "" {
		return false
	}
	c := t[0]
	return c == '{' || c == '['
}

// ReadTranscriptFile returns a whole Codex rollout file as messages, the
// person's inputs, replies, tool calls and tool results included. A copy of a
// chat handed to another program uses it as the chat's history.
func ReadTranscriptFile(path string) ([]llmtypes.MessageContent, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	messages, _ := readCodexTranscriptMessagesFile(path, time.Time{}, true)
	return messages, nil
}
