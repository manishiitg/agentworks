package musecli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/internal/stalldiag"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type museRetainedProgress struct {
	mu            sync.Mutex
	turnStart     time.Time
	lastSeq       int64
	seenTools     map[string]bool
	endedTools    map[string]bool
	toolStartedAt map[string]time.Time
}

// ReadRetainedTurnProgressMessages exposes committed text and visible status
// summaries even while the TUI is busy. Completion keeps its existing gate.
func ReadRetainedTurnProgressMessages(ownerSessionID string, turnStart time.Time) []llmtypes.MessageContent {
	return llmtypes.TranscriptProgressText(ReadRetainedTurnStructuredProgressMessages(ownerSessionID, turnStart), true)
}

func ReadRetainedTurnStructuredProgressMessages(ownerSessionID string, turnStart time.Time) []llmtypes.MessageContent {
	if turnStart.IsZero() {
		return nil
	}
	key, err := musePersistentKey(ownerSessionID)
	if err != nil {
		return nil
	}
	musePersistentPool.Lock()
	entry := musePersistentPool.m[key]
	if entry == nil {
		musePersistentPool.Unlock()
		return nil
	}
	path, baseline := entry.logPath, entry.retainedBaselineSequence
	if path == "" && entry.nativeSessionID != "" {
		path = museSessionLogPath(entry.nativeSessionID, entry.accountDataHome)
	}
	musePersistentPool.Unlock()
	progress := &entry.retainedProgress
	progress.mu.Lock()
	defer progress.mu.Unlock()
	if !progress.turnStart.Equal(turnStart) {
		progress.turnStart, progress.lastSeq = turnStart, baseline
		progress.seenTools, progress.endedTools = map[string]bool{}, map[string]bool{}
		progress.toolStartedAt = map[string]time.Time{}
	}
	raw, err := os.ReadFile(path) //nolint:gosec // adapter-owned transcript path
	if err != nil {
		return nil
	}
	var messages []llmtypes.MessageContent
	// Only newline-committed records advance the cursor.
	lines := strings.Split(string(raw), "\n")
	for _, line := range lines[:len(lines)-1] {
		var envelope struct {
			Sequence int64 `json:"sequence"`
		}
		if json.Unmarshal([]byte(line), &envelope) != nil || envelope.Sequence <= progress.lastSeq {
			continue
		}
		progress.lastSeq = envelope.Sequence
		for _, chunk := range museTranscriptLineToChunks(line, progress.seenTools, progress.endedTools, progress.toolStartedAt) {
			if message, ok := llmtypes.TranscriptChunkMessage(chunk); ok {
				messages = append(messages, message)
			}
		}
	}
	return messages
}

