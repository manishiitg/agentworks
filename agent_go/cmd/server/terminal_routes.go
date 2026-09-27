package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gorilla/mux"
	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/pkg/tmuxcapture"
	"github.com/manishiitg/multi-llm-provider-go/pkg/tmuxinput"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
)

const listTerminalContentMaxBytes = 64 * 1024
const terminalTmuxActionTimeout = 5 * time.Second
const terminalDefaultRefreshLines = 2000
const terminalActiveDetailHistoryLines = 1000
const terminalDefaultDetailHistoryLines = 1000
const terminalMaxCaptureLines = 20000
const terminalMinResizeCols = 40
const terminalMinResizeRows = 10

// Upper bound for an operator-supplied grid. Requests arrive from the browser,
// so an absurd value would otherwise become a real resize-window and make every
// later capture-pane proportionally huge. Shared with the live-attach transport
// (see clampLiveAttachGeometry) so both paths agree on the supported range.
const terminalMaxResizeCols = liveAttachMaxCols
const terminalMaxResizeRows = liveAttachMaxRows

// clampTerminalResize bounds a requested grid to the supported range. Callers
// validate the minimum themselves (it is a hard reject); oversized values are
// clamped so an odd client still gets a usable pane.
func clampTerminalResize(cols, rows int) (int, int) {
	if cols > terminalMaxResizeCols {
		cols = terminalMaxResizeCols
	}
	if rows > terminalMaxResizeRows {
		rows = terminalMaxResizeRows
	}
	return cols, rows
}

var runTerminalTmuxCommand = func(ctx context.Context, stdin string, args ...string) error {
	cmd := exec.CommandContext(ctx, "tmux", args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			return fmt.Errorf("tmux %s: %w", strings.Join(args, " "), err)
		}
		return fmt.Errorf("tmux %s: %w: %s", strings.Join(args, " "), err, message)
	}
	return nil
}

var runTerminalTmuxOutputCommand = func(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "tmux", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			return "", fmt.Errorf("tmux %s: %w", strings.Join(args, " "), err)
		}
		return "", fmt.Errorf("tmux %s: %w: %s", strings.Join(args, " "), err, message)
	}
	return string(output), nil
}

var forceCompleteCodingAgentTmuxSession = llmproviders.ForceCompleteCodingAgentTmuxSession

type listTerminalsResponse struct {
	Terminals     []terminals.Snapshot       `json:"terminals"`
	Total         int                        `json:"total"`
	RuntimeStates map[string]RuntimeSnapshot `json:"runtime_states,omitempty"`
}

type sendTerminalInputRequest struct {
	Text   string `json:"text"`
	Submit bool   `json:"submit,omitempty"`
}

type sendTerminalKeyRequest struct {
	Key string `json:"key"`
}

type resizeTerminalRequest struct {
	Cols      int    `json:"cols"`
	Rows      int    `json:"rows"`
	SessionID string `json:"session_id,omitempty"`
}

type terminalActionResponse struct {
	OK       bool               `json:"ok"`
	Terminal terminals.Snapshot `json:"terminal,omitempty"`
}

type terminalPaneCaptureStats struct {
	RequestedLines int
	CaptureLines   int
	RawLines       int
	RawBytes       int
	CollapsedLines int
	CollapsedBytes int
	Duration       time.Duration
	ContentSource  string
}

type terminalPaneRuntimeStats struct {
	HistoryLimit   string
	HistorySize    string
	AlternateOn    string
	PaneHeight     string
	PaneWidth      string
	PaneInMode     string
	ScrollPosition string
}

// mainTerminalForSession resolves the one product-facing coding-agent pane for
// a chat. It deliberately does not expose the terminal id to the browser or
// enumerate child/workflow-step panes; those remain diagnostics-only.
func (api *StreamingAPI) mainTerminalForSession(w http.ResponseWriter, r *http.Request) (terminals.Snapshot, bool) {
	if api.terminalStore == nil {
		http.Error(w, "Main terminal not found", http.StatusNotFound)
		return terminals.Snapshot{}, false
	}
	sessionID := strings.TrimSpace(mux.Vars(r)["session_id"])
	if sessionID == "" || !api.canAccessTerminalSession(r, sessionID) {
		http.Error(w, "Main terminal not found", http.StatusNotFound)
		return terminals.Snapshot{}, false
	}
	for _, snapshot := range api.terminalStore.List(sessionID) {
		if terminalSnapshotIsMainAgent(snapshot) {
			// A provider retry can replace its tmux pane before a new screen event
			// reaches the terminal store. If the prior pane has already been
			// reconciled as closed, recover the newest live pane carrying this
			// chat's tmux-native owner tag. This prevents the product Terminal view
			// from reconnecting forever to the pre-retry pane while chat continues
			// successfully on its replacement.
			if strings.TrimSpace(snapshot.TmuxSession) == "" {
				if tmuxSession := latestOwnedCodingTmuxSession(r.Context(), sessionID); tmuxSession != "" {
					if rebound, reboundOK := api.terminalStore.RebindLiveTmux(snapshot.TerminalID, tmuxSession); reboundOK {
						snapshot = rebound
					}
				}
			}
			return snapshot, true
		}
	}
	http.Error(w, "Main terminal not found", http.StatusNotFound)
	return terminals.Snapshot{}, false
}

func latestOwnedCodingTmuxSession(parent context.Context, sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(parent, terminalTmuxActionTimeout)
	defer cancel()
	out, err := runTerminalTmuxOutputCommand(ctx, "list-sessions", "-F",
		"#{session_name}\t#{session_created}\t#{@runloop_owner_session_id}")
	if err != nil {
		return ""
	}
	var latestName string
	var latestCreated int64
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) < 3 || strings.TrimSpace(fields[2]) != sessionID {
			continue
		}
		name := strings.TrimSpace(fields[0])
		if !isCodingAgentTmuxSessionName(name) {
			continue
		}
		created, parseErr := strconv.ParseInt(strings.TrimSpace(fields[1]), 10, 64)
		if parseErr != nil {
			continue
		}
		if latestName == "" || created > latestCreated {
			latestName = name
			latestCreated = created
		}
	}
	return latestName
}

func terminalSnapshotIsMainAgent(snapshot terminals.Snapshot) bool {
	kind := strings.ToLower(strings.TrimSpace(snapshot.ExecutionKind))
	scope := strings.ToLower(strings.TrimSpace(snapshot.Scope))
	ownerID := strings.ToLower(strings.TrimSpace(snapshot.OwnerID))
	return kind == "main_agent" || kind == "main" || kind == "chat" ||
		scope == "main_agent" || scope == "main" || scope == "chat" ||
		strings.HasPrefix(ownerID, "main:")
}

// withMainTerminalID passes a server-selected main terminal to the mature
// terminal handlers. Preserve session_id as well because live-attach uses it
// as an authorization/recovery hint after a backend restart.
func withMainTerminalID(r *http.Request, terminalID string) *http.Request {
	vars := mux.Vars(r)
	copyVars := make(map[string]string, len(vars)+1)
	for key, value := range vars {
		copyVars[key] = value
	}
	copyVars["terminal_id"] = terminalID
	return mux.SetURLVars(r, copyVars)
}

// handleGetMainTerminal is the normal product raw view. It reuses the same
// capture/enrichment path as the established terminal UI, but can select only
// the current session's main agent pane.
func (api *StreamingAPI) handleGetMainTerminal(w http.ResponseWriter, r *http.Request) {
	snapshot, ok := api.mainTerminalForSession(w, r)
	if !ok {
		return
	}
	api.handleGetTerminal(w, withMainTerminalID(r, snapshot.TerminalID))
}

// handleMainTerminalStream is the product counterpart to the diagnostics-only
// terminal stream. It preserves the existing xterm/tmux live-attach protocol
// while preventing arbitrary child-terminal access.
func (api *StreamingAPI) handleMainTerminalStream(w http.ResponseWriter, r *http.Request) {
	snapshot, ok := api.mainTerminalForSession(w, r)
	if !ok {
		return
	}
	api.handleTerminalStream(w, withMainTerminalID(r, snapshot.TerminalID))
}

// handleListTerminals returns current view-only terminal snapshots.
// GET /api/terminals?session_id=<session>
func (api *StreamingAPI) handleListTerminals(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if api.terminalStore == nil {
		_ = json.NewEncoder(w).Encode(listTerminalsResponse{Terminals: []terminals.Snapshot{}})
		return
	}

	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	contentMode := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("content")))
	activeOnly := parseTerminalActiveOnly(r.URL.Query().Get("active_only"))
	// A run triggered from this chat mints its own scheduled session, so its
	// step and sub-agent terminals are filed there rather than here. Listing the
	// chat alone showed only the chat's own agent while the work it asked for sat
	// under a session the tab never queries. Include those runs, and nothing else:
	// scopes stay per-chat, and cron runs (no originating chat) are unaffected.
	scopes := sessionsTriggeredFrom(sessionID)
	if len(scopes) == 0 {
		scopes = []string{sessionID}
	}
	var snapshots []terminals.Snapshot
	seenTerminalIDs := make(map[string]struct{})
	for _, scope := range scopes {
		var scoped []terminals.Snapshot
		if isMetadataOnlyTerminalList(contentMode) {
			scoped = api.terminalStore.ListMetadata(scope)
		} else {
			scoped = api.terminalStore.List(scope)
		}
		for _, snapshot := range scoped {
			if _, seen := seenTerminalIDs[snapshot.TerminalID]; seen {
				continue
			}
			seenTerminalIDs[snapshot.TerminalID] = struct{}{}
			snapshots = append(snapshots, snapshot)
		}
	}
	planTypes := newTerminalPlanTypeResolver(r.Context())
	filtered := make([]terminals.Snapshot, 0, len(snapshots))
	for _, snapshot := range snapshots {
		if snapshot.SessionID == "" {
			continue
		}
		if activeOnly && !terminalSnapshotIsActiveForList(snapshot) {
			continue
		}
		if api.canAccessTerminalSession(r, snapshot.SessionID) {
			filtered = append(filtered, api.terminalSnapshotForList(r.Context(), planTypes, snapshot, contentMode))
		}
	}
	if api.terminalPipeRecorder != nil {
		api.terminalPipeRecorder.ObserveSnapshots(filtered)
	}

	runtimeStates := make(map[string]RuntimeSnapshot)
	for _, terminal := range filtered {
		if _, exists := runtimeStates[terminal.SessionID]; exists {
			continue
		}
		if snapshot, ok := api.authoritativeRuntimeSnapshot(terminal.SessionID); ok {
			runtimeStates[terminal.SessionID] = snapshot
		}
	}
	_ = json.NewEncoder(w).Encode(listTerminalsResponse{
		Terminals: filtered, Total: len(filtered), RuntimeStates: runtimeStates,
	})
}

