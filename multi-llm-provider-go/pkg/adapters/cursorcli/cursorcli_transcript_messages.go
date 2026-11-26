package cursorcli

import (
	"context"

	"database/sql"
	"encoding/hex"
	"encoding/json"

	"strings"

	_ "modernc.org/sqlite"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// readCursorStoreDBMessagesAfter returns the messages committed after the
// baseline snapshot: one turn's own messages. Nothing is cached.
func readCursorStoreDBMessagesAfter(dbPath string, baseline map[string]struct{}) []llmtypes.MessageContent {
	msgs, _ := cursorStoreMessagesExcept(dbPath, baseline)
	return msgs
}

// cursorStoreMessagesExcept converts the store's current root, skipping refs
// in except, and returns the messages plus every ref it walked (nil on
// error).
func cursorStoreMessagesExcept(dbPath string, except map[string]struct{}) ([]llmtypes.MessageContent, []string) {
	// NB: modernc.org/sqlite returns empty results when `immutable=1`
	// is set on the URI (driver quirk — different from mattn). Stick
	// to `mode=ro` only.
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return nil, nil
	}
	defer db.Close()

	ctx := context.Background()
	refs, err := cursorStoreLatestRootRefs(ctx, db)
	if err != nil || len(refs) == 0 {
		return nil, nil
	}

	var out []llmtypes.MessageContent
	skippedFirstUserContext := false
	for _, ref := range refs {
		if _, skip := except[ref]; skip {
			continue
		}
		data := readCursorBlob(ctx, db, ref)
		if len(data) == 0 || data[0] != '{' {
			continue
		}
		var msg cursorMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(msg.Role)) {
		case "system":
			// Mcpagent maintains the outer system prompt; cursor's
			// internal "You are Composer..." duplicates that.
			continue
		case "user":
			// Cursor stores TWO user blobs per turn: the first carries
			// provider-options context (cwd, mcp config, rules); the
			// second the actual user query. Neither is returned: the
			// outer history already has the user message.
			if !skippedFirstUserContext {
				skippedFirstUserContext = true
			}
			continue
		case "assistant":
			parts := cursorAssistantPartsFromContent(msg.Content)
			if len(parts) == 0 {
				continue
			}
			out = append(out, llmtypes.MessageContent{Role: llmtypes.ChatMessageTypeAI, Parts: parts})
		case "tool":
			parts := cursorToolPartsFromContent(msg.Content)
			if len(parts) == 0 {
				continue
			}
			out = append(out, llmtypes.MessageContent{Role: llmtypes.ChatMessageTypeTool, Parts: parts})
		}
	}
	return out, refs
}

type cursorMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// cursorAssistantPartsFromContent maps a cursor assistant message's
// `content` (either a string or a list of typed blocks) into
// llmtypes parts. Returns nil if no usable parts remain.
func cursorAssistantPartsFromContent(raw json.RawMessage) []llmtypes.ContentPart {
	if len(raw) == 0 {
		return nil
	}
	// String content (rare on assistant, but possible).
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if s == "" {
			return nil
		}
		return []llmtypes.ContentPart{llmtypes.TextContent{Text: s}}
	}
	var blocks []struct {
		Type       string          `json:"type"`
		Text       string          `json:"text"`
		ToolCallID string          `json:"toolCallId"`
		ToolName   string          `json:"toolName"`
		Args       json.RawMessage `json:"args"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil
	}
	var parts []llmtypes.ContentPart
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if b.Text == "" {
				continue
			}
			parts = append(parts, llmtypes.TextContent{Text: b.Text})
		case "tool-call":
			name, args := normalizeCursorTranscriptToolCall(b.ToolName, b.Args)
			parts = append(parts, llmtypes.ToolCall{
				ID:   b.ToolCallID,
				Type: "function",
				FunctionCall: &llmtypes.FunctionCall{
					Name:      name,
					Arguments: args,
				},
			})
			// redacted-reasoning, redacted-* — silently skipped.
		}
	}
	return parts
}

// normalizeCursorTranscriptToolCall unwraps Cursor's native MCP envelope.
// Cursor records every bridge call as CallMcpTool and nests the actual tool
// name, server, and arguments beneath args. Passing that wrapper through makes
// the UI show only "CallMcpTool" and hides the useful command or payload.
func normalizeCursorTranscriptToolCall(rawName string, rawArgs json.RawMessage) (string, string) {
	name := rawName
	args := "{}"
	if len(rawArgs) > 0 {
		args = string(rawArgs)
	}
	if !strings.EqualFold(strings.TrimSpace(rawName), "CallMcpTool") && rawName != "CallDynamicTool" {
		return name, args
	}

	var envelope struct {
		Arguments json.RawMessage `json:"arguments"`
		Server    string          `json:"server"`
		Namespace string          `json:"namespace"`
		ToolName  string          `json:"toolName"`
	}
	if err := json.Unmarshal(rawArgs, &envelope); err != nil || strings.TrimSpace(envelope.ToolName) == "" {
		return name, args
	}
	if envelope.Server == "" {
		envelope.Server = envelope.Namespace
	}
	name = strings.TrimSpace(envelope.ToolName)
	if server := strings.TrimSpace(envelope.Server); server != "" {
		name = "mcp__" + server + "__" + name
	}
	if len(envelope.Arguments) > 0 && string(envelope.Arguments) != "null" {
		args = string(envelope.Arguments)
	}
	return name, args
}

// cursorToolPartsFromContent maps a tool-role message into a
// ToolCallResponse part.
func cursorToolPartsFromContent(raw json.RawMessage) []llmtypes.ContentPart {
	if len(raw) == 0 {
		return nil
	}
	var blocks []struct {
		Type       string          `json:"type"`
		ToolCallID string          `json:"toolCallId"`
		ToolName   string          `json:"toolName"`
		Result     json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil
	}
	var parts []llmtypes.ContentPart
	for _, b := range blocks {
		if b.Type != "tool-result" {
			continue
		}
		// `result` is typically a string but can be a structured
		// object; preserve as a JSON-encoded string when not a
		// plain string so nothing is dropped.
		var rs string
		if err := json.Unmarshal(b.Result, &rs); err != nil {
			rs = string(b.Result)
		}
		parts = append(parts, llmtypes.ToolCallResponse{
			ToolCallID: b.ToolCallID,
			Name:       b.ToolName,
			Content:    rs,
		})
	}
	return parts
}

// readCursorBlob reads one row from `blobs` by id. Cursor stores
// blobs as BLOB but sqlite3 driver may return TEXT (hex) depending
// on the affinity; handle both.
func readCursorBlob(ctx context.Context, db *sql.DB, id string) []byte {
	var raw any
	if err := db.QueryRowContext(ctx, `SELECT data FROM blobs WHERE id = ?`, id).Scan(&raw); err != nil {
		return nil
	}
	switch v := raw.(type) {
	case []byte:
		return v
	case string:
		if b, err := hex.DecodeString(v); err == nil {
			return b
		}
		return []byte(v)
	}
	return nil
}

// extractCursorRootChildRefs parses a cursor root protobuf blob and
// returns the ordered list of 32-byte child blob refs (field 1,
// wire type 2, length 32). Other fields are ignored.
func extractCursorRootChildRefs(buf []byte) []string {
	var refs []string
	off := 0
	for off < len(buf) {
		tag, n := varintUint64(buf[off:])
		if n == 0 {
			break
		}
		off += n
		field := tag >> 3
		wt := tag & 0x7
		switch wt {
		case 0: // varint
			_, n := varintUint64(buf[off:])
			if n == 0 {
				return refs
			}
			off += n
		case 2: // length-delimited
			ln, n := varintUint64(buf[off:])
			if n == 0 {
				return refs
			}
			off += n
			end := off + int(ln)
			if end > len(buf) || end < off {
				return refs
			}
			payload := buf[off:end]
			off = end
			if field == 1 && len(payload) == 32 {
				refs = append(refs, hex.EncodeToString(payload))
			}
		default:
			// fixed-32 (5), fixed-64 (1), or unknown — stop
			// rather than risk misaligned reads.
			return refs
		}
	}
	return refs
}

// varintUint64 decodes a protobuf varint and returns (value, bytes
// consumed). Returns (0, 0) on truncated/invalid input.
func varintUint64(buf []byte) (uint64, int) {
	var v uint64
	var shift uint
	for i, b := range buf {
		if i > 9 {
			return 0, 0
		}
		v |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return v, i + 1
		}
		shift += 7
	}
	return 0, 0
}
