package cursorcli

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// NativeTranscript is the human-visible conversation cursor-agent keeps for
// one of its own sessions: the typed user queries and the assistant prose,
// in order, with tool traffic and reasoning left out. It is what a caller
// that persists a durable conversation record needs to catch that record up
// after turns that never passed through the adapter's normal completion
// path (retained live-input turns).
type NativeTranscript struct {
	// Path is the store.db the messages came from.
	Path string
	// Messages holds Human/AI text-only messages in chronological order.
	Messages []llmtypes.MessageContent
	// UpdatedAt is the WAL-aware modification time of the store -- cursor's
	// blobs carry no per-message timestamps.
	UpdatedAt time.Time
}

// ReadNativeTranscript reads cursor-agent's own transcript for the session
// nativeSessionID that ran in workingDir.
// The store lives at ~/.cursor/chats/<md5(cwd)>/<sessionId>/store.db.
// A missing store is not an error: ok is false and the transcript is empty.
//
// Unlike readCursorStoreDBMessages this keeps the user's typed queries (the
// `<user_query>` payload of cursor's second user blob per turn) and drops
// the provider-options context blob, the system prompt, reasoning and tool
// calls/results.
func ReadNativeTranscript(workingDir, nativeSessionID string, accountHome ...string) (transcript NativeTranscript, ok bool, err error) {
	workingDir = strings.TrimSpace(workingDir)
	nativeSessionID = strings.TrimSpace(nativeSessionID)
	if workingDir == "" || nativeSessionID == "" {
		return NativeTranscript{}, false, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return NativeTranscript{}, false, err
	}
	if len(accountHome) > 0 && accountHome[0] != "" {
		home = accountHome[0]
	}
	dbPath := NativeRecordPath(workingDir, nativeSessionID, home)
	if dbPath == "" {
		return NativeTranscript{}, false, nil
	}
	return readNativeTranscriptDB(dbPath)
}

// ReadNativeTranscriptFromConfigDir reads the exact session in a host-issued
// private Cursor configuration directory, independently of the native cwd.
func ReadNativeTranscriptFromConfigDir(configDir, nativeSessionID string, workingDir ...string) (NativeTranscript, bool, error) {
	if !filepath.IsAbs(configDir) || nativeSessionID == "" || strings.ContainsAny(nativeSessionID, `/\\`) {
		return NativeTranscript{}, false, nil
	}
	cwd := ""
	if len(workingDir) > 0 {
		cwd = workingDir[0]
	}
	path := NativeRecordPathFromConfigDir(configDir, cwd, nativeSessionID)
	if path == "" {
		return NativeTranscript{}, false, nil
	}
	return readNativeTranscriptDB(path)
}

func readNativeTranscriptDB(dbPath string) (NativeTranscript, bool, error) {
	info, err := os.Stat(dbPath)
	if err != nil || info.IsDir() {
		return NativeTranscript{}, false, nil
	}

	messages, err := readCursorStoreDBConversation(dbPath)
	if err != nil {
		return NativeTranscript{}, false, err
	}
	updatedAt := info.ModTime()
	if walInfo, err := os.Stat(dbPath + "-wal"); err == nil && walInfo.ModTime().After(updatedAt) {
		updatedAt = walInfo.ModTime()
	}
	return NativeTranscript{Path: dbPath, Messages: messages, UpdatedAt: updatedAt}, true, nil
}

// readCursorStoreDBConversation walks the latest root's message blobs and
// returns the typed user queries and assistant text.
func readCursorStoreDBConversation(dbPath string) ([]llmtypes.MessageContent, error) {
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	ctx := context.Background()
	refs, err := cursorStoreLatestRootRefs(ctx, db)
	if err != nil {
		return nil, err
	}

	var out []llmtypes.MessageContent
	for _, ref := range refs {
		data := readCursorBlob(ctx, db, ref)
		if len(data) == 0 || data[0] != '{' {
			continue
		}
		var msg cursorMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(msg.Role)) {
		case "user":
			if query := cursorUserQueryFromContent(msg.Content); query != "" {
				out = append(out, llmtypes.MessageContent{
					Role:  llmtypes.ChatMessageTypeHuman,
					Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: query}},
				})
			}
		case "assistant":
			if text := cursorAssistantTextFromContent(msg.Content); text != "" {
				out = append(out, llmtypes.MessageContent{
					Role:  llmtypes.ChatMessageTypeAI,
					Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: text}},
				})
			}
		}
	}
	return out, nil
}

