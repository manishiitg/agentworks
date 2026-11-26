package codexcli

import (
	"bufio"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// codexTurnCompletionTracker follows the rollout JSONL for one interactive
// Codex session. The native task_complete event is a stronger completion
// signal than terminal text: status lines such as "Working" remain in tmux
// scrollback, and some Codex releases omit the visible "Worked for ..."
// footer even though the turn has ended.
//
// The rollout is resolved through resolveRollout when the caller supplies one
// (PLAT-108), which binds this tracker to its own session's conversation.
//
// This used to select purely by session_meta.cwd, justified by a comment
// claiming "interactive sessions use a unique working directory, so this
// remains isolated when several Codex agents run concurrently". That assumption
// is false: a workflow's interactive Chat and its scheduled run share one
// directory, so the tracker could latch onto the OTHER conversation's rollout
// and end this turn on its task_complete. The interactive adapter now always
// passes a bound resolver; only the structured transport still passes nil,
// because a one-shot `--json` process has no session identity to bind.
//
// Events older than turnStart are ignored for persistent sessions that contain
// multiple turns.
type codexTurnCompletionTracker struct {
	turnStart          time.Time
	expectedWorkingDir string
	// resolveRollout, when set, returns THIS session's rollout. It is lock-free
	// (see codexRolloutResolverForSession) because the tracker polls while the
	// adapter holds session.mu. nil falls back to the unsafe directory lookup,
	// which is only correct when no other Codex session shares the directory.
	resolveRollout   func(time.Time) string
	rolloutPath      string
	offset           int64
	pendingToolCalls map[string]struct{}
	sawTurnEvent     bool
	sawFinalAnswer   bool
	// completedLatched makes completion sticky. Reading the rollout advances
	// offset past each line before matching it, so task_complete is consumed by
	// the poll that finds it and no later poll can rediscover it. Callers that
	// re-ask after observing completion — the post-completion modal grace period
	// in the interactive adapter — would otherwise see false forever and hang
	// the turn even though it finished.
	completedLatched bool
	// startedTurnID is the turn Codex began after turnStart. A task_complete
	// carrying another turn_id belongs to a predecessor (an interrupted turn
	// flushing late) and must not end this one.
	startedTurnID string
	// abortReason is set when Codex recorded turn_aborted for this turn (Esc in
	// the TUI, a client interrupt). The turn is over without a task_complete,
	// so waiting for one would hang until the adapter's deadline.
	abortReason string
	// diagnostics is deliberately callback-only: it records identity and
	// lifecycle boundaries without retaining prompt, tool, or response content.
	// PLAT-116 needs this to distinguish "the provider never completed" from
	// "the adapter followed the wrong rollout" and "completion was observed but
	// not forwarded" on a live reproduction.
	diagnostics *codexCompletionDiagnosticHooks
}

type codexCompletionDiagnosticHooks struct {
	rolloutSelected func(path, threadID string)
	taskComplete    func(path, threadID, turnID string, completedAt time.Time, offset int64)
	// completedBy reports which signal ended the response wait: see the
	// codexCompletionSource* constants.
	completedBy func(source string)
}

// Completion sources reported through codexCompletionDiagnosticHooks.completedBy
// and surfaced as codex_completion_source (PLAT-354).
const (
	// codexCompletionSourceRollout: the turn's task_complete in the rollout.
	codexCompletionSourceRollout = "rollout_task_complete"
	// codexCompletionSourceRolloutLateFallback: the rollout recorded this turn's
	// final answer with no pending tool call, but task_complete never arrived,
	// and the pane has been idle for codexRolloutLateFallback.
	codexCompletionSourceRolloutLateFallback = "tmux_pane_after_rollout_final_answer"
	// codexCompletionSourcePane: no rollout recorded this turn; pane idleness
	// or the stale-pane backstop ended it (compatibility fallback).
	codexCompletionSourcePane = "tmux_pane"
)

// codexRolloutLateFallback bounds how long a turn whose rollout already holds
// the final answer may wait for a missing task_complete before a stable idle
// pane is allowed to end it, so a Codex build that skips task_complete cannot
// hang the turn forever.
var codexRolloutLateFallback = 30 * time.Second

func (h *codexCompletionDiagnosticHooks) reportCompletedBy(source string) {
	if h != nil && h.completedBy != nil {
		h.completedBy(source)
	}
}

func newCodexTurnCompletionTracker(turnStart time.Time, expectedWorkingDir string, resolveRollout func(time.Time) string) *codexTurnCompletionTracker {
	return &codexTurnCompletionTracker{
		turnStart:          turnStart,
		expectedWorkingDir: strings.TrimSpace(expectedWorkingDir),
		resolveRollout:     resolveRollout,
		pendingToolCalls:   make(map[string]struct{}),
	}
}

func (t *codexTurnCompletionTracker) setDiagnosticHooks(hooks *codexCompletionDiagnosticHooks) {
	if t != nil {
		t.diagnostics = hooks
	}
}

func (t *codexTurnCompletionTracker) completed() bool {
	if t == nil || t.turnStart.IsZero() || t.expectedWorkingDir == "" {
		return false
	}
	if t.completedLatched {
		return true
	}
	if t.rolloutPath == "" {
		if t.resolveRollout != nil {
			t.rolloutPath = t.resolveRollout(t.turnStart)
		} else {
			t.rolloutPath = findCodexRolloutByWorkingDirUnsafe(t.turnStart, t.expectedWorkingDir)
		}
		if t.rolloutPath == "" {
			return false
		}
		if t.diagnostics != nil && t.diagnostics.rolloutSelected != nil {
			t.diagnostics.rolloutSelected(t.rolloutPath, readCodexRolloutThreadID(t.rolloutPath))
		}
	}

	f, err := os.Open(t.rolloutPath)
	if err != nil {
		return false
	}
	defer f.Close()
	if _, err := f.Seek(t.offset, io.SeekStart); err != nil {
		return false
	}

	reader := bufio.NewReader(f)
	for {
		line, err := reader.ReadString('\n')
		if err != nil && !(err == io.EOF && len(line) > 0) {
			return false
		}
		// Codex appends one complete JSON object per line. Do not consume a
		// partially-written final record; it will be retried on the next poll.
		if err == io.EOF && !strings.HasSuffix(line, "\n") {
			return false
		}
		t.offset += int64(len(line))
		if t.observe(line) {
			t.completedLatched = true
			return true
		}
		if err == io.EOF {
			return false
		}
	}
}

// aborted reports whether the turn ended with Codex's turn_aborted event and
// the reason it recorded. Only meaningful once completed() returned true.
func (t *codexTurnCompletionTracker) aborted() (bool, string) {
	if t == nil || t.abortReason == "" {
		return false, ""
	}
	return true, t.abortReason
}

// blocksTerminalFallback reports whether the native rollout proves that Codex
// is still inside the turn. The TUI may show an idle composer while an MCP call
// is pending, so a ready-looking pane is not a completion signal once the
// rollout has shown tool activity or commentary without a final_answer.
// rolloutOwnsTurn reports that the rollout has recorded this turn. From then on
// only task_complete or turn_aborted ends it; the pane may end it only through
// the late fallback in waitForCodexInteractiveResponse.
func (t *codexTurnCompletionTracker) rolloutOwnsTurn() bool {
	return t != nil && t.sawTurnEvent
}

// finalAnswerSettled reports that the rollout holds this turn's final answer
// and no tool call is still pending.
func (t *codexTurnCompletionTracker) finalAnswerSettled() bool {
	return t != nil && t.sawTurnEvent && t.sawFinalAnswer && len(t.pendingToolCalls) == 0
}

func (t *codexTurnCompletionTracker) blocksTerminalFallback() bool {
	if t == nil || !t.sawTurnEvent {
		return false
	}
	return len(t.pendingToolCalls) > 0 || !t.sawFinalAnswer
}

func (t *codexTurnCompletionTracker) observe(line string) bool {
	type rolloutEvent struct {
		Type      string `json:"type"`
		Timestamp string `json:"timestamp"`
		Payload   struct {
			Type   string `json:"type"`
			Phase  string `json:"phase"`
			CallID string `json:"call_id"`
			TurnID string `json:"turn_id"`
			Reason string `json:"reason"`
		} `json:"payload"`
	}
	var event rolloutEvent
	if json.Unmarshal([]byte(line), &event) != nil {
		return false
	}
	timestamp, err := time.Parse(time.RFC3339Nano, event.Timestamp)
	if err != nil || timestamp.Before(t.turnStart) {
		return false
	}
	t.sawTurnEvent = true

	if event.Type == "event_msg" && event.Payload.Type == "task_started" {
		if t.startedTurnID == "" {
			t.startedTurnID = strings.TrimSpace(event.Payload.TurnID)
		}
		return false
	}
	if event.Type == "event_msg" && event.Payload.Type == "turn_aborted" {
		if !codexTaskCompleteBelongsToTurn(t.startedTurnID, event.Payload.TurnID) {
			return false
		}
		t.abortReason = strings.TrimSpace(event.Payload.Reason)
		if t.abortReason == "" {
			t.abortReason = "aborted"
		}
		return true
	}
	if event.Type == "event_msg" && event.Payload.Type == "task_complete" {
		if !codexTaskCompleteBelongsToTurn(t.startedTurnID, event.Payload.TurnID) {
			return false
		}
		if t.diagnostics != nil && t.diagnostics.taskComplete != nil {
			t.diagnostics.taskComplete(
				t.rolloutPath,
				readCodexRolloutThreadID(t.rolloutPath),
				event.Payload.TurnID,
				timestamp,
				t.offset,
			)
		}
		return true
	}
	if event.Type != "response_item" {
		return false
	}
	switch event.Payload.Type {
	case "function_call", "custom_tool_call", "tool_search_call":
		if event.Payload.CallID != "" {
			t.pendingToolCalls[event.Payload.CallID] = struct{}{}
		}
	case "function_call_output", "custom_tool_call_output", "tool_search_output":
		delete(t.pendingToolCalls, event.Payload.CallID)
	case "message":
		if event.Payload.Phase == "final_answer" || event.Payload.Phase == "final" {
			t.sawFinalAnswer = true
		}
	}
	return false
}

// codexTaskCompleteBelongsToTurn accepts a task_complete for the turn that
// started after turnStart. Rollouts without task_started (older fixtures) and
// completions without a turn_id keep the timestamp-only behavior.
func codexTaskCompleteBelongsToTurn(startedTurnID, completedTurnID string) bool {
	started := strings.TrimSpace(startedTurnID)
	completed := strings.TrimSpace(completedTurnID)
	return started == "" || completed == "" || started == completed
}

// findCodexRolloutByWorkingDirUnsafe resolves a rollout using ONLY working
// directory + newest modification time.
//
// It is named "Unsafe" deliberately: working directory is not a conversation
// identity, so this can return another session's transcript whenever two Codex
// sessions share a directory — which a workflow's Chat and Schedule always do
// (PLAT-106). Every remaining caller is a known gap tracked by PLAT-108. New
// code must use resolveCodexRolloutPath, which binds to the session's thread.
func findCodexRolloutByWorkingDirUnsafe(turnStart time.Time, expectedWorkingDir string, accountRoot ...string) string {
	return findCodexRolloutByWorkingDirExcluding(turnStart, expectedWorkingDir, nil, accountRoot...)
}

// findCodexRolloutByWorkingDirExcluding resolves a rollout by working directory
// and recency, skipping any path already claimed by another live session.
//
// Working directory alone is NOT an identity: a Chat and a Schedule for the same
// workflow run in the same directory, so "newest rollout in this cwd" can return
// the other conversation's transcript (PLAT-106). The exclusion set is the
// disambiguator until the session learns its own thread ID, after which
// findCodexRolloutForThread is exact and this path is not used.
func findCodexRolloutByWorkingDirExcluding(turnStart time.Time, expectedWorkingDir string, excluded map[string]bool, accountRoot ...string) string {
	candidates := codexRolloutCandidatesInWorkingDir(turnStart, expectedWorkingDir, excluded, accountRoot...)
	if len(candidates) == 0 {
		return ""
	}
	return candidates[0]
}

// findCodexRolloutForSessionScan is the directory scan a session uses before it
// knows its thread ID. With no bind prompt it is the legacy recency match.
// With one, a rollout qualifies only if it recorded that prompt as a user row:
// first a strict match, then a loose (shared-prefix) match that must be
// unique. It returns "" rather than guess, and callers retry on the next poll.
// bindSince, when set and earlier than turnStart, widens the scan to cover a
// prompt Codex recorded at launch, before the turn's own start time.
func findCodexRolloutForSessionScan(turnStart time.Time, expectedWorkingDir string, excluded map[string]bool, bindPrompt string, bindSince time.Time, accountRoot ...string) string {
	scanFrom := turnStart
	if !bindSince.IsZero() && (scanFrom.IsZero() || bindSince.Before(scanFrom)) {
		scanFrom = bindSince
	}
	candidates := codexRolloutCandidatesInWorkingDir(scanFrom, expectedWorkingDir, excluded, accountRoot...)
	bindPrompt = strings.TrimSpace(bindPrompt)
	if bindPrompt == "" {
		if len(candidates) == 0 {
			return ""
		}
		return candidates[0]
	}
	var loose []string
	for _, path := range candidates {
		strict, looseMatch := codexRolloutRecordsBindPrompt(path, bindPrompt, scanFrom)
		if strict {
			return path
		}
		if looseMatch {
			loose = append(loose, path)
		}
	}
	if len(loose) == 1 {
		return loose[0]
	}
	return ""
}

// codexRolloutRecordsBindPrompt reports whether a rollout has a user row for
// prompt at or after since. strict: identical text, or for a long prompt a
// shared 200-byte prefix AND a shared 120-byte suffix. The per-request part of
// a prompt is usually at its end, while two sessions of one workflow can share
// a long opening. loose: the durable-ack rule (a shared prefix is enough).
func codexRolloutRecordsBindPrompt(path, prompt string, since time.Time) (strict, loose bool) {
	f, err := os.Open(path)
	if err != nil {
		return false, false
	}
	defer f.Close()
	type contentPart struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type event struct {
		Type      string `json:"type"`
		Timestamp string `json:"timestamp"`
		Payload   struct {
			Role    string        `json:"role"`
			Content []contentPart `json:"content"`
		} `json:"payload"`
	}
	since = since.Add(-2 * time.Second)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var e event
		if json.Unmarshal(scanner.Bytes(), &e) != nil || e.Type != "response_item" || e.Payload.Role != "user" {
			continue
		}
		if ts, parseErr := time.Parse(time.RFC3339Nano, e.Timestamp); parseErr != nil || ts.Before(since) {
			continue
		}
		for _, part := range e.Payload.Content {
			if part.Type != "input_text" {
				continue
			}
			row := strings.TrimSpace(part.Text)
			if row == prompt {
				return true, true
			}
			const prefixLen, suffixLen = 200, 120
			if len(prompt) > prefixLen && len(row) > prefixLen && row[:prefixLen] == prompt[:prefixLen] &&
				strings.HasSuffix(row, prompt[len(prompt)-suffixLen:]) {
				return true, true
			}
			if codexUserRowMatches(row, prompt) {
				loose = true
			}
		}
	}
	return false, loose
}

