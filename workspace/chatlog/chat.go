package chatlog

// Chat folders: Crew chats and the AgentWorks home chat.
//
//	<chat root>/session-<id>/header.json   small: who, title, runtime, terminal
//	                                       snapshots, counts, updated_at
//	<chat root>/session-<id>/turns.jsonl   append-only, one JSON line per turn
//
// A save appends one line and rewrites the small header (temp file + rename,
// both fsynced), so it costs the same for a short chat and a long one.
//
// Turns have a stable id (the turn's query id) and a time. A later line with
// the same id replaces the earlier one on read (last line wins): that is how a
// turn still running when it was first written is amended (steering, a late
// final answer, late UI events). Nothing old is rewritten in place. Lines are
// ordered by time, then by where they first appear.
//
// A "boundary" turn marks where rebuilt model context starts (compaction,
// clear). Readers that rebuild model history use ContextStart.
//
// One writer per chat: an in-process mutex plus an OS file lock on turns.jsonl,
// so the workspace server and agent-server tools (handoff import, converter)
// never interleave. Readers take no lock: a torn last line (a crash mid-append)
// is skipped and logged; the next append moves its bytes to turns.jsonl.torn.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	HeaderFile   = "header.json"
	TurnsFile    = "turns.jsonl"
	TornSuffix   = ".torn"
	KindBoundary = "boundary"

	// ModeReplace stores the turn as given; ModeExtend adds the given
	// messages and UI events to the stored turn with the same id.
	ModeReplace = "replace"
	ModeExtend  = "extend"

	chatFormat = "chat-v1"
	// Ids of the newest turns kept in the header, so an amend of a recent
	// turn keeps its time and counts without reading the log.
	recentTurnIDs = 50
	// The assembled record keeps the newest UI events, as before.
	maxRecordUIEvents = 200

	hFormat   = "format"
	hTurns    = "turn_count"
	hMessages = "message_count"
	hRecent   = "recent_turns"
	hSystem   = "system"
	hUpdated  = "updated_at"
	// Written with every append, so a poll with nothing new reads no lines:
	// the newest event sequence, the newest turn time and the size of
	// turns.jsonl after that append (a bigger file means a later append whose
	// header update was lost, so readers scan).
	hNewestSeq  = "newest_seq"
	hNewestAt   = "newest_at"
	hTurnsBytes = "turns_bytes"
)

var internalHeaderFields = map[string]bool{hFormat: true, hRecent: true, hSystem: true, hNewestSeq: true, hNewestAt: true, hTurnsBytes: true}

// Turn is one line of turns.jsonl.
type Turn struct {
	ID   string `json:"id"`
	At   string `json:"at"`
	Kind string `json:"kind,omitempty"`
	// SenderUserID is the person whose message started this turn when that is
	// someone other than the owner (a shared agent's sender), from the turn's
	// user_message events; empty when none or mixed. Readers filter on it.
	SenderUserID string            `json:"sender_user_id,omitempty"`
	Messages     []json.RawMessage `json:"messages,omitempty"`
	UIEvents     []json.RawMessage `json:"ui_events,omitempty"`
}

type recentTurn struct {
	ID       string `json:"id"`
	At       string `json:"at"`
	Messages int    `json:"messages"`
}

// Chat is a header plus its effective turns (last line per id, time order).
type Chat struct {
	Header map[string]json.RawMessage
	Turns  []Turn
	// TotalTurns counts every turn of the chat, also when Turns is a page.
	TotalTurns int
	// TornBytes is the size of an incomplete last line that was skipped.
	TornBytes int
}

// SaveStats describes one append for the caller's log line.
type SaveStats struct {
	Appended     int
	Bytes        int
	TurnCount    int
	MessageCount int
	TornMoved    int
	HeaderBytes  int
}

