package terminals

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	orchestratorevents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"

	agentevents "github.com/manishiitg/mcpagent/events"
)

// Snapshot is the latest view-only terminal/TUI screen for one coding-agent execution.
type Snapshot struct {
	TerminalID        string     `json:"terminal_id"`
	SessionID         string     `json:"session_id"`
	OwnerID           string     `json:"owner_id,omitempty"`
	ExecutionID       string     `json:"execution_id,omitempty"`
	ExecutionKind     string     `json:"execution_kind,omitempty"`
	Label             string     `json:"label,omitempty"`
	Scope             string     `json:"scope,omitempty"`
	WorkflowPath      string     `json:"workflow_path,omitempty"`
	WorkflowName      string     `json:"workflow_name,omitempty"`
	WorkflowLabel     string     `json:"workflow_label,omitempty"`
	StepID            string     `json:"step_id,omitempty"`
	StepName          string     `json:"step_name,omitempty"`
	StepType          string     `json:"step_type,omitempty"`
	StepIndex         int        `json:"step_index,omitempty"`
	StepTotal         int        `json:"step_total,omitempty"`
	ParentStepID      string     `json:"parent_step_id,omitempty"`
	StepAttempt       int        `json:"step_attempt,omitempty"`
	StepExecutionMode string     `json:"step_execution_mode,omitempty"` // "scripted" | "agentic" (legacy: "learn_code" | "code_exec")
	StepTransport     string     `json:"step_transport,omitempty"`      // "tmux" | "api" | legacy labels
	StepTriggeredBy   string     `json:"step_triggered_by,omitempty"`   // e.g., "workflow_executor", "parent_step:X"
	AgentName         string     `json:"agent_name,omitempty"`
	DisplayTitle      string     `json:"display_title,omitempty"`
	DisplayMeta       string     `json:"display_meta,omitempty"`
	TmuxSession       string     `json:"tmux_session,omitempty"`
	Content           string     `json:"content"`
	ContentSource     string     `json:"content_source,omitempty"`
	Rows              []Row      `json:"rows"`
	ChunkIndex        int        `json:"chunk_index"`
	Active            bool       `json:"active"`
	State             string     `json:"state"`
	ProcessState      string     `json:"process_state,omitempty"`
	SnapshotKind      string     `json:"snapshot_kind,omitempty"`
	CloseReason       string     `json:"close_reason,omitempty"`
	ClosesAt          *time.Time `json:"closes_at,omitempty"`
	RetentionSeconds  int        `json:"retention_seconds,omitempty"`
	Status            Status     `json:"status"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// Status is a conservative, human-readable summary derived from the raw TUI.
type Status struct {
	ProviderLabel            string  `json:"provider_label,omitempty"`
	StatusText               string  `json:"status_text,omitempty"`
	AssistantPreview         string  `json:"assistant_preview,omitempty"`
	ToolSummary              string  `json:"tool_summary,omitempty"`
	ToolName                 string  `json:"tool_name,omitempty"`
	ToolCount                int     `json:"tool_count,omitempty"`
	InputTokens              int     `json:"input_tokens,omitempty"`
	OutputTokens             int     `json:"output_tokens,omitempty"`
	CacheCreationInputTokens int     `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int     `json:"cache_read_input_tokens,omitempty"`
	TotalInputTokens         int     `json:"total_input_tokens,omitempty"`
	TotalOutputTokens        int     `json:"total_output_tokens,omitempty"`
	CostUSD                  float64 `json:"cost_usd,omitempty"`
	// StatusMeta carries raw provider statusline extras that don't have a
	// first-class field (context window, git branch, rate limits, …) so the UI
	// can surface them without the store needing to know each provider's schema.
	StatusMeta                map[string]interface{} `json:"status_meta,omitempty"`
	DurationMs                int64                  `json:"duration_ms,omitempty"`
	PreValidationStatus       string                 `json:"pre_validation_status,omitempty"`
	PreValidationSummary      string                 `json:"pre_validation_summary,omitempty"`
	PreValidationPassedChecks int                    `json:"pre_validation_passed_checks,omitempty"`
	PreValidationFailedChecks int                    `json:"pre_validation_failed_checks,omitempty"`
	PreValidationTotalChecks  int                    `json:"pre_validation_total_checks,omitempty"`
	// RateLimited is set when the rendered pane content matches any
	// known provider rate-limit / quota-exhausted message (see
	// rate_limit.go). The terminal stays "running" from the store's
	// perspective because the tmux pane is still alive, but the
	// frontend can surface a distinct badge so the user knows the
	// underlying work is blocked, not making progress.
	RateLimited bool `json:"rate_limited,omitempty"`
}

// Context contains higher-level session data used to enrich terminal labels.
type Context struct {
	WorkflowName  string
	WorkflowLabel string
	WorkspacePath string
	ExecutionName string
}

// Store keeps current terminal screens outside durable event history.
type Store struct {
	mu             sync.RWMutex
	byID           map[string]Snapshot
	bySession      map[string]map[string]struct{}
	dismissed      map[string]struct{}
	forcedInactive map[string]time.Time
	toolLines      map[string]*terminalToolLines
	// delegationBackgroundAgent links a delegation's correlation ID to the
	// background agent that spawned it. The delegate tool's real content
	// events (tool calls, messages) carry only correlation_id — the
	// delegationID minted fresh per call — never the background agent's own
	// ID, so without this they'd resolve to a terminal ID disjoint from the
	// background_agent_started/completed lifecycle events, which DO carry
	// agent_id. Populated from delegation_start (which already carries both),
	// consulted in metadataForEvent to unify the two identities. Entries are
	// small (two short strings) and outlive their session for the process
	// lifetime; not worth the bookkeeping to evict per-session.
	delegationBackgroundAgent map[string]string
}

const terminalToolTextMaxRunes = 2400

