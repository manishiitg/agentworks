package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
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

// The before-capture of a warm Crew reuses cached file hashes and must not delay a
// send; a cold one (after a restart or the first capture) reads every file and
// needs the full budget (PLAT-746-163). Waiting for the Crew gate is separate.
const crewSnapshotBeforeWarm = 2 * time.Second
const crewSnapshotLockWait = 10 * time.Second

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
	record, err := snapshot.store.BeginWithin(crewSnapshotLockWait, crewSnapshotBeforeWarm, crewSnapshotTimeout, executionID)
	log.Printf("[FILE_SNAPSHOT] phase=before session=%s execution=%s snapshot=%s status=%s elapsed_ms=%d success=%t error=%v capture=%+v", sessionID, executionID, record.ID, record.Status, time.Since(started).Milliseconds(), err == nil, err, record.BeforeCapture)
	if err != nil && record.ID == "" {
		api.crewTurnSnapshots.Delete(executionID)
	}
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
func (api *StreamingAPI) crewSnapshotReady(snapshot *crewTurnSnapshot) bool {
	return api.conversationTurnTreeSnapshot(snapshot.executionID).terminal() && !api.retainedMainTurnTracked(snapshot.sessionID)
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
		log.Printf("[FILE_SNAPSHOT] phase=after session=%s execution=%s snapshot=%s status=%s elapsed_ms=%d success=%t error=%v capture=%+v", snapshot.sessionID, snapshot.executionID, record.ID, record.Status, time.Since(started).Milliseconds(), err == nil, err, record.AfterCapture)
		if err == nil && (record.Status == "complete" || record.Status == "partial") {
			api.publishCrewSnapshot(snapshot, record)
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
func (api *StreamingAPI) publishCrewSnapshot(snapshot *crewTurnSnapshot, record filesnapshots.Record) {
	end := snapshot.completedSequence.Load()
	if api.eventStore == nil || end <= snapshot.startSequence {
		return
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
	data := storeevents.NewGenericEventData("reply_file_changes", map[string]interface{}{
		"snapshot_id": record.ID, "workspace_path": snapshot.root,
		"execution_id": snapshot.executionID, "start_sequence": snapshot.startSequence, "end_sequence": end,
	})
	event := unifiedevents.NewAgentEvent(data)
	event.SessionID = snapshot.sessionID
	api.eventStore.AddEvent(snapshot.sessionID, storeevents.Event{
		ID: "file-snapshot:" + record.ID, Type: "reply_file_changes", Timestamp: time.Now(), SessionID: snapshot.sessionID,
		ExecutionID: snapshot.executionID, ExecutionKind: "main_agent", TerminalOwnerID: "main:" + snapshot.sessionID, Data: event,
	})
	log.Printf("[FILE_SNAPSHOT] phase=reply_reference session=%s execution=%s snapshot=%s start_sequence=%d end_sequence=%d", snapshot.sessionID, snapshot.executionID, record.ID, snapshot.startSequence, end)
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
		externalJSON(w, map[string]any{"snapshot_id": id, "groups": replyFileGroups(ctx, roots, prefix, comparison, allow)})
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