// IsChatHeaderPath reports whether path is a chat folder's header.json under
// a chat store (builder/ of a Crew, or a chat_history root).
func IsChatHeaderPath(path string) bool {
	slashed := filepath.ToSlash(path)
	if pathBase(slashed) != HeaderFile {
		return false
	}
	folder := pathBase(strings.TrimSuffix(slashed, "/"+HeaderFile))
	if !strings.HasPrefix(folder, "session-") || len(folder) <= len("session-") {
		return false
	}
	return strings.Contains(slashed, "/builder/") || strings.Contains(slashed, "/chat_history/") ||
		strings.HasPrefix(slashed, "builder/") || strings.HasPrefix(slashed, "chat_history/")
}

func pathBase(slashed string) string {
	if i := strings.LastIndex(slashed, "/"); i >= 0 {
		return slashed[i+1:]
	}
	return slashed
}

// ChatDir is the folder that holds a chat's header.json.
func ChatDir(headerPath string) string { return filepath.Dir(headerPath) }

var (
	chatLocksMu sync.Mutex
	chatLocks   = map[string]*sync.Mutex{}
)

// lockChat takes the in-process lock and the OS file lock for one chat.
func lockChat(dir string) (func(), *os.File, error) {
	dir = filepath.Clean(dir)
	chatLocksMu.Lock()
	mu, ok := chatLocks[dir]
	if !ok {
		mu = &sync.Mutex{}
		chatLocks[dir] = mu
	}
	chatLocksMu.Unlock()
	mu.Lock()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		mu.Unlock()
		return nil, nil, err
	}
	file, err := os.OpenFile(filepath.Join(dir, TurnsFile), os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		mu.Unlock()
		return nil, nil, err
	}
	if err := lockFile(file); err != nil {
		file.Close()
		mu.Unlock()
		return nil, nil, err
	}
	return func() {
		unlockFile(file)
		file.Close()
		mu.Unlock()
	}, file, nil
}

func readHeaderMap(dir string) (map[string]json.RawMessage, error) {
	raw, err := os.ReadFile(filepath.Join(dir, HeaderFile))
	if os.IsNotExist(err) {
		return map[string]json.RawMessage{}, nil
	}
	if err != nil {
		return nil, err
	}
	header := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &header); err != nil {
		return nil, fmt.Errorf("chatlog: header %s: %w", dir, err)
	}
	return header, nil
}

func headerInt(header map[string]json.RawMessage, key string) int {
	var n int
	_ = json.Unmarshal(header[key], &n)
	return n
}

func headerRecent(header map[string]json.RawMessage) []recentTurn {
	var recent []recentTurn
	_ = json.Unmarshal(header[hRecent], &recent)
	return recent
}

func isSystemMessage(raw json.RawMessage) bool {
	var row struct {
		Role string `json:"Role"`
	}
	return json.Unmarshal(raw, &row) == nil && row.Role == "system"
}

// repairTornTail moves an incomplete last line to turns.jsonl.torn so the
// next append starts on a line boundary. The bytes are kept, never dropped.
func repairTornTail(file *os.File, dir string) (int, error) {
	info, err := file.Stat()
	if err != nil || info.Size() == 0 {
		return 0, err
	}
	size := info.Size()
	last := make([]byte, 1)
	if _, err := file.ReadAt(last, size-1); err != nil {
		return 0, err
	}
	if last[0] == '\n' {
		return 0, nil
	}
	cut := lastNewline(file, size)
	torn := make([]byte, size-cut)
	if _, err := file.ReadAt(torn, cut); err != nil {
		return 0, err
	}
	sidecar, err := os.OpenFile(filepath.Join(dir, TurnsFile+TornSuffix), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return 0, err
	}
	_, writeErr := sidecar.Write(append(torn, '\n'))
	syncErr := sidecar.Sync()
	sidecar.Close()
	if writeErr != nil || syncErr != nil {
		return 0, errors.Join(writeErr, syncErr)
	}
	if err := file.Truncate(cut); err != nil {
		return 0, err
	}
	log.Printf("[CHAT_HISTORY] torn last line moved dir=%s bytes=%d to=%s", dir, len(torn), TurnsFile+TornSuffix)
	return len(torn), file.Sync()
}