// codexRolloutCandidatesInWorkingDir lists rollouts recorded for
// expectedWorkingDir, modified since shortly before turnStart and not claimed
// by another session, newest first.
func codexRolloutCandidatesInWorkingDir(turnStart time.Time, expectedWorkingDir string, excluded map[string]bool, accountRoot ...string) []string {
	root := codexSessionsRoot(accountRoot...)
	if root == "" {
		return nil
	}
	cutoff := turnStart.Add(-30 * time.Second)
	type candidate struct {
		path string
		mod  time.Time
	}
	var candidates []candidate
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		if excluded[path] {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr == nil && !info.ModTime().Before(cutoff) {
			candidates = append(candidates, candidate{path: path, mod: info.ModTime()})
		}
		return nil
	})
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].mod.After(candidates[j].mod) })
	var out []string
	for _, candidate := range candidates {
		if sameCodexWorkingDir(readCodexRolloutWorkingDir(candidate.path), expectedWorkingDir) {
			out = append(out, candidate.path)
		}
	}
	return out
}

// codexOwnThreadRolloutResolver returns a resolver for a one-shot structured run that finds
// ONLY the rollout of the thread the run announced on stdout. Until the thread id is known (or
// its rollout exists) it returns "", so nothing can complete. Matching by working directory
// alone let another Codex run in the same folder complete this one (website workflow,
// 2026-10-05). The stdout turn.completed path stays the primary signal.
func codexOwnThreadRolloutResolver(threadID func() string, accountRoot ...string) func(time.Time) string {
	return func(time.Time) string {
		id := threadID()
		if id == "" {
			return ""
		}
		return findCodexRolloutForThread(id, accountRoot...)
	}
}

