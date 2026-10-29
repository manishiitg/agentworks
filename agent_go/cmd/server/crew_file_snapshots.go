package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/filesnapshots"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	unifiedevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/mcpagent/llm"
)

const crewSnapshotTimeout = 15 * time.Second

// The send waits at most crewSnapshotBeforeBudget for the before-capture and
// never longer (PLAT-746-163: a 15 s cold capture on the send path made every
// first message after a restart fail to deliver). A capture still running then
// goes on beside the turn, up to crewSnapshotBeforeLimit, and is the reply's
// baseline only if it finished before the turn's first tool started (checked
// at Finish); otherwise the reply has no comparison, never a later baseline
// (PLAT-746-158). A Crew with no private index yet is not captured on send and
// builds its index in the background instead.
const crewSnapshotBeforeBudget = 2 * time.Second
const crewSnapshotBeforeLimit = time.Minute
const crewSnapshotWarmLimit = 2 * time.Minute

// Tool-start signals a provider path reports before (or as) the tool runs. A
// path with neither keeps the old rule: only a capture that finished within
// the send budget is a baseline.
const (
	// Claude Code in tmux: the transcript's tool_use record carries Claude's own
	// time, written before the tool runs.
	crewToolTimingProviderRecord = "provider_tool_use"
	// Claude SDK stream-json tool_use (before the tool runs), Codex app-server
	// item/started and Cursor ACP tool_call in_progress (as the tool starts):
	// the server's receipt time, within lateMargin of the real start.
	crewToolTimingStream = "stream_tool_start"
)

func crewSnapshotToolTiming(provider string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	contract, ok := llm.GetCodingAgentProviderContract(llm.Provider(provider), "")
	if !ok {
		return ""
	}
	is := func(p llm.Provider) bool { return provider == strings.ToLower(string(p)) }
	switch {
	case is(llm.ProviderClaudeCode) && contract.Transport == llm.CodingAgentTransportTmux:
		return crewToolTimingProviderRecord
	case is(llm.ProviderClaudeCode) && contract.Transport == llm.CodingAgentTransportSDK,
		is(llm.ProviderCodexCLI) && contract.Transport == llm.CodingAgentTransportAppServer,
		is(llm.ProviderCursorCLI) && contract.Transport == llm.CodingAgentTransportACP:
		return crewToolTimingStream
	}
	return ""
}

// PLAT-746-158: file review belongs to Studio. Read the server-owned project
// metadata before touching the snapshot store; browser mode is not the switch.
func crewSnapshotsEnabled(roots crewroot.Roots) bool {
	raw, err := os.ReadFile(filepath.Join(roots.Private, "product.json"))
	if err != nil {
		return false
	}
	var project struct {
		Mode string `json:"mode"`
	}
	if json.Unmarshal(raw, &project) != nil {
		return false
	}
	// An unspecified project mode is Studio throughout the existing product.
	mode := strings.TrimSpace(project.Mode)
	return mode == "" || mode == "dev"
}

type crewTurnSnapshot struct {
	store                        filesnapshots.Store
	root, executionID, sessionID string
	startSequence                int64
	completedSequence            atomic.Int64
	watching                     atomic.Bool
	outputDone                   atomic.Bool
	finish                       sync.Once
	// The before-capture runs beside the send; record and beforeErr are set
	// before beforeDone closes. background: it outlived the send budget.
	sentAt     time.Time
	toolTiming string
	beforeDone chan struct{}
	record     filesnapshots.Record
	beforeErr  error
	background atomic.Bool
	// Earliest tool signal of any kind, and earliest one this provider path
	// reports before or as the tool runs (Unix ns, 0 = none).
	firstTool, firstTrustedTool atomic.Int64
}