// handleGetTerminal returns one current view-only terminal snapshot.
// GET /api/terminals/{terminal_id}
func (api *StreamingAPI) handleGetTerminal(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if api.terminalStore == nil {
		http.Error(w, "Terminal not found", http.StatusNotFound)
		return
	}

	terminalID := strings.TrimSpace(mux.Vars(r)["terminal_id"])
	if terminalID == "" {
		http.Error(w, "Terminal ID is required", http.StatusBadRequest)
		return
	}
	snapshot, ok := api.terminalStore.Get(terminalID)
	if !ok || !api.canAccessTerminalSession(r, snapshot.SessionID) {
		http.Error(w, "Terminal not found", http.StatusNotFound)
		return
	}
	// Capture live tmux content when: explicitly requested via content=screen/history,
	// OR when the terminal is inactive (active=false) and has a tmux session.
	// Running content=screen captures the visible pane only. CLIs like
	// Claude repaint loading/spinner text in place, and tmux history can flatten
	// those old frames into repeated "loading/generating" fragments before
	// xterm.js ever sees them. content=history is the explicit scrollback path.
	// Completed/inactive terminals still use deeper history so users can review
	// the final transcript. Legacy content=tmux maps to screen/history based on
	// active state; legacy content=deep maps to history.
	//
	// Inactive tmux terminals receive no event-stream updates, so every GET acts
	// as a lightweight refresh — if the pane content changed (e.g. after Claude
	// Code context compaction), ChunkIndex increments, the list-poll returns the
	// new value, and the frontend re-fetches automatically.
	// The live-attach transcript already contains the seed plus every streamed
	// byte and therefore owns the retained Raw scrollback. Do not replace it with
	// a late capture-pane snapshot when the terminal settles: alternate-screen
	// CLIs often expose only their final viewport through tmux at that point.
	preserveSettledStream := !snapshot.Active &&
		wantsHistoryTerminalContent(r) &&
		strings.EqualFold(strings.TrimSpace(snapshot.ContentSource), "tmux_stream") &&
		strings.TrimSpace(snapshot.Content) != ""
	shouldCaptureTmux := shouldCaptureTerminalPaneForDetail(snapshot, r) && !preserveSettledStream
	debugTerminal := terminalDebugEnabled(r)
	debugSource := terminalDebugSource(r)
	captureSkipReason := terminalCaptureSkipReason(snapshot, r, shouldCaptureTmux)
	if debugTerminal {
		writeTerminalDebugDecisionHeaders(w, snapshot, r, shouldCaptureTmux, captureSkipReason)
		log.Printf("[TERMINAL_DEBUG] detail start source=%q terminal_id=%q session_id=%q tmux_session=%q active=%t state=%q chunk=%d content_mode=%q lines_param=%q stored_lines=%d stored_bytes=%d should_capture=%t",
			debugSource,
			snapshot.TerminalID,
			snapshot.SessionID,
			snapshot.TmuxSession,
			snapshot.Active,
			snapshot.State,
			snapshot.ChunkIndex,
			strings.TrimSpace(r.URL.Query().Get("content")),
			strings.TrimSpace(r.URL.Query().Get("lines")),
			terminalLineCount(snapshot.Content),
			len(snapshot.Content),
			shouldCaptureTmux,
		)
	}
	if shouldCaptureTmux {
		// This is the live terminal viewer's content path. Its sources, by state:
		//   - active + content=screen  → captureTerminalVisiblePaneWithStats (visible pane)
		//   - active + content=history → the pipe-pane recording (live byte stream)
		//   - idle                     → captureTerminalPaneForDetail = capture-pane -S (full buffer)
		// It always stores the latest capture via ReplaceContentWithSource — the
		// store keeps the latest content only, with no snapshot accumulation. If the
		// viewer shows wrong/duplicate content, fix it HERE.
		ctx, cancel := context.WithTimeout(r.Context(), terminalTmuxActionTimeout)
		var pipeContent string
		var pipeStats terminalPaneCaptureStats
		var havePipeContent bool
		// Only use the pipe-pane recording (the appended live byte stream) while the
		// pane is ACTIVE. Once the agent is idle/completed it keeps repainting its
		// prompt/footer/spinner, and pipe-pane appends every one of those raw frames;
		// replayed onto the seed buffer (different geometry) they land at the wrong
		// rows and pile up as duplicate lines. For an idle pane fall through to the
		// single static full-buffer capture-pane (-S) below, which renders once.
		if snapshot.Active && shouldReadTerminalPipeRecorderForDetail(r) && api.terminalPipeRecorder != nil {
			var ok bool
			var pipeErr error
			pipeContent, pipeStats, ok, pipeErr = api.terminalPipeRecorder.Content(ctx, snapshot, wantsHistoryTerminalContent(r))
			if pipeErr == nil && ok {
				havePipeContent = true
			} else if debugTerminal && pipeErr != nil {
				log.Printf("[TERMINAL_DEBUG] pipe recorder fallback source=%q terminal_id=%q tmux_session=%q err=%q", debugSource, snapshot.TerminalID, snapshot.TmuxSession, pipeErr.Error())
			}
		}
		content, stats, err := captureTerminalPaneForDetail(ctx, snapshot, r)
		if stats.ContentSource == "" {
			stats.ContentSource = "tmux_capture"
		}
		displayContent := pipeContent
		displayStats := pipeStats
		haveDisplayContent := havePipeContent
		var runtimeStats terminalPaneRuntimeStats
		var haveRuntimeStats bool
		if err == nil {
			haveDisplayContent = havePipeContent && shouldUseTerminalPipeContentForDetail(r, pipeStats, stats)
			if debugTerminal {
				runtimeStats, haveRuntimeStats = inspectTerminalPaneRuntimeStats(ctx, snapshot.TmuxSession)
				writeTerminalDebugHeaders(w, stats, runtimeStats, haveRuntimeStats)
				if haveDisplayContent {
					terminalPipeRecorderDebugHeaders(w, displayStats)
				}
			}
			refreshed, ok := api.terminalStore.ReplaceContentWithSource(snapshot.TerminalID, content, stats.ContentSource)
			if ok {
				snapshot = refreshed
			}
			if haveDisplayContent {
				if displayed, ok := api.terminalStore.SetDisplayContent(snapshot.TerminalID, displayContent, displayStats.ContentSource); ok {
					snapshot = displayed
				} else {
					snapshot.Content = displayContent
					snapshot.Rows = nil
					snapshot.ContentSource = displayStats.ContentSource
					snapshot.ChunkIndex += displayStats.RawBytes
					snapshot.UpdatedAt = time.Now()
				}
			} else {
				snapshot.ContentSource = stats.ContentSource
			}
			if debugTerminal {
				log.Printf("[TERMINAL_DEBUG] capture ok source=%q terminal_id=%q tmux_session=%q requested_lines=%d capture_lines=%d raw_lines=%d raw_bytes=%d collapsed_lines=%d collapsed_bytes=%d duration_ms=%d content_source=%q display_bytes=%d refreshed_chunk=%d history_limit=%q history_size=%q alternate_on=%q pane_height=%q pane_width=%q pane_in_mode=%q scroll_position=%q",
					debugSource,
					snapshot.TerminalID,
					snapshot.TmuxSession,
					stats.RequestedLines,
					stats.CaptureLines,
					stats.RawLines,
					stats.RawBytes,
					stats.CollapsedLines,
					stats.CollapsedBytes,
					stats.Duration.Milliseconds(),
					snapshot.ContentSource,
					displayStats.RawBytes,
					snapshot.ChunkIndex,
					runtimeStats.HistoryLimit,
					runtimeStats.HistorySize,
					runtimeStats.AlternateOn,
					runtimeStats.PaneHeight,
					runtimeStats.PaneWidth,
					runtimeStats.PaneInMode,
					runtimeStats.ScrollPosition,
				)
			}
		} else if isMissingTmuxTargetError(err) && !snapshot.Active {
			if haveDisplayContent {
				if displayed, ok := api.terminalStore.SetDisplayContent(snapshot.TerminalID, displayContent, displayStats.ContentSource); ok {
					snapshot = displayed
				}
			}
			// The backing tmux session is gone and no lifecycle event will
			// arrive to close it. Mark the snapshot stale so the frontend's
			// inactive-terminal probe stops capturing a dead session every 3s.
			if stale, ok := api.terminalStore.MarkStale(snapshot.TerminalID); ok {
				snapshot = stale
			}
			if debugTerminal {
				log.Printf("[TERMINAL_DEBUG] capture stale source=%q terminal_id=%q tmux_session=%q err=%q", debugSource, snapshot.TerminalID, snapshot.TmuxSession, err.Error())
			}
		} else if debugTerminal {
			log.Printf("[TERMINAL_DEBUG] capture error source=%q terminal_id=%q tmux_session=%q err=%q", debugSource, snapshot.TerminalID, snapshot.TmuxSession, err.Error())
		}
		cancel()
	}

	contentMode := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("content")))
	var response terminals.Snapshot
	if isMetadataOnlyTerminalList(contentMode) {
		response = compactTerminalSnapshotForList(
			api.enrichTerminalSnapshotMetadata(r.Context(), newTerminalPlanTypeResolver(r.Context()), snapshot),
			contentMode,
		)
	} else {
		response = api.enrichTerminalSnapshot(r.Context(), newTerminalPlanTypeResolver(r.Context()), snapshot)
	}
	if debugTerminal {
		log.Printf("[TERMINAL_DEBUG] detail response source=%q terminal_id=%q active=%t state=%q chunk=%d content_lines=%d content_bytes=%d row_count=%d",
			debugSource,
			response.TerminalID,
			response.Active,
			response.State,
			response.ChunkIndex,
			terminalLineCount(response.Content),
			len(response.Content),
			len(response.Rows),
		)
	}
	_ = json.NewEncoder(w).Encode(response)
}