// lastNewline returns the offset just past the last '\n' before size (0 if none).
func lastNewline(file io.ReaderAt, size int64) int64 {
	const chunk = 64 << 10
	buf := make([]byte, chunk)
	for end := size; end > 0; {
		start := end - chunk
		if start < 0 {
			start = 0
		}
		n, _ := file.ReadAt(buf[:end-start], start)
		if i := bytes.LastIndexByte(buf[:n], '\n'); i >= 0 {
			return start + int64(i) + 1
		}
		end = start
	}
	return 0
}

// AppendTurns adds turns to a chat and applies patch to its header (a null
// value removes a field). With no turns it only patches the header. ModeExtend
// adds each turn's messages and events to the stored turn with the same id.
func AppendTurns(dir string, turns []Turn, patch map[string]json.RawMessage, mode string) (SaveStats, error) {
	var stats SaveStats
	unlock, file, err := lockChat(dir)
	if err != nil {
		return stats, err
	}
	defer unlock()
	if stats.TornMoved, err = repairTornTail(file, dir); err != nil {
		return stats, err
	}
	header, err := readHeaderMap(dir)
	if err != nil {
		return stats, err
	}
	recent := headerRecent(header)
	turnCount, messageCount := headerInt(header, hTurns), headerInt(header, hMessages)
	if raw, ok := patch[hSystem]; ok {
		header[hSystem] = raw
	}

	var buf bytes.Buffer
	for _, turn := range turns {
		turn.ID = strings.TrimSpace(turn.ID)
		if turn.ID == "" {
			return stats, errors.New("chatlog: turn id required")
		}
		messages := turn.Messages[:0:0]
		for _, message := range turn.Messages {
			if isSystemMessage(message) {
				header[hSystem] = message
				continue
			}
			messages = append(messages, compactRaw(message))
		}
		turn.Messages = messages
		for i := range turn.UIEvents {
			turn.UIEvents[i] = compactRaw(turn.UIEvents[i])
		}
		known := -1
		for i := range recent {
			if recent[i].ID == turn.ID {
				known = i
			}
		}
		if mode == ModeExtend && known >= 0 {
			if stored, ok, err := findTurn(dir, turn.ID); err != nil {
				return stats, err
			} else if ok {
				turn = extendTurn(stored, turn)
			}
		}
		if known >= 0 {
			turn.At = recent[known].At // an amend keeps the turn's place
			messageCount += len(turn.Messages) - recent[known].Messages
			recent[known].Messages = len(turn.Messages)
		} else {
			if strings.TrimSpace(turn.At) == "" {
				turn.At = time.Now().UTC().Format(time.RFC3339Nano)
			}
			turnCount++
			messageCount += len(turn.Messages)
			recent = append(recent, recentTurn{ID: turn.ID, At: turn.At, Messages: len(turn.Messages)})
		}
		line, err := json.Marshal(turn)
		if err != nil {
			return stats, err
		}
		buf.Write(line)
		buf.WriteByte('\n')
		stats.Appended++
	}
	if buf.Len() > 0 {
		if _, err := file.Write(buf.Bytes()); err != nil {
			return stats, err
		}
		if err := file.Sync(); err != nil {
			return stats, err
		}
	}
	stats.Bytes = buf.Len()

	newestSeq, newestAt := headerInt(header, hNewestSeq), ""
	_ = json.Unmarshal(header[hNewestAt], &newestAt)
	for _, turn := range turns {
		for _, raw := range turn.UIEvents {
			if seq := eventSequence(raw); seq > newestSeq {
				newestSeq = seq
			}
		}
		if laterTime(turn.At, newestAt) {
			newestAt = turn.At
		}
	}
	if info, err := file.Stat(); err == nil {
		header[hTurnsBytes] = mustJSON(info.Size())
	}
	header[hNewestSeq] = mustJSON(newestSeq)
	if newestAt != "" {
		header[hNewestAt] = mustJSON(newestAt)
	}
	for key, value := range patch {
		if internalHeaderFields[key] || key == hTurns || key == hMessages {
			continue
		}
		if string(value) == "null" {
			delete(header, key)
			continue
		}
		header[key] = value
	}
	if len(recent) > recentTurnIDs {
		recent = recent[len(recent)-recentTurnIDs:]
	}
	header[hFormat] = mustJSON(chatFormat)
	header[hTurns] = mustJSON(turnCount)
	header[hMessages] = mustJSON(messageCount)
	header[hRecent] = mustJSON(recent)
	if _, ok := patch[hUpdated]; !ok {
		header[hUpdated] = mustJSON(time.Now().UTC().Format(time.RFC3339))
	}
	encoded, err := json.Marshal(header)
	if err != nil {
		return stats, err
	}
	stats.HeaderBytes = len(encoded)
	stats.TurnCount, stats.MessageCount = turnCount, messageCount
	return stats, writeFileSynced(filepath.Join(dir, HeaderFile), encoded)
}