// prepareCrewTurnSnapshot must run before provider delivery, outside native
// input locks. Steering does not call this: it belongs to the running turn.
// It holds the send for at most crewSnapshotBeforeBudget.
func (api *StreamingAPI) prepareCrewTurnSnapshot(executionID, sessionID string, req QueryRequest) {
	if api == nil || executionID == "" || sessionID == "" {
		return
	}
	if _, exists := api.crewTurnSnapshots.Load(executionID); exists {
		return
	}
	folder := strings.TrimSpace(req.SelectedFolder)
	provider := strings.TrimSpace(req.Provider)
	api.lastQueryMu.RLock()
	last := api.lastQueryRequests[sessionID]
	api.lastQueryMu.RUnlock()
	if folder == "" {
		folder = last.SelectedFolder
	}
	if provider == "" {
		provider = last.Provider
	}
	if provider == "" {
		provider = api.retainedCLIProvider(sessionID)
	}
	if folder == "" {
		// After a restart the last query is gone; the session's recorded
		// project (rehydrated from the chat journal) still names the Crew.
		folder = api.projectRootForSession(sessionID)
	}
	if folder == "" {
		log.Printf("[FILE_SNAPSHOT] phase=skipped session=%s execution=%s reason=no_folder", sessionID, executionID)
		return
	}
	roots, found, err := crewStorageRoots(folder)
	if err != nil || !found {
		return
	}
	if !crewSnapshotsEnabled(roots) {
		log.Printf("[FILE_SNAPSHOT] phase=skipped session=%s execution=%s reason=studio_only", sessionID, executionID)
		return
	}
	snapshot := &crewTurnSnapshot{store: filesnapshots.Store{Root: roots.Files, Private: roots.Private}, root: roots.Virtual, executionID: executionID, sessionID: sessionID, startSequence: api.crewSnapshotSequence(sessionID),
		sentAt: time.Now(), toolTiming: crewSnapshotToolTiming(provider), beforeDone: make(chan struct{})}
	if _, loaded := api.crewTurnSnapshots.LoadOrStore(executionID, snapshot); loaded {
		return
	}
	go func() {
		defer close(snapshot.beforeDone)
		snapshot.record, snapshot.beforeErr = snapshot.store.BeginWithin(crewSnapshotBeforeLimit, executionID)
	}()
	budget := time.NewTimer(crewSnapshotBeforeBudget)
	defer budget.Stop()
	select {
	case <-snapshot.beforeDone:
		api.logCrewBeforeCapture(snapshot)
	case <-budget.C:
		snapshot.background.Store(true)
		log.Printf("[FILE_SNAPSHOT] phase=before session=%s execution=%s pending=background budget_ms=%d tool_timing=%s", sessionID, executionID, crewSnapshotBeforeBudget.Milliseconds(), firstNonEmptyString(snapshot.toolTiming, "none"))
		go func() {
			<-snapshot.beforeDone
			api.logCrewBeforeCapture(snapshot)
		}()
	}
}

func (api *StreamingAPI) logCrewBeforeCapture(snapshot *crewTurnSnapshot) {
	record, err := snapshot.record, snapshot.beforeErr
	if errors.Is(err, filesnapshots.ErrWarming) {
		log.Printf("[FILE_SNAPSHOT] phase=before session=%s execution=%s skipped=warming reason=cache_warmup_running", snapshot.sessionID, snapshot.executionID)
		return
	}
	log.Printf("[FILE_SNAPSHOT] phase=before session=%s execution=%s snapshot=%s status=%s elapsed_ms=%d background=%t success=%t error=%v capture=%+v", snapshot.sessionID, snapshot.executionID, record.ID, record.Status, time.Since(snapshot.sentAt).Milliseconds(), snapshot.background.Load(), err == nil, err, record.BeforeCapture)
	if reason := record.BeforeCapture.Deferred; reason != "" {
		log.Printf("[FILE_SNAPSHOT] phase=before session=%s execution=%s deferred=background reason=%s baseline=none", snapshot.sessionID, snapshot.executionID, reason)
		go warmCrewSnapshotCache(snapshot.store, snapshot.sessionID, snapshot.executionID)
	}
}