var museRetainedTurnReady = func(tmuxName, logPath string) bool {
	if tmuxName == "" || logPath == "" || !museLogQuietSince(logPath, 5*time.Second) {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pane, err := museTmuxCapturePane(ctx, tmuxName)
	return err == nil && musePendingUserInputError(pane) == nil && museTUIAtPrompt(pane) && musePaneStable(ctx, tmuxName, pane)
}

// ReadRetainedTurnMessages returns the committed final response for a prompt
// injected into an idle persistent Muse TUI. A sequence cursor captured before
// submission prevents the previous answer from completing the new turn. The
// idle-pane and quiet-log gate matches Muse's normal bounded-turn completion
// contract, so intermediate tool-call model commits are never surfaced as the
// final chat response.
func ReadRetainedTurnMessages(ownerSessionID string, _ time.Time) []llmtypes.MessageContent {
	key, err := musePersistentKey(ownerSessionID)
	if err != nil {
		return nil
	}
	musePersistentPool.Lock()
	entry := musePersistentPool.m[key]
	if entry == nil {
		musePersistentPool.Unlock()
		museObserveRetainedQuiet(ownerSessionID, "pool-missing", false, func() string { return "pool=missing" })
		return nil
	}
	tmuxName := entry.tmuxName
	logPath := entry.logPath
	if logPath == "" && entry.nativeSessionID != "" {
		logPath = museSessionLogPath(entry.nativeSessionID, entry.accountDataHome)
	}
	baseline := entry.retainedBaselineSequence
	autoAnswer := entry.autoAnswer
	musePersistentPool.Unlock()
	museObserveRetainedQuiet(ownerSessionID, museLogSignal(logPath), museTmuxSessionAliveQuick(tmuxName), func() string {
		entry.retainedProgress.mu.Lock()
		cursor := entry.retainedProgress.lastSeq
		entry.retainedProgress.mu.Unlock()
		return fmt.Sprintf("muse_log_seq=%d app_cursor=%d baseline=%d pool=present", museNewestLogSequence(logPath), cursor, baseline)
	})
	// Live-input turns have no GenerateContent waiter. Service the same
	// native controls here before asking whether the retained turn is done.
	if autoAnswer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		ctx = context.WithValue(ctx, museAutoAnswerKey{}, autoAnswer)
		pane, captureErr := museTmuxCapturePane(ctx, tmuxName)
		pending, questionErr := false, captureErr
		if captureErr == nil {
			pending, questionErr = museHandlePendingQuestion(ctx, tmuxName, pane)
		}
		cancel()
		if pending || questionErr != nil {
			return nil
		}
	}
	if !museRetainedTurnReady(tmuxName, logPath) {
		return nil
	}

	raw, err := os.ReadFile(logPath) //nolint:gosec // adapter-owned transcript path
	if err != nil {
		return nil
	}
	var final string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec map[string]any
		if json.Unmarshal([]byte(line), &rec) != nil {
			continue
		}
		sequence, _ := rec["sequence"].(float64)
		if int64(sequence) <= baseline {
			continue
		}
		payload, _ := rec["payload"].(map[string]any)
		if payload == nil || museLogString(payload, "kind") != "run" {
			continue
		}
		event, _ := payload["event"].(map[string]any)
		if event == nil || museLogString(event, "kind") != "assistant_message_committed" {
			continue
		}
		if text := strings.TrimSpace(museLogString(event, "text")); text != "" {
			final = text
		}
	}
	if final == "" {
		return nil
	}
	return []llmtypes.MessageContent{{
		Role:  llmtypes.ChatMessageTypeAI,
		Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: final}},
	}}
}

// RetainedTurnEndedWithoutReply reports that Muse's own record shows the retained turn ended cancelled with no reply
// after it: the person pressed Escape and Muse wrote a run "terminal" of "cancelled" ("cancelled during model step").
// ReadRetainedTurnMessages returns no final text for such a turn, so without this the chat stays Working after Stop.
// A later run start or committed reply means the turn went on, so only the newest records decide. The reason is
// returned for the log.
func RetainedTurnEndedWithoutReply(ownerSessionID string, _ time.Time) (string, bool) {
	key, err := musePersistentKey(ownerSessionID)
	if err != nil {
		return "", false
	}
	musePersistentPool.Lock()
	entry := musePersistentPool.m[key]
	if entry == nil {
		musePersistentPool.Unlock()
		return "", false
	}
	logPath, baseline := entry.logPath, entry.retainedBaselineSequence
	if logPath == "" && entry.nativeSessionID != "" {
		logPath = museSessionLogPath(entry.nativeSessionID, entry.accountDataHome)
	}
	musePersistentPool.Unlock()
	if logPath == "" {
		return "", false
	}
	raw, err := os.ReadFile(logPath) //nolint:gosec // adapter-owned transcript path
	if err != nil {
		return "", false
	}
	return museLogEndedWithoutReply(string(raw), baseline)
}

