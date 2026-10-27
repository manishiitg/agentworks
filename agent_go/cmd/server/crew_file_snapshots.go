package server

import (
	"context"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/filesnapshots"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

const crewSnapshotTimeout = 15 * time.Second
const crewSnapshotBeforeTimeout = 2 * time.Second

type crewTurnSnapshot struct {
	store                        filesnapshots.Store
	root, executionID, sessionID string
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
	roots, found, err := crewStorageRoots(folder)
	if err != nil || !found {
		return
	}
	snapshot := &crewTurnSnapshot{store: filesnapshots.Store{Root: roots.Files, Private: roots.Private}, root: roots.Virtual, executionID: executionID, sessionID: sessionID}
	if _, loaded := api.crewTurnSnapshots.LoadOrStore(executionID, snapshot); loaded {
		return
	}
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), crewSnapshotBeforeTimeout)
	defer cancel()
	record, err := snapshot.store.Begin(ctx, executionID)
	log.Printf("[FILE_SNAPSHOT] phase=before session=%s execution=%s snapshot=%s status=%s elapsed_ms=%d success=%t error=%v", sessionID, executionID, record.ID, record.Status, time.Since(started).Milliseconds(), err == nil, err)
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
		log.Printf("[FILE_SNAPSHOT] phase=after session=%s execution=%s snapshot=%s status=%s elapsed_ms=%d success=%t error=%v", snapshot.sessionID, snapshot.executionID, record.ID, record.Status, time.Since(started).Milliseconds(), err == nil, err)
	})
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
	if op != "snapshot_compare" {
		externalError(w, 400, "invalid_action", "Choose snapshots or snapshot_compare.")
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
	comparison, err := store.Compare(ctx, id, file, allow)
	if err != nil {
		externalError(w, 404, "snapshot_unavailable", "This file comparison is not available. List snapshots and choose a completed turn.")
		return
	}
	comparison.Record.ExecutionID = ""
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