// observeCrewSnapshotTool records the earliest tool signal of each running
// turn's chat, from every stored event (all provider paths). The earlier of
// the provider's own time and the server's receipt time is used.
func (api *StreamingAPI) observeCrewSnapshotTool(sessionID string, event storeevents.Event) {
	at, timing, ok := crewToolSignal(event)
	if !ok {
		return
	}
	api.crewTurnSnapshots.Range(func(_, value any) bool {
		snapshot := value.(*crewTurnSnapshot)
		if snapshot.sessionID != sessionID || at.Before(snapshot.sentAt) {
			return true
		}
		storeEarliest(&snapshot.firstTool, at)
		if timing != "" && timing == snapshot.toolTiming {
			storeEarliest(&snapshot.firstTrustedTool, at)
		}
		return true
	})
}

// crewToolSignal returns a tool event's earliest time and, when the event is
// a start reported before or as the tool runs, which provider path's timing it is.
func crewToolSignal(event storeevents.Event) (time.Time, string, bool) {
	if event.Data == nil {
		return time.Time{}, "", false
	}
	earliest := func(times ...time.Time) time.Time {
		var out time.Time
		for _, t := range times {
			if !t.IsZero() && (out.IsZero() || t.Before(out)) {
				out = t
			}
		}
		return out
	}
	switch data := event.Data.Data.(type) {
	case *unifiedevents.ToolCallStartEvent:
		timing := crewToolTimingStream
		if data.ServerName == "native" { // read back from the transcript after the fact
			timing = ""
		}
		return earliest(event.Timestamp, data.Timestamp), timing, true
	case *unifiedevents.ToolCallEndEvent:
		return earliest(event.Timestamp, data.Timestamp), "", true
	case *unifiedevents.ToolCallErrorEvent:
		return earliest(event.Timestamp, data.Timestamp), "", true
	case *unifiedevents.CodingAgentNativeActivityEvent:
		record := data.Record
		switch record.Kind {
		case "tool", "file", "helper", "background_command":
		default:
			return time.Time{}, "", false
		}
		timing := ""
		if record.Kind == "tool" && record.State == "started" && !record.Timestamp.IsZero() {
			timing = crewToolTimingProviderRecord
		}
		return earliest(event.Timestamp, record.Timestamp), timing, true
	}
	return time.Time{}, "", false
}

func storeEarliest(value *atomic.Int64, at time.Time) {
	n := at.UnixNano()
	for {
		current := value.Load()
		if current != 0 && current <= n || value.CompareAndSwap(current, n) {
			return
		}
	}
}

// beforeBoundary is the time the before-capture must have finished by to be
// this reply's baseline (zero: no check, the capture finished within the send
// budget, before delivery). Without a pre-execution tool signal for this
// provider path, or with a tool seen but no such signal, the old rule holds:
// a capture that outlived the send is not a baseline.
func (snapshot *crewTurnSnapshot) beforeBoundary() (time.Time, string) {
	if !snapshot.background.Load() {
		return time.Time{}, ""
	}
	first, trusted := snapshot.firstTool.Load(), snapshot.firstTrustedTool.Load()
	switch {
	case snapshot.toolTiming == "":
		return snapshot.sentAt, "no_tool_timing"
	case first == 0:
		return time.Time{}, "no_tool"
	case trusted == 0:
		return snapshot.sentAt, "no_trusted_tool_start"
	}
	return time.Unix(0, min(first, trusted)), "first_tool"
}