// cursorStoreLatestRootRefs resolves meta.latestRootBlobId and returns the
// root's child refs in chronological order. Shared by the streaming/turn
// reader and the native-transcript reader so the meta/root quirks (hex vs
// raw JSON, protobuf root) are handled in one place.
func cursorStoreLatestRootRefs(ctx context.Context, db *sql.DB) ([]string, error) {
	// meta.value affinity varies across cursor versions: some writes
	// store raw JSON bytes (BLOB), some store hex-encoded JSON (TEXT).
	// Scan as any and normalize.
	var metaRaw any
	if err := db.QueryRowContext(ctx, `SELECT value FROM meta LIMIT 1`).Scan(&metaRaw); err != nil {
		return nil, err
	}
	var metaBytes []byte
	switch v := metaRaw.(type) {
	case []byte:
		// Could be raw JSON bytes OR hex bytes-as-text. Try JSON first.
		if len(v) > 0 && (v[0] == '{' || v[0] == '[') {
			metaBytes = v
		} else if decoded, err := hex.DecodeString(string(v)); err == nil {
			metaBytes = decoded
		} else {
			metaBytes = v
		}
	case string:
		if len(v) > 0 && (v[0] == '{' || v[0] == '[') {
			metaBytes = []byte(v)
		} else if decoded, err := hex.DecodeString(v); err == nil {
			metaBytes = decoded
		} else {
			metaBytes = []byte(v)
		}
	default:
		return nil, errors.New("cursor store meta has an unexpected value type")
	}
	var meta struct {
		LatestRootBlobID string `json:"latestRootBlobId"`
	}
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		return nil, err
	}
	if meta.LatestRootBlobID == "" {
		return nil, errors.New("cursor store meta has no latestRootBlobId")
	}

	// Fetch the root blob; rows are stored as BLOB but the cursor
	// CLI stores them hex-encoded as TEXT -- handle both.
	rootData := readCursorBlob(ctx, db, meta.LatestRootBlobID)
	if len(rootData) == 0 || rootData[0] != 0x0a {
		return nil, errors.New("cursor store root blob is not a protobuf root")
	}
	return extractCursorRootChildRefs(rootData), nil
}

// cursorUserQueryFromContent returns what the user actually typed. Cursor
// writes two user blobs per turn: a provider-options context blob (a plain
// string starting with <user_info>, no <user_query>) and the query blob, a
// text block wrapping the typed text in <user_query>...</user_query> behind
// an optional <timestamp> tag. Only the latter yields text.
func cursorUserQueryFromContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	for _, block := range blocks {
		if block.Type != "text" {
			continue
		}
		start := strings.Index(block.Text, "<user_query>")
		if start < 0 {
			continue
		}
		body := block.Text[start+len("<user_query>"):]
		if end := strings.Index(body, "</user_query>"); end >= 0 {
			body = body[:end]
		}
		if text := strings.TrimSpace(body); text != "" {
			return text
		}
	}
	return ""
}

// cursorAssistantTextFromContent joins an assistant blob's text blocks and
// ignores reasoning, redacted reasoning and tool calls.
func cursorAssistantTextFromContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return strings.TrimSpace(asString)
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	texts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Type != "text" {
			continue
		}
		if text := strings.TrimSpace(block.Text); text != "" {
			texts = append(texts, text)
		}
	}
	return strings.TrimSpace(strings.Join(texts, "\n\n"))
}