type terminalEventsResponse struct {
	TerminalID     string              `json:"terminal_id"`
	Events         []storeevents.Event `json:"events"`
	HasOlder       bool                `json:"has_older"`
	HasNewer       bool                `json:"has_newer"`
	OldestSequence int64               `json:"oldest_sequence,omitempty"`
	LatestSequence int64               `json:"latest_sequence,omitempty"`
}

// handleGetTerminalEvents returns one cursor page from one canonical terminal
// transcript. It deliberately does not return session status; the existing
// light session status/SSE channel remains authoritative for runtime state.
// GET /api/terminals/{terminal_id}/events?limit=&before_sequence=&after_sequence=
func (api *StreamingAPI) handleGetTerminalEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if api.terminalStore == nil || api.eventStore == nil {
		http.Error(w, "Terminal not found", http.StatusNotFound)
		return
	}

	terminalID := strings.TrimSpace(mux.Vars(r)["terminal_id"])
	snapshot, ok := api.terminalStore.Get(terminalID)
	if !ok || !api.canAccessTerminalSession(r, snapshot.SessionID) {
		http.Error(w, "Terminal not found", http.StatusNotFound)
		return
	}

	limit := storeevents.InitialEventsLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 || parsed > storeevents.MaxPollingLimit {
			http.Error(w, fmt.Sprintf("limit must be between 1 and %d", storeevents.MaxPollingLimit), http.StatusBadRequest)
			return
		}
		limit = parsed
	}

	parseCursor := func(name string) (int64, error) {
		raw := strings.TrimSpace(r.URL.Query().Get(name))
		if raw == "" {
			return 0, nil
		}
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value <= 0 {
			return 0, fmt.Errorf("%s must be a positive integer", name)
		}
		return value, nil
	}
	before, err := parseCursor("before_sequence")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	after, err := parseCursor("after_sequence")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if before > 0 && after > 0 {
		http.Error(w, "before_sequence and after_sequence are mutually exclusive", http.StatusBadRequest)
		return
	}

	result := api.eventStore.GetTerminalEvents(snapshot.SessionID, terminalID, storeevents.GetTerminalEventsOptions{
		Limit:          limit,
		BeforeSequence: before,
		AfterSequence:  after,
	})
	if !result.Exists {
		http.Error(w, "Terminal events not found", http.StatusNotFound)
		return
	}
	_ = json.NewEncoder(w).Encode(terminalEventsResponse{
		TerminalID:     terminalID,
		Events:         result.Events,
		HasOlder:       result.HasOlder,
		HasNewer:       result.HasNewer,
		OldestSequence: result.OldestSequence,
		LatestSequence: result.LatestSequence,
	})
}

func shouldCaptureTerminalPaneForDetail(snapshot terminals.Snapshot, r *http.Request) bool {
	if strings.TrimSpace(snapshot.TmuxSession) == "" {
		return false
	}
	if wantsMetadataTerminalContent(r) {
		return false
	}
	if wantsStoredTerminalContent(r) {
		return false
	}
	if wantsScreenTerminalContent(r) || wantsHistoryTerminalContent(r) || !snapshot.Active {
		return true
	}
	return terminalSnapshotHasPromptCompletionFallback(snapshot.Content)
}

func shouldReadTerminalPipeRecorderForDetail(r *http.Request) bool {
	return wantsHistoryTerminalContent(r)
}

func shouldUseTerminalPipeContentForDetail(r *http.Request, pipeStats, captureStats terminalPaneCaptureStats) bool {
	if !wantsHistoryTerminalContent(r) {
		return false
	}
	return true
}

func terminalCaptureSkipReason(snapshot terminals.Snapshot, r *http.Request, shouldCapture bool) string {
	if shouldCapture {
		return ""
	}
	if strings.TrimSpace(snapshot.TmuxSession) == "" {
		return "no_tmux_session"
	}
	if wantsStoredTerminalContent(r) {
		return "requested_stored_content"
	}
	if snapshot.Active {
		return "active_without_screen_or_history_request"
	}
	return "capture_not_requested"
}

func captureTerminalPaneForDetail(ctx context.Context, snapshot terminals.Snapshot, r *http.Request) (string, terminalPaneCaptureStats, error) {
	if shouldCaptureVisibleTerminalScreen(snapshot, r) {
		return captureTerminalVisiblePaneWithStats(ctx, snapshot.TmuxSession)
	}
	lines := terminalCaptureLinesFromRequest(r, terminalDefaultDetailHistoryLines)
	if shouldCaptureActiveTerminalHistoryForDetail(snapshot, r) {
		lines = terminalCaptureLinesFromRequest(r, terminalActiveDetailHistoryLines)
	}
	return captureTerminalPaneLinesWithStats(ctx, snapshot.TmuxSession, lines)
}

func shouldCaptureVisibleTerminalScreen(snapshot terminals.Snapshot, r *http.Request) bool {
	if !wantsScreenTerminalContent(r) || wantsHistoryTerminalContent(r) {
		return false
	}
	if !snapshot.Active {
		return false
	}
	return true
}

func shouldCaptureActiveTerminalHistoryForDetail(snapshot terminals.Snapshot, r *http.Request) bool {
	if !snapshot.Active || terminalSnapshotHasPromptCompletionFallback(snapshot.Content) {
		return false
	}
	if strings.TrimSpace(r.URL.Query().Get("lines")) != "" || wantsDeepTerminalContent(r) {
		return false
	}
	return wantsScreenTerminalContent(r)
}

func terminalSnapshotHasPromptCompletionFallback(content string) bool {
	lower := strings.ToLower(content)
	return strings.Contains(lower, "status: completed") ||
		strings.Contains(lower, "status: complete")
}

// handleDismissTerminal removes one terminal snapshot from the UI.
// DELETE /api/terminals/{terminal_id}
func (api *StreamingAPI) handleDismissTerminal(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if api.terminalStore == nil {
		http.Error(w, "Terminal not found", http.StatusNotFound)
		return
	}

	terminalID := strings.TrimSpace(mux.Vars(r)["terminal_id"])
	if terminalID == "" {
		http.Error(w, "Terminal ID is required", http.StatusBadRequest)
		return
	}
	snapshot, ok := api.terminalStore.Get(terminalID)
	if !ok || !api.canAccessTerminalSession(r, snapshot.SessionID) {
		http.Error(w, "Terminal not found", http.StatusNotFound)
		return
	}
	if !api.terminalStore.Dismiss(terminalID) {
		http.Error(w, "Terminal not found", http.StatusNotFound)
		return
	}
	if registry := api.ensureTerminalLeaseRegistry(); registry != nil {
		registry.Observe(snapshot, snapshot.UpdatedAt)
		registry.MarkSnapshotDismissed(terminalID)
	}
	_ = json.NewEncoder(w).Encode(map[string]bool{"dismissed": true})
}

// handleCompleteTerminal marks one terminal snapshot complete and, when the
// backing provider wait loop is still active, asks it to return through the
// normal execution path. The workflow controller then runs validation,
// learning/KB hooks, progress updates, and auto-notification itself.
// POST /api/terminals/{terminal_id}/complete
func (api *StreamingAPI) handleCompleteTerminal(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	before, ok := api.requireAccessibleTerminal(w, r)
	if !ok {
		return
	}
	snapshot, ok := api.terminalStore.MarkCompleted(before.TerminalID)
	if !ok {
		http.Error(w, "Terminal not found", http.StatusNotFound)
		return
	}
	if before.Active && strings.TrimSpace(before.TmuxSession) != "" {
		forceCompleteCodingAgentTmuxSession(before.TmuxSession)
	}
	if registry := api.ensureTerminalLeaseRegistry(); registry != nil {
		registry.Observe(snapshot, snapshot.UpdatedAt)
	}
	_ = json.NewEncoder(w).Encode(terminalActionResponse{OK: true, Terminal: api.enrichTerminalSnapshot(r.Context(), newTerminalPlanTypeResolver(r.Context()), snapshot)})
}

