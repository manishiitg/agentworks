package agycli

import (
	"sync"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// agy compacts its own context (CHECKPOINT steps in its conversation store).
// The ACP stream does not report them, so after each turn the chat reads the
// ones agy finished during it and shows them like any app's compaction.
var agyACPCompactionMarks = struct {
	sync.Mutex
	next map[string]int // native session -> first step index not yet reported
}{next: map[string]int{}}

// agyACPCompactionFloor fixes, before a resumed conversation's first turn in
// this process, where reporting starts, so older compactions are not replayed.
func agyACPCompactionFloor(home, session string) {
	if session == "" {
		return
	}
	agyACPCompactionMarks.Lock()
	_, known := agyACPCompactionMarks.next[session]
	agyACPCompactionMarks.Unlock()
	if known {
		return
	}
	next := 0
	if path, info, err := agyACPConversationFile(home, session); err == nil && info != nil {
		if db, err := agyACPOpenLog(path); err == nil {
			var max *int
			if db.QueryRow(`SELECT MAX(idx) FROM steps`).Scan(&max) == nil && max != nil {
				next = *max + 1
			}
			_ = db.Close()
		}
	}
	agyACPCompactionMarks.Lock()
	if _, known := agyACPCompactionMarks.next[session]; !known {
		agyACPCompactionMarks.next[session] = next
	}
	agyACPCompactionMarks.Unlock()
}

// agyACPTurnCompactions returns the compactions agy finished since the last
// report for this conversation. A compaction still running is reported once
// it finishes, after a later turn.
func agyACPTurnCompactions(home, session string) []llmtypes.ContextCompaction {
	if session == "" {
		return nil
	}
	path, info, err := agyACPConversationFile(home, session)
	if err != nil || info == nil {
		return nil
	}
	db, err := agyACPOpenLog(path)
	if err != nil {
		return nil
	}
	defer func() { _ = db.Close() }()
	agyACPCompactionMarks.Lock()
	defer agyACPCompactionMarks.Unlock()
	from := agyACPCompactionMarks.next[session]
	rows, err := db.Query(`SELECT idx, status, step_payload FROM steps WHERE step_type = ? AND idx >= ? ORDER BY idx`, agyStepCheckpoint, from)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	var out []llmtypes.ContextCompaction
	next := from
	for rows.Next() {
		var idx, status int
		var payload []byte
		if rows.Scan(&idx, &status, &payload) != nil {
			return out
		}
		if status != agyStepStatusDone {
			break
		}
		if compaction, ok := agyCheckpointCompaction(session, idx, payload); ok {
			out = append(out, compaction)
		}
		next = idx + 1
	}
	agyACPCompactionMarks.next[session] = next
	return out
}