// findCodexRolloutForThread resolves the rollout for an EXACT Codex thread ID.
// Codex names each rollout `rollout-<timestamp>-<thread-id>.jsonl` and repeats
// the same value in `session_meta.payload.id`, so the filename is a cheap
// pre-filter and the payload is the authority.
func findCodexRolloutForThread(threadID string, accountRoot ...string) string {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return ""
	}
	root := codexSessionsRoot(accountRoot...)
	if root == "" {
		return ""
	}
	var match string
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || filepath.Ext(path) != ".jsonl" || match != "" {
			return nil
		}
		if !strings.Contains(filepath.Base(path), threadID) {
			return nil
		}
		if readCodexRolloutThreadID(path) == threadID {
			match = path
		}
		return nil
	})
	return match
}

// readCodexRolloutThreadID returns `session_meta.payload.id` — Codex's thread
// identity for the conversation recorded in this rollout.
func readCodexRolloutThreadID(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	type sessionMeta struct {
		Type    string `json:"type"`
		Payload struct {
			ID string `json:"id"`
		} `json:"payload"`
	}
	reader := bufio.NewReader(f)
	for i := 0; i < 8; i++ {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			var event sessionMeta
			if json.Unmarshal(line, &event) == nil && event.Type == "session_meta" {
				return strings.TrimSpace(event.Payload.ID)
			}
		}
		if readErr != nil {
			break
		}
	}
	return ""
}

func codexSessionsRoot(accountRoot ...string) string {
	if len(accountRoot) > 0 && accountRoot[0] != "" {
		return filepath.Join(accountRoot[0], "sessions")
	}
	if codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME")); codexHome != "" {
		return filepath.Join(codexHome, "sessions")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex", "sessions")
}

func readCodexRolloutWorkingDir(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	type sessionMeta struct {
		Type    string `json:"type"`
		Payload struct {
			CWD string `json:"cwd"`
		} `json:"payload"`
	}
	reader := bufio.NewReader(f)
	for i := 0; i < 8; i++ {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			var event sessionMeta
			if json.Unmarshal(line, &event) == nil && event.Type == "session_meta" {
				return strings.TrimSpace(event.Payload.CWD)
			}
		}
		if readErr != nil {
			break
		}
	}
	return ""
}