// handleFailTerminal fails the terminal and closes its backing process.
// POST /api/terminals/{terminal_id}/fail
func (api *StreamingAPI) handleFailTerminal(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	before, ok := api.requireAccessibleTerminal(w, r)
	if !ok {
		return
	}
	snapshot, ok := api.terminalStore.MarkFailed(before.TerminalID)
	if !ok {
		http.Error(w, "Terminal not found", http.StatusNotFound)
		return
	}
	api.reconcileUnexpectedTerminalExit(before, "failed by operator")
	if before.Active && strings.TrimSpace(before.TmuxSession) != "" {
		if closeCodingAgentTmuxSessionByName(before.TmuxSession, "failed by operator") {
			if registry := api.ensureTerminalLeaseRegistry(); registry != nil {
				registry.MarkClosed(before.TmuxSession, "failed by operator", time.Now())
			}
			if archived, archivedOK := api.terminalStore.MarkProcessClosed(before.TerminalID, "failed by operator"); archivedOK {
				snapshot = archived
			}
		}
	}
	if current, exists := api.terminalStore.Get(before.TerminalID); exists {
		snapshot = current
	}
	_ = json.NewEncoder(w).Encode(terminalActionResponse{OK: true, Terminal: api.enrichTerminalSnapshot(r.Context(), newTerminalPlanTypeResolver(r.Context()), snapshot)})
}

// handleRefreshTerminal captures the current tmux pane and updates the snapshot.
// POST /api/terminals/{terminal_id}/refresh
func (api *StreamingAPI) handleRefreshTerminal(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	snapshot, ok := api.requireAccessibleTerminal(w, r)
	if !ok {
		return
	}
	if strings.TrimSpace(snapshot.TmuxSession) == "" {
		http.Error(w, "Terminal has no tmux session", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), terminalTmuxActionTimeout)
	defer cancel()
	lines := terminalCaptureLinesFromRequest(r, terminalDefaultRefreshLines)
	content, err := captureTerminalPaneLines(ctx, snapshot.TmuxSession, lines)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	updated, ok := api.terminalStore.ReplaceContent(snapshot.TerminalID, content)
	if !ok {
		http.Error(w, "Terminal not found", http.StatusNotFound)
		return
	}
	_ = json.NewEncoder(w).Encode(terminalActionResponse{OK: true, Terminal: api.enrichTerminalSnapshot(r.Context(), newTerminalPlanTypeResolver(r.Context()), updated)})
}

// handleKillTerminal kills the backing tmux session. An active execution is
// failed; a logically completed execution keeps its completed outcome while
// its retained continuation process is closed.
// POST /api/terminals/{terminal_id}/kill
func (api *StreamingAPI) handleKillTerminal(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	snapshot, ok := api.requireAccessibleTerminal(w, r)
	if !ok {
		return
	}
	if strings.TrimSpace(snapshot.TmuxSession) == "" {
		http.Error(w, "Terminal has no tmux session", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), terminalTmuxActionTimeout)
	defer cancel()
	if err := runTerminalTmuxCommand(ctx, "", "kill-session", "-t", snapshot.TmuxSession); err != nil {
		if !isMissingTmuxTargetError(err) {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
	}
	updated := snapshot
	if snapshot.Active {
		var marked bool
		updated, marked = api.terminalStore.MarkFailed(snapshot.TerminalID)
		if !marked {
			http.Error(w, "Terminal not found", http.StatusNotFound)
			return
		}
		api.reconcileUnexpectedTerminalExit(snapshot, "killed by operator")
	}
	if registry := api.ensureTerminalLeaseRegistry(); registry != nil {
		registry.MarkClosed(snapshot.TmuxSession, "killed by operator", time.Now())
	}
	if archived, archivedOK := api.terminalStore.MarkProcessClosed(snapshot.TerminalID, "killed by operator"); archivedOK {
		updated = archived
	}
	if current, exists := api.terminalStore.Get(snapshot.TerminalID); exists {
		updated = current
	}
	_ = json.NewEncoder(w).Encode(terminalActionResponse{OK: true, Terminal: api.enrichTerminalSnapshot(r.Context(), newTerminalPlanTypeResolver(r.Context()), updated)})
}

// handleSendTerminalInput pastes text into the terminal's tmux pane.
// POST /api/terminals/{terminal_id}/input
func (api *StreamingAPI) handleSendTerminalInput(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	snapshot, ok := api.requireAccessibleTerminal(w, r)
	if !ok {
		return
	}

	var req sendTerminalInputRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid terminal input request", http.StatusBadRequest)
		return
	}
	// Reject only a genuinely empty payload — NOT whitespace-only. A lone space
	// (or tab) is a valid keystroke in keyboard-passthrough mode; TrimSpace here
	// would drop it, so spaces never reach the CLI.
	if req.Text == "" && !req.Submit {
		http.Error(w, "Text or submit=true is required", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(snapshot.TmuxSession) == "" {
		http.Error(w, "Terminal has no tmux session", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), terminalTmuxActionTimeout)
	defer cancel()
	if err := deliverTerminalInput(ctx, snapshot.TmuxSession, req.Text, req.Submit, terminalSnapshotPrefersReadablePaste(snapshot), "terminal-http"); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	_ = json.NewEncoder(w).Encode(terminalActionResponse{OK: true, Terminal: api.enrichTerminalSnapshot(r.Context(), newTerminalPlanTypeResolver(r.Context()), snapshot)})
}

// handleSendTerminalKey sends a small allowlisted key to the terminal's tmux pane.
// POST /api/terminals/{terminal_id}/key
func (api *StreamingAPI) handleSendTerminalKey(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	snapshot, ok := api.requireAccessibleTerminal(w, r)
	if !ok {
		return
	}

	var req sendTerminalKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid terminal key request", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(snapshot.TmuxSession) == "" {
		http.Error(w, "Terminal has no tmux session", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), terminalTmuxActionTimeout)
	defer cancel()
	if err := sendTerminalKey(ctx, snapshot.TmuxSession, req.Key); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = json.NewEncoder(w).Encode(terminalActionResponse{OK: true, Terminal: api.enrichTerminalSnapshot(r.Context(), newTerminalPlanTypeResolver(r.Context()), snapshot)})
}

// handleTerminalSizeHint records the operator's viewport size as the preferred
// tmux launch size WITHOUT needing an existing terminal. Called by the frontend
// at startup so the very first coding-agent session launches at the correct
// width rather than the default 120×36.
// POST /api/terminals/size-hint  body: {"cols": int, "rows": int}
func (api *StreamingAPI) handleTerminalSizeHint(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var req resizeTerminalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Cols <= 0 || req.Rows <= 0 {
		http.Error(w, "cols and rows must be positive integers", http.StatusBadRequest)
		return
	}
	if req.Cols < terminalMinResizeCols || req.Rows < terminalMinResizeRows {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "resized": 0, "ignored": true})
		return
	}
	req.Cols, req.Rows = clampTerminalResize(req.Cols, req.Rows)
	llmproviders.SetCodingAgentTmuxSize(req.Cols, req.Rows)
	resized := api.resizeLiveTerminalWindowsForSession(r.Context(), r, req.SessionID, req.Cols, req.Rows)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "resized": resized})
}

func (api *StreamingAPI) resizeLiveTerminalWindowsForSession(ctx context.Context, r *http.Request, sessionID string, cols, rows int) int {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || api.terminalStore == nil || cols < terminalMinResizeCols || rows < terminalMinResizeRows {
		return 0
	}
	cols, rows = clampTerminalResize(cols, rows)
	snapshots := api.terminalStore.List(sessionID)
	if len(snapshots) == 0 {
		return 0
	}

	resizeCtx, cancel := context.WithTimeout(ctx, terminalTmuxActionTimeout)
	defer cancel()
	resized := 0
	for _, snapshot := range snapshots {
		if !snapshot.Active || strings.TrimSpace(snapshot.TmuxSession) == "" || !api.canAccessTerminalSession(r, snapshot.SessionID) {
			continue
		}
		if api.isTerminalLiveAttached(snapshot.TmuxSession) {
			continue
		}
		// Skip when already at the requested geometry: a no-op resize would still
		// truncate+reseed the live recording and stack an inline TUI's redraws.
		if curW, curH, ok := terminalWindowSize(resizeCtx, snapshot.TmuxSession); ok && curW == cols && curH == rows {
			continue
		}
		// Re-assert window-size manual so resize-window is authoritative (the window
		// follows the xterm, not the launch/preferred size or any client).
		_ = runTerminalTmuxCommand(resizeCtx, "", "set-window-option", "-t", snapshot.TmuxSession, "window-size", "manual")
		if err := runTerminalTmuxCommand(resizeCtx, "", "resize-window", "-t", snapshot.TmuxSession, "-x", strconv.Itoa(cols), "-y", strconv.Itoa(rows)); err != nil {
			if isMissingTmuxTargetError(err) {
				api.terminalStore.MarkStale(snapshot.TerminalID)
			}
			continue
		}
		// Geometry changed: drop the pipe recording's old-width frames and re-seed
		// an authoritative current-screen snapshot so the live stream never replays
		// mismatched-width frames.
		if api.terminalPipeRecorder != nil {
			api.terminalPipeRecorder.ResetForResize(resizeCtx, snapshot.TmuxSession)
		}
		resized++
	}
	return resized
}