// warmCrewSnapshotCache builds a Crew's persisted private index off the send
// path. Its own context, not the request's; one per Crew (the store refuses a
// second).
func warmCrewSnapshotCache(store filesnapshots.Store, sessionID, executionID string) {
	started := time.Now()
	stats, err := store.Warm(crewSnapshotWarmLimit)
	if errors.Is(err, filesnapshots.ErrWarming) {
		return
	}
	log.Printf("[FILE_SNAPSHOT] phase=warm session=%s execution=%s elapsed_ms=%d success=%t error=%v capture=%+v", sessionID, executionID, time.Since(started).Milliseconds(), err == nil, err, stats)
}
func (api *StreamingAPI) startCrewTurnSnapshot(executionID, sessionID string, req QueryRequest) {
	api.prepareCrewTurnSnapshot(executionID, sessionID, req)
	api.watchCrewTurnSnapshot(executionID)
}
func (api *StreamingAPI) watchCrewTurnSnapshot(executionID string) {
	value, ok := api.crewTurnSnapshots.Load(executionID)
	if !ok {
		return
	}
	snapshot := value.(*crewTurnSnapshot)
	if !snapshot.watching.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer api.crewTurnSnapshots.Delete(executionID)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		deadline := time.NewTimer(24 * time.Hour)
		defer deadline.Stop()
		for {
			if api.crewSnapshotReady(snapshot) {
				api.finishCrewTurnSnapshot(snapshot)
				return
			}
			select {
			case <-ticker.C:
			case <-deadline.C:
				<-snapshot.beforeDone
				snapshot.store.Abandon(executionID)
				return
			}
		}
	}()
}

// Completion is observed after the response is published. This does not hold
// provider input locks or change native turn/child scheduling.
// A turn whose reply stream ended cleanly is ready once no child work runs:
// the after-capture does not wait for the transcript save that follows
// (seconds on a long chat), which the capture excludes anyway (builder/).
func (api *StreamingAPI) crewSnapshotReady(snapshot *crewTurnSnapshot) bool {
	if api.retainedMainTurnTracked(snapshot.sessionID) {
		return false
	}
	tree := api.conversationTurnTreeSnapshot(snapshot.executionID)
	return tree.terminal() || snapshot.outputDone.Load() && tree.RootFound && tree.RunningChildren == 0
}

// markCrewTurnOutputDone runs when the main reply stream closes without
// cancellation. A turn still answering live input keeps the ordinary
// completion path. The reply boundary is bound when the capture publishes.
func (api *StreamingAPI) markCrewTurnOutputDone(executionID string) {
	value, ok := api.crewTurnSnapshots.Load(executionID)
	if !ok {
		return
	}
	snapshot := value.(*crewTurnSnapshot)
	if !api.retainedMainTurnTracked(snapshot.sessionID) {
		snapshot.outputDone.Store(true)
	}
}
func (api *StreamingAPI) finishCrewTurnSnapshot(snapshot *crewTurnSnapshot) {
	snapshot.finish.Do(func() {
		defer api.crewTurnSnapshots.Delete(snapshot.executionID)
		// The send never waited for a slow before-capture; the after-capture does.
		<-snapshot.beforeDone
		roots, found, err := crewStorageRoots(snapshot.root)
		if err != nil || !found || roots.Files != snapshot.store.Root || roots.Private != snapshot.store.Private {
			return
		}
		if errors.Is(snapshot.beforeErr, filesnapshots.ErrWarming) {
			api.publishCrewSnapshotUnavailable(snapshot, "setting_up")
			return
		}
		if snapshot.record.ID == "" {
			return
		}
		boundary, why := snapshot.beforeBoundary()
		ctx, cancel := context.WithTimeout(context.Background(), crewSnapshotTimeout)
		defer cancel()
		started := time.Now()
		record, err := snapshot.store.FinishAfter(ctx, snapshot.executionID, boundary)
		if snapshot.background.Load() {
			completed, firstTool := "none", "none"
			if record.BeforeCompletedAt != nil {
				completed = strconv.FormatInt(record.BeforeCompletedAt.Sub(snapshot.sentAt).Milliseconds(), 10)
			}
			if !boundary.IsZero() {
				firstTool = strconv.FormatInt(boundary.Sub(snapshot.sentAt).Milliseconds(), 10)
			}
			reason := firstNonEmptyString(record.Reason, record.BeforeCapture.Deferred, why)
			log.Printf("[FILE_SNAPSHOT] phase=before session=%s execution=%s snapshot=%s completed_ms=%s first_tool_ms=%s tool_timing=%s valid=%t reason=%s", snapshot.sessionID, snapshot.executionID, record.ID, completed, firstTool, firstNonEmptyString(snapshot.toolTiming, "none"), record.Status == "complete" || record.Status == "partial", reason)
		}
		log.Printf("[FILE_SNAPSHOT] phase=after session=%s execution=%s snapshot=%s status=%s output_done=%t elapsed_ms=%d success=%t error=%v capture=%+v", snapshot.sessionID, snapshot.executionID, record.ID, record.Status, snapshot.outputDone.Load(), time.Since(started).Milliseconds(), err == nil, err, record.AfterCapture)
		switch {
		case err == nil && (record.Status == "complete" || record.Status == "partial"):
			api.publishCrewSnapshot(snapshot, roots, record)
		case record.BeforeCapture.Deferred == "cold":
			api.publishCrewSnapshotUnavailable(snapshot, "setting_up")
		case record.BeforeCapture.Deferred == "budget" || record.Reason == "capture_late":
			api.publishCrewSnapshotUnavailable(snapshot, "busy")
		}
	})
}