func extendTurn(stored, add Turn) Turn {
	stored.Messages = append(append([]json.RawMessage(nil), stored.Messages...), add.Messages...)
	seen := map[string]bool{}
	for _, event := range stored.UIEvents {
		if id := eventID(event); id != "" {
			seen[id] = true
		}
	}
	stored.UIEvents = append([]json.RawMessage(nil), stored.UIEvents...)
	for _, event := range add.UIEvents {
		if id := eventID(event); id == "" || !seen[id] {
			stored.UIEvents = append(stored.UIEvents, event)
		}
	}
	if add.Kind != "" {
		stored.Kind = add.Kind
	}
	stored.SenderUserID = TurnSender(stored.UIEvents)
	return stored
}

func eventID(raw json.RawMessage) string {
	var event struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &event)
	return event.ID
}

func compactRaw(raw json.RawMessage) json.RawMessage {
	var buf bytes.Buffer
	if json.Compact(&buf, raw) != nil {
		return raw
	}
	return buf.Bytes()
}

func mustJSON(value interface{}) json.RawMessage {
	raw, _ := json.Marshal(value)
	return raw
}

func writeFileSynced(path string, content []byte) error {
	dir := filepath.Dir(path)
	temporary, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
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
	if err := os.Rename(name, path); err != nil {
		return err
	}
	syncDir(dir)
	return nil
}

// findTurn returns the stored (last) line for id, reading from the end.
func findTurn(dir, id string) (Turn, bool, error) {
	var found Turn
	ok := false
	err := scanBackward(filepath.Join(dir, TurnsFile), func(turn Turn) bool {
		if turn.ID == id {
			found, ok = turn, true
			return false
		}
		return true
	})
	return found, ok, err
}

// scanBackward calls fn for each complete line from the newest to the oldest
// until fn returns false. An incomplete last line is skipped.
func scanBackward(path string, fn func(Turn) bool) error {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	end := lastNewline(file, info.Size())
	if end != info.Size() {
		log.Printf("[CHAT_HISTORY] skipped torn last line file=%s bytes=%d", path, info.Size()-end)
	}
	const chunk = 256 << 10
	var carry []byte
	for end > 0 {
		start := end - chunk
		if start < 0 {
			start = 0
		}
		block := make([]byte, end-start, int(end-start)+len(carry))
		if _, err := file.ReadAt(block, start); err != nil && err != io.EOF {
			return err
		}
		block = append(block, carry...)
		lines := bytes.Split(block, []byte{'\n'})
		// lines[0] may be partial unless we reached the file start.
		first := 1
		if start == 0 {
			first = 0
		}
		for i := len(lines) - 1; i >= first; i-- {
			line := bytes.TrimSpace(lines[i])
			if len(line) == 0 {
				continue
			}
			var turn Turn
			if json.Unmarshal(line, &turn) != nil {
				log.Printf("[CHAT_HISTORY] skipped unreadable line file=%s bytes=%d", path, len(line))
				continue
			}
			if !fn(turn) {
				return nil
			}
		}
		if start == 0 {
			break
		}
		carry = append([]byte(nil), lines[0]...)
		end = start
	}
	return nil
}

