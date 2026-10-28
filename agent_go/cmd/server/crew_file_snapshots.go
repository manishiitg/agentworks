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
)

const crewSnapshotTimeout = 15 * time.Second

// The before-capture runs on the message send path: gate wait plus capture
// never exceed this. A Crew with no private index yet, or a capture that runs
// out of time, leaves that reply without a snapshot (never a later baseline)
// and builds the Crew's private index in the background instead (PLAT-746-163:
// a 15 s cold capture on the send path made every first message after a
// restart fail to deliver).
const crewSnapshotBeforeBudget = 2 * time.Second
const crewSnapshotWarmLimit = 2 * time.Minute

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
}

// prepareCrewTurnSnapshot must run before provider delivery, outside native
// input locks. Steering does not call this: it belongs to the running turn.
func (api *StreamingAPI) prepareCrewTurnSnapshot(executionID, sessionID string, req QueryRequest) {
	if api == nil || executionID == "" || sessionID == "" {
		return
	}
	if _, exists := api.crewTurnSnapshots.Load(executionID); exists {
		return
	}
	folder := strings.TrimSpace(req.SelectedFolder)
	if folder == "" {
		api.lastQueryMu.RLock()
		folder = api.lastQueryRequests[sessionID].SelectedFolder
		api.lastQueryMu.RUnlock()
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
	snapshot := &crewTurnSnapshot{store: filesnapshots.Store{Root: roots.Files, Private: roots.Private}, root: roots.Virtual, executionID: executionID, sessionID: sessionID, startSequence: api.crewSnapshotSequence(sessionID)}
	if _, loaded := api.crewTurnSnapshots.LoadOrStore(executionID, snapshot); loaded {
		return
	}
	started := time.Now()
	record, err := snapshot.store.BeginWithin(crewSnapshotBeforeBudget, executionID)
	if errors.Is(err, filesnapshots.ErrWarming) {
		log.Printf("[FILE_SNAPSHOT] phase=before session=%s execution=%s skipped=warming reason=cache_warmup_running", sessionID, executionID)
		api.crewTurnSnapshots.Delete(executionID)
		return
	}
	log.Printf("[FILE_SNAPSHOT] phase=before session=%s execution=%s snapshot=%s status=%s elapsed_ms=%d success=%t error=%v capture=%+v", sessionID, executionID, record.ID, record.Status, time.Since(started).Milliseconds(), err == nil, err, record.BeforeCapture)
	if err != nil && record.ID == "" {
		api.crewTurnSnapshots.Delete(executionID)
	}
	if reason := record.BeforeCapture.Deferred; reason != "" {
		log.Printf("[FILE_SNAPSHOT] phase=before session=%s execution=%s deferred=background reason=%s baseline=none", sessionID, executionID, reason)
		go warmCrewSnapshotCache(snapshot.store, sessionID, executionID)
	}
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
		roots, found, err := crewStorageRoots(snapshot.root)
		if err != nil || !found || roots.Files != snapshot.store.Root || roots.Private != snapshot.store.Private {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), crewSnapshotTimeout)
		defer cancel()
		started := time.Now()
		record, err := snapshot.store.Finish(ctx, snapshot.executionID)
		log.Printf("[FILE_SNAPSHOT] phase=after session=%s execution=%s snapshot=%s status=%s output_done=%t elapsed_ms=%d success=%t error=%v capture=%+v", snapshot.sessionID, snapshot.executionID, record.ID, record.Status, snapshot.outputDone.Load(), time.Since(started).Milliseconds(), err == nil, err, record.AfterCapture)
		if err == nil && (record.Status == "complete" || record.Status == "partial") {
			api.publishCrewSnapshot(snapshot, roots, record)
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

func (api *StreamingAPI) publishCrewSnapshot(snapshot *crewTurnSnapshot, roots crewroot.Roots, record filesnapshots.Record) {
	if snapshot.outputDone.Load() && api.eventStore != nil {
		// The reply's final events were stored before its stream closed.
		snapshot.completedSequence.CompareAndSwap(0, api.crewSnapshotSequence(snapshot.sessionID))
	}
	end := snapshot.completedSequence.Load()
	if api.eventStore == nil || end <= snapshot.startSequence {
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
			go snapshot.store.Abandon(executionID)
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
