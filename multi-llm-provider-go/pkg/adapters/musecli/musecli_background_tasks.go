package musecli

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"strings"
)

// BackgroundTaskEvent is a committed native Muse task transition. Sequence is
// the native journal identity; the foreground GenerateContent stream may
// already be closed when this event appears.
type BackgroundTaskEvent struct {
	Sequence int64  `json:"sequence"`
	RunID    string `json:"run_id"`
	TaskID   string `json:"task_id"`
	Kind     string `json:"kind"`
	Message  string `json:"message,omitempty"`
}

type BackgroundTaskReader struct {
	path       string
	logRetry   museLogPathRetry
	owner      string
	nativeID   string
	offset     int64
	after      int64
	background map[string]bool
	terminal   map[string]bool
}

// NewBackgroundTaskReader begins after the supplied native sequence. It scans
// prior rows for task_backgrounded markers and parent run terminals so a
// reader started after foreground completion can correlate subsequent events.
func NewBackgroundTaskReader(nativeSessionID string, afterSequence int64) *BackgroundTaskReader {
	return NewBackgroundTaskReaderForOwner("", nativeSessionID, afterSequence)
}

// NewBackgroundTaskReaderForOwner resolves the isolated account data home
// from the adapter's owner-scoped persistent session when it is available.
// Path resolution is lazy so restored sessions can attach before Muse boots.
func NewBackgroundTaskReaderForOwner(ownerSessionID, nativeSessionID string, afterSequence int64) *BackgroundTaskReader {
	return &BackgroundTaskReader{owner: ownerSessionID, nativeID: nativeSessionID, after: afterSequence, background: make(map[string]bool), terminal: make(map[string]bool)}
}

// Snapshot the already known path/account home without doing directory IO.
func museBackgroundLogSource(ownerSessionID, nativeSessionID string) (string, string) {
	if ownerSessionID != "" {
		musePersistentPool.Lock()
		entry := musePersistentPool.m[ownerSessionID]
		if entry != nil && entry.nativeSessionID == nativeSessionID {
			path, home := entry.logPath, entry.accountDataHome
			musePersistentPool.Unlock()
			return path, home
		}
		musePersistentPool.Unlock()
	}
	return "", ""
}
func museBackgroundLogPath(ownerSessionID, nativeSessionID string) string {
	path, home := museBackgroundLogSource(ownerSessionID, nativeSessionID)
	if path != "" {
		return path
	}
	return museSessionLogPath(nativeSessionID, home)
}

// LatestNativeSequence is a pre-turn baseline for an existing Muse session.
func LatestNativeSequence(nativeSessionID string) int64 {
	return LatestNativeSequenceForOwner("", nativeSessionID)
}

func LatestNativeSequenceForOwner(ownerSessionID, nativeSessionID string) int64 {
	path := museBackgroundLogPath(ownerSessionID, nativeSessionID)
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	var sequence int64
	reader := bufio.NewReader(f)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			break
		}
		var row struct {
			Sequence int64 `json:"sequence"`
		}
		if json.Unmarshal(line, &row) == nil && row.Sequence > sequence {
			sequence = row.Sequence
		}
	}
	return sequence
}

// Poll consumes only newline-committed JSONL rows. The reader belongs to one
// Session watcher and is not safe for concurrent calls.
func (r *BackgroundTaskReader) Poll() ([]BackgroundTaskEvent, error) {
	if r == nil {
		return nil, nil
	}
	if r.path == "" {
		path, home := museBackgroundLogSource(r.owner, r.nativeID)
		if home == "" {
			home = museXDGDataHome()
		}
		if path == "" {
			path = r.logRetry.resolve(home+"\x00"+r.nativeID, func() string { return museSessionLogPath(r.nativeID, home) })
		}
		r.path = path
	}
	if r.path == "" {
		return nil, nil
	}
	f, err := os.Open(r.path)
	if os.IsNotExist(err) {
		r.path = ""
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if r.offset > info.Size() {
		r.offset = 0
		r.background = make(map[string]bool)
		r.terminal = make(map[string]bool)
	}
	if _, err := f.Seek(r.offset, io.SeekStart); err != nil {
		return nil, err
	}
	reader := bufio.NewReader(f)
	var result []BackgroundTaskEvent
	for {
		line, readErr := reader.ReadBytes('\n')
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return result, readErr
		}
		r.offset += int64(len(line))
		var row struct {
			Sequence int64  `json:"sequence"`
			Type     string `json:"payload_type"`
			Payload  struct {
				Kind  string `json:"kind"`
				RunID string `json:"run_id"`
				Event struct {
					Kind    string `json:"kind"`
					TaskID  string `json:"task_id"`
					Message string `json:"message"`
					Reason  string `json:"reason"`
					Chunk   string `json:"chunk"`
				} `json:"event"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &row) != nil || row.Type != "runtime.session" || row.Payload.RunID == "" {
			continue
		}
		e := row.Payload.Event
		if row.Payload.Kind == "run" && e.Kind == "terminal" {
			r.terminal[row.Payload.RunID] = true
		}
		if e.TaskID == "" {
			continue
		}
		key := row.Payload.RunID + ":" + e.TaskID
		if row.Payload.Kind == "run" && e.Kind == "task_backgrounded" {
			r.background[key] = true
		} else if row.Payload.Kind != "task" || (!r.background[key] && !(r.terminal[row.Payload.RunID] && museLateTaskEventKind(e.Kind))) {
			continue
		}
		if row.Sequence <= r.after {
			continue
		}
		// Output can include command results or secrets. Publish a bounded
		// preview; the native journal retains the full output.
		message := e.Message
		if message == "" {
			message = e.Reason
		}
		if message == "" && e.Kind == "output" {
			message = e.Chunk
		}
		message = strings.TrimSpace(message)
		if len(message) > 1024 {
			message = message[:1024]
		}
		result = append(result, BackgroundTaskEvent{Sequence: row.Sequence, RunID: row.Payload.RunID, TaskID: e.TaskID, Kind: e.Kind, Message: message})
	}
	return result, nil
}

func museLateTaskEventKind(kind string) bool {
	switch kind {
	case "status", "output", "completed", "failed", "cancelled", "rejected", "tool_output_ref":
		return true
	default:
		return false
	}
}