// handleResizeTerminal resizes the backing tmux window and records the size
// as the process-wide preferred size so subsequent coding-agent tmux launches
// match the operator's viewport.
// POST /api/terminals/{terminal_id}/resize  body: {"cols": int, "rows": int}
func (api *StreamingAPI) handleResizeTerminal(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	snapshot, ok := api.requireAccessibleTerminal(w, r)
	if !ok {
		return
	}

	var req resizeTerminalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid resize request", http.StatusBadRequest)
		return
	}
	if req.Cols <= 0 || req.Rows <= 0 {
		http.Error(w, "cols and rows must be positive", http.StatusBadRequest)
		return
	}
	if req.Cols < terminalMinResizeCols || req.Rows < terminalMinResizeRows {
		http.Error(w, "cols and rows below minimum terminal size", http.StatusBadRequest)
		return
	}
	req.Cols, req.Rows = clampTerminalResize(req.Cols, req.Rows)
	// Always update the preferred size so newly-launched sessions adopt the
	// operator's viewport even if THIS terminal has no live tmux window.
	llmproviders.SetCodingAgentTmuxSize(req.Cols, req.Rows)

	if strings.TrimSpace(snapshot.TmuxSession) == "" {
		// No live pane to resize, but the preferred size was still recorded.
		_ = json.NewEncoder(w).Encode(terminalActionResponse{OK: true, Terminal: api.enrichTerminalSnapshot(r.Context(), newTerminalPlanTypeResolver(r.Context()), snapshot)})
		return
	}
	if api.isTerminalLiveAttached(snapshot.TmuxSession) {
		// The live WebSocket owns this tmux session's geometry. Keep /resize as a
		// preferred-size update only; otherwise the legacy capture/pipe resize path
		// would also reseed the removed pipe recorder while live-attach is already
		// resizing the same tmux window.
		_ = json.NewEncoder(w).Encode(terminalActionResponse{OK: true, Terminal: api.enrichTerminalSnapshot(r.Context(), newTerminalPlanTypeResolver(r.Context()), snapshot)})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), terminalTmuxActionTimeout)
	defer cancel()
	// Geometry-change guard: skip BOTH the resize-window and the recording reseed
	// when the pane is already at the requested geometry. The resize-on-tmux-appear
	// effect POSTs the xterm grid the moment a (re)launched/resumed session goes
	// live; when tmux already matches (the common case — sessions launch at the
	// operator's preferred size), resize-window is a no-op but ResetForResize would
	// still truncate the live pipe recording mid-stream, and a normal-buffer inline
	// TUI (pi-cli) then replays its incremental redraws out of context → stacked
	// spinners / duplicated status. A true no-op leaves the recording (and its
	// continuous redraw context) intact; the backfill still renders via the content
	// prop, so the resumed first-click terminal is unaffected.
	if curW, curH, ok := terminalWindowSize(ctx, snapshot.TmuxSession); ok && curW == req.Cols && curH == req.Rows {
		log.Printf("[SPINNER_DEBUG] resize session=%s requested=%dx%d win=%dx%d (no-op: geometry unchanged, recording preserved)", snapshot.TmuxSession, req.Cols, req.Rows, curW, curH)
		_ = json.NewEncoder(w).Encode(terminalActionResponse{OK: true, Terminal: api.enrichTerminalSnapshot(r.Context(), newTerminalPlanTypeResolver(r.Context()), snapshot)})
		return
	}
	// Make resize-window AUTHORITATIVE for this detached session. The session is
	// created at the operator's preferred size (new-session -x -y from
	// tmuxsize.Args()); when the operator's viewport later narrows (a wide earlier
	// layout left the preferred at e.g. 130x37, the current xterm is 119x27), the
	// window must follow the xterm — otherwise pi-cli renders past the xterm's
	// columns and wide content (markdown tables) wraps. Re-assert window-size manual
	// right before resizing so tmux uses our explicit dimensions rather than the
	// launch/preferred size or any client; idempotent if the adapter already set it.
	_ = runTerminalTmuxCommand(ctx, "", "set-window-option", "-t", snapshot.TmuxSession, "window-size", "manual")
	if err := runTerminalTmuxCommand(ctx, "", "resize-window", "-t", snapshot.TmuxSession, "-x", strconv.Itoa(req.Cols), "-y", strconv.Itoa(req.Rows)); err != nil {
		// If the backing tmux session is gone, mark the terminal stale (which
		// also clears TmuxSession) and report success — the preferred-size
		// update already landed, and there is no live pane to resize. Without
		// this branch, every subsequent frontend resize POST returns 502 until
		// the next pane refresh re-detects staleness.
		if isMissingTmuxTargetError(err) {
			if stale, ok := api.terminalStore.MarkStale(snapshot.TerminalID); ok {
				snapshot = stale
			}
			_ = json.NewEncoder(w).Encode(terminalActionResponse{OK: true, Terminal: api.enrichTerminalSnapshot(r.Context(), newTerminalPlanTypeResolver(r.Context()), snapshot)})
			return
		}
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	// [SPINNER_DEBUG] requested-vs-actual geometry — diagnoses live-region (spinner)
	// stacking and wide-content wrapping from an xterm/tmux size mismatch. After the
	// window-size-manual fix this must read win==pane==requested. Remove later.
	if curW, curH, ok := terminalWindowSize(ctx, snapshot.TmuxSession); ok {
		match := "MATCH"
		if curW != req.Cols || curH != req.Rows {
			match = "MISMATCH"
		}
		if out, qerr := runTerminalTmuxOutputCommand(ctx, "display-message", "-p", "-t", snapshot.TmuxSession,
			"win=#{window_width}x#{window_height} pane=#{pane_width}x#{pane_height} alt=#{alternate_on}"); qerr == nil {
			log.Printf("[SPINNER_DEBUG] resize session=%s requested=%dx%d %s (%s)", snapshot.TmuxSession, req.Cols, req.Rows, strings.TrimSpace(out), match)
		} else {
			log.Printf("[SPINNER_DEBUG] resize session=%s requested=%dx%d win=%dx%d (%s)", snapshot.TmuxSession, req.Cols, req.Rows, curW, curH, match)
		}
	}
	// The pane was resized: the pipe recording still holds frames captured at the
	// OLD geometry. Truncate it and re-seed a screen-mode-aware prologue, then
	// force a fresh full repaint, so the frontend never replays old-width frames
	// concatenated with new-width frames (the resize-time litter).
	if api.terminalPipeRecorder != nil {
		api.terminalPipeRecorder.ResetForResize(ctx, snapshot.TmuxSession)
	}
	_ = json.NewEncoder(w).Encode(terminalActionResponse{OK: true, Terminal: api.enrichTerminalSnapshot(r.Context(), newTerminalPlanTypeResolver(r.Context()), snapshot)})
}

func (api *StreamingAPI) isTerminalLiveAttached(tmuxSession string) bool {
	return api != nil && api.liveAttach != nil && api.liveAttach.hasSession(tmuxSession)
}

func (api *StreamingAPI) requireAccessibleTerminal(w http.ResponseWriter, r *http.Request) (terminals.Snapshot, bool) {
	if api.terminalStore == nil {
		http.Error(w, "Terminal not found", http.StatusNotFound)
		return terminals.Snapshot{}, false
	}
	terminalID := strings.TrimSpace(mux.Vars(r)["terminal_id"])
	if terminalID == "" {
		http.Error(w, "Terminal ID is required", http.StatusBadRequest)
		return terminals.Snapshot{}, false
	}
	snapshot, ok := api.terminalStore.Get(terminalID)
	if !ok || !api.canAccessTerminalSession(r, snapshot.SessionID) {
		http.Error(w, "Terminal not found", http.StatusNotFound)
		return terminals.Snapshot{}, false
	}
	return snapshot, true
}

func pasteTerminalText(ctx context.Context, tmuxSession, text string) error {
	return pasteTerminalTextWithReadableMode(ctx, tmuxSession, text, terminalTmuxSessionLooksCursor(tmuxSession))
}

func pasteTerminalTextWithReadableMode(ctx context.Context, tmuxSession, text string, readable bool) error {
	tmuxSession = strings.TrimSpace(tmuxSession)
	if tmuxSession == "" {
		return fmt.Errorf("tmux session is required")
	}
	_, err := tmuxinput.Default.Do(ctx, tmuxinput.Request{
		SessionID: tmuxSession,
		Source:    "terminal-paste",
	}, func(ctx context.Context) error {
		return pasteTerminalTextUnserialized(ctx, tmuxSession, text, readable)
	})
	return err
}

func pasteTerminalTextUnserialized(ctx context.Context, tmuxSession, text string, readable bool) error {
	bufferName := fmt.Sprintf("mcp-terminal-ui-%d", time.Now().UnixNano())
	if err := runTerminalTmuxCommand(ctx, text, "load-buffer", "-b", bufferName, "-"); err != nil {
		return fmt.Errorf("failed to load terminal input into tmux buffer: %w", err)
	}
	args := []string{"paste-buffer", "-d"}
	if !readable {
		args = append(args, "-p")
	}
	args = append(args, "-r", "-b", bufferName, "-t", tmuxSession)
	if err := runTerminalTmuxCommand(ctx, "", args...); err != nil {
		return fmt.Errorf("failed to paste terminal input into tmux session: %w", err)
	}
	return nil
}

func deliverTerminalInput(ctx context.Context, tmuxSession, text string, submit, readable bool, source string) error {
	tmuxSession = strings.TrimSpace(tmuxSession)
	if tmuxSession == "" {
		return fmt.Errorf("tmux session is required")
	}
	_, err := tmuxinput.Default.Do(ctx, tmuxinput.Request{
		SessionID:       tmuxSession,
		Source:          source,
		BypassReadiness: true,
	}, func(ctx context.Context) error {
		if text != "" {
			if err := pasteTerminalTextUnserialized(ctx, tmuxSession, text, readable); err != nil {
				return err
			}
		}
		if submit {
			return sendTerminalKeyUnserialized(ctx, tmuxSession, "enter")
		}
		return nil
	})
	return err
}

func terminalSnapshotPrefersReadablePaste(snapshot terminals.Snapshot) bool {
	if terminalTmuxSessionLooksCursor(snapshot.TmuxSession) {
		return true
	}
	if strings.Contains(strings.ToLower(snapshot.Status.ProviderLabel), "cursor") {
		return true
	}
	return strings.Contains(strings.ToLower(snapshot.Content), "cursor agent")
}

func terminalTmuxSessionLooksCursor(tmuxSession string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(tmuxSession)), "cursor")
}

