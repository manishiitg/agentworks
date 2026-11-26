package agycli

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// AgyDurableAck is the CLI-record half of a live-input receipt. AGY's
// SQLite user step has no timestamp, so RowTimestamp remains zero.
type AgyDurableAck struct {
	Latency      time.Duration
	ProofPath    string
	RowTimestamp time.Time
}

type agyPendingDurableAck struct {
	message        string
	conversationID string
	baselineIdx    int
	occurrence     int
	sentAt         time.Time
	claimed        bool
}

const agyDurableAckBudget = 90 * time.Second

func agyStashDurableAck(session *agyInteractiveSession, message, conversationID string, baselineIdx int) {
	session.durableMu.Lock()
	defer session.durableMu.Unlock()
	now := time.Now()
	kept := session.pendingDurable[:0]
	for _, receipt := range session.pendingDurable {
		if now.Sub(receipt.sentAt) < 10*time.Minute {
			kept = append(kept, receipt)
		}
	}
	occurrence := 1
	for _, receipt := range kept {
		if receipt.message == message && receipt.conversationID == conversationID && receipt.baselineIdx == baselineIdx {
			occurrence++
		}
	}
	kept = append(kept, agyPendingDurableAck{
		message: message, conversationID: conversationID, baselineIdx: baselineIdx,
		occurrence: occurrence, sentAt: now,
	})
	if len(kept) > 16 {
		kept = append([]agyPendingDurableAck(nil), kept[len(kept)-16:]...)
	}
	session.pendingDurable = kept
}

func agyTakeDurableAck(session *agyInteractiveSession, message string) (agyPendingDurableAck, bool) {
	session.durableMu.Lock()
	defer session.durableMu.Unlock()
	for i := range session.pendingDurable {
		receipt := &session.pendingDurable[i]
		if !receipt.claimed && receipt.message == message && time.Since(receipt.sentAt) < 10*time.Minute {
			receipt.claimed = true
			return *receipt, true
		}
	}
	return agyPendingDurableAck{}, false
}

// AwaitAgyInputDurable confirms that the CLI saved the exact live-input
// message as a new type-14 user row after its pre-send baseline. A pane echo
// cannot confirm it: a draft echoes before Enter, and an old echo can remain
// visible after a failed send. Repeated identical sends need distinct rows.
func AwaitAgyInputDurable(ctx context.Context, ownerSessionID, message string, timeout time.Duration) (AgyDurableAck, error) {
	session, ok := activeAgyInteractiveSession(ownerSessionID)
	if !ok {
		return AgyDurableAck{}, fmt.Errorf("no agy interactive session for owner %q", ownerSessionID)
	}
	receipt, ok := agyTakeDurableAck(session, message)
	if !ok {
		return AgyDurableAck{}, fmt.Errorf("no pending agy live-input receipt for owner %q", ownerSessionID)
	}
	if timeout <= 0 {
		timeout = agyDurableAckBudget
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		conversationID := receipt.conversationID
		if conversationID == "" {
			conversationID = agyConversationIDFromPane(ctx, session.tmuxSessionName)
		}
		if conversationID == "" {
			conversationID = agyDiscoverConversationID(session.createdAt, message, session.transcriptHome)
		}
		if conversationID != "" {
			count, err := agyCountUserSteps(conversationID, receipt.baselineIdx, message, session.transcriptHome)
			if err == nil && count >= receipt.occurrence {
				home, err := agyHome(session.transcriptHome)
				if err != nil {
					return AgyDurableAck{}, err
				}
				return AgyDurableAck{
					Latency:   time.Since(receipt.sentAt),
					ProofPath: filepath.Join(home, ".gemini", "antigravity-cli", "conversations", conversationID+".db"),
				}, nil
			}
		}
		select {
		case <-ctx.Done():
			return AgyDurableAck{}, ctx.Err()
		case <-deadline.C:
			return AgyDurableAck{}, fmt.Errorf("agy live input has no matching SQLite user step after %s", timeout)
		case <-tick.C:
		}
	}
}

func agyCountUserSteps(conversationID string, sinceIdx int, message string, accountHome ...string) (int, error) {
	indices, err := agyMatchingUserStepIndices(conversationID, sinceIdx, message, accountHome...)
	return len(indices), err
}

func agyMatchingUserStepIndices(conversationID string, sinceIdx int, message string, accountHome ...string) ([]int, error) {
	home, err := agyHome(accountHome...)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(home, ".gemini", "antigravity-cli", "conversations", conversationID+".db")
	tmpPath, cleanup, err := agyCopyDBFile(path)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	db, err := sql.Open("sqlite", "file:"+tmpPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	rows, err := db.QueryContext(context.Background(), `SELECT idx, step_payload FROM steps WHERE idx > ? AND step_type = ? ORDER BY idx`, sinceIdx, agyStepUser)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var indices []int
	for rows.Next() {
		var idx int
		var payload []byte
		if err := rows.Scan(&idx, &payload); err != nil {
			return indices, err
		}
		stored, _, ok := agyTranscriptStepText(agyStepUser, payload)
		if ok && strings.TrimSpace(stored) == strings.TrimSpace(message) {
			indices = append(indices, idx)
		}
	}
	return indices, rows.Err()
}