// Capture the reply boundary before the next turn is released. After-capture
// can finish later; neither event arrival nor a clock identifies its reply.
func (api *StreamingAPI) crewSnapshotSequence(sessionID string) int64 {
	if api.eventStore == nil {
		return 0
	}
	events := api.eventStore.LastEventsRaw(sessionID, 1)
	if len(events) == 0 {
		return 0
	}
	return events[0].Sequence
}
func (api *StreamingAPI) bindCrewSnapshotCompletion(executionID string, sequence int64) {
	if value, ok := api.crewTurnSnapshots.Load(executionID); ok {
		snapshot := value.(*crewTurnSnapshot)
		if sequence == 0 {
			sequence = api.crewSnapshotSequence(snapshot.sessionID)
		}
		snapshot.completedSequence.CompareAndSwap(0, sequence)
	}
}

// replyFileSummary is the card's header and file list, computed once when the
// after-capture finishes so the card renders from the event with no request.
// It uses the same path rules as the compare op (hidden and caller-private
// paths are never listed); the PR lookup stays a lazy request.
func replyFileSummary(ctx context.Context, store filesnapshots.Store, roots crewroot.Roots, id string) (map[string]any, bool) {
	prefix := ""
	if roots.Local {
		prefix = crewroot.FilesSegment + "/"
	}
	allow := func(path string) bool {
		_, ok := confineSharedProjectPath(roots.Virtual, prefix+path)
		return ok && !crewCallerPrivatePath(prefix+path)
	}
	comparison, err := store.Compare(ctx, id, "", allow)
	if err != nil {
		return nil, false
	}
	totals := reviewSummary(comparison)
	groups := filesnapshots.GroupChanges(ctx, roots.Files, comparison.Changes, allow)
	for i := range groups {
		if groups[i].Root != "" {
			groups[i].Root = prefix + groups[i].Root
		}
		for j := range groups[i].Files {
			groups[i].Files[j].Path = prefix + groups[i].Files[j].Path
		}
	}
	// The actions every viewer gets (Review changes), so the card draws them
	// before its own request returns this viewer's list.
	summary := map[string]any{"files": totals.Files, "added": totals.Added, "removed": totals.Removed, "groups": groups, "actions": filesnapshots.CardActions(groups, false, nil)}
	// Stay well inside one durable event row; a huge reply falls back to the
	// card's request.
	if encoded, err := json.Marshal(summary); err != nil || len(encoded) > 24<<10 {
		return nil, false
	}
	return summary, true
}

// crewTurnLastSequence returns the newest stored event after the boundary
// that still belongs to the same turn (before the next user message).
func (api *StreamingAPI) crewTurnLastSequence(sessionID string, boundary int64) int64 {
	last := boundary
	for _, event := range api.eventStore.LastEventsRaw(sessionID, 200) {
		if event.Sequence <= boundary {
			continue
		}
		if event.Type == "user_message" {
			break
		}
		if event.Type != "reply_file_changes" && event.Sequence > last {
			last = event.Sequence
		}
	}
	return last
}

