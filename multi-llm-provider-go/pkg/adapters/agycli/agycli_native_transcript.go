package agycli

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"google.golang.org/protobuf/encoding/protowire"
	_ "modernc.org/sqlite"
)

// NativeTranscript is agy's on-disk conversation read back after the fact
// for chat-history resync. Mirrors musecli.NativeTranscript so
// mcp-agent-builder-go's nativeTranscriptMessagesForRuntime can treat every
// tmux-backed provider the same way.
//
// Wire format (reverse-engineered from agy 1.2.7
// ~/.gemini/antigravity-cli/conversations/<conversation-id>.db, verified
// against live CLI output; see TestAgyCLIRealNativeTranscriptContract):
//
//   - SQLite, WAL mode, one row per step in `steps`, chronological by idx.
//   - step_payload is a protobuf message: field 1 echoes the step type,
//     field 19 carries a user turn (field 2 = prompt text), field 20
//     carries an assistant turn (field 1 = reply text; field 3 is thinking
//     and is deliberately NOT surfaced).
//   - Step types: 14 = user, 15 = assistant, 17 = API error, 101 = system
//     notice, 132 = tool call. Only 14/15 carry conversation text.
//
// The reader parses protobuf structurally with protowire (no generated
// schema exists); unknown step types are skipped so a CLI-side addition
// degrades to missing messages rather than a failed recovery. Format drift
// is caught loudly by the live contract test, not here.
type NativeTranscript struct {
	// Path is the conversation .db the messages came from.
	Path string
	// Messages holds Human/AI text-only messages in chronological order.
	Messages []llmtypes.MessageContent
	// UpdatedAt is the conversation .db's modification time.
	UpdatedAt time.Time
}

// Agy conversation step types carrying user/assistant text.
const (
	agyStepUser      = 14
	agyStepAssistant = 15
)

// ReadNativeTranscript reads agy's own conversation .db for nativeSessionID
// (the exec lane's conversation_id) back into a NativeTranscript. ok is
// false when no conversation exists for this id -- resync callers then leave
// the persisted chat-history record as-is, the same contract as
// picli.ReadNativeTranscript.
//
// The .db is WAL-mode and usually ships without its -wal/-shm companions,
// a state mode=ro refuses to open; the reader copies to a temp file and
// opens the copy instead. No file is ever created inside the user's config.
func ReadNativeTranscript(nativeSessionID string, accountHome ...string) (NativeTranscript, bool, error) {
	return readAgyNativeTranscript(nativeSessionID, false, accountHome...)
}