func captureTerminalPane(ctx context.Context, tmuxSession string) (string, error) {
	return captureTerminalPaneLines(ctx, tmuxSession, terminalDefaultRefreshLines)
}

func captureTerminalPaneLines(ctx context.Context, tmuxSession string, lines int) (string, error) {
	content, _, err := captureTerminalPaneLinesWithStats(ctx, tmuxSession, lines)
	return content, err
}

func captureTerminalVisiblePaneWithStats(ctx context.Context, tmuxSession string) (string, terminalPaneCaptureStats, error) {
	stats := terminalPaneCaptureStats{ContentSource: "tmux_capture"}
	tmuxSession = strings.TrimSpace(tmuxSession)
	if tmuxSession == "" {
		return "", stats, fmt.Errorf("tmux session is required")
	}
	start := time.Now()
	content, err := runTerminalTmuxOutputCommand(ctx, "capture-pane", "-p", "-e", "-J", "-t", tmuxSession)
	stats.Duration = time.Since(start)
	if err != nil {
		return "", stats, fmt.Errorf("failed to capture terminal pane: %w", err)
	}
	stats.RawLines = terminalLineCount(content)
	stats.RawBytes = len(content)
	collapsed := collapseBlankRuns(content)
	stats.CollapsedLines = terminalLineCount(collapsed)
	stats.CollapsedBytes = len(collapsed)
	return collapsed, stats, nil
}

func captureTerminalPaneLinesWithStats(ctx context.Context, tmuxSession string, lines int) (string, terminalPaneCaptureStats, error) {
	stats := terminalPaneCaptureStats{RequestedLines: lines, ContentSource: "tmux_capture"}
	tmuxSession = strings.TrimSpace(tmuxSession)
	if tmuxSession == "" {
		return "", stats, fmt.Errorf("tmux session is required")
	}
	if lines <= 0 {
		lines = terminalDefaultRefreshLines
	}
	if lines > terminalMaxCaptureLines {
		lines = terminalMaxCaptureLines
	}
	stats.CaptureLines = lines
	// -e preserves ANSI SGR (color, bold, dim, …) so xterm.js can render the
	// captured pane as a terminal. Without it, terminal refresh / completion
	// fetches would silently strip color even though the running session emitted
	// it.
	//
	// -J rejoins lines that tmux hard-wrapped at the pane width into a single
	// logical line. The web pane is a whitespace-pre-wrap <pre> whose width
	// rarely equals the tmux pane width, so without -J a long line gets
	// double-wrapped (once by tmux, again by the browser) and shows a ragged
	// right edge. -J only joins genuinely wrapped continuations — TUI box
	// borders, which end at the pane edge deliberately, are left intact.
	start := time.Now()
	content, err := runTerminalTmuxOutputCommand(ctx, "capture-pane", "-p", "-e", "-J", "-t", tmuxSession, "-S", fmt.Sprintf("-%d", lines))
	stats.Duration = time.Since(start)
	if err != nil {
		return "", stats, fmt.Errorf("failed to capture terminal pane: %w", err)
	}
	stats.RawLines = terminalLineCount(content)
	stats.RawBytes = len(content)
	collapsed := collapseBlankRuns(content)
	stats.CollapsedLines = terminalLineCount(collapsed)
	stats.CollapsedBytes = len(collapsed)
	return collapsed, stats, nil
}

func inspectTerminalPaneRuntimeStats(ctx context.Context, tmuxSession string) (terminalPaneRuntimeStats, bool) {
	tmuxSession = strings.TrimSpace(tmuxSession)
	if tmuxSession == "" {
		return terminalPaneRuntimeStats{}, false
	}
	format := strings.Join([]string{
		"#{history_limit}",
		"#{history_size}",
		"#{alternate_on}",
		"#{pane_height}",
		"#{pane_width}",
		"#{pane_in_mode}",
		"#{scroll_position}",
	}, "\t")
	output, err := runTerminalTmuxOutputCommand(ctx, "display-message", "-p", "-t", tmuxSession, format)
	if err != nil {
		return terminalPaneRuntimeStats{}, false
	}
	parts := strings.Split(strings.TrimSpace(output), "\t")
	if len(parts) < 7 {
		return terminalPaneRuntimeStats{}, false
	}
	return terminalPaneRuntimeStats{
		HistoryLimit:   parts[0],
		HistorySize:    parts[1],
		AlternateOn:    parts[2],
		PaneHeight:     parts[3],
		PaneWidth:      parts[4],
		PaneInMode:     parts[5],
		ScrollPosition: parts[6],
	}, true
}

func writeTerminalDebugHeaders(w http.ResponseWriter, stats terminalPaneCaptureStats, runtimeStats terminalPaneRuntimeStats, haveRuntimeStats bool) {
	w.Header().Set("X-Runloop-Terminal-Content-Source", stats.ContentSource)
	w.Header().Set("X-Runloop-Terminal-Requested-Lines", strconv.Itoa(stats.RequestedLines))
	w.Header().Set("X-Runloop-Terminal-Capture-Lines", strconv.Itoa(stats.CaptureLines))
	w.Header().Set("X-Runloop-Terminal-Raw-Lines", strconv.Itoa(stats.RawLines))
	w.Header().Set("X-Runloop-Terminal-Raw-Bytes", strconv.Itoa(stats.RawBytes))
	w.Header().Set("X-Runloop-Terminal-Collapsed-Lines", strconv.Itoa(stats.CollapsedLines))
	w.Header().Set("X-Runloop-Terminal-Collapsed-Bytes", strconv.Itoa(stats.CollapsedBytes))
	// Scrollback accumulation was removed; the store now always keeps the latest
	// capture only. The header is retained (always false) for compatibility.
	w.Header().Set("X-Runloop-Terminal-Preserve-Scrollback", strconv.FormatBool(false))
	if !haveRuntimeStats {
		return
	}
	w.Header().Set("X-Runloop-Terminal-Tmux-History-Limit", runtimeStats.HistoryLimit)
	w.Header().Set("X-Runloop-Terminal-Tmux-History-Size", runtimeStats.HistorySize)
	w.Header().Set("X-Runloop-Terminal-Tmux-Alternate-On", runtimeStats.AlternateOn)
	w.Header().Set("X-Runloop-Terminal-Tmux-Pane-Height", runtimeStats.PaneHeight)
	w.Header().Set("X-Runloop-Terminal-Tmux-Pane-Width", runtimeStats.PaneWidth)
	w.Header().Set("X-Runloop-Terminal-Tmux-Pane-In-Mode", runtimeStats.PaneInMode)
	w.Header().Set("X-Runloop-Terminal-Tmux-Scroll-Position", runtimeStats.ScrollPosition)
}

func writeTerminalDebugDecisionHeaders(w http.ResponseWriter, snapshot terminals.Snapshot, r *http.Request, shouldCapture bool, skipReason string) {
	w.Header().Set("X-Runloop-Terminal-Debug-Should-Capture", strconv.FormatBool(shouldCapture))
	w.Header().Set("X-Runloop-Terminal-Debug-Skip-Reason", skipReason)
	w.Header().Set("X-Runloop-Terminal-Debug-Tmux-Session", strings.TrimSpace(snapshot.TmuxSession))
	w.Header().Set("X-Runloop-Terminal-Debug-Step-Transport", strings.TrimSpace(snapshot.StepTransport))
	w.Header().Set("X-Runloop-Terminal-Debug-Active", strconv.FormatBool(snapshot.Active))
	w.Header().Set("X-Runloop-Terminal-Debug-State", strings.TrimSpace(snapshot.State))
	w.Header().Set("X-Runloop-Terminal-Debug-Chunk-Index", strconv.Itoa(snapshot.ChunkIndex))
	w.Header().Set("X-Runloop-Terminal-Debug-Content-Mode", strings.TrimSpace(r.URL.Query().Get("content")))
	w.Header().Set("X-Runloop-Terminal-Debug-Lines-Param", strings.TrimSpace(r.URL.Query().Get("lines")))
	w.Header().Set("X-Runloop-Terminal-Debug-Stored-Lines", strconv.Itoa(terminalLineCount(snapshot.Content)))
	w.Header().Set("X-Runloop-Terminal-Debug-Stored-Bytes", strconv.Itoa(len(snapshot.Content)))
}

func terminalLineCount(content string) int {
	if content == "" {
		return 0
	}
	return strings.Count(content, "\n") + 1
}

func terminalDebugEnabled(r *http.Request) bool {
	for _, value := range []string{
		r.URL.Query().Get("debug"),
		r.Header.Get("X-Runloop-Terminal-Debug"),
		os.Getenv("RUNLOOP_TERMINAL_DEBUG"),
	} {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "1", "true", "yes", "on":
			return true
		}
	}
	return false
}

func terminalDebugSource(r *http.Request) string {
	source := strings.TrimSpace(r.URL.Query().Get("debug_source"))
	if source == "" {
		source = strings.TrimSpace(r.Header.Get("X-Runloop-Terminal-Debug-Source"))
	}
	if source == "" {
		source = "unknown"
	}
	return source
}

// collapseBlankRuns keeps the existing server call sites while sharing the
// capture cleanup used by query_step and the standalone delegation MCP.
func collapseBlankRuns(s string) string {
	return tmuxcapture.CollapseBlankRuns(s)
}

func wantsDeepTerminalContent(r *http.Request) bool {
	return wantsHistoryTerminalContent(r)
}

func wantsStoredTerminalContent(r *http.Request) bool {
	contentMode := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("content")))
	return contentMode == "stored"
}

func wantsMetadataTerminalContent(r *http.Request) bool {
	contentMode := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("content")))
	return contentMode == "none" || contentMode == "metadata"
}