// ReadChat returns the chat's header and its turns; lastTurns > 0 returns only
// the newest that many turns (a page). A missing chat is os.ErrNotExist.
func ReadChat(dir string, lastTurns int) (*Chat, error) {
	raw, err := os.ReadFile(filepath.Join(dir, HeaderFile))
	if err != nil {
		return nil, err
	}
	chat := &Chat{Header: map[string]json.RawMessage{}}
	if err := json.Unmarshal(raw, &chat.Header); err != nil {
		return nil, fmt.Errorf("chatlog: header %s: %w", dir, err)
	}
	chat.TotalTurns = headerInt(chat.Header, hTurns)
	path := filepath.Join(dir, TurnsFile)
	if lastTurns > 0 {
		// Lines are in append order, turns in time order: an amend of an old
		// turn is appended late but keeps its old time. Keep scanning until a
		// line is older than the newest lastTurns turns found so far.
		seen := map[string]bool{}
		err = scanBackward(path, func(turn Turn) bool {
			if seen[turn.ID] {
				return true
			}
			if len(chat.Turns) >= lastTurns {
				sortTurns(chat.Turns)
				if laterTime(chat.Turns[len(chat.Turns)-lastTurns].At, turn.At) {
					return false
				}
			}
			seen[turn.ID] = true
			chat.Turns = append(chat.Turns, turn)
			return true
		})
		if err != nil {
			return nil, err
		}
		sortTurns(chat.Turns)
		if len(chat.Turns) > lastTurns {
			chat.Turns = chat.Turns[len(chat.Turns)-lastTurns:]
		}
		return chat, nil
	}
	turns, torn, err := readAllTurns(path)
	if err != nil {
		return nil, err
	}
	chat.Turns, chat.TornBytes = turns, torn
	if chat.TotalTurns < len(turns) {
		chat.TotalTurns = len(turns)
	}
	return chat, nil
}

func readAllTurns(path string) ([]Turn, int, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	reader := bufio.NewReaderSize(file, 1<<20)
	index := map[string]int{}
	var turns []Turn
	torn := 0
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 && line[len(line)-1] != '\n' {
			torn = len(line)
			log.Printf("[CHAT_HISTORY] skipped torn last line file=%s bytes=%d", path, torn)
			break
		}
		if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
			var turn Turn
			if json.Unmarshal(trimmed, &turn) != nil {
				log.Printf("[CHAT_HISTORY] skipped unreadable line file=%s bytes=%d", path, len(trimmed))
			} else if i, ok := index[turn.ID]; ok {
				turns[i] = turn // last line per id wins
			} else {
				index[turn.ID] = len(turns)
				turns = append(turns, turn)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, 0, err
		}
	}
	sortTurns(turns)
	return turns, torn, nil
}

func sortTurns(turns []Turn) {
	sort.SliceStable(turns, func(i, j int) bool {
		a, aErr := time.Parse(time.RFC3339Nano, turns[i].At)
		b, bErr := time.Parse(time.RFC3339Nano, turns[j].At)
		if aErr != nil || bErr != nil {
			return false
		}
		return a.Before(b)
	})
}

// TurnIDs returns the ids of every stored turn (for sync).
func TurnIDs(dir string) (map[string]bool, error) {
	turns, _, err := readAllTurns(filepath.Join(dir, TurnsFile))
	if err != nil {
		return nil, err
	}
	ids := make(map[string]bool, len(turns))
	for _, turn := range turns {
		ids[turn.ID] = true
	}
	return ids, nil
}