// crewSnapshotReplyEnd binds the reply's last event; false when the turn
// stored no reply after its start.
func (api *StreamingAPI) crewSnapshotReplyEnd(snapshot *crewTurnSnapshot) (int64, bool) {
	if api.eventStore == nil {
		return 0, false
	}
	if snapshot.outputDone.Load() {
		// The reply's final events were stored before its stream closed.
		snapshot.completedSequence.CompareAndSwap(0, api.crewSnapshotSequence(snapshot.sessionID))
	}
	end := snapshot.completedSequence.Load()
	return end, end > snapshot.startSequence
}

// crewSnapshotUnavailableText is the reply's one line when its file changes
// were not saved. Sent only when a tool ran in the turn: a reply with no tool
// cannot have changed files.
var crewSnapshotUnavailableText = map[string]string{
	"setting_up": "File changes for this reply weren't saved (setting up file tracking for this Crew).",
	"busy":       "File changes for this reply weren't saved (the project was too busy to snapshot in time).",
}

func (api *StreamingAPI) publishCrewSnapshotUnavailable(snapshot *crewTurnSnapshot, reason string) {
	if snapshot.firstTool.Load() == 0 {
		log.Printf("[FILE_SNAPSHOT] phase=reply_reference session=%s execution=%s skipped=unavailable_no_tool reason=%s", snapshot.sessionID, snapshot.executionID, reason)
		return
	}
	end, ok := api.crewSnapshotReplyEnd(snapshot)
	if !ok {
		return
	}
	data := storeevents.NewGenericEventData("reply_file_changes", map[string]interface{}{
		"status": "unavailable", "reason": reason, "message": crewSnapshotUnavailableText[reason], "workspace_path": snapshot.root,
		"execution_id": snapshot.executionID, "start_sequence": snapshot.startSequence, "end_sequence": end,
	})
	event := unifiedevents.NewAgentEvent(data)
	event.SessionID = snapshot.sessionID
	api.eventStore.AddEvent(snapshot.sessionID, storeevents.Event{
		ID: "file-snapshot:" + filesnapshots.ID(snapshot.executionID), Type: "reply_file_changes", Timestamp: time.Now(), SessionID: snapshot.sessionID,
		ExecutionID: snapshot.executionID, ExecutionKind: "main_agent", TerminalOwnerID: "main:" + snapshot.sessionID, Data: event,
	})
	log.Printf("[FILE_SNAPSHOT] phase=reply_reference session=%s execution=%s status=unavailable reason=%s start_sequence=%d end_sequence=%d", snapshot.sessionID, snapshot.executionID, reason, snapshot.startSequence, end)
}