var (
	regexpMCPToken      = regexp.MustCompile(`(?i)(MCP_API_TOKEN=)[^\s"'\\]+`)
	regexpSensitiveEnv  = regexp.MustCompile(`(?i)\b([A-Z0-9_]*(?:API_KEY|TOKEN|SECRET)=)[^\s"'\\]+`)
	regexpBearerToken   = regexp.MustCompile(`(?i)(Authorization:\s*Bearer\s+)[^\s"'\\]+`)
	regexpSecretEnv     = regexp.MustCompile(`(?m)(SECRET_[A-Z0-9_]+=)[^\s"'\\]+`)
	regexpProviderSKKey = regexp.MustCompile(`\bsk-[A-Za-z0-9][A-Za-z0-9_-]{10,}\b`)
	regexpGoogleAPIKey  = regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{20,}\b`)
)

type terminalToolLines struct {
	order []string
	items map[string]*terminalToolLine
}

type terminalToolLine struct {
	name         string
	args         string
	result       string
	resultPrefix string
}

func NewStore() *Store {
	return &Store{
		byID:                      make(map[string]Snapshot),
		bySession:                 make(map[string]map[string]struct{}),
		dismissed:                 make(map[string]struct{}),
		forcedInactive:            make(map[string]time.Time),
		toolLines:                 make(map[string]*terminalToolLines),
		delegationBackgroundAgent: make(map[string]string),
	}
}

func (s *Store) recordDelegationBackgroundAgent(delegationID, backgroundAgentID string) {
	delegationID = strings.TrimSpace(delegationID)
	backgroundAgentID = strings.TrimSpace(backgroundAgentID)
	if s == nil || delegationID == "" || backgroundAgentID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.delegationBackgroundAgent[delegationID] = backgroundAgentID
}

func (s *Store) lookupDelegationBackgroundAgent(delegationID string) string {
	delegationID = strings.TrimSpace(delegationID)
	if s == nil || delegationID == "" {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.delegationBackgroundAgent[delegationID]
}

// metadataForEvent projects an event's typed payload into the generic metadata
// map every owner-resolution check reads, then unifies a delegate-tool
// sub-agent's content events with its background_agent_started/completed
// lifecycle events. See delegationBackgroundAgent's doc comment.
func (s *Store) metadataForEvent(event storeevents.Event) map[string]interface{} {
	metadata := metadataForEvent(event)
	if stringValue(metadata, "background_agent_id") == "" {
		if bgID := s.lookupDelegationBackgroundAgent(stringValue(metadata, "correlation_id")); bgID != "" {
			metadata["background_agent_id"] = bgID
		}
	}
	return metadata
}

// HandleEvent ingests terminal streaming events emitted by coding-agent adapters.
func (s *Store) HandleEvent(sessionID string, event storeevents.Event) {
	s.handleEvent(sessionID, event)
}

// HandleEventWithChange preserves the callback-compatible HandleEvent API while
// telling the server whether downstream terminal lifecycle work is necessary.
func (s *Store) HandleEventWithChange(sessionID string, event storeevents.Event) bool {
	return s.handleEvent(sessionID, event)
}

func (s *Store) handleEvent(sessionID string, event storeevents.Event) bool {
	if s == nil {
		return false
	}
	sessionID = firstNonEmpty(sessionID, event.SessionID)
	if sessionID == "" {
		return false
	}

	switch event.Type {
	case "streaming_chunk":
		content, chunkIndex, metadata, ok := terminalChunk(s, event)
		if !ok || strings.TrimSpace(content) == "" {
			return false
		}
		if isTerminalMetadata(metadata) {
			s.upsertTerminal(sessionID, event, metadata, content, chunkIndex)
			return true
		}
		if !isStructuredExecutionMetadata(sessionID, event, metadata) {
			return false
		}
		s.upsertStructuredChunk(sessionID, event, metadata, content, chunkIndex)
		return true
	case "streaming_end":
		metadata := s.metadataForEvent(event)
		if !isTerminalMetadata(metadata) {
			return false
		}
		s.markInactive(sessionID, terminalOwnerID(sessionID, event, metadata), metadata, event.Timestamp)
		return true
	case string(agentevents.ToolCallStart), string(agentevents.ToolCallEnd), string(agentevents.ToolCallError):
		metadata := s.metadataForEvent(event)
		if !isNonTmuxWorkflowTerminalMetadata(metadata) {
			return false
		}
		s.upsertToolLine(sessionID, event, metadata)
		return true
	case string(agentevents.UserMessage):
		content := structuredUserMessage(event)
		if strings.TrimSpace(content) == "" {
			return false
		}
		if isAutoNotificationMessage(content) {
			return s.reconcileAsyncSubAgentCompletionBatch(sessionID, content, event.Timestamp)
		}
		metadata := s.metadataForEvent(event)
		if !isStructuredExecutionMetadata(sessionID, event, metadata) {
			return false
		}
		s.upsertStructuredMessage(sessionID, event, metadata, "> user: "+content)
		return true
	case "delegation_start":
		// Bookkeeping only, no snapshot: link this delegation's correlation ID
		// to the background agent that spawned it (if any), so the delegate
		// tool's real content events — tagged only with correlation_id by
		// DelegationEventObserver, never agent_id — resolve to the SAME
		// terminal as their background_agent_started/completed lifecycle
		// events instead of a disjoint one. See delegationBackgroundAgent.
		if event.Data != nil {
			if data, ok := event.Data.Data.(*storeevents.DelegationStartEventData); ok {
				s.recordDelegationBackgroundAgent(data.DelegationID, data.BackgroundAgentID)
			}
		}
		return false
	case "pre_validation_completed":
		s.updatePreValidationStatus(sessionID, event)
		return true
	case "status_line":
		s.handleStatusLine(sessionID, event)
		return true
	case "orchestrator_agent_start", "background_agent_started":
		metadata := s.metadataForEvent(event)
		if !isStructuredExecutionMetadata(sessionID, event, metadata) {
			return false
		}
		s.ensureStructuredExecution(sessionID, event, metadata)
		return true
	case "orchestrator_agent_end", "background_agent_completed", "todo_task_step_completed":
		metadata := s.metadataForEvent(event)
		if !isStructuredExecutionMetadata(sessionID, event, metadata) {
			return false
		}
		if structuredLifecycleIsNestedSequence(event, metadata) {
			s.appendStructuredLifecycleResult(sessionID, event, metadata)
			// A message-sequence item reuses the owning step transcript. Settle
			// the transcript when the item ends so a completed final item does
			// not remain in the Live rail forever. A later item start/chunk
			// reactivates the same transcript through the normal upsert path.
			s.completeStructuredExecution(sessionID, event, metadata, structuredExecutionFailed(event))
			return true
		}
		s.completeStructuredExecution(sessionID, event, metadata, structuredExecutionFailed(event))
		return true
	case "orchestrator_agent_error":
		metadata := s.metadataForEvent(event)
		if !isStructuredExecutionMetadata(sessionID, event, metadata) {
			return false
		}
		s.completeStructuredExecution(sessionID, event, metadata, true)
		return true
	}
	return false
}

func (s *Store) List(sessionID string) []Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.pruneExpiredLocked(now)

	var out []Snapshot
	if strings.TrimSpace(sessionID) != "" {
		for terminalID := range s.bySession[sessionID] {
			if _, dismissed := s.dismissed[terminalID]; dismissed {
				continue
			}
			if snapshot, ok := s.reconcileTerminalStateLocked(terminalID, now); ok {
				out = append(out, snapshot)
			}
		}
	} else {
		out = make([]Snapshot, 0, len(s.byID))
		for terminalID := range s.byID {
			if _, dismissed := s.dismissed[terminalID]; dismissed {
				continue
			}
			if snapshot, ok := s.reconcileTerminalStateLocked(terminalID, now); ok {
				out = append(out, snapshot)
			}
		}
	}
	out = dedupeCurrentMainAgentSnapshots(out)

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Active != out[j].Active {
			return out[i].Active
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out
}

// ListMetadata returns current snapshots without content-dependent reconciliation.
// It is used by high-frequency UI rail polls that only need identity, state,
// timestamps, and compact status fields. Avoiding content scans here keeps large
// streaming tmux panes from blocking the terminal list endpoint.
func (s *Store) ListMetadata(sessionID string) []Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.pruneExpiredLocked(now)

	var out []Snapshot
	if strings.TrimSpace(sessionID) != "" {
		for terminalID := range s.bySession[sessionID] {
			if _, dismissed := s.dismissed[terminalID]; dismissed {
				continue
			}
			if snapshot, ok := s.byID[terminalID]; ok {
				out = append(out, snapshot)
			}
		}
	} else {
		out = make([]Snapshot, 0, len(s.byID))
		for terminalID, snapshot := range s.byID {
			if _, dismissed := s.dismissed[terminalID]; dismissed {
				continue
			}
			out = append(out, snapshot)
		}
	}
	out = dedupeCurrentMainAgentSnapshots(out)

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Active != out[j].Active {
			return out[i].Active
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out
}

// ListRaw returns ownership-bearing snapshots without pruning expired or
// dismissed UI entries. Process cleanup must use this path rather than List:
// hiding or expiring a rendered snapshot cannot erase knowledge of a live tmux
// process before the lifecycle coordinator closes it.
func (s *Store) ListRaw(sessionID string) []Snapshot {
	if s == nil {
		return nil
	}
	sessionID = strings.TrimSpace(sessionID)
	s.mu.RLock()
	defer s.mu.RUnlock()

	var out []Snapshot
	if sessionID != "" {
		out = make([]Snapshot, 0, len(s.bySession[sessionID]))
		for terminalID := range s.bySession[sessionID] {
			if snapshot, ok := s.byID[terminalID]; ok {
				out = append(out, snapshot)
			}
		}
	} else {
		out = make([]Snapshot, 0, len(s.byID))
		for _, snapshot := range s.byID {
			out = append(out, snapshot)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out
}

// SessionHasBusyCodingTmux reports whether the session has a coding-agent tmux
// terminal whose pane currently looks busy (actively processing). This lets a
// resumed/launch-only coding agent — which has no server-managed foreground
// turn — still accept live "steer" input while it's mid-task. Content is the
// last-refreshed pane snapshot (the frontend probe refreshes inactive tmux
// terminals every few seconds), so this is an in-memory read, no tmux capture.
func (s *Store) SessionHasBusyCodingTmux(sessionID string) bool {
	return s.sessionHasBusyCodingTmux(sessionID, false)
}

// SessionHasBusyMainCodingTmux applies the same live-pane busy check but only
// to the session's main coding agent. Child workflow/background terminals must
// not make chat input steerable when the main pane is absent.
func (s *Store) SessionHasBusyMainCodingTmux(sessionID string) bool {
	return s.sessionHasBusyCodingTmux(sessionID, true)
}

func (s *Store) sessionHasBusyCodingTmux(sessionID string, mainOnly bool) bool {
	if s == nil {
		return false
	}
	// Dismissal only hides a terminal from the UI. A dismissed live pane still
	// owns runtime work and must remain visible to busy/input routing.
	for _, snapshot := range s.ListRaw(sessionID) {
		if mainOnly && !currentTerminalIsMainAgent(snapshot) {
			continue
		}
		if strings.TrimSpace(snapshot.TmuxSession) == "" {
			continue
		}
		// Only a LIVE terminal can be busy. A completed/exited/stale pane (Active=false,
		// set by MarkCompleted/markTerminalState/MarkStale) keeps its last-captured
		// content, which for a coding-agent (codex) that exited mid-spinner still shows
		// a "Working…"/"esc to interrupt" line — but it is no longer processing. Counting
		// that stale snapshot as busy keeps the session "steerable"/running forever:
		// session_status never flips to completed, the chat's per-tab isStreaming stays
		// stuck true, and the user's next message routes to live-input on the dead pane
		// (silently lost / stranded) instead of starting a new /api/query turn. Skipping
		// non-Active terminals lets the session complete so follow-up turns submit.
		if !snapshot.Active {
			continue
		}
		// Scrollback can retain an old spinner after Codex or Claude has returned
		// to its input prompt. Only report the pane as busy when that spinner has
		// not been superseded by a later settled prompt.
		if terminalContentLooksBusy(snapshot.Content) &&
			!terminalHasSettledPromptAfterBusy(snapshot.Content, map[string]interface{}{
				"provider": snapshot.Status.ProviderLabel,
			}) {
			return true
		}
	}
	return false
}

// SessionHasRetainedCodingTmux reports whether this session still has a live
// tmux-backed coding-agent pane. This intentionally does not inspect "busy"
// text: an idle retained CLI can still hold agent context, accept the next user
// message, and be terminated by New Chat.
func (s *Store) SessionHasRetainedCodingTmux(sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	if s == nil || sessionID == "" {
		return false
	}
	for _, snapshot := range s.ListRaw(sessionID) {
		// Active describes the logical agent turn, not the lifetime of the
		// retained CLI process. A normal streaming_end settles the turn with
		// Active=false while deliberately leaving ProcessState="live" so the
		// same tmux can accept a follow-up. Requiring Active here made every
		// successfully completed Claude/Codex turn look as if its tmux vanished.
		if strings.TrimSpace(snapshot.TmuxSession) != "" &&
			(snapshot.Active || strings.EqualFold(strings.TrimSpace(snapshot.ProcessState), "live")) {
			return true
		}
	}
	return false
}

func (s *Store) Get(terminalID string) (Snapshot, bool) {
	terminalID = strings.TrimSpace(terminalID)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.pruneExpiredLocked(now)
	if _, dismissed := s.dismissed[terminalID]; dismissed {
		return Snapshot{}, false
	}
	return s.reconcileTerminalStateLocked(terminalID, now)
}

// GetRaw returns a snapshot even when it is dismissed from the UI. It is for
// lifecycle coordination only; HTTP handlers should continue to use Get.
func (s *Store) GetRaw(terminalID string) (Snapshot, bool) {
	terminalID = strings.TrimSpace(terminalID)
	if s == nil || terminalID == "" {
		return Snapshot{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	snapshot, ok := s.byID[terminalID]
	return snapshot, ok
}

// RebindLiveTmux attaches an existing terminal record to a replacement tmux
// pane. Coding-agent providers can replace their pane while recovering a
// failed turn; the logical main terminal remains the same, but its process
// identity changes. Keeping this update in the store makes every terminal
// route and subsequent poll converge on the replacement pane.
func (s *Store) RebindLiveTmux(terminalID, tmuxSession string) (Snapshot, bool) {
	terminalID = strings.TrimSpace(terminalID)
	tmuxSession = strings.TrimSpace(tmuxSession)
	if s == nil || terminalID == "" || tmuxSession == "" {
		return Snapshot{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, ok := s.byID[terminalID]
	if !ok {
		return Snapshot{}, false
	}
	snapshot.TmuxSession = tmuxSession
	snapshot.StepTransport = "tmux"
	snapshot.ContentSource = "tmux_live"
	snapshot.Active = true
	snapshot.State = "running"
	snapshot.ProcessState = "live"
	snapshot.SnapshotKind = "live"
	snapshot.CloseReason = ""
	snapshot.ClosesAt = nil
	snapshot.RetentionSeconds = 0
	snapshot.UpdatedAt = time.Now()
	delete(s.forcedInactive, terminalID)
	s.byID[terminalID] = snapshot
	return snapshot, true
}

func (s *Store) RemoveSession(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if s == nil || sessionID == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for terminalID := range s.bySession[sessionID] {
		delete(s.byID, terminalID)
		delete(s.forcedInactive, terminalID)
		delete(s.dismissed, terminalID)
		delete(s.toolLines, terminalID)
	}
	delete(s.bySession, sessionID)
}

func (s *Store) Dismiss(terminalID string) bool {
	terminalID = strings.TrimSpace(terminalID)
	if s == nil || terminalID == "" {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byID[terminalID]; !ok {
		return false
	}
	// Dismissal is presentation-only. Keep the snapshot's ownership metadata so
	// a live or closing tmux process remains visible to the lease/reaper path.
	s.dismissed[terminalID] = struct{}{}
	return true
}

// MarkCompleted is an operator override for terminal lifecycle bugs. It marks
// only the view-only terminal snapshot complete; it does not kill tmux or mutate
// workflow execution state.
func (s *Store) MarkCompleted(terminalID string) (Snapshot, bool) {
	return s.markTerminalState(terminalID, "completed")
}

// MarkFailed is an operator override for terminal lifecycle bugs. It marks
// only the view-only terminal snapshot failed; it does not mutate workflow
// execution state.
func (s *Store) MarkFailed(terminalID string) (Snapshot, bool) {
	return s.markTerminalState(terminalID, "failed")
}

// MarkTurnRunning reactivates a retained terminal for a new logical agent turn.
// Synthetic auto-notification turns reuse the existing coding-CLI tmux instead
// of passing through the normal query setup path, so no fresh terminal-start
// event is guaranteed. Advancing the revision also tells list/detail clients
// that any previously cached completed snapshot is stale.
func (s *Store) MarkTurnRunning(terminalID string) (Snapshot, bool) {
	terminalID = strings.TrimSpace(terminalID)
	if s == nil || terminalID == "" {
		return Snapshot{}, false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, ok := s.byID[terminalID]
	if !ok {
		return Snapshot{}, false
	}
	now := time.Now()
	snapshot.Active = true
	snapshot.State = "running"
	if strings.TrimSpace(snapshot.TmuxSession) != "" {
		snapshot.ProcessState = "live"
		snapshot.SnapshotKind = "live"
	}
	snapshot.CloseReason = ""
	snapshot.ClosesAt = nil
	snapshot.RetentionSeconds = 0
	// A live-attach transcript ("tmux_stream") is only the output a viewer saw
	// while it was attached. The new turn's output is not in it, so it can no
	// longer own the settled scrollback: without this, every later settled
	// detail read kept serving the transcript from the last attach and the
	// product terminal view froze on an old turn. A viewer attached during this
	// turn re-persists a fresh transcript (and the marker) when it detaches.
	if strings.EqualFold(strings.TrimSpace(snapshot.ContentSource), "tmux_stream") {
		snapshot.ContentSource = "tmux_capture"
	}
	snapshot.ChunkIndex++
	snapshot.UpdatedAt = now
	s.byID[terminalID] = snapshot
	delete(s.forcedInactive, terminalID)
	return snapshot, true
}

// MarkTurnCompleted settles a logical turn while retaining its live coding-CLI
// tmux for a later continuation. This differs from the operator override above:
// the process is idle/live, not closing, and may be reactivated by the next turn.
func (s *Store) MarkTurnCompleted(terminalID string) (Snapshot, bool) {
	return s.markTurnSettled(terminalID, "completed")
}

// MarkTurnFailed is the failed-turn counterpart to MarkTurnCompleted.
func (s *Store) MarkTurnFailed(terminalID string) (Snapshot, bool) {
	return s.markTurnSettled(terminalID, "failed")
}

// MarkStale flags a terminal whose backing tmux session has disappeared without
// a lifecycle completion event. Unlike MarkCompleted/MarkFailed it does not set
// a forcedInactive override, so a later successful capture can still reclassify
// the pane through RefreshContent's stale-recovery path. It is idempotent so the
// frontend's inactive-terminal probe can stop once the snapshot reads stale.
//
// The backing TmuxSession is also cleared so downstream handlers that act on
// the live pane (resize-window, send-keys, paste-buffer) short-circuit at their
// "no live pane" branch and return OK instead of hitting tmux and bubbling up a
// "can't find session" failure as a 502. The trade-off: a transient tmux hiccup
// that would previously self-heal via the recovery path now requires the
// terminal to be re-attached. In practice a dead session name does not come
// back, so this is the right default.
func (s *Store) MarkStale(terminalID string) (Snapshot, bool) {
	terminalID = strings.TrimSpace(terminalID)
	if s == nil || terminalID == "" {
		return Snapshot{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, ok := s.byID[terminalID]
	if !ok {
		return Snapshot{}, false
	}
	if snapshot.State == "stale" && !snapshot.Active && strings.TrimSpace(snapshot.TmuxSession) == "" {
		return snapshot, true
	}
	now := time.Now()
	snapshot.Active = false
	snapshot.State = "stale"
	snapshot.ProcessState = "closed"
	snapshot.SnapshotKind = "archived"
	snapshot.TmuxSession = ""
	snapshot.UpdatedAt = now
	s.byID[terminalID] = snapshot
	return snapshot, true
}

// MarkArchived records that the backing process was deliberately reconciled
// and closed while retaining the read-only capture until its snapshot deadline.
func (s *Store) MarkArchived(terminalID, reason string) (Snapshot, bool) {
	terminalID = strings.TrimSpace(terminalID)
	if s == nil || terminalID == "" {
		return Snapshot{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, ok := s.byID[terminalID]
	if !ok {
		return Snapshot{}, false
	}
	snapshot.Active = false
	snapshot.State = "stale"
	snapshot.ProcessState = "closed"
	snapshot.SnapshotKind = "archived"
	snapshot.TmuxSession = ""
	snapshot.CloseReason = strings.TrimSpace(reason)
	snapshot.UpdatedAt = time.Now()
	s.byID[terminalID] = snapshot
	return snapshot, true
}

// MarkProcessClosed separates process liveness from the terminal outcome. It
// preserves completed/failed state while converting the capture to an archive.
func (s *Store) MarkProcessClosed(terminalID, reason string) (Snapshot, bool) {
	terminalID = strings.TrimSpace(terminalID)
	if s == nil || terminalID == "" {
		return Snapshot{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, ok := s.byID[terminalID]
	if !ok {
		return Snapshot{}, false
	}
	snapshot.Active = false
	snapshot.ProcessState = "closed"
	snapshot.SnapshotKind = "archived"
	snapshot.CloseReason = strings.TrimSpace(reason)
	snapshot.TmuxSession = ""
	snapshot.UpdatedAt = time.Now()
	s.byID[terminalID] = snapshot
	return snapshot, true
}

// UpsertStaticSnapshot publishes a persisted terminal buffer as a read-only
// snapshot for a new/restored UI session. It intentionally clears TmuxSession:
// this snapshot is only the last rendered pane, not a live tmux target.
func (s *Store) UpsertStaticSnapshot(sessionID string, snapshot Snapshot) (Snapshot, bool) {
	sessionID = strings.TrimSpace(sessionID)
	if s == nil || sessionID == "" || strings.TrimSpace(snapshot.Content) == "" {
		return Snapshot{}, false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	ownerID := strings.TrimSpace(snapshot.OwnerID)
	if ownerID == "" || strings.HasPrefix(ownerID, "main:") || currentTerminalIsMainAgent(snapshot) {
		ownerID = "main:" + sessionID
	}
	terminalID := terminalIDFor(sessionID, ownerID)
	// Never clobber a LIVE tmux terminal with the static buffer. Once a resumed
	// session's transport has been materialized under this canonical terminalID
	// (Active + a real TmuxSession), a late static re-publish — the frontend's
	// restore-terminal POST races the /api/query re-launch that materializes the
	// live pane — must NOT reset it back to Active:false / TmuxSession:"". Doing so
	// strips the tmux_session the frontend needs to fire /resize, so tmux geometry
	// never matches the xterm and pi-cli's full-screen redraws append instead of
	// overwrite (duplicated status bar / stacked "Working..."). The live snapshot
	// already shows current content, so the static buffer adds nothing — keep live.
	if existing, ok := s.byID[terminalID]; ok && existing.Active && strings.TrimSpace(existing.TmuxSession) != "" {
		return existing, true
	}
	snapshot.TerminalID = terminalID
	snapshot.SessionID = sessionID
	snapshot.OwnerID = ownerID
	snapshot.TmuxSession = ""
	snapshot.Active = false
	snapshot.ProcessState = "closed"
	snapshot.SnapshotKind = "archived"
	if strings.TrimSpace(snapshot.State) == "" || snapshot.State == "running" || snapshot.State == "closing" {
		snapshot.State = "stale"
	}
	snapshot.ClosesAt = nil
	snapshot.RetentionSeconds = 0
	if snapshot.ExecutionKind == "" {
		snapshot.ExecutionKind = "main_agent"
	}
	if snapshot.Scope == "" {
		snapshot.Scope = "main_agent"
	}
	if snapshot.StepID == "" && currentTerminalIsMainAgent(snapshot) {
		snapshot.StepID = "main_agent:" + sessionID
	}
	if snapshot.StepTransport == "" {
		snapshot.StepTransport = "tmux"
	}
	if snapshot.ContentSource == "" {
		snapshot.ContentSource = "tmux_capture"
	}
	if snapshot.CreatedAt.IsZero() {
		snapshot.CreatedAt = now
	}
	snapshot.UpdatedAt = now
	snapshot.Rows = nil
	previousStatus := snapshot.Status
	snapshot.Status = DeriveStatus(snapshot.Content, nil)
	preserveEphemeralStatusFields(&snapshot.Status, previousStatus)
	fillDisplayContext(&snapshot)

	if _, ok := s.dismissed[terminalID]; ok {
		delete(s.dismissed, terminalID)
	}
	s.byID[terminalID] = snapshot
	if s.bySession[sessionID] == nil {
		s.bySession[sessionID] = make(map[string]struct{})
	}
	s.bySession[sessionID][terminalID] = struct{}{}
	if currentTerminalIsMainAgent(snapshot) {
		s.removeCurrentMainAgentAliasesLocked(sessionID, terminalID)
	}
	return snapshot, true
}

func (s *Store) markTerminalState(terminalID, state string) (Snapshot, bool) {
	terminalID = strings.TrimSpace(terminalID)
	if s == nil || terminalID == "" {
		return Snapshot{}, false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, ok := s.byID[terminalID]
	if !ok {
		return Snapshot{}, false
	}
	now := time.Now()
	snapshot.Active = false
	snapshot.State = state
	if strings.TrimSpace(snapshot.TmuxSession) != "" {
		snapshot.ProcessState = "closing"
		snapshot.SnapshotKind = "live"
	}
	snapshot.ClosesAt = nil
	snapshot.RetentionSeconds = 0
	snapshot.UpdatedAt = now
	s.byID[terminalID] = snapshot
	s.forcedInactive[terminalID] = now
	return snapshot, true
}

func (s *Store) markTurnSettled(terminalID, state string) (Snapshot, bool) {
	terminalID = strings.TrimSpace(terminalID)
	if s == nil || terminalID == "" {
		return Snapshot{}, false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, ok := s.byID[terminalID]
	if !ok {
		return Snapshot{}, false
	}
	now := time.Now()
	snapshot.Active = false
	snapshot.State = state
	if strings.TrimSpace(snapshot.TmuxSession) != "" {
		snapshot.ProcessState = "live"
		snapshot.SnapshotKind = "live"
	}
	snapshot.ClosesAt = nil
	snapshot.RetentionSeconds = 0
	snapshot.ChunkIndex++
	snapshot.UpdatedAt = now
	s.byID[terminalID] = snapshot
	s.forcedInactive[terminalID] = now
	return snapshot, true
}

// RefreshContent stores the latest terminal pane content from an
// operator-requested tmux capture. It keeps manual inactive overrides inactive,
// but otherwise lets the same lifecycle heuristics classify the refreshed pane.
// It simply replaces the stored content (no snapshot accumulation).
func (s *Store) RefreshContent(terminalID, content string) (Snapshot, bool) {
	return s.RefreshContentWithSource(terminalID, content, "")
}

// RefreshContentWithSource is RefreshContent plus a display-source marker used
// by the UI to keep retained tmux snapshots on the xterm rendering path.
func (s *Store) RefreshContentWithSource(terminalID, content, contentSource string) (Snapshot, bool) {
	return s.refreshContent(terminalID, content, contentSource)
}

// ReplaceContent refreshes a terminal pane with an authoritative capture,
// storing the latest content. It is identical to RefreshContent.
func (s *Store) ReplaceContent(terminalID, content string) (Snapshot, bool) {
	return s.ReplaceContentWithSource(terminalID, content, "")
}

// ReplaceContentWithSource is ReplaceContent plus a display-source marker used
// by the UI to keep retained tmux snapshots on the xterm rendering path.
func (s *Store) ReplaceContentWithSource(terminalID, content, contentSource string) (Snapshot, bool) {
	return s.refreshContent(terminalID, content, contentSource)
}

func (s *Store) SetDisplayContent(terminalID, content, contentSource string) (Snapshot, bool) {
	terminalID = strings.TrimSpace(terminalID)
	if s == nil || terminalID == "" {
		return Snapshot{}, false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, ok := s.byID[terminalID]
	if !ok {
		return Snapshot{}, false
	}
	now := time.Now()
	contentSource = strings.TrimSpace(contentSource)
	contentChanged := snapshot.Content != content
	sourceChanged := contentSource != "" && snapshot.ContentSource != contentSource
	if contentChanged {
		snapshot.Content = content
		snapshot.Rows = nil
	}
	if sourceChanged {
		snapshot.ContentSource = contentSource
	}
	if contentChanged || sourceChanged {
		snapshot.ChunkIndex++
		snapshot.UpdatedAt = now
	}
	s.byID[terminalID] = snapshot
	return snapshot, true
}

func (s *Store) refreshContent(terminalID, content, contentSource string) (Snapshot, bool) {
	terminalID = strings.TrimSpace(terminalID)
	if s == nil || terminalID == "" {
		return Snapshot{}, false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, ok := s.byID[terminalID]
	if !ok {
		return Snapshot{}, false
	}
	now := time.Now()
	lifecycleContent := content
	contentChanged := snapshot.Content != content
	snapshot.Content = content
	if contentChanged {
		snapshot.ChunkIndex++
	}
	if contentSource = strings.TrimSpace(contentSource); contentSource != "" {
		snapshot.ContentSource = contentSource
	}
	previousStatus := snapshot.Status
	snapshot.Status = DeriveStatus(lifecycleContent, nil)
	preserveEphemeralStatusFields(&snapshot.Status, previousStatus)
	if _, forced := s.forcedInactive[terminalID]; !forced {
		if snapshot.Active {
			if terminalCanCompleteFromCapturedIdle(snapshot) && terminalContentLooksIdle(lifecycleContent) {
				snapshot.Active = false
				snapshot.State = terminalStateFromContent(lifecycleContent, false)
				snapshot.ClosesAt = nil
				snapshot.RetentionSeconds = 0
			} else {
				snapshot.State = terminalStateFromContent(lifecycleContent, true)
			}
		} else if snapshot.State == "stale" {
			snapshot.Active = terminalStateFromContent(lifecycleContent, true) == "running" && !terminalContentLooksIdle(lifecycleContent)
			snapshot.State = terminalStateFromContent(lifecycleContent, snapshot.Active)
		} else if contentChanged && snapshot.State == "completed" && terminalContentLooksBusy(lifecycleContent) &&
			!terminalHasSettledPromptAfterBusy(lifecycleContent, nil) {
			// tmux session restarted inside the same terminal (e.g. Claude Code
			// context compaction: /exit then a fresh process in the same pane).
			// The content changed and now looks busy — re-activate so the UI
			// picks up the live output again.
			snapshot.Active = true
			snapshot.State = "running"
		} else if terminalStateFromContent(lifecycleContent, false) == "failed" {
			snapshot.State = "failed"
		}
	} else if contentChanged && snapshot.State == "completed" && terminalContentLooksBusy(lifecycleContent) &&
		!terminalHasSettledPromptAfterBusy(lifecycleContent, nil) {
		// Even force-completed terminals should restart when the tmux pane shows a
		// new process running (e.g. after Claude Code compaction). Clear the
		// forcedInactive entry so the terminal can participate in normal lifecycle.
		delete(s.forcedInactive, terminalID)
		snapshot.Active = true
		snapshot.State = "running"
	}
	if contentChanged || snapshot.UpdatedAt.IsZero() {
		snapshot.UpdatedAt = now
	}
	s.byID[terminalID] = snapshot
	return snapshot, true
}

func terminalCanCompleteFromCapturedIdle(snapshot Snapshot) bool {
	if !terminalUsesIdleTimeout(snapshot) {
		return false
	}
	if snapshot.ExecutionKind == "main_agent" || strings.HasPrefix(snapshot.OwnerID, "main:") {
		return false
	}
	return snapshot.ExecutionKind == "workflow_step" ||
		snapshot.Scope == "workflow_step" ||
		strings.HasPrefix(snapshot.OwnerID, "workflow-step:")
}

func (s *Store) upsertTerminal(sessionID string, event storeevents.Event, metadata map[string]interface{}, content string, chunkIndex int) {
	ownerID := terminalOwnerID(sessionID, event, metadata)
	terminalID := terminalIDFor(sessionID, ownerID)
	now := event.Timestamp
	if now.IsZero() {
		now = time.Now()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	current, exists := s.byID[terminalID]
	if forcedAt, forced := s.forcedInactive[terminalID]; forced {
		if !isNewTerminalTurnAfterManualComplete(current, now, chunkIndex, forcedAt) {
			return
		}
		delete(s.forcedInactive, terminalID)
	}
	inactiveNewTurn := exists && !current.Active && isNewTerminalTurn(current, now, chunkIndex)
	sameTurnExtension := exists && chunkIndex < current.ChunkIndex && terminalContentExtends(current.Content, content)
	chunkIndexResetTurn := exists && current.Active && !sameTurnExtension && isChunkIndexResetTerminalTurn(current, content, chunkIndex)
	if exists && chunkIndex < current.ChunkIndex && !inactiveNewTurn && !chunkIndexResetTurn && !sameTurnExtension {
		return
	}
	freshTurn := inactiveNewTurn || chunkIndexResetTurn
	// Rerun: a new turn has arrived for an owner whose previous turn
	// already completed. Archive the existing entry under a derived
	// terminalID so the read-only snapshot of the prior run stays in
	// the rail, then drop through to create a fresh entry at the
	// canonical terminalID for the new live turn. Skips when the
	// current entry is empty (no real content yet) — that's just a
	// pre-stream placeholder, not a finished run worth archiving.
	if exists && freshTurn && strings.TrimSpace(current.Content) != "" && shouldArchiveTerminalTurn(current) {
		archived := current
		archived.TerminalID = fmt.Sprintf("%s:turn-%d", terminalID, current.CreatedAt.UnixNano())
		archived.Active = false
		s.byID[archived.TerminalID] = archived
		if s.bySession[sessionID] == nil {
			s.bySession[sessionID] = make(map[string]struct{})
		}
		s.bySession[sessionID][archived.TerminalID] = struct{}{}
		// Force the canonical ID to be repopulated from scratch below.
		exists = false
	}
	if !exists {
		current = Snapshot{
			TerminalID: terminalID,
			SessionID:  sessionID,
			OwnerID:    ownerID,
			CreatedAt:  now,
		}
	}

	current.ExecutionID = firstNonEmpty(event.ExecutionID, stringValue(metadata, "execution_id"), stringValue(metadata, "execution_owner_id"), ownerID)
	// Enriched metadata can correct a provider event that inherited the parent
	// main-agent kind. Prefer it so child reviewers remain separate terminals.
	current.ExecutionKind = terminalExecutionKind(sessionID, ownerID, event, metadata)
	current.Label = terminalLabel(event, metadata, ownerID)
	current.Scope = normalizedTerminalScope(sessionID, ownerID, terminalScope(event, metadata), current.ExecutionKind)
	current.WorkflowPath = firstNonEmpty(stringValue(metadata, "workflow_path"), stringValue(metadata, "workspace_path"), stringValue(metadata, "working_directory"))
	current.WorkflowName = firstNonEmpty(stringValue(metadata, "workflow_name"), stringValue(metadata, "workflow_id"), workflowNameFromPath(current.WorkflowPath))
	current.WorkflowLabel = firstNonEmpty(stringValue(metadata, "workflow_label"), stringValue(metadata, "preset_name"), current.WorkflowName)
	current.StepID = firstNonEmpty(workflowStepIDFromOwner(ownerID), stringValue(metadata, "current_step_id"), stringValue(metadata, "workflow_step_id"), stringValue(metadata, "step_id"), current.StepID)
	// Main-agent terminals have no natural step_id, but the rail tree
	// needs a stable identifier so child terminals (workshop backgrounds
	// spawned by the main agent via run_full_workflow / run_step / etc.)
	// can point to it as their parent. Synthesize one per session — the
	// rail builder uses this value as a parent key, not for display.
	if current.StepID == "" && current.ExecutionKind == "main_agent" {
		current.StepID = "main_agent:" + sessionID
	}
	// step_name (from rich-context push) takes priority over the
	// legacy step_title key; both are accepted for backward compat.
	current.StepName = firstNonEmpty(stringValue(metadata, "step_name"), stringValue(metadata, "step_title"), stringValue(metadata, "current_step_title"), current.StepName)
	current.StepType = firstNonEmpty(stringValue(metadata, "plan_step_type"), stringValue(metadata, "workflow_step_type"), stringValue(metadata, "current_step_type"), stringValue(metadata, "step_type"), current.StepType)
	if stepIndex := intValue(metadata["step_index"]); stepIndex > 0 {
		current.StepIndex = stepIndex
	}
	if stepTotal := intValue(metadata["step_total"]); stepTotal > 0 {
		current.StepTotal = stepTotal
	}
	current.ParentStepID = firstNonEmpty(stringValue(metadata, "parent_step_id"), current.ParentStepID)
	// Default rooting: a terminal with no parent_step_id and not itself
	// the main agent gets implicitly parented to the main-agent
	// terminal of this session. The rail's buildTree only nests when
	// the parent step_id matches another terminal in the list, so this
	// is self-correcting: if no main_agent terminal exists for this
	// session, the synthetic parent_step_id won't resolve and the
	// terminal just renders at root anyway. Workshop backgrounds
	// spawned via run_full_workflow / run_step now hang off the main
	// agent in the rail.
	if current.ParentStepID == "" && current.ExecutionKind != "main_agent" {
		current.ParentStepID = "main_agent:" + sessionID
	}
	if stepAttempt := intValue(metadata["step_attempt"]); stepAttempt > 0 {
		current.StepAttempt = stepAttempt
	}
	current.StepExecutionMode = firstNonEmpty(stringValue(metadata, "step_execution_mode"), current.StepExecutionMode)
	current.StepTransport = firstNonEmpty(stringValue(metadata, "step_transport"), current.StepTransport)
	current.StepTriggeredBy = firstNonEmpty(stringValue(metadata, "step_triggered_by"), current.StepTriggeredBy)
	current.AgentName = firstNonEmpty(stringValue(metadata, "agent_name"), stringValue(metadata, "orchestrator_agent_name"), current.AgentName)
	current.TmuxSession = firstNonEmpty(
		stringValue(metadata, "tmux_session"),
		stringValue(metadata, "tmux_session_name"),
		stringValue(metadata, "pi_interactive_session"),
		stringValue(metadata, "claude_code_interactive_session"),
		stringValue(metadata, "codex_interactive_session"),
		stringValue(metadata, "gemini_interactive_session"),
		stringValue(metadata, "cursor_interactive_session"),
		current.TmuxSession,
	)
	content = s.contentWithToolLinesLocked(terminalID, content)
	lifecycleContent := content
	contentChanged := !exists || current.Content != content
	current.Content = content
	if rows := terminalRowsFromMetadata(metadata); len(rows) > 0 {
		current.Rows = rows
	} else if contentChanged {
		current.Rows = nil
	}
	if sameTurnExtension && chunkIndex < current.ChunkIndex {
		chunkIndex = current.ChunkIndex
	}
	current.ChunkIndex = chunkIndex
	current.Active = true
	current.State = terminalStateFromContent(lifecycleContent, true)
	current.ProcessState = "live"
	current.SnapshotKind = "live"
	current.CloseReason = ""
	current.ClosesAt = nil
	current.RetentionSeconds = 0
	previousStatus := current.Status
	current.Status = DeriveStatus(lifecycleContent, metadata)
	preserveEphemeralStatusFields(&current.Status, previousStatus)
	if contentChanged || current.UpdatedAt.IsZero() {
		current.UpdatedAt = now
	}
	fillDisplayContext(&current)

	s.removeTmuxAliasesLocked(sessionID, terminalID, current.TmuxSession)
	s.byID[terminalID] = current
	if _, ok := s.bySession[sessionID]; !ok {
		s.bySession[sessionID] = make(map[string]struct{})
	}
	s.bySession[sessionID][terminalID] = struct{}{}
	if currentTerminalIsCanonicalMainAgent(current) {
		s.removeCurrentMainAgentAliasesLocked(sessionID, terminalID)
	}
}

func (s *Store) ensureStructuredExecution(sessionID string, event storeevents.Event, metadata map[string]interface{}) {
	ownerID := terminalOwnerID(sessionID, event, metadata)
	terminalID := terminalIDFor(sessionID, ownerID)
	if terminalID == "" {
		return
	}
	now := event.Timestamp
	if now.IsZero() {
		now = time.Now()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dismissed := s.dismissed[terminalID]; dismissed {
		return
	}
	snapshot, exists := s.byID[terminalID]
	if !exists {
		snapshot = Snapshot{
			TerminalID: terminalID,
			SessionID:  sessionID,
			OwnerID:    ownerID,
			CreatedAt:  now,
			Content:    "Starting…",
		}
	}
	enrichStructuredExecutionSnapshot(&snapshot, sessionID, ownerID, event, metadata)
	snapshot.Active = true
	snapshot.State = "running"
	snapshot.ProcessState = "live"
	snapshot.SnapshotKind = "live"
	snapshot.CloseReason = ""
	snapshot.ClosesAt = nil
	snapshot.RetentionSeconds = 0
	snapshot.UpdatedAt = now
	fillDisplayContext(&snapshot)
	s.byID[terminalID] = snapshot
	if s.bySession[sessionID] == nil {
		s.bySession[sessionID] = make(map[string]struct{})
	}
	s.bySession[sessionID][terminalID] = struct{}{}
}

func (s *Store) upsertStructuredChunk(sessionID string, event storeevents.Event, metadata map[string]interface{}, content string, chunkIndex int) {
	ownerID := terminalOwnerID(sessionID, event, metadata)
	terminalID := terminalIDFor(sessionID, ownerID)
	if terminalID == "" {
		return
	}
	now := event.Timestamp
	if now.IsZero() {
		now = time.Now()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dismissed := s.dismissed[terminalID]; dismissed {
		return
	}
	snapshot, exists := s.byID[terminalID]
	if !exists {
		snapshot = Snapshot{
			TerminalID: terminalID,
			SessionID:  sessionID,
			OwnerID:    ownerID,
			CreatedAt:  now,
		}
	}
	enrichStructuredExecutionSnapshot(&snapshot, sessionID, ownerID, event, metadata)
	if strings.TrimSpace(snapshot.Content) == "Starting…" {
		snapshot.Content = ""
	}
	if structuredChunkIsDelta(event) {
		snapshot.Content += content
	} else if strings.TrimSpace(content) != "" && !strings.HasSuffix(snapshot.Content, content) {
		if snapshot.Content != "" && !strings.HasSuffix(snapshot.Content, "\n") {
			snapshot.Content += "\n"
		}
		snapshot.Content += content
	}
	snapshot.Content = s.contentWithToolLinesLocked(terminalID, snapshot.Content)
	snapshot.ChunkIndex = max(snapshot.ChunkIndex, chunkIndex)
	snapshot.Active = true
	snapshot.State = "running"
	snapshot.ProcessState = "live"
	snapshot.SnapshotKind = "live"
	snapshot.CloseReason = ""
	snapshot.ClosesAt = nil
	snapshot.RetentionSeconds = 0
	snapshot.UpdatedAt = now
	previousStatus := snapshot.Status
	snapshot.Status = DeriveStatus(snapshot.Content, metadata)
	preserveEphemeralStatusFields(&snapshot.Status, previousStatus)
	fillDisplayContext(&snapshot)
	s.byID[terminalID] = snapshot
	if s.bySession[sessionID] == nil {
		s.bySession[sessionID] = make(map[string]struct{})
	}
	s.bySession[sessionID][terminalID] = struct{}{}
}

func (s *Store) upsertStructuredMessage(sessionID string, event storeevents.Event, metadata map[string]interface{}, content string) {
	ownerID := terminalOwnerID(sessionID, event, metadata)
	terminalID := terminalIDFor(sessionID, ownerID)
	if terminalID == "" {
		return
	}
	now := event.Timestamp
	if now.IsZero() {
		now = time.Now()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dismissed := s.dismissed[terminalID]; dismissed {
		return
	}
	snapshot, exists := s.byID[terminalID]
	if !exists {
		snapshot = Snapshot{
			TerminalID: terminalID,
			SessionID:  sessionID,
			OwnerID:    ownerID,
			CreatedAt:  now,
		}
	}
	enrichStructuredExecutionSnapshot(&snapshot, sessionID, ownerID, event, metadata)
	if strings.TrimSpace(snapshot.Content) == "Starting…" {
		snapshot.Content = ""
	}
	snapshot.Content = appendStructuredContent(snapshot.Content, content)
	snapshot.Content = s.contentWithToolLinesLocked(terminalID, snapshot.Content)
	snapshot.Active = true
	snapshot.State = "running"
	snapshot.ProcessState = "live"
	snapshot.SnapshotKind = "live"
	snapshot.CloseReason = ""
	snapshot.ClosesAt = nil
	snapshot.RetentionSeconds = 0
	snapshot.UpdatedAt = now
	fillDisplayContext(&snapshot)
	s.byID[terminalID] = snapshot
	if s.bySession[sessionID] == nil {
		s.bySession[sessionID] = make(map[string]struct{})
	}
	s.bySession[sessionID][terminalID] = struct{}{}
}

func (s *Store) appendStructuredLifecycleResult(sessionID string, event storeevents.Event, metadata map[string]interface{}) {
	result := firstNonEmpty(stringValue(metadata, "result"), stringValue(metadata, "error"))
	if strings.TrimSpace(result) == "" {
		return
	}
	prefix := "< asst: "
	if structuredExecutionFailed(event) {
		prefix = "[error] "
	}
	s.upsertStructuredMessage(sessionID, event, metadata, prefix+result)
}

func appendStructuredContent(content, addition string) string {
	addition = strings.TrimSpace(addition)
	if addition == "" {
		return content
	}
	trimmed := strings.TrimRight(content, "\n")
	if strings.Contains(trimmed, addition) {
		return content
	}
	if trimmed == "" {
		return addition
	}
	return trimmed + "\n" + addition
}

func (s *Store) completeStructuredExecution(sessionID string, event storeevents.Event, metadata map[string]interface{}, failed bool) {
	ownerID := terminalOwnerID(sessionID, event, metadata)
	terminalID := terminalIDFor(sessionID, ownerID)
	if terminalID == "" {
		return
	}
	now := event.Timestamp
	if now.IsZero() {
		now = time.Now()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, exists := s.byID[terminalID]
	if !exists {
		snapshot = Snapshot{
			TerminalID: terminalID,
			SessionID:  sessionID,
			OwnerID:    ownerID,
			CreatedAt:  now,
		}
	}
	enrichStructuredExecutionSnapshot(&snapshot, sessionID, ownerID, event, metadata)
	if strings.TrimSpace(snapshot.Content) == "" || strings.TrimSpace(snapshot.Content) == "Starting…" {
		if failed {
			snapshot.Content = "The execution failed before producing output."
		} else {
			snapshot.Content = "The execution completed."
		}
	}
	snapshot.Active = false
	if failed {
		snapshot.State = "failed"
		snapshot.CloseReason = firstNonEmpty(event.Error, "execution failed")
	} else {
		snapshot.State = "completed"
		snapshot.CloseReason = ""
	}
	snapshot.ProcessState = "closed"
	snapshot.SnapshotKind = "archived"
	snapshot.ClosesAt = nil
	snapshot.RetentionSeconds = 0
	snapshot.UpdatedAt = now
	fillDisplayContext(&snapshot)
	s.byID[terminalID] = snapshot
	if s.bySession[sessionID] == nil {
		s.bySession[sessionID] = make(map[string]struct{})
	}
	s.bySession[sessionID][terminalID] = struct{}{}
}

func enrichStructuredExecutionSnapshot(snapshot *Snapshot, sessionID, ownerID string, event storeevents.Event, metadata map[string]interface{}) {
	if snapshot == nil {
		return
	}
	snapshot.ExecutionID = firstNonEmpty(workflowExecutionIDFromOwner(ownerID), snapshot.ExecutionID, event.ExecutionID, stringValue(metadata, "execution_id"), stringValue(metadata, "execution_owner_id"), ownerID)
	snapshot.ExecutionKind = terminalExecutionKind(sessionID, ownerID, event, metadata)
	snapshot.Scope = normalizedTerminalScope(sessionID, ownerID, terminalScope(event, metadata), snapshot.ExecutionKind)
	snapshot.WorkflowPath = firstNonEmpty(stringValue(metadata, "workflow_path"), stringValue(metadata, "workspace_path"), stringValue(metadata, "working_directory"), snapshot.WorkflowPath)
	snapshot.WorkflowName = firstNonEmpty(stringValue(metadata, "workflow_name"), stringValue(metadata, "workflow_id"), workflowNameFromPath(snapshot.WorkflowPath), snapshot.WorkflowName)
	snapshot.WorkflowLabel = firstNonEmpty(stringValue(metadata, "workflow_label"), stringValue(metadata, "preset_name"), snapshot.WorkflowName, snapshot.WorkflowLabel)
	ownerStepID := workflowStepIDFromOwner(ownerID)
	metadataStepID := firstNonEmpty(stringValue(metadata, "current_step_id"), stringValue(metadata, "workflow_step_id"), stringValue(metadata, "step_id"))
	snapshot.StepID = firstNonEmpty(ownerStepID, metadataStepID, snapshot.StepID)
	stepContextMatchesOwner := ownerStepID == "" || metadataStepID == "" || ownerStepID == metadataStepID
	if stepContextMatchesOwner {
		// The first event that creates the execution has the authoritative plan
		// title. Later completion events can inherit a sibling's shared
		// current_step_title while retaining this execution's correct step ID.
		// Never let that late shared context relabel an existing transcript.
		snapshot.StepName = firstNonEmpty(snapshot.StepName, stringValue(metadata, "step_name"), stringValue(metadata, "step_title"), stringValue(metadata, "current_step_title"), humanizeIdentifier(snapshot.StepID))
	} else {
		// Shared workflow context can briefly point at a parallel sibling.
		snapshot.StepName = firstNonEmpty(snapshot.StepName, humanizeIdentifier(snapshot.StepID))
	}
	if workflowStepIDFromOwner(ownerID) != "" {
		snapshot.Label = firstNonEmpty(snapshot.StepName, humanizeIdentifier(snapshot.StepID), snapshot.Label)
	} else {
		snapshot.Label = firstNonEmpty(terminalLabel(event, metadata, ownerID), snapshot.Label)
	}
	if stepContextMatchesOwner {
		snapshot.StepType = firstNonEmpty(stringValue(metadata, "plan_step_type"), stringValue(metadata, "workflow_step_type"), stringValue(metadata, "current_step_type"), stringValue(metadata, "step_type"), snapshot.StepType)
	}
	snapshot.ParentStepID = firstNonEmpty(stringValue(metadata, "parent_step_id"), snapshot.ParentStepID)
	if snapshot.ParentStepID == "" && snapshot.ExecutionKind != "main_agent" {
		snapshot.ParentStepID = "main_agent:" + sessionID
	}
	snapshot.AgentName = firstNonEmpty(stringValue(metadata, "agent_name"), stringValue(metadata, "orchestrator_agent_name"), snapshot.AgentName)
	snapshot.StepTransport = "structured"
}

func structuredChunkIsDelta(event storeevents.Event) bool {
	if event.Data == nil || event.Data.Data == nil {
		return false
	}
	switch data := event.Data.Data.(type) {
	case *agentevents.StreamingChunkEvent:
		return data.IsDelta
	case *agentevents.GenericEventData:
		value, _ := data.Data["is_delta"].(bool)
		return value
	default:
		return false
	}
}

func structuredUserMessage(event storeevents.Event) string {
	if event.Data == nil || event.Data.Data == nil {
		return ""
	}
	switch data := event.Data.Data.(type) {
	case *agentevents.UserMessageEvent:
		return data.Content
	case *agentevents.GenericEventData:
		return firstNonEmpty(stringValue(data.Data, "content"), stringValue(data.Data, "message"))
	default:
		return ""
	}
}

func isAutoNotificationMessage(content string) bool {
	return strings.HasPrefix(strings.TrimSpace(content), "[AUTO-NOTIFICATION]")
}

type asyncSubAgentTerminalCompletion struct {
	ExecutionID string `json:"execution_id"`
	RouteID     string `json:"route_id"`
	Status      string `json:"status"`
	Error       string `json:"error"`
}

// reconcileAsyncSubAgentCompletionBatch consumes the runtime's authoritative
// child-completion batch without rendering its technical JSON as a user
// message. Async predefined routes have two identities:
//   - todo-sub-* is the lightweight lifecycle wrapper returned to the parent;
//   - workflow-step:exec-*:route-id owns the real structured transcript.
//
// The runtime batch is the first place where both identities and the terminal
// status are available together. Settling both prevents a completed structured
// child from remaining in the Live rail indefinitely.
func (s *Store) reconcileAsyncSubAgentCompletionBatch(sessionID, content string, at time.Time) bool {
	const header = "[AUTO-NOTIFICATION] SUB-AGENT COMPLETION BATCH"
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, header) {
		return false
	}
	jsonStart := strings.Index(trimmed, "[\n")
	if jsonStart < 0 {
		jsonStart = strings.Index(trimmed, "[{")
	}
	if jsonStart < 0 {
		return false
	}
	jsonEnd := strings.Index(trimmed[jsonStart:], "\n\nContinue the same task now.")
	if jsonEnd < 0 {
		return false
	}

	var completions []asyncSubAgentTerminalCompletion
	if err := json.Unmarshal([]byte(strings.TrimSpace(trimmed[jsonStart:jsonStart+jsonEnd])), &completions); err != nil {
		return false
	}
	if len(completions) == 0 {
		return false
	}
	if at.IsZero() {
		at = time.Now()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for _, completion := range completions {
		status := strings.ToLower(strings.TrimSpace(completion.Status))
		failed := status == "failed" || status == "canceled" || status == "cancelled" //nolint:misspell // Accept both API spellings.

		// Settle the lifecycle wrapper if its start event created one.
		wrapperID := terminalIDFor(sessionID, strings.TrimSpace(completion.ExecutionID))
		if snapshot, ok := s.byID[wrapperID]; ok {
			settleStructuredSnapshot(&snapshot, failed, completion.Error, at)
			s.byID[wrapperID] = snapshot
			changed = true
		}

		// A predefined route's real transcript is keyed by route_id. Select
		// the newest active attempt so retries remain independently inspectable.
		routeID := strings.TrimSpace(completion.RouteID)
		if routeID == "" {
			continue
		}
		var selectedID string
		var selected Snapshot
		for terminalID := range s.bySession[sessionID] {
			candidate, ok := s.byID[terminalID]
			if !ok || !candidate.Active || candidate.StepID != routeID ||
				!strings.Contains(candidate.OwnerID, "workflow-step:") {
				continue
			}
			if selectedID == "" || candidate.CreatedAt.After(selected.CreatedAt) {
				selectedID = terminalID
				selected = candidate
			}
		}
		if selectedID != "" {
			settleStructuredSnapshot(&selected, failed, completion.Error, at)
			s.byID[selectedID] = selected
			changed = true
		}
	}
	return changed
}

func settleStructuredSnapshot(snapshot *Snapshot, failed bool, errorText string, at time.Time) {
	if snapshot == nil {
		return
	}
	snapshot.Active = false
	if failed {
		snapshot.State = "failed"
		snapshot.CloseReason = firstNonEmpty(strings.TrimSpace(errorText), "execution failed")
	} else {
		snapshot.State = "completed"
		snapshot.CloseReason = ""
	}
	snapshot.ProcessState = "closed"
	snapshot.SnapshotKind = "archived"
	snapshot.ClosesAt = nil
	snapshot.RetentionSeconds = 0
	snapshot.UpdatedAt = at
	fillDisplayContext(snapshot)
}

func structuredLifecycleIsNestedSequence(event storeevents.Event, metadata map[string]interface{}) bool {
	agentID := firstNonEmpty(
		stringValue(metadata, "agent_id"),
		stringValue(metadata, "background_agent_id"),
		event.ExecutionID,
	)
	parentID := firstNonEmpty(
		stringValue(metadata, "parent_execution_id"),
		stringValue(metadata, "owner_execution_id"),
	)
	return strings.HasPrefix(agentID, "msgseq-") && strings.HasPrefix(parentID, "exec-")
}

func structuredExecutionFailed(event storeevents.Event) bool {
	if strings.TrimSpace(event.Error) != "" {
		return true
	}
	if event.Data == nil || event.Data.Data == nil {
		return false
	}
	switch data := event.Data.Data.(type) {
	case *agentevents.AgentEndEvent:
		return !data.Success || strings.TrimSpace(data.Error) != ""
	case *agentevents.GenericEventData:
		if errText, _ := data.Data["error"].(string); strings.TrimSpace(errText) != "" {
			return true
		}
		if success, ok := data.Data["success"].(bool); ok {
			return !success
		}
	}
	return false
}

func terminalUsesIdleTimeout(snapshot Snapshot) bool {
	return strings.EqualFold(strings.TrimSpace(snapshot.StepTransport), "tmux") ||
		strings.TrimSpace(snapshot.TmuxSession) != ""
}

func (s *Store) upsertToolLine(sessionID string, event storeevents.Event, metadata map[string]interface{}) {
	if event.Data == nil || event.Data.Data == nil {
		return
	}
	ownerID := terminalOwnerID(sessionID, event, metadata)
	terminalID := terminalIDFor(sessionID, ownerID)
	if terminalID == "" {
		return
	}
	now := event.Timestamp
	if now.IsZero() {
		now = time.Now()
	}

	var toolCallID, toolName, serverName, args, result, resultPrefix string
	switch data := event.Data.Data.(type) {
	case *agentevents.ToolCallStartEvent:
		toolCallID = data.ToolCallID
		toolName = data.ToolName
		serverName = data.ServerName
		args = data.ToolParams.Arguments
	case *agentevents.ToolCallEndEvent:
		toolCallID = data.ToolCallID
		toolName = data.ToolName
		serverName = data.ServerName
		result = data.Result
		resultPrefix = "✓"
	case *agentevents.ToolCallErrorEvent:
		toolCallID = data.ToolCallID
		toolName = data.ToolName
		serverName = data.ServerName
		result = data.Error
		resultPrefix = "✗"
	default:
		return
	}
	toolName = strings.TrimSpace(firstNonEmpty(
		toolName,
		stringValue(metadata, "tool_name"),
		serverName,
		stringValue(metadata, "server_name"),
	))
	if toolName == "" {
		toolName = "tool"
	}
	toolCallID = strings.TrimSpace(toolCallID)
	if toolCallID == "" {
		toolCallID = fmt.Sprintf("%s:%d", toolName, now.UnixNano())
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.dismissed[terminalID]; ok {
		return
	}
	lines := s.toolLines[terminalID]
	if lines == nil {
		lines = &terminalToolLines{items: make(map[string]*terminalToolLine)}
		s.toolLines[terminalID] = lines
	}
	item := lines.items[toolCallID]
	if item == nil {
		item = &terminalToolLine{}
		lines.items[toolCallID] = item
		lines.order = append(lines.order, toolCallID)
	}
	item.name = firstNonEmpty(toolName, item.name)
	if args != "" {
		item.args = redactTerminalToolText(args)
	}
	if result != "" {
		item.result = redactTerminalToolText(result)
		item.resultPrefix = resultPrefix
	}

	snapshot, ok := s.byID[terminalID]
	if !ok {
		return
	}
	if strings.TrimSpace(snapshot.Content) == "Starting…" {
		snapshot.Content = ""
	}
	snapshot.Content = s.contentWithToolLinesLocked(terminalID, snapshot.Content)
	previousStatus := snapshot.Status
	snapshot.Status = DeriveStatus(snapshot.Content, metadata)
	preserveEphemeralStatusFields(&snapshot.Status, previousStatus)
	snapshot.UpdatedAt = now
	s.byID[terminalID] = snapshot
}

func (s *Store) contentWithToolLinesLocked(terminalID, content string) string {
	lines := s.toolLines[terminalID]
	if lines == nil || len(lines.order) == 0 {
		return content
	}

	base, doneFooter := splitTerminalDoneFooter(stripTerminalToolLines(content))
	var b strings.Builder
	b.WriteString(strings.TrimRight(base, "\n"))
	b.WriteString("\n")
	for _, id := range lines.order {
		item := lines.items[id]
		if item == nil {
			continue
		}
		name := firstNonEmpty(item.name, "tool")
		fmt.Fprintf(&b, "→ tool: %s(%s)\n", name, truncateTerminalToolText(item.args))
		if item.result != "" {
			prefix := firstNonEmpty(item.resultPrefix, "✓")
			fmt.Fprintf(&b, "%s result %s: %s\n", prefix, name, truncateTerminalToolText(item.result))
		}
	}
	if doneFooter != "" {
		b.WriteString(strings.TrimLeft(doneFooter, "\n"))
	}
	return b.String()
}

func splitTerminalDoneFooter(content string) (string, string) {
	trimmed := strings.TrimRight(content, "\n")
	if strings.HasPrefix(trimmed, "[done") {
		return "", trimmed + "\n"
	}
	if idx := strings.LastIndex(trimmed, "\n[done"); idx >= 0 {
		return trimmed[:idx], trimmed[idx+1:] + "\n"
	}
	return content, ""
}

func stripTerminalToolLines(content string) string {
	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(line, "→ tool: ") || strings.HasPrefix(line, "✓ result ") || strings.HasPrefix(line, "✗ result ") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func redactTerminalToolText(value string) string {
	return RedactSensitiveTerminalText(value)
}

// RedactSensitiveTerminalText removes common secret shapes before terminal text
// is surfaced outside the terminal package.
func RedactSensitiveTerminalText(value string) string {
	value = regexpMCPToken.ReplaceAllString(value, "$1[redacted]")
	value = regexpSensitiveEnv.ReplaceAllString(value, "$1[redacted]")
	value = regexpBearerToken.ReplaceAllString(value, "$1[redacted]")
	value = regexpSecretEnv.ReplaceAllString(value, "$1[redacted]")
	value = regexpProviderSKKey.ReplaceAllString(value, "sk-[redacted]")
	value = regexpGoogleAPIKey.ReplaceAllString(value, "AIza[redacted]")
	return value
}

func truncateTerminalToolText(value string) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= terminalToolTextMaxRunes {
		return value
	}
	return string(runes[:terminalToolTextMaxRunes]) + "... [truncated]"
}

func terminalRowsFromMetadata(metadata map[string]interface{}) []Row {
	if len(metadata) == 0 {
		return nil
	}
	raw, ok := metadata["rows"]
	if !ok || raw == nil {
		raw, ok = metadata["terminal_rows"]
	}
	if !ok || raw == nil {
		return nil
	}
	switch rows := raw.(type) {
	case []Row:
		return cloneTerminalRows(rows)
	case []map[string]interface{}:
		return terminalRowsFromMaps(rows)
	case []interface{}:
		return terminalRowsFromInterfaces(rows)
	default:
		data, err := json.Marshal(raw)
		if err != nil {
			return nil
		}
		var parsed []Row
		if err := json.Unmarshal(data, &parsed); err != nil {
			return nil
		}
		return cloneTerminalRows(parsed)
	}
}

func terminalRowsFromMaps(items []map[string]interface{}) []Row {
	rows := make([]Row, 0, len(items))
	for _, item := range items {
		row := Row{
			Kind:         stringValue(item, "kind"),
			Text:         stringValue(item, "text"),
			Name:         stringValue(item, "name"),
			Args:         stringValue(item, "args"),
			Result:       stringValue(item, "result"),
			ResultPrefix: stringValue(item, "result_prefix"),
		}
		if row.Kind != "" {
			rows = append(rows, row)
		}
	}
	return rows
}

func terminalRowsFromInterfaces(items []interface{}) []Row {
	rows := make([]Row, 0, len(items))
	for _, item := range items {
		switch value := item.(type) {
		case Row:
			rows = append(rows, value)
		case map[string]interface{}:
			rows = append(rows, terminalRowsFromMaps([]map[string]interface{}{value})...)
		default:
			data, err := json.Marshal(value)
			if err != nil {
				continue
			}
			var row Row
			if err := json.Unmarshal(data, &row); err != nil {
				continue
			}
			if row.Kind != "" {
				rows = append(rows, row)
			}
		}
	}
	return rows
}

func cloneTerminalRows(rows []Row) []Row {
	out := make([]Row, 0, len(rows))
	for _, row := range rows {
		if row.Kind == "" {
			continue
		}
		out = append(out, row)
	}
	return out
}

func (s *Store) reconcileTerminalStateLocked(terminalID string, _ time.Time) (Snapshot, bool) {
	snapshot, ok := s.byID[terminalID]
	if !ok {
		return Snapshot{}, false
	}
	// Do not infer completion from elapsed time. A coding CLI can sit at a stable
	// prompt for hours while its tmux pane remains alive and ready for input. The
	// server's tmux watchdog owns liveness reconciliation using the real pane state.
	return snapshot, true
}

func (s *Store) removeTmuxAliasesLocked(sessionID, terminalID, tmuxSession string) {
	tmuxSession = strings.TrimSpace(tmuxSession)
	if tmuxSession == "" {
		return
	}
	for existingID := range s.bySession[sessionID] {
		if existingID == terminalID {
			continue
		}
		if strings.Contains(existingID, ":turn-") {
			continue
		}
		existing, ok := s.byID[existingID]
		if !ok || strings.TrimSpace(existing.TmuxSession) != tmuxSession {
			continue
		}
		delete(s.byID, existingID)
		delete(s.forcedInactive, existingID)
		delete(s.bySession[sessionID], existingID)
	}
	if len(s.bySession[sessionID]) == 0 {
		delete(s.bySession, sessionID)
	}
}

func (s *Store) removeCurrentMainAgentAliasesLocked(sessionID, keepTerminalID string) {
	for terminalID := range s.bySession[sessionID] {
		if terminalID == keepTerminalID || strings.Contains(terminalID, ":turn-") {
			continue
		}
		snapshot, ok := s.byID[terminalID]
		if !ok || !currentTerminalIsMainAgent(snapshot) {
			continue
		}
		s.removeTerminalLocked(terminalID)
	}
}

func (s *Store) pruneExpiredLocked(now time.Time) {
	if now.IsZero() {
		now = time.Now()
	}
	for terminalID, snapshot := range s.byID {
		if snapshot.Active || snapshot.ClosesAt == nil || now.Before(*snapshot.ClosesAt) {
			continue
		}
		s.removeTerminalLocked(terminalID)
	}
}

func (s *Store) removeTerminalLocked(terminalID string) {
	snapshot, ok := s.byID[terminalID]
	if !ok {
		return
	}
	delete(s.byID, terminalID)
	delete(s.forcedInactive, terminalID)
	delete(s.dismissed, terminalID)
	if snapshot.SessionID == "" {
		return
	}
	delete(s.bySession[snapshot.SessionID], terminalID)
	delete(s.toolLines, terminalID)
	if len(s.bySession[snapshot.SessionID]) == 0 {
		delete(s.bySession, snapshot.SessionID)
	}
}

func isNewTerminalTurn(current Snapshot, eventTime time.Time, chunkIndex int) bool {
	if !current.Active {
		return true
	}
	if chunkIndex > 2 {
		return false
	}
	if eventTime.IsZero() || current.UpdatedAt.IsZero() {
		return false
	}
	return eventTime.After(current.UpdatedAt.Add(250 * time.Millisecond))
}

func isChunkIndexResetTerminalTurn(current Snapshot, content string, chunkIndex int) bool {
	if current.ChunkIndex <= 2 || chunkIndex > 2 {
		return false
	}
	if strings.TrimSpace(content) == "" || content == current.Content {
		return false
	}
	return true
}

func shouldArchiveTerminalTurn(current Snapshot) bool {
	if currentTerminalIsMainAgent(current) && strings.TrimSpace(current.TmuxSession) != "" {
		return false
	}
	return true
}

func terminalContentExtends(existing, next string) bool {
	existing = strings.TrimRight(existing, "\n")
	next = strings.TrimRight(next, "\n")
	if existing == "" || next == "" || existing == next {
		return false
	}
	return strings.HasPrefix(next, existing)
}

func dedupeCurrentMainAgentSnapshots(snapshots []Snapshot) []Snapshot {
	if len(snapshots) <= 1 {
		return snapshots
	}
	out := make([]Snapshot, 0, len(snapshots))
	mainBySession := map[string]int{}
	for _, snapshot := range snapshots {
		if !currentTerminalIsMainAgent(snapshot) {
			out = append(out, snapshot)
			continue
		}
		sessionID := strings.TrimSpace(snapshot.SessionID)
		if sessionID == "" {
			sessionID = strings.TrimSpace(snapshot.OwnerID)
		}
		if sessionID == "" {
			sessionID = strings.TrimSpace(snapshot.TerminalID)
		}
		if idx, ok := mainBySession[sessionID]; ok {
			if shouldPreferTerminalSnapshot(snapshot, out[idx]) {
				out[idx] = snapshot
			}
			continue
		}
		mainBySession[sessionID] = len(out)
		out = append(out, snapshot)
	}
	return out
}

func currentTerminalIsMainAgent(snapshot Snapshot) bool {
	if strings.Contains(snapshot.TerminalID, ":turn-") {
		return false
	}
	kind := strings.ToLower(strings.TrimSpace(firstNonEmpty(snapshot.ExecutionKind, snapshot.Scope)))
	if kind != "" {
		return kind == "main_agent" || kind == "main" || kind == "chat"
	}
	owner := strings.TrimSpace(snapshot.OwnerID)
	return owner != "" && (owner == snapshot.SessionID || strings.HasPrefix(owner, "main:"))
}

func currentTerminalIsCanonicalMainAgent(snapshot Snapshot) bool {
	return currentTerminalIsMainAgent(snapshot) && terminalOwnerIsCanonicalMain(snapshot.SessionID, snapshot.OwnerID)
}

func shouldPreferTerminalSnapshot(candidate, existing Snapshot) bool {
	if candidate.Active != existing.Active {
		return candidate.Active
	}
	candidateRunning := candidate.State == "running"
	existingRunning := existing.State == "running"
	if candidateRunning != existingRunning {
		return candidateRunning
	}
	return candidate.UpdatedAt.After(existing.UpdatedAt)
}

func isNewTerminalTurnAfterManualComplete(current Snapshot, eventTime time.Time, chunkIndex int, forcedAt time.Time) bool {
	if chunkIndex > 2 {
		return false
	}
	if eventTime.IsZero() {
		return false
	}
	if !forcedAt.IsZero() && !eventTime.After(forcedAt.Add(250*time.Millisecond)) {
		return false
	}
	if !current.UpdatedAt.IsZero() && !eventTime.After(current.UpdatedAt.Add(250*time.Millisecond)) {
		return false
	}
	return true
}

// WithContext returns a copy enriched with session-level context. Terminal
// stream metadata wins; session context fills gaps.
func (snapshot Snapshot) WithContext(ctx Context) Snapshot {
	snapshot.WorkflowPath = firstNonEmpty(snapshot.WorkflowPath, ctx.WorkspacePath)
	snapshot.WorkflowName = firstNonEmpty(snapshot.WorkflowName, ctx.WorkflowName, workflowNameFromPath(snapshot.WorkflowPath))
	snapshot.WorkflowLabel = firstNonEmpty(snapshot.WorkflowLabel, ctx.WorkflowLabel, snapshot.WorkflowName)
	if snapshot.Scope == "" && ctx.ExecutionName != "" {
		snapshot.Scope = "session"
	}
	fillDisplayContext(&snapshot)
	return snapshot
}

func (s *Store) markInactive(sessionID, ownerID string, metadata map[string]interface{}, completedAt time.Time) {
	terminalID := terminalIDFor(sessionID, ownerID)
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, ok := s.byID[terminalID]
	if !ok {
		var resolvedID string
		resolvedID, snapshot, ok = s.findInactiveTargetLocked(sessionID, ownerID, metadata)
		if !ok {
			return
		}
		terminalID = resolvedID
	}
	// A delayed end event from an older turn must not close a newer turn that
	// has already produced terminal output under the same persistent pane.
	if !completedAt.IsZero() && !snapshot.UpdatedAt.IsZero() &&
		snapshot.UpdatedAt.After(completedAt.Add(250*time.Millisecond)) {
		return
	}
	now := completedAt
	if now.IsZero() {
		now = time.Now()
	}
	// streaming_end is the provider's authoritative turn boundary once the pane
	// has settled. Historical spinner text can remain in tmux scrollback after
	// Codex/Claude is back at its prompt, so compare the latest spinner/prompt
	// order instead of letting any old busy line override completion. A pane whose
	// latest visible state is still an active spinner remains running; some CLIs
	// emit an intermediate end event before their TUI has actually settled.
	explicitOutcome := terminalLatestExplicitOutcome(snapshot.Content)
	if terminalContentLooksBusy(snapshot.Content) && explicitOutcome == "" &&
		!terminalHasSettledPromptAfterBusy(snapshot.Content, metadata) {
		snapshot.Active = true
		snapshot.State = "running"
		snapshot.ProcessState = "live"
		snapshot.SnapshotKind = "live"
		snapshot.CloseReason = ""
		snapshot.ClosesAt = nil
		snapshot.RetentionSeconds = 0
		snapshot.UpdatedAt = now
		s.byID[terminalID] = snapshot
		return
	}
	state := "completed"
	if terminalContentLooksFatal(snapshot.Content) || explicitOutcome == "failed" {
		state = "failed"
	}
	snapshot.Active = false
	if strings.TrimSpace(snapshot.TmuxSession) != "" {
		// Persistent main-agent panes remain alive for resume even though this
		// execution is complete. Transient panes switch to closing below when a
		// retention window is present.
		snapshot.ProcessState = "live"
		snapshot.SnapshotKind = "live"
	}
	// Retention is caller-decided: whoever emits the streaming events
	// attaches terminal_retention_seconds to metadata when the
	// terminal is transient (workflow step, sub-agent, one-shot CLI
	// invocation, etc.). The store has no opinion — main_agent and
	// any other persistent context simply never set this key.
	retentionSeconds := intValue(metadata["terminal_retention_seconds"])
	if retentionSeconds > 0 {
		closesAt := now.Add(time.Duration(retentionSeconds) * time.Second)
		snapshot.ClosesAt = &closesAt
		snapshot.RetentionSeconds = retentionSeconds
		snapshot.ProcessState = "closing"
		if state != "failed" {
			state = "closing"
		}
	}
	snapshot.State = state
	snapshot.UpdatedAt = now
	// Surface per-call completion meta (tokens, cost, duration) attached
	// to the streaming_end event. Tmux terminals don't carry these in
	// their pane content (the synthetic [done · ...] line is suppressed
	// to avoid clobbering the pane scrape), so the streaming_end is the
	// only place the structured numbers arrive. Non-tmux transports also
	// benefit — Status fields beat regex-parsing the trailer.
	if in := intValue(metadata["input_tokens"]); in > 0 {
		snapshot.Status.InputTokens = in
	}
	if out := intValue(metadata["output_tokens"]); out > 0 {
		snapshot.Status.OutputTokens = out
	}
	if cost := floatValue(metadata["cost_usd_estimated"]); cost > 0 && !hasProviderCumulativeCost(snapshot.Status) {
		snapshot.Status.CostUSD = cost
	}
	if dur := int64Value(metadata["duration_ms"]); dur > 0 {
		snapshot.Status.DurationMs = dur
	}
	s.byID[terminalID] = snapshot
	s.forcedInactive[terminalID] = now
}

func terminalHasSettledPromptAfterBusy(content string, metadata map[string]interface{}) bool {
	lines := cleanedLines(content)
	if len(lines) == 0 {
		return false
	}
	provider := providerLabel(content, metadata)
	// Cursor's follow-up prompt remains visible while it is composing, so it
	// cannot disambiguate an intermediate end event from a settled pane.
	if provider != "Codex CLI" && provider != "Claude Code" {
		return false
	}
	lastBusy := -1
	lastPrompt := -1
	for i, line := range lines {
		if isTerminalBusyLine(line) {
			lastBusy = i
		}
		if isProviderIdlePromptLine(provider, line, true) {
			lastPrompt = i
		}
	}
	return lastPrompt > lastBusy
}

func (s *Store) findInactiveTargetLocked(sessionID, ownerID string, metadata map[string]interface{}) (string, Snapshot, bool) {
	sessionTerminals := s.bySession[sessionID]
	if len(sessionTerminals) == 0 {
		return "", Snapshot{}, false
	}

	tmuxSession := firstNonEmpty(
		stringValue(metadata, "tmux_session"),
		stringValue(metadata, "tmux_session_name"),
		stringValue(metadata, "pi_interactive_session"),
		stringValue(metadata, "claude_code_interactive_session"),
		stringValue(metadata, "codex_interactive_session"),
		stringValue(metadata, "gemini_interactive_session"),
		stringValue(metadata, "cursor_interactive_session"),
	)
	stepID := firstNonEmpty(
		workflowStepIDFromOwner(ownerID),
		stringValue(metadata, "current_step_id"),
		stringValue(metadata, "workflow_step_id"),
		stringValue(metadata, "step_id"),
	)

	if tmuxSession != "" {
		for terminalID := range sessionTerminals {
			snapshot, ok := s.byID[terminalID]
			if ok && snapshot.TmuxSession == tmuxSession {
				return terminalID, snapshot, true
			}
		}
	}

	for terminalID := range sessionTerminals {
		snapshot, ok := s.byID[terminalID]
		if !ok {
			continue
		}
		if ownerMatchesTerminal(ownerID, snapshot) {
			return terminalID, snapshot, true
		}
		if stepID != "" && (snapshot.StepID == stepID || strings.HasSuffix(snapshot.OwnerID, ":"+stepID)) {
			return terminalID, snapshot, true
		}
	}

	return "", Snapshot{}, false
}

type preValidationStatusUpdate struct {
	stepID       string
	stepPath     string
	stepTitle    string
	status       string
	summary      string
	passedChecks int
	failedChecks int
	totalChecks  int
}

func (s *Store) updatePreValidationStatus(sessionID string, event storeevents.Event) {
	update, ok := preValidationStatusFromEvent(event)
	if !ok {
		return
	}
	metadata := s.metadataForEvent(event)
	if update.stepID != "" && stringValue(metadata, "step_id") == "" {
		metadata["step_id"] = update.stepID
	}
	if update.stepPath != "" && stringValue(metadata, "step_path") == "" {
		metadata["step_path"] = update.stepPath
	}
	if update.stepTitle != "" && stringValue(metadata, "step_title") == "" {
		metadata["step_title"] = update.stepTitle
	}
	ownerID := terminalOwnerID(sessionID, event, metadata)
	now := event.Timestamp
	if now.IsZero() {
		now = time.Now()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	terminalID, snapshot, ok := s.findPreValidationTargetLocked(sessionID, ownerID, update, metadata)
	if !ok {
		return
	}
	snapshot.Status.PreValidationStatus = update.status
	snapshot.Status.PreValidationSummary = update.summary
	snapshot.Status.PreValidationPassedChecks = update.passedChecks
	snapshot.Status.PreValidationFailedChecks = update.failedChecks
	snapshot.Status.PreValidationTotalChecks = update.totalChecks
	if strings.TrimSpace(snapshot.Status.StatusText) == "" {
		snapshot.Status.StatusText = update.summary
	}
	snapshot.UpdatedAt = now
	fillDisplayContext(&snapshot)
	s.byID[terminalID] = snapshot
}

func (s *Store) findPreValidationTargetLocked(sessionID, ownerID string, update preValidationStatusUpdate, metadata map[string]interface{}) (string, Snapshot, bool) {
	if terminalID, snapshot, ok := s.findInactiveTargetLocked(sessionID, ownerID, metadata); ok {
		return terminalID, snapshot, true
	}

	sessionTerminals := s.bySession[sessionID]
	var bestID string
	var best Snapshot
	for terminalID := range sessionTerminals {
		snapshot, ok := s.byID[terminalID]
		if !ok || !snapshotMatchesPreValidation(snapshot, update) {
			continue
		}
		if bestID == "" || betterPreValidationTarget(snapshot, best) {
			bestID = terminalID
			best = snapshot
		}
	}
	if bestID == "" {
		return "", Snapshot{}, false
	}
	return bestID, best, true
}

func snapshotMatchesPreValidation(snapshot Snapshot, update preValidationStatusUpdate) bool {
	if update.stepID != "" {
		if snapshot.StepID == update.stepID ||
			strings.HasSuffix(snapshot.OwnerID, ":"+update.stepID) ||
			strings.Contains(snapshot.OwnerID, ":"+update.stepID+":") ||
			strings.Contains(snapshot.ExecutionID, ":"+update.stepID+":") ||
			strings.HasSuffix(snapshot.ExecutionID, ":"+update.stepID) {
			return true
		}
	}
	if update.stepPath != "" {
		if snapshot.StepID == update.stepPath ||
			strings.Contains(snapshot.OwnerID, update.stepPath) ||
			strings.Contains(snapshot.ExecutionID, update.stepPath) {
			return true
		}
	}
	if update.stepTitle != "" {
		return strings.EqualFold(snapshot.StepName, update.stepTitle) || strings.EqualFold(snapshot.Label, update.stepTitle)
	}
	return false
}

func betterPreValidationTarget(candidate, current Snapshot) bool {
	if candidate.Active != current.Active {
		return candidate.Active
	}
	candidateArchived := strings.Contains(candidate.TerminalID, ":turn-")
	currentArchived := strings.Contains(current.TerminalID, ":turn-")
	if candidateArchived != currentArchived {
		return !candidateArchived
	}
	candidateUpdated := candidate.UpdatedAt
	if candidateUpdated.IsZero() {
		candidateUpdated = candidate.CreatedAt
	}
	currentUpdated := current.UpdatedAt
	if currentUpdated.IsZero() {
		currentUpdated = current.CreatedAt
	}
	return candidateUpdated.After(currentUpdated)
}

func preValidationStatusFromEvent(event storeevents.Event) (preValidationStatusUpdate, bool) {
	data, ok := eventDataMap(event)
	if !ok {
		return preValidationStatusUpdate{}, false
	}
	overallPass, hasOverallPass := boolValue(data["overall_pass"])
	passedChecks := intValue(data["passed_checks"])
	failedChecks := intValue(data["failed_checks"])
	totalChecks := intValue(data["total_checks"])
	if !hasOverallPass && totalChecks == 0 && passedChecks == 0 && failedChecks == 0 {
		return preValidationStatusUpdate{}, false
	}
	if failedChecks == 0 && totalChecks > 0 && passedChecks <= totalChecks {
		failedChecks = totalChecks - passedChecks
	}

	status := "failed"
	if overallPass {
		status = "passed"
	}
	summaryStatus := status
	if status == "passed" {
		summaryStatus = "passed"
	}
	summary := fmt.Sprintf("Pre-validation %s", summaryStatus)
	if totalChecks > 0 {
		summary = fmt.Sprintf("Pre-validation %s: %d/%d checks", summaryStatus, passedChecks, totalChecks)
	}
	if status == "failed" {
		if errors := stringSliceValue(data["errors"]); len(errors) > 0 {
			summary = fmt.Sprintf("%s - %s", summary, errors[0])
		}
	}

	return preValidationStatusUpdate{
		stepID:       firstNonEmpty(stringValue(data, "step_id"), stringValue(data, "current_step_id"), stringValue(data, "workflow_step_id")),
		stepPath:     stringValue(data, "step_path"),
		stepTitle:    stringValue(data, "step_title"),
		status:       status,
		summary:      summary,
		passedChecks: passedChecks,
		failedChecks: failedChecks,
		totalChecks:  totalChecks,
	}, true
}

func eventDataMap(event storeevents.Event) (map[string]interface{}, bool) {
	if event.Data == nil || event.Data.Data == nil {
		return nil, false
	}
	switch data := event.Data.Data.(type) {
	case *agentevents.GenericEventData:
		if data == nil || len(data.Data) == 0 {
			return nil, false
		}
		return data.Data, true
	}
	encoded, err := json.Marshal(event.Data.Data)
	if err != nil {
		return nil, false
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(encoded, &decoded); err != nil || len(decoded) == 0 {
		return nil, false
	}
	return decoded, true
}

func preserveEphemeralStatusFields(status *Status, previous Status) {
	if status == nil {
		return
	}
	// Preserve pre-validation
	if previous.PreValidationStatus != "" {
		status.PreValidationStatus = previous.PreValidationStatus
		status.PreValidationSummary = previous.PreValidationSummary
		status.PreValidationPassedChecks = previous.PreValidationPassedChecks
		status.PreValidationFailedChecks = previous.PreValidationFailedChecks
		status.PreValidationTotalChecks = previous.PreValidationTotalChecks
		if strings.TrimSpace(status.StatusText) == "" && strings.HasPrefix(previous.StatusText, "Pre-validation ") {
			status.StatusText = previous.StatusText
		}
	}
	// Preserve real-time telemetry (tokens/cost/label). DeriveStatus rebuilds
	// Status from pane content on every refresh and has no statusline data, so
	// without this the out-of-band telemetry set by handleStatusLine is wiped.
	if status.InputTokens == 0 {
		status.InputTokens = previous.InputTokens
	}
	if status.OutputTokens == 0 {
		status.OutputTokens = previous.OutputTokens
	}
	if status.CacheCreationInputTokens == 0 {
		status.CacheCreationInputTokens = previous.CacheCreationInputTokens
	}
	if status.CacheReadInputTokens == 0 {
		status.CacheReadInputTokens = previous.CacheReadInputTokens
	}
	if status.TotalInputTokens == 0 {
		status.TotalInputTokens = previous.TotalInputTokens
	}
	if status.TotalOutputTokens == 0 {
		status.TotalOutputTokens = previous.TotalOutputTokens
	}
	if cumulativeCost, ok := providerCumulativeCost(previous); ok {
		status.CostUSD = cumulativeCost
	} else if status.CostUSD == 0 {
		status.CostUSD = previous.CostUSD
	}
	if status.StatusMeta == nil {
		status.StatusMeta = previous.StatusMeta
	}
	// Keep the previous ProviderLabel when the freshly-derived one is empty, or
	// when the previous one carries a model detail (the "provider · model" form
	// set by handleStatusLine) that the derived one lacks. A length comparison
	// would wrongly drop a newer-but-shorter label; checking for the " · "
	// separator captures the actual "has model detail" intent.
	hasModelDetail := func(s string) bool { return strings.Contains(s, " · ") }
	if status.ProviderLabel == "" ||
		(previous.ProviderLabel != "" && hasModelDetail(previous.ProviderLabel) && !hasModelDetail(status.ProviderLabel)) {
		status.ProviderLabel = previous.ProviderLabel
	}
}

func hasProviderCumulativeCost(status Status) bool {
	_, ok := providerCumulativeCost(status)
	return ok
}

func providerCumulativeCost(status Status) (float64, bool) {
	if status.StatusMeta == nil {
		return 0, false
	}
	if cost, ok := status.StatusMeta["cost"].(map[string]interface{}); ok {
		if total := floatValue(cost["total_cost_usd"]); total > 0 {
			return total, true
		}
	}
	if total := floatValue(status.StatusMeta["total_cost_usd"]); total > 0 {
		return total, true
	}
	return 0, false
}

func ownerMatchesTerminal(ownerID string, snapshot Snapshot) bool {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		return false
	}
	return snapshot.OwnerID == ownerID ||
		snapshot.ExecutionID == ownerID ||
		strings.HasSuffix(snapshot.OwnerID, ":"+ownerID) ||
		strings.HasSuffix(ownerID, ":"+snapshot.OwnerID)
}

func terminalChunk(s *Store, event storeevents.Event) (string, int, map[string]interface{}, bool) {
	metadata := s.metadataForEvent(event)
	if event.Data == nil || event.Data.Data == nil {
		return "", 0, metadata, false
	}

	switch data := event.Data.Data.(type) {
	case *agentevents.StreamingChunkEvent:
		metadata = mergeMetadata(metadata, data.Metadata)
		return data.Content, data.ChunkIndex, metadata, true
	case *agentevents.GenericEventData:
		metadata = mergeMetadata(metadata, data.Metadata)
		content, _ := data.Data["content"].(string)
		return content, intValue(data.Data["chunk_index"]), metadata, content != ""
	default:
		return "", 0, metadata, false
	}
}

func metadataForEvent(event storeevents.Event) map[string]interface{} {
	metadata := map[string]interface{}{}
	if event.Data == nil {
		return metadata
	}
	if event.Data.SessionID != "" {
		metadata["session_id"] = event.Data.SessionID
	}
	if event.Data.CorrelationID != "" {
		metadata["correlation_id"] = event.Data.CorrelationID
	}
	switch data := event.Data.Data.(type) {
	case *agentevents.StreamingEndEvent:
		metadata = mergeMetadata(metadata, data.Metadata)
	case *agentevents.GenericEventData:
		metadata = mergeMetadata(metadata, data.Metadata)
		metadata = mergeMetadata(metadata, data.Data)
		if nested, ok := data.Data["metadata"].(map[string]interface{}); ok {
			metadata = mergeMetadata(metadata, nested)
		}
	// internal/events.GenericEventData is a second, differently-shaped
	// "generic event" type (EventType/Fields, not Metadata/Data) used by
	// emitBackgroundAgentEvent and friends. Without this case, every event
	// built that way — background_agent_started/completed among them —
	// resolves to an almost-empty metadata map here (only session_id/
	// correlation_id survive), so agent_id/parent_execution_id/step_id are
	// invisible to every downstream owner-resolution check.
	case *storeevents.GenericEventData:
		metadata = mergeMetadata(metadata, data.Fields)
		if nested, ok := data.Fields["metadata"].(map[string]interface{}); ok {
			metadata = mergeMetadata(metadata, nested)
		}
	// Typed background-agent events (replacing the untyped GenericEventData
	// map that used to carry this data). Their meaningful fields — agent_id,
	// parent_execution_id, status, ... — live as named struct fields, not
	// inside BaseEventData.Metadata, so the generic `default` case below
	// would miss them; project them into the metadata map explicitly so
	// every existing owner-resolution/status helper below keeps working
	// unchanged.
	case *orchestratorevents.BackgroundAgentStartedEvent:
		metadata = mergeMetadata(metadata, data.Metadata)
		metadata["agent_id"] = data.AgentID
		metadata["name"] = data.Name
		// current.AgentName (store.go ensureStructuredExecution/
		// completeStructuredExecution) reads "agent_name", not "name" — without
		// this the friendly name set here is invisible to snapshot building,
		// leaving AgentName empty and the frontend falling back to the raw
		// agent_id slug (e.g. "coun-0001" title-cased into "Coun 0001").
		if data.Name != "" {
			metadata["agent_name"] = data.Name
		}
		if data.Instruction != "" {
			metadata["instruction"] = data.Instruction
		}
		// The creator's own declaration of what this execution is. Projected
		// under the same key legacy events use, so terminalExecutionKind picks
		// it up at its highest precedence and never falls through to sniffing.
		if data.Kind != "" {
			metadata["execution_kind"] = string(data.Kind)
		}
		if data.ParentExecutionID != "" {
			metadata["parent_execution_id"] = data.ParentExecutionID
		}
	case *orchestratorevents.BackgroundAgentCompletedEvent:
		metadata = mergeMetadata(metadata, data.Metadata)
		metadata["agent_id"] = data.AgentID
		metadata["name"] = data.Name
		if data.Name != "" {
			metadata["agent_name"] = data.Name
		}
		metadata["status"] = data.Status
		if data.Result != "" {
			metadata["result"] = data.Result
		}
		if data.Error != "" {
			metadata["error"] = data.Error
		}
		if data.Duration != "" {
			metadata["duration"] = data.Duration
		}
		if data.Kind != "" {
			metadata["execution_kind"] = string(data.Kind)
		}
		if data.ParentExecutionID != "" {
			metadata["parent_execution_id"] = data.ParentExecutionID
		}
	case *orchestratorevents.BackgroundAgentTerminatedEvent:
		metadata = mergeMetadata(metadata, data.Metadata)
		metadata["agent_id"] = data.AgentID
		metadata["name"] = data.Name
		if data.Name != "" {
			metadata["agent_name"] = data.Name
		}
		if data.Status != "" {
			metadata["status"] = data.Status
		}
		if data.ParentExecutionID != "" {
			metadata["parent_execution_id"] = data.ParentExecutionID
		}
	case *orchestratorevents.SyntheticTurnReadyEvent:
		metadata = mergeMetadata(metadata, data.Metadata)
		metadata["agent_id"] = data.AgentID
		metadata["status"] = data.Status
		if data.Name != "" {
			metadata["name"] = data.Name
		}
	case *orchestratorevents.AutoNotificationSteeredEvent:
		metadata = mergeMetadata(metadata, data.Metadata)
		metadata["agent_id"] = data.AgentID
		metadata["name"] = data.Name
		metadata["status"] = data.Status
		metadata["provider"] = data.Provider
		if data.Kind != "" {
			metadata["execution_kind"] = string(data.Kind)
		}
	default:
		if withBase, ok := event.Data.Data.(interface {
			GetBaseEventData() *agentevents.BaseEventData
		}); ok {
			if base := withBase.GetBaseEventData(); base != nil {
				metadata = mergeMetadata(metadata, base.Metadata)
			}
		}
	}
	return metadata
}

func mergeMetadata(base, extra map[string]interface{}) map[string]interface{} {
	if base == nil {
		base = map[string]interface{}{}
	}
	for key, value := range extra {
		base[key] = value
	}
	return base
}

func isTerminalMetadata(metadata map[string]interface{}) bool {
	kind := strings.ToLower(firstNonEmpty(
		stringValue(metadata, "kind"),
		stringValue(metadata, "stream_kind"),
		stringValue(metadata, "display_kind"),
		stringValue(metadata, "mode"),
	))
	return kind == "terminal" || kind == "tmux" || kind == "tui"
}

func isNonTmuxWorkflowTerminalMetadata(metadata map[string]interface{}) bool {
	if strings.ToLower(strings.TrimSpace(stringValue(metadata, "step_transport"))) == "tmux" {
		return false
	}
	return firstNonEmpty(
		stringValue(metadata, "execution_owner_id"),
		stringValue(metadata, "current_step_id"),
		stringValue(metadata, "workflow_step_id"),
		stringValue(metadata, "step_id"),
	) != ""
}

func isStructuredExecutionMetadata(sessionID string, event storeevents.Event, metadata map[string]interface{}) bool {
	ownerID := terminalOwnerID(sessionID, event, metadata)
	if ownerID == "" || terminalOwnerIsCanonicalMain(sessionID, ownerID) {
		return false
	}
	// Honor an explicitly DECLARED kind: a full run is a container, a
	// message-sequence item is an internal turn, a router is a decision
	// record. None of them is a conversation, so none gets a terminal.
	//
	// Only a declared kind may suppress. Most events still carry no kind at
	// all, and treating "undeclared" as "no terminal" would delete nearly
	// every pane. Undeclared falls through to the legacy checks below.
	if declared := orchestratorevents.ParseExecutionKind(stringValue(metadata, "execution_kind")); declared != orchestratorevents.ExecutionKindUnknown {
		if !declared.OwnsTerminal() && !declared.FoldsIntoParent() {
			return false
		}
	}
	kind := strings.ToLower(strings.TrimSpace(terminalExecutionKind(sessionID, ownerID, event, metadata)))
	if kind == "main" || kind == "main_agent" || kind == "chat" {
		return false
	}
	if kind != "" && kind != "execution" {
		return true
	}
	return firstNonEmpty(
		stringValue(metadata, "execution_owner_id"),
		stringValue(metadata, "background_agent_id"),
		stringValue(metadata, "current_step_id"),
		stringValue(metadata, "workflow_step_id"),
		stringValue(metadata, "step_id"),
	) != ""
}

func terminalOwnerID(sessionID string, event storeevents.Event, metadata map[string]interface{}) string {
	if ownerID := strings.TrimSpace(event.TerminalOwnerID); ownerID != "" {
		return ownerID
	}
	return storeevents.ResolveTerminalOwnerID(sessionID, event, metadata)
}

func terminalOwnerIsCanonicalMain(sessionID, ownerID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	ownerID = strings.TrimSpace(ownerID)
	return sessionID != "" && (ownerID == sessionID || ownerID == "main:"+sessionID)
}

func terminalExecutionKind(sessionID, ownerID string, event storeevents.Event, metadata map[string]interface{}) string {
	kind := strings.ToLower(strings.TrimSpace(firstNonEmpty(
		stringValue(metadata, "execution_kind"),
		event.ExecutionKind,
	)))
	if terminalOwnerIsCanonicalMain(sessionID, ownerID) {
		if kind == "" || terminalKindIsMain(kind) {
			return "main_agent"
		}
		return kind
	}
	// Only trust an explicitly declared kind. An empty kind (e.g.
	// BackgroundAgentCompletedEvent, which has no Kind field to project —
	// unlike BackgroundAgentStartedEvent) must fall through to the
	// scope/ownerID inference below instead of returning "" here, or the
	// completion event never reaches the isStructuredExecutionMetadata bar
	// and the execution is stuck "running" forever.
	if kind != "" && !terminalKindIsMain(kind) {
		return kind
	}

	scope := strings.ToLower(strings.TrimSpace(stringValue(metadata, "scope")))
	switch scope {
	case "workflow_step", "step", "execution_only":
		return "workflow_step"
	case "background_agent", "background":
		return "background_agent"
	case "delegation", "todo_task", "sub_agent":
		return "delegation"
	}
	if workflowStepIDFromOwner(ownerID) != "" {
		return "workflow_step"
	}
	if ownerID == stringValue(metadata, "background_agent_id") || ownerID == stringValue(metadata, "agent_id") {
		return "background_agent"
	}
	if ownerID == stringValue(metadata, "delegation_id") {
		return "delegation"
	}
	return "execution"
}

func normalizedTerminalScope(sessionID, ownerID, scope, executionKind string) string {
	scope = strings.ToLower(strings.TrimSpace(scope))
	if terminalOwnerIsCanonicalMain(sessionID, ownerID) {
		if scope == "" || scope == "session" || terminalKindIsMain(scope) {
			return "main_agent"
		}
		return scope
	}
	if scope != "" && scope != "session" && !terminalKindIsMain(scope) {
		return scope
	}
	switch executionKind {
	case "workflow_step", "step", "execution_only":
		return "workflow_step"
	case "background_agent", "background":
		return "background_agent"
	case "delegation", "todo_task", "sub_agent":
		return "delegation"
	default:
		return "execution"
	}
}

func terminalKindIsMain(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "main_agent", "main", "chat":
		return true
	default:
		return false
	}
}

func terminalIDFor(sessionID, ownerID string) string {
	return storeevents.TerminalIDForOwner(sessionID, ownerID)
}

func workflowStepIDFromOwner(ownerID string) string {
	parts := strings.Split(strings.TrimSpace(ownerID), ":")
	if len(parts) < 3 || parts[0] != "workflow-step" {
		return ""
	}
	return strings.TrimSpace(parts[len(parts)-1])
}

func workflowExecutionIDFromOwner(ownerID string) string {
	parts := strings.Split(strings.TrimSpace(ownerID), ":")
	if len(parts) < 3 || parts[0] != "workflow-step" {
		return ""
	}
	return strings.TrimSpace(strings.Join(parts[1:len(parts)-1], ":"))
}

func terminalLabel(event storeevents.Event, metadata map[string]interface{}, ownerID string) string {
	return firstNonEmpty(
		stringValue(metadata, "agent_name"),
		stringValue(metadata, "orchestrator_agent_name"),
		stringValue(metadata, "step_title"),
		stringValue(metadata, "title"),
		stringValue(metadata, "name"),
		stringValue(metadata, "current_step_id"),
		stringValue(metadata, "workflow_step_id"),
		stringValue(metadata, "step_id"),
		event.ExecutionID,
		ownerID,
		"Terminal",
	)
}

func fillDisplayContext(snapshot *Snapshot) {
	if snapshot == nil {
		return
	}
	workflowLabel := firstNonEmpty(snapshot.WorkflowLabel, snapshot.WorkflowName, workflowNameFromPath(snapshot.WorkflowPath))
	kindLabel := executionKindLabel(snapshot.ExecutionKind, snapshot.Scope)
	taskLabel := firstNonEmpty(terminalTaskLabel(*snapshot), cleanOpaqueLabel(snapshot.Label))
	stepTypeLabel := humanizeIdentifier(snapshot.StepType)

	switch {
	case workflowLabel != "" && taskLabel != "" && kindLabel != "":
		snapshot.DisplayTitle = fmt.Sprintf("%s -> %s", workflowLabel, taskLabel)
		snapshot.DisplayMeta = strings.Join(uniqueNonEmpty(stepTypeLabel, kindLabel), " · ")
	case workflowLabel != "" && kindLabel != "":
		snapshot.DisplayTitle = fmt.Sprintf("%s -> %s", workflowLabel, kindLabel)
		snapshot.DisplayMeta = strings.Join(uniqueNonEmpty(stepTypeLabel, taskLabel), " · ")
	case taskLabel != "" && kindLabel != "":
		snapshot.DisplayTitle = fmt.Sprintf("%s -> %s", kindLabel, taskLabel)
		snapshot.DisplayMeta = strings.Join(uniqueNonEmpty(stepTypeLabel, workflowLabel), " · ")
	case taskLabel != "":
		snapshot.DisplayTitle = taskLabel
		snapshot.DisplayMeta = strings.Join(uniqueNonEmpty(stepTypeLabel, firstNonEmpty(workflowLabel, kindLabel)), " · ")
	default:
		snapshot.DisplayTitle = firstNonEmpty(workflowLabel, kindLabel, "Terminal")
		snapshot.DisplayMeta = strings.Join(uniqueNonEmpty(stepTypeLabel, cleanOpaqueLabel(firstNonEmpty(snapshot.ExecutionID, snapshot.OwnerID))), " · ")
	}

	snapshot.DisplayMeta = strings.Join(uniqueNonEmpty(snapshot.DisplayMeta), " · ")
}

func terminalTaskLabel(snapshot Snapshot) string {
	switch firstNonEmpty(snapshot.ExecutionKind, snapshot.Scope) {
	case "workflow_step", "step", "execution_only":
		// Prefer a human title (plan step title, or the agent's own name for
		// step-less maintenance agents like learning/organize) over the raw step
		// ID. The ID — e.g. "_global" for the global-learnings skill — is a folder
		// / lookup key, not a display name. Falls back to the ID when no human name
		// is present, so genuine steps without a title are unchanged.
		return firstNonEmpty(snapshot.StepName, snapshot.AgentName, snapshot.StepID)
	case "main_agent", "main", "chat":
		return firstNonEmpty(snapshot.AgentName, snapshot.StepName)
	case "background_agent", "background", "delegation", "todo_task", "sub_agent":
		return firstNonEmpty(snapshot.AgentName, snapshot.StepName, cleanOpaqueLabel(snapshot.Label), snapshot.StepID)
	default:
		return firstNonEmpty(snapshot.StepName, snapshot.AgentName, snapshot.StepID)
	}
}

func executionKindLabel(kind, scope string) string {
	switch firstNonEmpty(kind, scope) {
	case "main_agent":
		return "Main agent"
	case "workflow_step", "step", "execution_only":
		return "Workflow step"
	case "background_agent", "background":
		return "Background agent"
	case "delegation", "todo_task", "sub_agent":
		return "Sub-agent"
	case "session":
		return "Session"
	case "execution":
		return "Execution"
	default:
		return humanizeIdentifier(firstNonEmpty(kind, scope))
	}
}

func workflowNameFromPath(path string) string {
	trimmed := strings.Trim(strings.TrimSpace(path), "/")
	if trimmed == "" {
		return ""
	}
	parts := strings.FieldsFunc(trimmed, func(r rune) bool {
		return r == '/' || r == '\\'
	})
	for i, part := range parts {
		if part == "Workflow" && i+1 < len(parts) {
			return humanizeIdentifier(parts[i+1])
		}
	}
	return humanizeIdentifier(parts[len(parts)-1])
}

func cleanOpaqueLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || looksOpaqueID(value) {
		return ""
	}
	return humanizeIdentifier(value)
}

func looksOpaqueID(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	if lower == "" {
		return false
	}
	if strings.HasPrefix(lower, "main:") && len(lower) > len("main:")+16 {
		return true
	}
	hexish := 0
	for _, r := range lower {
		if (r >= 'a' && r <= 'f') || (r >= '0' && r <= '9') || r == '-' {
			hexish++
		}
	}
	return len(lower) >= 24 && hexish == len(lower)
}

func humanizeIdentifier(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.TrimPrefix(value, "exec-")
	value = strings.TrimPrefix(value, "exec_")
	value = strings.ReplaceAll(value, "_", " ")
	value = strings.ReplaceAll(value, "-", " ")
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		return ""
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

func uniqueNonEmpty(values ...string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func terminalScope(event storeevents.Event, metadata map[string]interface{}) string {
	if scope := stringValue(metadata, "scope"); scope != "" {
		return scope
	}
	switch event.ExecutionKind {
	case "workflow_step", "step", "execution_only":
		return "workflow_step"
	case "background_agent", "background":
		return "background_agent"
	case "delegation", "todo_task", "sub_agent":
		return "delegation"
	}
	if terminalOwnerID(event.SessionID, event, metadata) == "" {
		return "session"
	}
	return "execution"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func stringValue(values map[string]interface{}, key string) string {
	if values == nil {
		return ""
	}
	value, ok := values[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func floatValue(value interface{}) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case string:
		parsed, _ := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return parsed
	default:
		return 0
	}
}

func boolValue(value interface{}) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		trimmed := strings.TrimSpace(strings.ToLower(typed))
		if trimmed == "" {
			return false, false
		}
		switch trimmed {
		case "true", "1", "yes", "y", "passed", "pass":
			return true, true
		case "false", "0", "no", "n", "failed", "fail":
			return false, true
		default:
			return false, false
		}
	case int:
		return typed != 0, true
	case int64:
		return typed != 0, true
	case float64:
		return typed != 0, true
	case float32:
		return typed != 0, true
	default:
		return false, false
	}
}

func stringSliceValue(value interface{}) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []interface{}:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			text := strings.TrimSpace(fmt.Sprint(item))
			if text != "" {
				out = append(out, text)
			}
		}
		return out
	case string:
		if trimmed := strings.TrimSpace(typed); trimmed != "" {
			return []string{trimmed}
		}
	}
	return nil
}

func int64Value(value interface{}) int64 {
	switch typed := value.(type) {
	case int64:
		return typed
	case int:
		return int64(typed)
	case float64:
		return int64(typed)
	case float32:
		return int64(typed)
	case string:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed
	default:
		return 0
	}
}

func intValue(value interface{}) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case float32:
		return int(typed)
	case string:
		parsed, _ := strconv.Atoi(strings.TrimSpace(typed))
		return parsed
	default:
		return 0
	}
}

func (s *Store) handleStatusLine(sessionID string, event storeevents.Event) {
	if event.Data == nil || event.Data.Data == nil {
		return
	}

	var provider, model, tmuxSession string
	var inputTokens, outputTokens int
	var cacheCreationTokens, cacheReadTokens, totalInputTokens, totalOutputTokens int
	var costUSD float64
	var statusMeta map[string]interface{}
	var found bool

	switch data := event.Data.Data.(type) {
	case *agentevents.StreamingStatusLineEvent:
		provider = data.Provider
		model = data.Model
		tmuxSession = data.TmuxSession
		inputTokens = data.InputTokens
		outputTokens = data.OutputTokens
		cacheCreationTokens = data.CacheCreationInputTokens
		cacheReadTokens = data.CacheReadInputTokens
		totalInputTokens = data.TotalInputTokens
		totalOutputTokens = data.TotalOutputTokens
		costUSD = data.CostUSD
		statusMeta = data.Metadata
		found = true
	case *agentevents.GenericEventData:
		provider, _ = data.Data["provider"].(string)
		model, _ = data.Data["model"].(string)
		tmuxSession, _ = data.Data["tmux_session"].(string)
		inputTokens = intValue(data.Data["input_tokens"])
		outputTokens = intValue(data.Data["output_tokens"])
		cacheCreationTokens = intValue(data.Data["cache_creation_input_tokens"])
		cacheReadTokens = intValue(data.Data["cache_read_input_tokens"])
		totalInputTokens = intValue(data.Data["total_input_tokens"])
		totalOutputTokens = intValue(data.Data["total_output_tokens"])
		costUSD = floatValue(data.Data["cost_usd"])
		statusMeta, _ = data.Data["metadata"].(map[string]interface{})
		found = true
	}

	if !found {
		return
	}
	tmuxSession = firstNonEmpty(
		tmuxSession,
		stringValue(statusMeta, "tmux_session"),
		stringValue(statusMeta, "tmux_session_name"),
		stringValue(statusMeta, "pi_interactive_session"),
		stringValue(statusMeta, "claude_code_interactive_session"),
		stringValue(statusMeta, "codex_interactive_session"),
		stringValue(statusMeta, "gemini_interactive_session"),
		stringValue(statusMeta, "cursor_interactive_session"),
	)

	// Use the provider name verbatim — the adapter owns its display name
	// (e.g. "codex-cli", "claudecode"); the store must not re-map provider ids.
	providerLabel := provider
	if model != "" {
		if providerLabel != "" {
			providerLabel = fmt.Sprintf("%s · %s", provider, model)
		} else {
			providerLabel = model
		}
	}

	tmuxSession = strings.TrimSpace(tmuxSession)
	// A structured (non-tmux) transport carries no pane identity. Scope its
	// telemetry to the terminal that owns the event instead, so a workflow
	// step's live context meter never lands on a sibling step's terminal.
	// Resolved before taking s.mu: metadataForEvent read-locks it.
	eventMetadata := s.metadataForEvent(event)
	ownerScope := ""
	if tmuxSession == "" {
		ownerScope = terminalOwnerID(sessionID, event, eventMetadata)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	if ownerScope != "" {
		found := false
		for terminalID := range s.bySession[sessionID] {
			if snapshot, exists := s.byID[terminalID]; exists && snapshot.OwnerID == ownerScope {
				found = true
				break
			}
		}
		if !found {
			ownerScope = ""
		}
	}
	updated := false
	for terminalID := range s.bySession[sessionID] {
		snapshot, exists := s.byID[terminalID]
		if !exists {
			continue
		}
		if ownerScope != "" && snapshot.OwnerID != ownerScope {
			continue
		}
		// When the event identifies its tmux session, scope the update to the
		// terminal that owns it — a session can host several coding-agent panes,
		// and the telemetry belongs to exactly one. Fall back to updating every
		// terminal only when no tmux session is carried (older producers).
		if tmuxSession != "" && strings.TrimSpace(snapshot.TmuxSession) != tmuxSession {
			continue
		}
		snapshot.Status.InputTokens = inputTokens
		snapshot.Status.OutputTokens = outputTokens
		snapshot.Status.CacheCreationInputTokens = cacheCreationTokens
		snapshot.Status.CacheReadInputTokens = cacheReadTokens
		snapshot.Status.TotalInputTokens = totalInputTokens
		snapshot.Status.TotalOutputTokens = totalOutputTokens
		snapshot.Status.CostUSD = costUSD
		snapshot.Status.ProviderLabel = providerLabel
		if statusMeta != nil {
			snapshot.Status.StatusMeta = statusMeta
		}
		snapshot.UpdatedAt = now
		s.byID[terminalID] = snapshot
		updated = true
	}
	if updated || tmuxSession == "" {
		return
	}
	statusMetadata := mergeMetadata(eventMetadata, statusMeta)
	statusMetadata["kind"] = firstNonEmpty(stringValue(statusMetadata, "kind"), "terminal")
	statusMetadata["tmux_session"] = tmuxSession
	statusMetadata["step_transport"] = firstNonEmpty(stringValue(statusMetadata, "step_transport"), "tmux")
	ownerID := terminalOwnerID(sessionID, event, statusMetadata)
	if ownerID == "" {
		ownerID = "main:" + sessionID
	}
	executionKind := terminalExecutionKind(sessionID, ownerID, event, statusMetadata)
	scope := normalizedTerminalScope(sessionID, ownerID, terminalScope(event, statusMetadata), executionKind)
	label := terminalLabel(event, statusMetadata, ownerID)
	if label == "Terminal" && providerLabel != "" {
		label = providerLabel
	}
	snapshot := Snapshot{
		TerminalID:    terminalIDFor(sessionID, ownerID),
		SessionID:     sessionID,
		OwnerID:       ownerID,
		ExecutionID:   firstNonEmpty(event.ExecutionID, stringValue(statusMeta, "execution_id")),
		ExecutionKind: executionKind,
		Label:         label,
		Scope:         scope,
		StepID:        firstNonEmpty(workflowStepIDFromOwner(ownerID), stringValue(statusMetadata, "current_step_id"), stringValue(statusMetadata, "workflow_step_id"), stringValue(statusMetadata, "step_id")),
		StepTransport: "tmux",
		TmuxSession:   tmuxSession,
		ContentSource: "tmux_live",
		ChunkIndex:    0,
		Active:        true,
		State:         "running",
		Status: Status{
			InputTokens:              inputTokens,
			OutputTokens:             outputTokens,
			CacheCreationInputTokens: cacheCreationTokens,
			CacheReadInputTokens:     cacheReadTokens,
			TotalInputTokens:         totalInputTokens,
			TotalOutputTokens:        totalOutputTokens,
			CostUSD:                  costUSD,
			ProviderLabel:            providerLabel,
			StatusMeta:               statusMeta,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if snapshot.ExecutionID == "" {
		snapshot.ExecutionID = snapshot.OwnerID
	}
	if snapshot.StepID == "" && currentTerminalIsMainAgent(snapshot) {
		snapshot.StepID = "main_agent:" + sessionID
	}
	fillDisplayContext(&snapshot)
	if _, ok := s.dismissed[snapshot.TerminalID]; ok {
		delete(s.dismissed, snapshot.TerminalID)
	}
	s.byID[snapshot.TerminalID] = snapshot
	if s.bySession[sessionID] == nil {
		s.bySession[sessionID] = make(map[string]struct{})
	}
	s.bySession[sessionID][snapshot.TerminalID] = struct{}{}
	if currentTerminalIsCanonicalMainAgent(snapshot) {
		s.removeCurrentMainAgentAliasesLocked(sessionID, snapshot.TerminalID)
	}
}