func wantsHistoryTerminalContent(r *http.Request) bool {
	contentMode := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("content")))
	refresh := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("refresh")))
	return contentMode == "history" || contentMode == "deep" || refresh == "true" || refresh == "1"
}

func wantsScreenTerminalContent(r *http.Request) bool {
	contentMode := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("content")))
	return contentMode == "screen" || contentMode == "tmux"
}

func terminalCaptureLinesFromRequest(r *http.Request, fallback int) int {
	raw := strings.TrimSpace(r.URL.Query().Get("lines"))
	if raw == "" {
		return fallback
	}
	lines, err := strconv.Atoi(raw)
	if err != nil || lines <= 0 {
		return fallback
	}
	if lines > terminalMaxCaptureLines {
		return terminalMaxCaptureLines
	}
	return lines
}

func sendTerminalKey(ctx context.Context, tmuxSession, key string) error {
	tmuxSession = strings.TrimSpace(tmuxSession)
	if tmuxSession == "" {
		return fmt.Errorf("tmux session is required")
	}
	_, err := tmuxinput.Default.Do(ctx, tmuxinput.Request{
		SessionID:       tmuxSession,
		Source:          "terminal-key",
		Priority:        tmuxinput.PriorityForKey(key),
		BypassReadiness: true,
	}, func(ctx context.Context) error {
		return sendTerminalKeyUnserialized(ctx, tmuxSession, key)
	})
	return err
}

func sendTerminalKeyUnserialized(ctx context.Context, tmuxSession, key string) error {
	tmuxKey, ok := tmuxKeyName(key)
	if !ok {
		return fmt.Errorf("unsupported terminal key %q", key)
	}
	log.Printf("[TERMINAL_KEY] send key=%q resolved=%q tmux_session=%s", key, tmuxKey, tmuxSession)
	return runTerminalTmuxCommand(ctx, "", "send-keys", "-t", tmuxSession, tmuxKey)
}

// tmuxKeyName maps a frontend key identifier to the key name understood by
// `tmux send-keys`. It covers the named keys used by the terminal debug menu
// plus the broader set needed for live keyboard passthrough from the chat
// input (arrows, editing keys, navigation keys, and arbitrary Ctrl chords).
func tmuxKeyName(key string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "enter", "return":
		return "Enter", true
	case "esc", "escape":
		return "Escape", true
	case "tab":
		// e.g. allowlist a gated MCP-tool prompt, accept a menu selection.
		return "Tab", true
	case "btab", "shift-tab", "shift_tab":
		return "BTab", true
	case "space":
		return "Space", true
	case "backspace", "bspace":
		return "BSpace", true
	case "delete", "del":
		return "DC", true
	case "up", "arrow-up", "arrow_up":
		return "Up", true
	case "down", "arrow-down", "arrow_down":
		return "Down", true
	case "left", "arrow-left", "arrow_left":
		return "Left", true
	case "right", "arrow-right", "arrow_right":
		return "Right", true
	case "home":
		return "Home", true
	case "end":
		return "End", true
	case "pageup", "page-up", "page_up", "pgup":
		return "PageUp", true
	case "pagedown", "page-down", "page_down", "pgdn":
		return "PageDown", true
	case "ctrl-o", "ctrl_o", "expand":
		return "C-o", true
	case "ctrl-c", "ctrl_c", "interrupt", "cancel":
		return "C-c", true
	}
	// Generic Ctrl chord, e.g. "ctrl-d" -> "C-d", "ctrl-l" -> "C-l".
	normalized := strings.ToLower(strings.TrimSpace(key))
	for _, prefix := range []string{"ctrl-", "ctrl_"} {
		if rest := strings.TrimPrefix(normalized, prefix); rest != normalized && len(rest) == 1 {
			r := rest[0]
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				return "C-" + rest, true
			}
		}
	}
	return "", false
}

func isMissingTmuxTargetError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "can't find pane") ||
		strings.Contains(message, "can't find session") ||
		strings.Contains(message, "no server running")
}

func (api *StreamingAPI) enrichTerminalSnapshot(ctx context.Context, planTypes *terminalPlanTypeResolver, snapshot terminals.Snapshot) terminals.Snapshot {
	active, exists := api.getActiveSession(snapshot.SessionID)
	if !exists || active == nil {
		enrichedSnapshot := snapshot.WithContext(terminals.Context{})
		enrichedSnapshot = withTerminalPlanStepType(ctx, planTypes, enrichedSnapshot)
		return withTerminalRows(enrichedSnapshot.WithContext(terminals.Context{}))
	}
	enriched := api.buildActiveSessionInfoSummary(active)
	if enriched == nil {
		enrichedSnapshot := snapshot.WithContext(terminals.Context{})
		enrichedSnapshot = withTerminalPlanStepType(ctx, planTypes, enrichedSnapshot)
		return withTerminalRows(enrichedSnapshot.WithContext(terminals.Context{}))
	}
	enrichedSnapshot := snapshot.WithContext(terminals.Context{
		WorkflowName:  enriched.WorkflowName,
		WorkflowLabel: enriched.WorkflowLabel,
		WorkspacePath: enriched.WorkspacePath,
		ExecutionName: enriched.CurrentExecutionName,
	})
	enrichedSnapshot = withTerminalPlanStepType(ctx, planTypes, enrichedSnapshot)
	return withTerminalRows(enrichedSnapshot.WithContext(terminals.Context{}))
}

func (api *StreamingAPI) terminalSnapshotForList(ctx context.Context, planTypes *terminalPlanTypeResolver, snapshot terminals.Snapshot, contentMode string) terminals.Snapshot {
	if isMetadataOnlyTerminalList(contentMode) {
		return compactTerminalSnapshotForList(api.enrichTerminalSnapshotMetadata(ctx, planTypes, snapshot), contentMode)
	}
	return compactTerminalSnapshotForList(api.enrichTerminalSnapshot(ctx, planTypes, snapshot), contentMode)
}

func (api *StreamingAPI) enrichTerminalSnapshotMetadata(ctx context.Context, planTypes *terminalPlanTypeResolver, snapshot terminals.Snapshot) terminals.Snapshot {
	active, exists := api.getActiveSession(snapshot.SessionID)
	if !exists || active == nil {
		enrichedSnapshot := snapshot.WithContext(terminals.Context{})
		enrichedSnapshot = withTerminalPlanStepType(ctx, planTypes, enrichedSnapshot)
		return enrichedSnapshot.WithContext(terminals.Context{})
	}
	enriched := api.buildActiveSessionInfoSummary(active)
	if enriched == nil {
		enrichedSnapshot := snapshot.WithContext(terminals.Context{})
		enrichedSnapshot = withTerminalPlanStepType(ctx, planTypes, enrichedSnapshot)
		return enrichedSnapshot.WithContext(terminals.Context{})
	}
	enrichedSnapshot := snapshot.WithContext(terminals.Context{
		WorkflowName:  enriched.WorkflowName,
		WorkflowLabel: enriched.WorkflowLabel,
		WorkspacePath: enriched.WorkspacePath,
		ExecutionName: enriched.CurrentExecutionName,
	})
	enrichedSnapshot = withTerminalPlanStepType(ctx, planTypes, enrichedSnapshot)
	return enrichedSnapshot.WithContext(terminals.Context{})
}

type terminalPlanTypeResolver struct {
	ctx        context.Context
	byWorkflow map[string]map[string]string
}

func newTerminalPlanTypeResolver(ctx context.Context) *terminalPlanTypeResolver {
	if ctx == nil {
		ctx = context.Background()
	}
	return &terminalPlanTypeResolver{
		ctx:        ctx,
		byWorkflow: make(map[string]map[string]string),
	}
}

func withTerminalPlanStepType(ctx context.Context, resolver *terminalPlanTypeResolver, snapshot terminals.Snapshot) terminals.Snapshot {
	if strings.TrimSpace(snapshot.StepType) != "" {
		return snapshot
	}
	stepID := strings.TrimSpace(snapshot.StepID)
	if stepID == "" || strings.HasPrefix(stepID, "main_agent:") {
		return snapshot
	}
	workflowPath := strings.Trim(strings.TrimSpace(snapshot.WorkflowPath), "/")
	if workflowPath == "" {
		return snapshot
	}
	if resolver == nil {
		resolver = newTerminalPlanTypeResolver(ctx)
	}
	if stepType := resolver.stepType(workflowPath, stepID); stepType != "" {
		snapshot.StepType = stepType
	}
	return snapshot
}

func (r *terminalPlanTypeResolver) stepType(workflowPath, stepID string) string {
	workflowPath = strings.Trim(strings.TrimSpace(workflowPath), "/")
	stepID = strings.TrimSpace(stepID)
	if workflowPath == "" || stepID == "" {
		return ""
	}
	if r.byWorkflow == nil {
		r.byWorkflow = make(map[string]map[string]string)
	}
	stepTypes, ok := r.byWorkflow[workflowPath]
	if !ok {
		stepTypes = loadTerminalPlanStepTypes(r.ctx, workflowPath)
		r.byWorkflow[workflowPath] = stepTypes
	}
	return stepTypes[stepID]
}

func loadTerminalPlanStepTypes(ctx context.Context, workflowPath string) map[string]string {
	stepTypes := make(map[string]string)
	content, exists, err := readFileFromWorkspace(ctx, strings.Trim(workflowPath, "/")+"/planning/plan.json")
	if err != nil || !exists || strings.TrimSpace(content) == "" {
		return stepTypes
	}
	var raw any
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return stepTypes
	}
	collectTerminalPlanStepTypes(raw, stepTypes)
	return stepTypes
}

