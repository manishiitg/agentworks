// Package chatlog stores a chat conversation as a small header plus an
// append-only history log, so adding a message costs one line instead of a
// rewrite of the whole conversation.
//
// Chat conversations are "*-conversation.json" files under a builder chat
// folder (/builder/conversation/) or a chat_history folder; step execution
// logs keep their single-file format. In the new format:
//
//   - <name>-conversation.json is a header: every field of the conversation
//     record, plus "history_log":"jsonl-v1", "history_count" (committed
//     lines) and "history_system" (the newest system prompt, which is
//     regenerated every turn and so kept out of the log). Its
//     conversation_history holds only the latest messages (at most
//     headerTailRows / headerTailBytes), marked "history_tail":true with
//     "history_total", so a tool or agent reading the file directly still
//     sees recent context; the full history is in the log;
//   - <name>-conversation.history.jsonl holds conversation_history, one
//     message per line, only ever appended to (or rewritten whole by Save
//     when a writer changed earlier rows).
//
// Load returns the full legacy record, so readers need not know the format.
// A file in the old single-record format is returned unchanged and converted
// on its next Save or Append.
//
// Consistency without locks for readers: Append writes the lines first and
// then raises history_count, and a reader reads only history_count lines, so
// it never sees a half-written message. Save replaces each file atomically.
package chatlog

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const (
	headerTailRows  = 40
	headerTailBytes = 256 << 10

	formatMarker   = "jsonl-v1"
	fieldHistory   = "conversation_history"
	fieldLog       = "history_log"
	fieldCount     = "history_count"
	fieldSystem    = "history_system"
	fieldTailTotal = "history_total"
	fieldTailFlag  = "history_tail"
)

// ErrPartialRecord refuses to save a record read with LoadTail: writing it
// back would drop every message outside the tail.
var ErrPartialRecord = errors.New("chatlog: refusing to save a partial (tail) conversation record")

// IsConversationPath reports whether path is a chat conversation record
// stored in this format (builder chats and chat_history, not step logs).
func IsConversationPath(path string) bool {
	slashed := filepath.ToSlash(path)
	return strings.HasSuffix(slashed, "-conversation.json") &&
		(strings.Contains(slashed, "/builder/conversation/") || strings.Contains(slashed, "/chat_history/") ||
			strings.HasPrefix(slashed, "builder/conversation/") || strings.HasPrefix(slashed, "chat_history/"))
}

// HistoryLogPath is the append-only log next to a conversation header.
func HistoryLogPath(path string) string {
	return strings.TrimSuffix(path, ".json") + ".history.jsonl"
}

var (
	locksMu sync.Mutex
	locks   = map[string]*sync.Mutex{}
)

func lockFor(path string) func() {
	locksMu.Lock()
	lock, ok := locks[path]
	if !ok {
		lock = &sync.Mutex{}
		locks[path] = lock
	}
	locksMu.Unlock()
	lock.Lock()
	return lock.Unlock
}

type field struct {
	name  string
	value json.RawMessage
}

// header is a decoded header or legacy record, fields in their original order.
type header struct {
	fields []field
	isLog  bool
}

func (h *header) get(name string) (json.RawMessage, bool) {
	for _, f := range h.fields {
		if f.name == name {
			return f.value, true
		}
	}
	return nil, false
}

func (h *header) set(name string, value json.RawMessage) {
	for i := range h.fields {
		if h.fields[i].name == name {
			h.fields[i].value = value
			return
		}
	}
	h.fields = append(h.fields, field{name, value})
}

func (h *header) drop(name string) {
	out := h.fields[:0]
	for _, f := range h.fields {
		if f.name != name {
			out = append(out, f)
		}
	}
	h.fields = out
}

func (h *header) count() int {
	raw, ok := h.get(fieldCount)
	if !ok {
		return 0
	}
	var n int
	_ = json.Unmarshal(raw, &n)
	return n
}

