package agycli

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	_ "modernc.org/sqlite"
)

// A bounded, incremental view of an agy conversation, for the chat's history and resume. ReadNativeTranscript parses
// every step on every call; a long conversation read once per turn made that cost grow with the chat. Here the caller
// asks for the newest few conversation rows after the last step it already holds, and a cheap stamp (two stat calls)
// says whether anything changed at all.

// NativeTranscriptStamp is the on-disk state of a conversation: the .db and its -wal companion (a running TUI keeps
// fresh steps in the WAL). Equal stamps mean nothing was added.
type NativeTranscriptStamp struct {
	Path       string
	Size       int64
	ModTime    time.Time
	WALSize    int64
	WALModTime time.Time
}

// NativeTailRow is one conversation message and the step it came from.
type NativeTailRow struct {
	Idx     int64
	Message llmtypes.MessageContent
}

// NativeTail is the newest conversation rows after a step. MaxIdx is the newest step of any kind: a MaxIdx below the
// caller's last step means the conversation was replaced, and the caller starts over. More is true when rows between
// the caller's last step and the returned rows were left out by the limit.
type NativeTail struct {
	Path   string
	Rows   []NativeTailRow
	MaxIdx int64
	More   bool
}

func agyConversationPath(nativeSessionID string, accountHome ...string) string {
	id := strings.TrimSpace(nativeSessionID)
	if id == "" || strings.ContainsAny(id, `/\`) {
		return ""
	}
	home := ""
	if len(accountHome) > 0 {
		home = strings.TrimSpace(accountHome[0])
	}
	if home == "" {
		var err error
		if home, err = os.UserHomeDir(); err != nil || home == "" {
			return ""
		}
	}
	return filepath.Join(home, ".gemini", "antigravity-cli", "conversations", id+".db")
}

// StatNativeTranscript reads the stamp without opening the database. ok is false when the conversation has no file.
func StatNativeTranscript(nativeSessionID string, accountHome ...string) (NativeTranscriptStamp, bool) {
	path := agyConversationPath(nativeSessionID, accountHome...)
	if path == "" {
		return NativeTranscriptStamp{}, false
	}
	info, err := os.Stat(path)
	if err != nil {
		return NativeTranscriptStamp{}, false
	}
	stamp := NativeTranscriptStamp{Path: path, Size: info.Size(), ModTime: info.ModTime()}
	if wal, err := os.Stat(path + "-wal"); err == nil {
		stamp.WALSize, stamp.WALModTime = wal.Size(), wal.ModTime()
	}
	return stamp, true
}

// ReadNativeTranscriptSince returns the newest `limit` user and assistant rows with a step after afterIdx (pass -1 for
// the start). Only those rows are parsed. ok is false when the conversation has no readable file.
func ReadNativeTranscriptSince(nativeSessionID string, afterIdx int64, limit int, accountHome ...string) (NativeTail, bool, error) {
	path := agyConversationPath(nativeSessionID, accountHome...)
	if path == "" || limit <= 0 {
		return NativeTail{}, false, nil
	}
	if _, err := os.Stat(path); err != nil {
		return NativeTail{}, false, nil
	}
	tmpPath, cleanup, err := agyCopyDBFile(path)
	if err != nil {
		return NativeTail{}, false, nil
	}
	defer cleanup()
	db, err := sql.Open("sqlite", "file:"+tmpPath)
	if err != nil {
		return NativeTail{}, false, err
	}
	defer func() { _ = db.Close() }()
	tail := NativeTail{Path: path, MaxIdx: -1}
	if err := db.QueryRowContext(context.Background(), `SELECT COALESCE(MAX(idx), -1) FROM steps`).Scan(&tail.MaxIdx); err != nil {
		return NativeTail{}, false, nil
	}
	rows, err := db.QueryContext(context.Background(), `SELECT idx, step_type, step_payload FROM steps WHERE idx > ? AND step_type IN (?, ?) ORDER BY idx DESC LIMIT ?`, afterIdx, agyStepUser, agyStepAssistant, limit+1)
	if err != nil {
		return NativeTail{}, false, nil
	}
	defer func() { _ = rows.Close() }()
	var newest []NativeTailRow // newest first
	seen := 0
	for rows.Next() {
		var idx int64
		var stepType int
		var payload []byte
		if err := rows.Scan(&idx, &stepType, &payload); err != nil {
			continue
		}
		seen++
		if seen > limit {
			tail.More = true
			break
		}
		if text, role, ok := agyTranscriptStepText(stepType, payload); ok && strings.TrimSpace(text) != "" {
			newest = append(newest, NativeTailRow{Idx: idx, Message: llmtypes.TextPart(role, text)})
		}
	}
	if err := rows.Err(); err != nil {
		return NativeTail{}, false, nil
	}
	for i := len(newest) - 1; i >= 0; i-- {
		tail.Rows = append(tail.Rows, newest[i])
	}
	return tail, true, nil
}