func (api *StreamingAPI) publishCrewSnapshot(snapshot *crewTurnSnapshot, roots crewroot.Roots, record filesnapshots.Record) {
	end, ok := api.crewSnapshotReplyEnd(snapshot)
	if !ok {
		return
	}
	if late := api.crewTurnLastSequence(snapshot.sessionID, end); late > end {
		// Log only: this turn stored events after its bound reply boundary.
		log.Printf("[FILE_SNAPSHOT] phase=publish session=%s execution=%s snapshot=%s boundary_late end_sequence=%d turn_last_sequence=%d output_done=%t", snapshot.sessionID, snapshot.executionID, record.ID, end, late, snapshot.outputDone.Load())
	}
	checkCtx, cancelCheck := context.WithTimeout(context.Background(), 5*time.Second)
	unchanged := snapshot.store.Unchanged(checkCtx, record)
	cancelCheck()
	if unchanged {
		log.Printf("[FILE_SNAPSHOT] phase=reply_reference session=%s execution=%s snapshot=%s skipped=no_changes", snapshot.sessionID, snapshot.executionID, record.ID)
		return
	}
	api.trackedWorkflowExecutionsMux.Lock()
	if execution := api.trackedWorkflowExecutions[snapshot.executionID]; execution != nil {
		if execution.Metadata == nil {
			execution.Metadata = map[string]string{}
		}
		execution.Metadata["file_snapshot_id"] = record.ID
		execution.Metadata["file_snapshot_end_sequence"] = strconv.FormatInt(end, 10)
	}
	api.trackedWorkflowExecutionsMux.Unlock()
	fields := map[string]interface{}{
		"snapshot_id": record.ID, "workspace_path": snapshot.root,
		"execution_id": snapshot.executionID, "start_sequence": snapshot.startSequence, "end_sequence": end,
	}
	summaryStarted := time.Now()
	summaryCtx, cancelSummary := context.WithTimeout(context.Background(), 5*time.Second)
	summary, ok := replyFileSummary(summaryCtx, snapshot.store, roots, record.ID)
	cancelSummary()
	if ok {
		fields["summary"] = summary
	}
	data := storeevents.NewGenericEventData("reply_file_changes", fields)
	event := unifiedevents.NewAgentEvent(data)
	event.SessionID = snapshot.sessionID
	api.eventStore.AddEvent(snapshot.sessionID, storeevents.Event{
		ID: "file-snapshot:" + record.ID, Type: "reply_file_changes", Timestamp: time.Now(), SessionID: snapshot.sessionID,
		ExecutionID: snapshot.executionID, ExecutionKind: "main_agent", TerminalOwnerID: "main:" + snapshot.sessionID, Data: event,
	})
	log.Printf("[FILE_SNAPSHOT] phase=reply_reference session=%s execution=%s snapshot=%s start_sequence=%d end_sequence=%d summary=%t summary_ms=%d", snapshot.sessionID, snapshot.executionID, record.ID, snapshot.startSequence, end, ok, time.Since(summaryStarted).Milliseconds())
}

func (api *StreamingAPI) discardUnstartedCrewSnapshot(executionID string) {
	if value, ok := api.crewTurnSnapshots.Load(executionID); ok {
		snapshot := value.(*crewTurnSnapshot)
		if !snapshot.watching.Load() {
			go func() {
				<-snapshot.beforeDone
				snapshot.store.Abandon(executionID)
			}()
			api.crewTurnSnapshots.Delete(executionID)
		}
	}
}