func (h *header) encode() []byte {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, f := range h.fields {
		if i > 0 {
			buf.WriteByte(',')
		}
		name, _ := json.Marshal(f.name)
		buf.Write(name)
		buf.WriteByte(':')
		buf.Write(f.value)
	}
	buf.WriteByte('}')
	return buf.Bytes()
}

// decodeRecord reads a JSON object; rows receives each conversation_history
// element (nil to skip them). The history field itself is not kept.
func decodeRecord(r io.Reader, rows func(json.RawMessage) error) (*header, bool, error) {
	decoder := json.NewDecoder(bufio.NewReaderSize(r, 1<<20))
	token, err := decoder.Token()
	if err != nil {
		return nil, false, err
	}
	if token != json.Delim('{') {
		return nil, false, errors.New("not a JSON object")
	}
	h := &header{}
	sawHistory := false
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, false, err
		}
		name, _ := token.(string)
		if name != fieldHistory {
			var value json.RawMessage
			if err := decoder.Decode(&value); err != nil {
				return nil, false, err
			}
			h.fields = append(h.fields, field{name, value})
			if name == fieldLog {
				h.isLog = true
			}
			continue
		}
		sawHistory = true
		token, err = decoder.Token()
		if err != nil {
			return nil, false, err
		}
		if token == nil {
			continue
		}
		if token != json.Delim('[') {
			return nil, false, errors.New("conversation_history is not an array")
		}
		for decoder.More() {
			var raw json.RawMessage
			if err := decoder.Decode(&raw); err != nil {
				return nil, false, err
			}
			if rows != nil {
				if err := rows(raw); err != nil {
					return nil, false, err
				}
			}
		}
		if _, err := decoder.Token(); err != nil {
			return nil, false, err
		}
	}
	return h, sawHistory, nil
}

func readHeader(path string) (*header, []byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	// Only a log-format header is decoded here; a legacy record can be large.
	if !bytes.Contains(raw, []byte(`"`+fieldLog+`"`)) {
		return &header{}, raw, nil
	}
	h, _, err := decodeRecord(bytes.NewReader(raw), nil)
	if err != nil {
		return nil, nil, err
	}
	return h, raw, nil
}

// readLines returns up to limit committed lines (limit < 0: all).
func readLines(path string, limit int) ([][]byte, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader := bufio.NewReaderSize(file, 1<<20)
	var lines [][]byte
	for limit < 0 || len(lines) < limit {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 && line[len(line)-1] == '\n' {
			if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
				lines = append(lines, trimmed)
			}
		}
		// A line without its newline is an append still in progress (or cut
		// short by a crash): not committed, so not read.
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	return lines, nil
}

// Load returns the conversation record in the legacy single-object form.
func Load(path string) ([]byte, error) {
	return load(path, -1)
}

// LoadTail returns the record with only the last n messages of its history,
// plus "history_total" (all messages) and "history_tail": true. A legacy
// record is returned whole. Never Save the result.
func LoadTail(path string, n int) ([]byte, error) {
	if n < 0 {
		n = 0
	}
	return load(path, n)
}