func readAgyNativeTranscript(nativeSessionID string, includeTools bool, accountHome ...string) (NativeTranscript, bool, error) {
	id := strings.TrimSpace(nativeSessionID)
	if id == "" || strings.ContainsAny(id, `/\`) {
		return NativeTranscript{}, false, nil
	}
	home := ""
	if len(accountHome) > 0 {
		home = strings.TrimSpace(accountHome[0])
	}
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil || home == "" {
			return NativeTranscript{}, false, nil
		}
	}
	path := filepath.Join(home, ".gemini", "antigravity-cli", "conversations", id+".db")
	info, err := os.Stat(path)
	if err != nil {
		return NativeTranscript{}, false, nil
	}
	tmpPath, cleanup, err := agyCopyDBFile(path)
	if err != nil {
		return NativeTranscript{}, false, nil
	}
	defer cleanup()

	db, err := sql.Open("sqlite", "file:"+tmpPath)
	if err != nil {
		return NativeTranscript{}, false, err
	}
	defer func() { _ = db.Close() }()
	query := `SELECT step_type, step_payload, 0 FROM steps ORDER BY idx`
	if includeTools {
		query = `SELECT step_type, step_payload, status FROM steps ORDER BY idx`
	}
	rows, err := db.QueryContext(context.Background(), query)
	if err != nil {
		return NativeTranscript{}, false, nil
	}
	defer func() { _ = rows.Close() }()

	var messages []llmtypes.MessageContent
	sawSteps := false
	for rows.Next() {
		var stepType, status int
		var payload []byte
		if err := rows.Scan(&stepType, &payload, &status); err != nil {
			continue
		}
		sawSteps = true
		if includeTools && stepType == agyStepToolCall {
			if section, ok := agyProtoSubmessage(payload, 5); ok {
				if call, ok := agyProtoSubmessage(section, 4); ok {
					id, _ := agyProtoStringField(call, 1)
					name, _ := agyProtoStringField(call, 2)
					args, _ := agyProtoStringField(call, 3)
					messages = append(messages, llmtypes.MessageContent{Role: llmtypes.ChatMessageTypeAI, Parts: []llmtypes.ContentPart{llmtypes.ToolCall{ID: id, Type: "function", FunctionCall: &llmtypes.FunctionCall{Name: name, Arguments: args}}}})
					if status == 3 {
						messages = append(messages, llmtypes.MessageContent{Role: llmtypes.ChatMessageTypeTool, Parts: []llmtypes.ContentPart{llmtypes.ToolCallResponse{ToolCallID: id, Name: name}}})
					}
				}
			}
		}
		text, role, ok := agyTranscriptStepText(stepType, payload)
		if !ok || strings.TrimSpace(text) == "" {
			continue
		}
		messages = append(messages, llmtypes.TextPart(role, text))
	}
	if err := rows.Err(); err != nil {
		return NativeTranscript{}, false, nil
	}
	if !sawSteps || len(messages) == 0 {
		return NativeTranscript{}, false, nil
	}
	return NativeTranscript{Path: path, Messages: messages, UpdatedAt: info.ModTime()}, true, nil
}

// agyTranscriptStepText extracts conversation text from one step payload.
// ok is false for non-conversation step types and unparseable payloads.
func agyTranscriptStepText(stepType int, payload []byte) (string, llmtypes.ChatMessageType, bool) {
	var role llmtypes.ChatMessageType
	var outer, inner protowire.Number
	switch stepType {
	case agyStepUser:
		role, outer, inner = llmtypes.ChatMessageTypeHuman, 19, 2
	case agyStepAssistant:
		role, outer, inner = llmtypes.ChatMessageTypeAI, 20, 1
	default:
		return "", "", false
	}
	sub, ok := agyProtoSubmessage(payload, outer)
	if !ok {
		return "", "", false
	}
	text, ok := agyProtoStringField(sub, inner)
	if !ok {
		return "", "", false
	}
	return text, role, true
}

// DecodeNativeStepText extracts only visible user/assistant text. Native event
// readers reuse the provider's protobuf decoder without exposing thinking.
func DecodeNativeStepText(stepType int, payload []byte) (string, llmtypes.ChatMessageType, bool) {
	return agyTranscriptStepText(stepType, payload)
}

// agyProtoSubmessage returns the first length-delimited field num's raw
// bytes. Structurally invalid input yields ok=false, never a panic.
func agyProtoSubmessage(msg []byte, num protowire.Number) ([]byte, bool) {
	for len(msg) > 0 {
		field, typ, n := protowire.ConsumeTag(msg)
		if n < 0 {
			return nil, false
		}
		msg = msg[n:]
		if field != num {
			m := protowire.ConsumeFieldValue(field, typ, msg)
			if m < 0 {
				return nil, false
			}
			msg = msg[m:]
			continue
		}
		if typ != protowire.BytesType {
			return nil, false
		}
		val, n := protowire.ConsumeBytes(msg)
		if n < 0 {
			return nil, false
		}
		return val, true
	}
	return nil, false
}

// agyProtoStringField returns the first field num's string value.
func agyProtoStringField(msg []byte, num protowire.Number) (string, bool) {
	raw, ok := agyProtoSubmessage(msg, num)
	if !ok {
		return "", false
	}
	return string(raw), true
}