// Both HTTP and MCP use this reader after resolving the caller's Crew access.
// Current file guards apply to historical names and bytes as well.
func (api *StreamingAPI) serveCrewFileSnapshots(w http.ResponseWriter, r *http.Request, root, op, id, file string) {
	roots, found, err := crewStorageRoots(root)
	if err != nil || !found {
		externalError(w, 404, "not_found", "File history is unavailable for this Crew.")
		return
	}
	if claims := GetUserFromContext(r.Context()); claims != nil && claims.AccessToken != nil {
		if !claims.AccessToken.Allows("crews:read") || !claims.AccessToken.AllowsCrew(roots.ID) {
			externalError(w, 404, "not_found", "Crew not found or not allowed for this connection.")
			return
		}
	}
	if op == "snapshot_review" && !crewSnapshotsEnabled(roots) {
		externalError(w, 400, "studio_only", "Review file changes in Studio.")
		return
	}
	store := filesnapshots.Store{Root: roots.Files, Private: roots.Private}
	guard := externalFileGuard(r.Context(), false)
	if err := guard.Validate(); err != nil {
		externalError(w, 403, "forbidden", "This connection cannot read these files.")
		return
	}
	prefix := ""
	if roots.Local {
		prefix = crewroot.FilesSegment + "/"
	}
	allow := func(path string) bool {
		if !guard.Allows(prefix+path, false) {
			return false
		}
		_, ok := confineSharedProjectPath(roots.Virtual, prefix+path)
		return ok && !crewCallerPrivatePath(prefix+path)
	}
	if op == "snapshot_undo_preview" || op == "snapshot_undo" {
		allowWrite := func(path string) bool {
			if !guard.Allows(prefix+path, true) {
				return false
			}
			_, ok := confineSharedProjectPath(roots.Virtual, prefix+path)
			return ok && !crewCallerPrivatePath(prefix+path)
		}
		api.serveCrewReplyUndo(w, r, roots.Virtual, prefix, op, id, store, allow, allowWrite)
		return
	}
	if r.URL.Query().Get("session_id") != "" {
		api.serveChatFileReview(w, r, roots, prefix, op, id, file, store, allow)
		return
	}
	if op == "snapshots" {
		records, err := store.List()
		if err != nil {
			externalError(w, 500, "snapshot_unavailable", "File history could not be read. Try again.")
			return
		}
		for i := range records {
			if records[i].Status == "capturing" {
				if _, active := api.crewTurnSnapshots.Load(records[i].ExecutionID); !active {
					records[i].Status = "unavailable"
				}
			}
			records[i].Skipped = nil
			records[i].ExecutionID = ""
		}
		externalJSON(w, map[string]any{"snapshots": records, "note": "File comparisons cover changes observed during a turn. Other chats or editors may contribute. Undo is not available.", "limits": map[string]any{"max_file_bytes": filesnapshots.MaxFileBytes, "retained_turns": 50, "retained_days": 14}})
		return
	}
	if op != "snapshot_compare" && op != "snapshot_file" && op != "snapshot_groups" {
		externalError(w, 400, "invalid_action", "Choose snapshots, snapshot_compare, snapshot_groups or snapshot_file.")
		return
	}
	if prefix != "" && file != "" {
		if !strings.HasPrefix(file, prefix) {
			externalError(w, 404, "not_found", "Use a file path returned by the comparison.")
			return
		}
		file = strings.TrimPrefix(file, prefix)
	}
	ctx, cancel := context.WithTimeout(r.Context(), crewSnapshotTimeout)
	defer cancel()
	if op == "snapshot_file" {
		content, err := store.ReadFile(ctx, id, file, r.URL.Query().Get("side"), allow)
		if err != nil {
			externalError(w, 404, "snapshot_unavailable", "This saved file is unavailable or you cannot read it.")
			return
		}
		writeSavedSnapshotFile(w, r, prefix+file, content)
		return
	}
	comparison, err := store.Compare(ctx, id, file, allow)
	if err != nil {
		externalError(w, 404, "snapshot_unavailable", "This file comparison is not available. List snapshots and choose a completed turn.")
		return
	}
	comparison.Record.ExecutionID = ""
	if op == "snapshot_groups" {
		externalJSON(w, map[string]any{"snapshot_id": id, "groups": replyFileGroups(ctx, roots, prefix, comparison, allow, true)})
		return
	}
	for i := range comparison.Changes {
		comparison.Changes[i].Path = prefix + comparison.Changes[i].Path
	}
	for i := range comparison.Record.Skipped {
		comparison.Record.Skipped[i] = prefix + comparison.Record.Skipped[i]
	}
	externalJSON(w, comparison)
}

func (api *StreamingAPI) handleCrewFileSnapshots(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	root, ok := workspaceGitCanonicalPath(r, requestWorkflowWorkspacePath(r))
	if !ok {
		externalError(w, 400, "invalid_path", "Choose a Crew to see its file history.")
		return
	}
	// The route has the same file-read access gate as the existing Files panel.
	q := r.URL.Query()
	op := q.Get("op")
	if op == "" {
		op = "snapshots"
	}
	api.serveCrewFileSnapshots(w, r, root, op, q.Get("snapshot_id"), q.Get("file"))
}

func writeSavedSnapshotFile(w http.ResponseWriter, r *http.Request, file string, content []byte) {
	if r.URL.Query().Get("format") == "json" {
		out := map[string]any{"path": file, "side": r.URL.Query().Get("side"), "size": len(content)}
		binary := !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0
		limit := 2 << 20
		if binary {
			limit = 512 << 10
		}
		if len(content) > limit {
			out["content_omitted"] = true
			out["note"] = "This saved file is too large for a chat response. Open its saved version in the file-change card."
		} else if binary {
			out["content_base64"] = base64.StdEncoding.EncodeToString(content)
		} else {
			out["content"] = string(content)
		}
		externalJSON(w, out)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(file)}))
	w.Header().Set("Cache-Control", "no-store")
	w.Write(content)
	return
}