func load(path string, tail int) ([]byte, error) {
	h, raw, err := readHeader(path)
	if err != nil {
		return nil, err
	}
	if !h.isLog {
		return raw, nil
	}
	count := h.count()
	lines, err := readLines(HistoryLogPath(path), count)
	if err != nil {
		return nil, err
	}
	total := len(lines)
	system, hasSystem := h.get(fieldSystem)
	if hasSystem && string(system) == "null" {
		hasSystem = false
	}
	if tail >= 0 && tail < len(lines) {
		lines = lines[len(lines)-tail:]
	}
	var buf bytes.Buffer
	buf.WriteByte('{')
	first := true
	for _, f := range h.fields {
		if f.name == fieldLog || f.name == fieldCount || f.name == fieldSystem || f.name == fieldTailFlag || f.name == fieldTailTotal {
			continue
		}
		if !first {
			buf.WriteByte(',')
		}
		first = false
		name, _ := json.Marshal(f.name)
		buf.Write(name)
		buf.WriteByte(':')
		buf.Write(f.value)
	}
	if !first {
		buf.WriteByte(',')
	}
	buf.WriteString(`"` + fieldHistory + `":[`)
	wrote := false
	if hasSystem {
		buf.Write(system)
		wrote = true
	}
	for _, line := range lines {
		if wrote {
			buf.WriteByte(',')
		}
		buf.Write(line)
		wrote = true
	}
	buf.WriteByte(']')
	if tail >= 0 {
		fmt.Fprintf(&buf, `,"%s":%d,"%s":true`, fieldTailTotal, total, fieldTailFlag)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func isSystemRow(raw json.RawMessage) bool {
	var row struct {
		Role string `json:"Role"`
	}
	return json.Unmarshal(raw, &row) == nil && row.Role == "system"
}

func compactLine(raw json.RawMessage) ([]byte, error) {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Save stores a full conversation record. When the stored log is a prefix of
// the new history only the new rows are appended; otherwise the log is
// rewritten. Content that is not a record with conversation_history is
// written as-is.
func Save(path string, content []byte) error {
	unlock := lockFor(path)
	defer unlock()
	return save(path, content)
}

func save(path string, content []byte) error {
	var rows [][]byte
	var system json.RawMessage
	h, sawHistory, err := decodeRecord(bytes.NewReader(content), func(raw json.RawMessage) error {
		if len(rows) == 0 && system == nil && isSystemRow(raw) {
			system = append(json.RawMessage(nil), raw...)
			return nil
		}
		line, err := compactLine(raw)
		if err != nil {
			return err
		}
		rows = append(rows, line)
		return nil
	})
	if err != nil || !sawHistory {
		return writeAtomic(path, content)
	}
	if flag, ok := h.get(fieldTailFlag); ok && string(flag) == "true" {
		return ErrPartialRecord
	}
	h.drop(fieldTailFlag)
	h.drop(fieldTailTotal)

	logPath := HistoryLogPath(path)
	existing, current, err := committedLines(path)
	if err != nil {
		return err
	}
	clean := true
	if existing != nil {
		// Bytes past the committed lines are an interrupted append.
		if info, statErr := os.Stat(logPath); statErr == nil && info.Size() != committedSize(current) {
			clean = false
		}
	}
	if existing != nil && clean && hasLinePrefix(rows, current) {
		if err := appendLines(logPath, rows[len(current):]); err != nil {
			return err
		}
	} else if err := writeLines(logPath, rows); err != nil {
		return err
	}
	return writeHeader(path, h, rows, system)
}

// committedLines returns the stored header (nil for a legacy or missing
// record) and its committed log lines.
func committedLines(path string) (*header, [][]byte, error) {
	h, _, err := readHeader(path)
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if !h.isLog {
		return nil, nil, nil
	}
	lines, err := readLines(HistoryLogPath(path), h.count())
	return h, lines, err
}

func hasLinePrefix(rows, prefix [][]byte) bool {
	if len(prefix) > len(rows) {
		return false
	}
	for i := range prefix {
		if !bytes.Equal(rows[i], prefix[i]) {
			return false
		}
	}
	return true
}

// Append adds messages to the history and applies patch (top-level fields,
// e.g. updated_at, revision) to the header. A system message replaces the
// stored system prompt instead of being logged.
func Append(path string, messages []json.RawMessage, patch map[string]json.RawMessage) error {
	return AppendIfVersion(path, messages, patch, nil)
}

// Check the expected version under the same per-file lock as Save/Append.
func AppendIfVersion(path string, messages []json.RawMessage, patch map[string]json.RawMessage, expected *Version) error {
	unlock := lockFor(path)
	defer unlock()
	if expected != nil {
		current, err := CurrentVersion(path)
		if err != nil {
			return err
		}
		if current != *expected {
			return ErrSourceChanged
		}
	}
	h, lines, err := committedLines(path)
	if err != nil {
		return err
	}
	if h == nil {
		// Missing or legacy: convert first, so the append lands in the log.
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if err := save(path, raw); err != nil {
			return err
		}
		if h, lines, err = committedLines(path); err != nil {
			return err
		}
		if h == nil {
			return errors.New("chatlog: record has no conversation_history")
		}
	}
	system, _ := h.get(fieldSystem)
	var add [][]byte
	for _, message := range messages {
		if isSystemRow(message) {
			system = append(json.RawMessage(nil), message...)
			continue
		}
		line, err := compactLine(message)
		if err != nil {
			return err
		}
		add = append(add, line)
	}
	logPath := HistoryLogPath(path)
	// Drop any uncommitted tail left by an interrupted append before adding.
	if info, statErr := os.Stat(logPath); statErr == nil && info.Size() > committedSize(lines) {
		if err := writeLines(logPath, lines); err != nil {
			return err
		}
	}
	if err := appendLines(logPath, add); err != nil {
		return err
	}
	keys := make([]string, 0, len(patch))
	for key := range patch {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if key == fieldHistory || key == fieldLog || key == fieldCount || key == fieldSystem || key == fieldTailFlag || key == fieldTailTotal {
			continue
		}
		h.set(key, patch[key])
	}
	return writeHeader(path, h, append(lines, add...), system)
}

func committedSize(lines [][]byte) int64 {
	var size int64
	for _, line := range lines {
		size += int64(len(line)) + 1
	}
	return size
}

func writeHeader(path string, h *header, lines [][]byte, system json.RawMessage) error {
	h.set(fieldLog, json.RawMessage(`"`+formatMarker+`"`))
	h.set(fieldCount, json.RawMessage(fmt.Sprint(len(lines))))
	if system != nil {
		h.set(fieldSystem, system)
	}
	// Recent context inline for direct readers, bounded by rows and bytes.
	start, size := len(lines), 0
	for start > 0 && len(lines)-start < headerTailRows && size+len(lines[start-1]) <= headerTailBytes {
		start--
		size += len(lines[start]) + 1
	}
	var tail bytes.Buffer
	tail.WriteByte('[')
	for i, line := range lines[start:] {
		if i > 0 {
			tail.WriteByte(',')
		}
		tail.Write(line)
	}
	tail.WriteByte(']')
	h.set(fieldHistory, tail.Bytes())
	h.set(fieldTailFlag, json.RawMessage("true"))
	h.set(fieldTailTotal, json.RawMessage(fmt.Sprint(len(lines))))
	return writeAtomic(path, h.encode())
}

func appendLines(path string, lines [][]byte) error {
	if len(lines) == 0 {
		return nil
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	for _, line := range lines {
		buf.Write(line)
		buf.WriteByte('\n')
	}
	if _, err := file.Write(buf.Bytes()); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func writeLines(path string, lines [][]byte) error {
	var buf bytes.Buffer
	for _, line := range lines {
		buf.Write(line)
		buf.WriteByte('\n')
	}
	return writeAtomic(path, buf.Bytes())
}

func writeAtomic(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Chmod(0o644); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// Remove deletes a conversation's header and log.
func Remove(path string) error {
	unlock := lockFor(path)
	defer unlock()
	if err := os.Remove(HistoryLogPath(path)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Remove(path)
}

// Move renames a conversation's header and log together.
func Move(from, to string) error {
	unlock := lockFor(from)
	defer unlock()
	if _, err := os.Stat(HistoryLogPath(from)); err == nil {
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		if err := os.Rename(HistoryLogPath(from), HistoryLogPath(to)); err != nil {
			return err
		}
	}
	return os.Rename(from, to)
}

// Fingerprint identifies a history's content, for tests and diagnostics.
func Fingerprint(record []byte) (string, error) {
	var rows [][]byte
	if _, _, err := decodeRecord(bytes.NewReader(record), func(raw json.RawMessage) error {
		line, err := compactLine(raw)
		rows = append(rows, line)
		return err
	}); err != nil {
		return "", err
	}
	sum := sha256.New()
	for _, row := range rows {
		sum.Write(row)
		sum.Write([]byte{'\n'})
	}
	return fmt.Sprintf("%x", sum.Sum(nil)), nil
}