// museLogEndedWithoutReply is true when, after the baseline sequence, the last run event is a "cancelled" terminal and
// no run started or reply was committed since.
func museLogEndedWithoutReply(log string, baseline int64) (string, bool) {
	reason := ""
	for _, line := range strings.Split(log, "\n") {
		var rec map[string]any
		if json.Unmarshal([]byte(strings.TrimSpace(line)), &rec) != nil {
			continue
		}
		if sequence, _ := rec["sequence"].(float64); int64(sequence) <= baseline {
			continue
		}
		payload, _ := rec["payload"].(map[string]any)
		if payload == nil || museLogString(payload, "kind") != "run" {
			continue
		}
		event, _ := payload["event"].(map[string]any)
		switch museLogString(event, "kind") {
		case "started":
			reason = ""
		case "assistant_message_committed":
			if strings.TrimSpace(museLogString(event, "text")) != "" {
				reason = ""
			}
		case "terminal":
			if museLogString(event, "terminal") == "cancelled" {
				reason = "cancelled"
			} else {
				reason = ""
			}
		}
	}
	return reason, reason != ""
}

// museRetainedQuiet holds one silence watch per owner session for the retained (live-input) turns, which have no
// waiter loop of their own: the app polls ReadRetainedTurnMessages, so that poll is where silence is seen.
var museRetainedQuiet sync.Map

func museObserveRetainedQuiet(owner, signal string, alive bool, detail func() string) {
	v, _ := museRetainedQuiet.LoadOrStore(owner, &stalldiag.Watch{Provider: "muse-cli", Session: owner})
	v.(*stalldiag.Watch).ObserveSignal(time.Now(), signal, alive, detail)
}

// museLogSignal changes whenever Muse appends to its session log.
func museLogSignal(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return "no-log"
	}
	return fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())
}

func museTmuxSessionAliveQuick(tmuxName string) bool {
	if tmuxName == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return museTmuxSessionAlive(ctx, tmuxName)
}

// museNewestLogSequence is the largest record sequence in the last 64 KB of the session log (numbers only; nothing
// else is kept). Muse appends, so the newest records are at the end; the read stays cheap on a huge log.
func museNewestLogSequence(path string) int64 {
	const tail = 64 * 1024
	f, err := os.Open(path) //nolint:gosec // adapter-owned transcript path
	if err != nil {
		return -1
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return -1
	}
	offset := info.Size() - tail
	if offset < 0 {
		offset = 0
	}
	buf := make([]byte, info.Size()-offset)
	if _, err := f.ReadAt(buf, offset); err != nil && err != io.EOF {
		return -1
	}
	lines := strings.Split(string(buf), "\n")
	if offset > 0 && len(lines) > 0 {
		lines = lines[1:] // the first line may be cut in the middle
	}
	var newest int64
	for _, line := range lines {
		var envelope struct {
			Sequence int64 `json:"sequence"`
		}
		if json.Unmarshal([]byte(line), &envelope) == nil && envelope.Sequence > newest {
			newest = envelope.Sequence
		}
	}
	return newest
}

// RetainedTurnDiagnostics describes a retained Muse turn for the server's still_running heartbeat, numbers and fixed
// words only: the newest sequence in Muse's session log, the last sequence the app's progress reader consumed, the
// sequence when the turn started, and whether the pool entry exists. Equal numbers mean Muse itself is quiet; a log
// sequence far ahead of the cursor means we stopped reading.
func RetainedTurnDiagnostics(ownerSessionID string) string {
	key, err := musePersistentKey(ownerSessionID)
	if err != nil {
		return "pool=missing"
	}
	musePersistentPool.Lock()
	entry := musePersistentPool.m[key]
	if entry == nil {
		musePersistentPool.Unlock()
		return "pool=missing"
	}
	logPath := entry.logPath
	if logPath == "" && entry.nativeSessionID != "" {
		logPath = museSessionLogPath(entry.nativeSessionID, entry.accountDataHome)
	}
	baseline := entry.retainedBaselineSequence
	tmuxName := entry.tmuxName
	musePersistentPool.Unlock()
	entry.retainedProgress.mu.Lock()
	cursor := entry.retainedProgress.lastSeq
	entry.retainedProgress.mu.Unlock()
	return fmt.Sprintf("muse_log_seq=%d app_cursor=%d baseline=%d pool=present alive=%t", museNewestLogSequence(logPath), cursor, baseline, museTmuxSessionAliveQuick(tmuxName))
}