func collectTerminalPlanStepTypes(value any, stepTypes map[string]string) {
	switch typed := value.(type) {
	case map[string]any:
		id, hasID := typed["id"].(string)
		stepType, hasType := typed["type"].(string)
		if hasID && hasType && isWorkflowPlanStepType(stepType) {
			if _, exists := stepTypes[id]; !exists {
				stepTypes[id] = stepType
			}
		}
		for _, child := range typed {
			collectTerminalPlanStepTypes(child, stepTypes)
		}
	case []any:
		for _, child := range typed {
			collectTerminalPlanStepTypes(child, stepTypes)
		}
	}
}

func isWorkflowPlanStepType(stepType string) bool {
	switch strings.TrimSpace(stepType) {
	case "regular", "human_input", "orchestrator", "todo_task", "routing", "message_sequence":
		return true
	default:
		return false
	}
}

func (api *StreamingAPI) canAccessTerminalSession(r *http.Request, sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false
	}

	currentUserID := GetUserIDFromContext(r.Context())
	activeSession, exists := api.getActiveSession(sessionID)
	if exists {
		if sessionVisibleTo(activeSession.UserID, GetUserFromContext(r.Context())) {
			return true
		}
		allowed := botSessionTerminalAllowed(r.Context(), activeSession)
		if !allowed && activeSession.BotPlatform != "" {
			log.Printf("[TERMINAL] bot session %s terminal denied for user=%s (needs Owner or Write on %s)", sessionID, currentUserID, activeSession.WorkspacePath)
		}
		return allowed
	}
	if api.eventStore == nil {
		return currentUserID == GetDefaultUserID()
	}
	owner := api.eventStore.GetSessionOwner(sessionID)
	if owner != "" {
		return owner == currentUserID
	}
	return currentUserID == GetDefaultUserID()
}

// botSessionTerminalAllowed lets the people who run a workflow open the
// terminal of its Slack/WhatsApp bot sessions. Those sessions are owned by the
// bot's own user, so the owner check alone left every human with a terminal
// that 404ed and flipped back to the conversation (RTS 2026-09-25). The
// terminal stream can type into the pane, so workflow readers (who may see the
// run in the monitor) do not get it: Owner or Write access is required.
func botSessionTerminalAllowed(ctx context.Context, session *ActiveSessionInfo) bool {
	if session == nil ||
		!isWorkflowBotHistory(ChatHistorySession{SessionID: session.SessionID, UserID: session.UserID, BotPlatform: session.BotPlatform}) ||
		!strings.HasPrefix(strings.TrimSpace(session.WorkspacePath), "Workflow/") {
		return false
	}
	claims := GetUserFromContext(ctx)
	access, manifest := workflowAccessForWorkspacePath(ctx, claims, session.WorkspacePath)
	if manifest == nil || manifest.ID != session.PresetQueryID || !userAllowedWorkflowID(claims, manifest.ID) {
		return false
	}
	return access == WorkflowAccessOwner || access == WorkflowAccessWrite
}

func (api *StreamingAPI) canUseSessionIDForQuery(r *http.Request, sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false
	}

	currentUserID := GetUserIDFromContext(r.Context())
	activeSession, exists := api.getActiveSession(sessionID)
	if exists {
		return sessionVisibleTo(activeSession.UserID, GetUserFromContext(r.Context()))
	}
	if api.eventStore != nil {
		if owner := api.eventStore.GetSessionOwner(sessionID); owner != "" {
			return owner == currentUserID
		}
	}
	return true
}

func compactTerminalSnapshotForList(snapshot terminals.Snapshot, contentMode string) terminals.Snapshot {
	switch contentMode {
	case "none", "metadata":
		snapshot.Content = ""
		snapshot.Rows = []terminals.Row{}
		snapshot.Status = compactTerminalStatusForList(snapshot.Status)
	case "full":
		snapshot = withTerminalRows(snapshot)
		return snapshot
	default:
		snapshot.Content = terminalContentTail(snapshot.Content, listTerminalContentMaxBytes)
		snapshot = withTerminalRows(snapshot)
	}
	return snapshot
}

func isMetadataOnlyTerminalList(contentMode string) bool {
	return contentMode == "none" || contentMode == "metadata"
}

func parseTerminalActiveOnly(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "active", "live":
		return true
	default:
		return false
	}
}

func terminalSnapshotIsActiveForList(snapshot terminals.Snapshot) bool {
	state := strings.ToLower(strings.TrimSpace(snapshot.State))
	if strings.TrimSpace(snapshot.TmuxSession) != "" {
		return state != "stale" && state != "failed"
	}
	if snapshot.Active {
		return true
	}
	switch state {
	case "running", "active", "in_progress", "paused", "waiting", "waiting_feedback", "idle":
		return true
	default:
		return false
	}
}

func compactTerminalStatusForList(status terminals.Status) terminals.Status {
	status.StatusText = truncateTerminalListString(status.StatusText, 240)
	status.AssistantPreview = truncateTerminalListString(status.AssistantPreview, 800)
	status.ToolSummary = truncateTerminalListString(status.ToolSummary, 240)
	status.ToolName = truncateTerminalListString(status.ToolName, 180)
	status.PreValidationSummary = truncateTerminalListString(status.PreValidationSummary, 800)
	status.StatusMeta = compactTerminalStatusMetaForList(status.StatusMeta)
	return status
}

func compactTerminalStatusMetaForList(meta map[string]interface{}) map[string]interface{} {
	if len(meta) == 0 {
		return nil
	}
	const maxEntries = 12
	out := make(map[string]interface{}, min(len(meta), maxEntries))
	// Plan-usage structures the composer renders (statusline chip + terminal
	// icon hover) are lists, which the scalar-only compaction below would drop.
	// Keep them — ahead of the entry cap — when they stay small.
	for _, key := range terminalListStatusMetaStructuredKeys {
		if value, ok := compactTerminalStatusMetaStructuredValueForList(meta[key]); ok {
			out[key] = value
		}
	}
	keys := make([]string, 0, len(meta))
	for key := range meta {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if len(out) >= maxEntries {
			break
		}
		if _, kept := out[key]; kept {
			continue
		}
		value, ok := compactTerminalStatusMetaValueForList(meta[key])
		if !ok {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// terminalListStatusMetaStructuredKeys are status_meta list values that
// survive metadata-only terminal lists: llmtypes.StatusExtrasMetaKey
// (display strings) and llmtypes.RateLimitWindowsMetaKey (structured windows).
var terminalListStatusMetaStructuredKeys = []string{"status_extras", "rate_limit_windows"}

const terminalListStatusMetaStructuredMaxBytes = 2048

func compactTerminalStatusMetaStructuredValueForList(value interface{}) (interface{}, bool) {
	if value == nil {
		return nil, false
	}
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) > terminalListStatusMetaStructuredMaxBytes || len(encoded) == 0 || encoded[0] != '[' {
		return nil, false
	}
	return json.RawMessage(encoded), true
}

func compactTerminalStatusMetaValueForList(value interface{}) (interface{}, bool) {
	switch typed := value.(type) {
	case nil:
		return nil, false
	case string:
		return truncateTerminalListString(typed, 160), true
	case bool:
		return typed, true
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return typed, true
	default:
		return nil, false
	}
}

func truncateTerminalListString(value string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	if utf8.RuneCountInString(value) <= maxRunes {
		return value
	}
	runes := []rune(value)
	return strings.TrimSpace(string(runes[:maxRunes])) + "..."
}

func withTerminalRows(snapshot terminals.Snapshot) terminals.Snapshot {
	if strings.TrimSpace(snapshot.Content) == "" {
		snapshot.Rows = []terminals.Row{}
		return snapshot
	}
	if strings.ToLower(strings.TrimSpace(snapshot.StepTransport)) == "tmux" {
		// Completed codex panes: strip the TUI chrome/repaint clutter from the
		// static capture while keeping ANSI colors, so xterm renders a clean,
		// colored final answer instead of the redraw-littered scrollback. Live
		// panes are left untouched (xterm replays them in real time); the cleanup
		// self-guards and falls back to the original content if it removes too much.
		if !snapshot.Active && terminals.SnapshotIsCodex(snapshot) {
			snapshot.Content = terminals.CleanCompletedCodexContent(snapshot.Content)
		}
		snapshot.Rows = []terminals.Row{}
		return snapshot
	}
	if len(snapshot.Rows) > 0 {
		snapshot.Status = terminals.StatusWithRows(snapshot.Status, snapshot.Rows)
		return snapshot
	}
	snapshot.Rows = terminals.ParseRows(snapshot.Content)
	if snapshot.Rows == nil {
		snapshot.Rows = []terminals.Row{}
	}
	snapshot.Status = terminals.StatusWithRows(snapshot.Status, snapshot.Rows)
	return snapshot
}

func terminalContentTail(content string, maxBytes int) string {
	if maxBytes <= 0 || len(content) <= maxBytes {
		return content
	}

	start := len(content) - maxBytes
	for start < len(content) && !utf8.RuneStart(content[start]) {
		start++
	}

	tail := content[start:]
	tail = trimLeadingPartialTerminalControl(tail)
	if newline := strings.IndexByte(tail, '\n'); newline >= 0 && newline < 4096 {
		tail = tail[newline+1:]
	}
	return "[terminal output truncated; showing latest output]\n" + tail
}

func trimLeadingPartialTerminalControl(content string) string {
	// A byte tail can start in the middle of an OSC title sequence:
	// ESC ] 0;title BEL. If the ESC ] prefix is cut off, xterm renders
	// "0;title" as visible text. Drop that orphaned fragment before replaying
	// the saved tail.
	if bel := strings.IndexByte(content, '\a'); bel >= 0 && bel < 1024 {
		if esc := strings.IndexByte(content[:bel], '\x1b'); esc < 0 {
			return content[bel+1:]
		}
	}
	return content
}