// AppendMissingTurns appends the turns whose ids the chat does not have yet
// (sync and retried imports) and applies patch. It returns how many it added.
func AppendMissingTurns(dir string, turns []Turn, patch map[string]json.RawMessage) (SaveStats, error) {
	ids, err := TurnIDs(dir)
	if err != nil {
		return SaveStats{}, err
	}
	missing := make([]Turn, 0, len(turns))
	for _, turn := range turns {
		if !ids[turn.ID] {
			missing = append(missing, turn)
		}
	}
	return AppendTurns(dir, missing, patch, ModeReplace)
}

// ContextStart is the index of the first turn after the last boundary turn
// (the boundary itself is included, since it carries the compacted context).
func ContextStart(turns []Turn) int {
	for i := len(turns) - 1; i >= 0; i-- {
		if turns[i].Kind == KindBoundary {
			return i
		}
	}
	return 0
}

// Record assembles the chat as one conversation record: the header's fields,
// conversation_history (system prompt, then every turn's messages) and the
// newest UI events. history_turns_total and history_context_start are added;
// a page (lastTurns > 0) is marked history_page. Never write a record back.
// With lines, a page also carries its raw turns as history_page_lines.
func Record(dir string, lastTurns int, lines bool) ([]byte, error) {
	chat, err := ReadChat(dir, lastTurns)
	if err != nil {
		return nil, err
	}
	return chat.Record(lastTurns > 0, lines), nil
}

// Record encodes a read chat; see the package-level Record.
func (chat *Chat) Record(page, lines bool) []byte {
	out := make(map[string]json.RawMessage, len(chat.Header)+6)
	for key, value := range chat.Header {
		if !internalHeaderFields[key] {
			out[key] = value
		}
	}
	history := make([]json.RawMessage, 0, 64)
	if system, ok := chat.Header[hSystem]; ok && string(system) != "null" {
		history = append(history, system)
	}
	contextStart := len(history)
	boundary := ContextStart(chat.Turns)
	var events []json.RawMessage
	for i, turn := range chat.Turns {
		if i == boundary && boundary > 0 {
			contextStart = len(history)
		}
		history = append(history, turn.Messages...)
		events = append(events, turn.UIEvents...)
	}
	if len(events) > maxRecordUIEvents {
		events = events[len(events)-maxRecordUIEvents:]
	}
	out["conversation_history"] = mustJSON(history)
	if len(events) > 0 {
		out["ui_events"] = mustJSON(events)
	}
	out["history_turns_total"] = mustJSON(chat.TotalTurns)
	out["history_context_start"] = mustJSON(contextStart)
	if page {
		out["history_page"] = mustJSON(true)
		out["history_page_turns"] = mustJSON(len(chat.Turns))
		if lines {
			out["history_page_lines"] = mustJSON(chat.Turns)
		}
	}
	encoded, _ := json.Marshal(out)
	return encoded
}

// RemoveChat deletes a chat folder under the chat's writer lock.
func RemoveChat(dir string) error {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}
	unlock, file, err := lockChat(dir)
	if err != nil {
		return err
	}
	// Remove while holding the lock; the handle is released first on
	// Windows, where an open file cannot be deleted.
	if isWindows {
		unlockFile(file)
		file.Close()
		err = os.RemoveAll(dir)
		chatLockRelease(dir)
		return err
	}
	defer unlock()
	return os.RemoveAll(dir)
}

func chatLockRelease(dir string) {
	chatLocksMu.Lock()
	mu := chatLocks[filepath.Clean(dir)]
	chatLocksMu.Unlock()
	if mu != nil {
		mu.Unlock()
	}
}

// SplitHistory turns a flat conversation history into turns: a new turn at
// each person message, a leading system prompt returned on its own. Turn ids
// are idPrefix-<n>, and times count up from start so the order is kept.
// Used when a whole history arrives at once (conversion, imports).
func SplitHistory(history []json.RawMessage, idPrefix string, start time.Time) (json.RawMessage, []Turn) {
	var system json.RawMessage
	var turns []Turn
	for _, message := range history {
		var row struct {
			Role string `json:"Role"`
		}
		_ = json.Unmarshal(message, &row)
		if row.Role == "system" {
			system = message
			continue
		}
		if len(turns) == 0 || row.Role == "human" || row.Role == "user" {
			n := len(turns)
			turns = append(turns, Turn{
				ID: fmt.Sprintf("%s-%d", idPrefix, n+1),
				At: start.UTC().Add(time.Duration(n) * time.Millisecond).Format(time.RFC3339Nano),
			})
		}
		last := &turns[len(turns)-1]
		last.Messages = append(last.Messages, message)
	}
	return system, turns
}

// TurnSender returns the one sender_user_id carried by a turn's user_message
// events, or "" when there is none or more than one.
func TurnSender(events []json.RawMessage) string {
	sender := ""
	for _, raw := range events {
		var event struct {
			Type string `json:"type"`
			Data struct {
				Data struct {
					Metadata struct {
						SenderUserID string `json:"sender_user_id"`
					} `json:"metadata"`
				} `json:"data"`
			} `json:"data"`
		}
		if json.Unmarshal(raw, &event) != nil || event.Type != "user_message" {
			continue
		}
		id := event.Data.Data.Metadata.SenderUserID
		if id == "" || (sender != "" && id != sender) {
			return ""
		}
		sender = id
	}
	return sender
}

// ScanTurnsBackward calls fn with each stored line from the newest to the
// oldest until fn returns false. Several lines can carry one turn id; the
// first one seen is that turn's current line. A torn last line is skipped.
func ScanTurnsBackward(dir string, fn func(Turn) bool) error {
	return scanBackward(filepath.Join(dir, TurnsFile), fn)
}

func eventSequence(raw json.RawMessage) int {
	var head struct {
		Sequence int `json:"sequence"`
	}
	_ = json.Unmarshal(raw, &head)
	return head.Sequence
}

// laterTime reports whether RFC3339 time a is after b (an unreadable b is
// treated as older).
func laterTime(a, b string) bool {
	ta, errA := time.Parse(time.RFC3339Nano, a)
	if errA != nil {
		return false
	}
	tb, errB := time.Parse(time.RFC3339Nano, b)
	return errB != nil || ta.After(tb)
}

// EventsAfter returns the UI events of the turns that hold events newer than
// sequence after, each turn's current line whole (its earlier events too),
// oldest turn first; keep, when set, skips lines (for example another
// sender's). scanned is how many lines were read: none when the header says
// nothing is newer and turns.jsonl has not grown since. Lines are in append
// order and turns in time order (an amend keeps its old time), so the scan
// stops only at a line older than the oldest turn already found with newer
// events.
func EventsAfter(dir string, after int, keep func(Turn) bool) ([]json.RawMessage, int, error) {
	header, err := readHeaderMap(dir)
	if err != nil {
		return nil, 0, err
	}
	path := filepath.Join(dir, TurnsFile)
	if _, ok := header[hNewestSeq]; ok && headerInt(header, hNewestSeq) <= after {
		if info, err := os.Stat(path); err != nil || info.Size() == int64(headerInt(header, hTurnsBytes)) {
			return nil, 0, nil
		}
	}
	seen := map[string]bool{}
	var kept []Turn
	oldestNew := ""
	scanned := 0
	err = scanBackward(path, func(turn Turn) bool {
		scanned++
		if seen[turn.ID] {
			return true
		}
		seen[turn.ID] = true
		newest := 0
		for _, raw := range turn.UIEvents {
			if seq := eventSequence(raw); seq > newest {
				newest = seq
			}
		}
		if newest <= after {
			return oldestNew == "" || !laterTime(oldestNew, turn.At)
		}
		if oldestNew == "" || laterTime(oldestNew, turn.At) {
			oldestNew = turn.At
		}
		if keep == nil || keep(turn) {
			kept = append(kept, turn)
		}
		return true
	})
	if err != nil {
		return nil, scanned, err
	}
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}
	sortTurns(kept)
	var events []json.RawMessage
	for _, turn := range kept {
		events = append(events, turn.UIEvents...)
	}
	return events, scanned, nil
}
