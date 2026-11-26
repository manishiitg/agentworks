package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type crewOfflineAppJob struct{ again bool }

var crewOfflineAppJobs = struct {
	sync.Mutex
	pending map[string]*crewOfflineAppJob
}{pending: map[string]*crewOfflineAppJob{}}

// A finished turn only queues work. No native I/O, remote request or failed
// copy can delay or fail that turn. Overlapping turns coalesce, without polling.
func (api *StreamingAPI) queueCrewOfflineAppTurn(user, session, workspace string) {
	if user == "" || session == "" || workspace == "" {
		return
	}
	key := user + "\x00" + session
	crewOfflineAppJobs.Lock()
	if current := crewOfflineAppJobs.pending[key]; current != nil {
		current.again = true
		crewOfflineAppJobs.Unlock()
		return
	}
	job := &crewOfflineAppJob{}
	crewOfflineAppJobs.pending[key] = job
	crewOfflineAppJobs.Unlock()
	go func() {
		for {
			// One catch-up may span many deltas; each delta has its own deadline (sendCrewOfflineAppDelta),
			// so a long backlog is not cut off mid-upload (live: invalid_chunks at the 60-second mark).
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
			err := api.syncCrewOfflineAppTurn(ctx, user, session, workspace)
			cancel()
			if err != nil {
				log.Printf("[AGENT_OFFLINE_COPY] session=%s event=refused reason=source_sync_failed", session)
			}
			crewOfflineAppJobs.Lock()
			if !job.again {
				delete(crewOfflineAppJobs.pending, key)
				crewOfflineAppJobs.Unlock()
				return
			}
			job.again = false
			crewOfflineAppJobs.Unlock()
		}
	}()
}

// syncCrewOfflineAppProjectNow brings every shared chat of this Mac Crew up to date on the server before a copy is
// sent (#3218, owner 19 Nov: sharing and then sending at once must still work). It waits for a sync already running
// for a chat, then runs one itself; a turn that finishes meanwhile queues the usual follow-up. Unshared chats return
// after one status call.
func (api *StreamingAPI) syncCrewOfflineAppProjectNow(ctx context.Context, user, project string) error {
	records, err := defaultProductConversationRegistryStore().projectChats(ctx, user, crewProfileID, project)
	if err != nil {
		return err
	}
	for _, r := range records {
		if r.SessionID == "" || r.WorkspacePath == "" {
			continue
		}
		if err = api.syncCrewOfflineAppNow(ctx, user, r.SessionID, r.WorkspacePath); err != nil {
			return err
		}
	}
	return nil
}

func (api *StreamingAPI) syncCrewOfflineAppNow(ctx context.Context, user, session, workspace string) error {
	key := user + "\x00" + session
	job := &crewOfflineAppJob{}
	for {
		crewOfflineAppJobs.Lock()
		if crewOfflineAppJobs.pending[key] == nil {
			crewOfflineAppJobs.pending[key] = job
			crewOfflineAppJobs.Unlock()
			break
		}
		crewOfflineAppJobs.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
	err := api.syncCrewOfflineAppTurn(ctx, user, session, workspace)
	crewOfflineAppJobs.Lock()
	again := job.again
	delete(crewOfflineAppJobs.pending, key)
	crewOfflineAppJobs.Unlock()
	if again {
		api.queueCrewOfflineAppTurn(user, session, workspace)
	}
	return err
}

func (api *StreamingAPI) syncCrewOfflineAppTurn(ctx context.Context, user, session, workspace string) (retErr error) {
	claims := &UserClaims{UserID: user, Username: user}
	ctx = context.WithValue(ctx, UserContextKey, claims)
	if !IsLocalMode() {
		return api.syncCrewOfflineServerTurn(ctx, user, session, workspace)
	}
	stage := "source_registry"
	defer func() {
		if retErr != nil {
			status := 0
			var remote *crewRemoteHTTPError
			if errors.As(retErr, &remote) {
				status = remote.Status
			}
			reason := "operation_failed"
			switch {
			case errors.Is(retErr, context.DeadlineExceeded):
				reason = "deadline_exceeded"
			case errors.Is(retErr, context.Canceled):
				reason = "canceled"
			case os.IsNotExist(retErr):
				reason = "not_found"
			case os.IsPermission(retErr):
				reason = "permission_denied"
			case remote != nil:
				reason = "remote_refused"
			}
			// No raw error text: remote bodies, transcript/parser errors and
			// OS paths may hold private content. Stage/status are enough to
			// locate the failing boundary without logging that content.
			log.Printf("[AGENT_OFFLINE_COPY] session=%s event=source_sync_failed stage=%s status=%d reason=%s error_type=%T retry=next_finished_turn", session, stage, status, reason, retErr)
		}
	}()
	records, err := defaultProductConversationRegistryStore().crewWorkspaceChats(ctx, user, workspace)
	if err != nil {
		return err
	}
	var record ProductConversationRecord
	for _, r := range records {
		if r.ProfileID == crewProfileID && r.SessionID == session && r.WorkspacePath == workspace {
			if record.ConversationKey != "" {
				return fmt.Errorf("source session is ambiguous")
			}
			record = r
		}
	}
	if record.ConversationKey == "" {
		return nil
	}
	project := crewResourceProjectID(record.ConversationKey)
	stage = "source_pair"
	pair, err := crewroot.ReadSync(project)
	if err != nil {
		return err
	}
	if pair == nil || pair.Side != "local" || pair.ConnectionID == "" || pair.PeerProjectID == "" {
		return nil
	}
	stage = "source_connection"
	connection, err := desktopConnectionFor(user, pair.ConnectionID)
	if err != nil {
		return err
	}
	tab := strings.TrimPrefix(record.ConversationKey, project+codeChatSideMarker)
	if record.ConversationKey == project || record.ConversationKey == crewOwnerMainChatKey(project) {
		tab = macMainTab
	}
	binding := crewOfflineBinding{Provider: "agentworks", Session: record.SessionID, Attachment: record.ConversationID}
	var status struct {
		crewOfflineCursor
		Wanted bool   `json:"wanted"`
		Agent  string `json:"agent"`
		Reset  bool   `json:"reset"`
	}
	reset := crewOfflineRewindPending(record)
	base := "/api/crew-teams/" + url.PathEscape(pair.PeerProjectID)
	stage = "remote_status"
	if err = crewTeamRemoteJSON(ctx, user, connection, http.MethodPost, base+"/offline-copy-status", map[string]any{"provider": binding.Provider, "session": binding.Session, "attachment": binding.Attachment, "source_tab": tab, "reset": reset}, &status); err != nil {
		return err
	}
	if reset && status.Wanted && status.Reset {
		clearCrewOfflineRewind(record)
	}
	if !status.Wanted {
		log.Printf("[AGENT_OFFLINE_COPY] project=%s session=%s event=skipped reason=app_not_shared", project, session)
		return nil
	}
	if status.Agent == "" {
		return fmt.Errorf("source agent unavailable")
	}
	stage = "source_roots"
	roots, found, err := crewStorageRoots(workspace)
	if err != nil || !found || roots.Owner != user {
		return fmt.Errorf("source project ownership changed")
	}
	stage = "saved_history"
	raw, err := ReadChatHistoryConversation(user, session, workspace)
	if err != nil {
		return err
	}
	var saved struct {
		UserID  string                    `json:"user_id"`
		Session string                    `json:"session_id"`
		History []llmtypes.MessageContent `json:"conversation_history"`
	}
	if json.Unmarshal(raw, &saved) != nil || saved.UserID != user || saved.Session != session {
		return fmt.Errorf("source history owner changed")
	}
	stage = "portable_journal"
	path, err := crewOfflineAppJournal(roots, record, saved.History)
	if err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	sourceSize := info.Size()
	stage = "cursor_prefix"
	if status.Offset < 0 || status.Offset > sourceSize {
		return fmt.Errorf("source cursor exceeds its journal")
	}
	if got, err := crewOfflineFileTailHash(file, status.Offset); err != nil || got != status.TailHash {
		return fmt.Errorf("source journal prefix changed")
	}
	cursor := status.crewOfflineCursor
	route := base + "/agents/" + url.PathEscape(status.Agent) + "/offline-copy/"
	for cursor.Offset < sourceSize {
		if err = ctx.Err(); err != nil {
			return err
		}
		n := min(int64(crewOfflineDeltaLimit), sourceSize-cursor.Offset)
		data := make([]byte, n)
		if _, err = file.ReadAt(data, cursor.Offset); err != nil {
			return err
		}
		// The portable writer always emits whole rows. Never split a JSON object.
		end := bytes.LastIndexByte(data, '\n') + 1
		if end == 0 {
			return fmt.Errorf("one source message exceeds delta capacity")
		}
		data = data[:end]
		stage = "delta_upload"
		ack, e := sendCrewOfflineAppDelta(ctx, user, connection, route, binding, cursor, sourceSize, data)
		if e != nil {
			return e
		}
		stage = "delta_receipt"
		wantHash, e := crewOfflineFileTailHash(file, cursor.Offset+int64(len(data)))
		if e != nil || ack.Offset != cursor.Offset+int64(len(data)) || ack.Revision != cursor.Revision+1 || ack.TailHash != wantHash {
			return fmt.Errorf("source acknowledgement changed")
		}
		cursor = ack
		stage = "save_cursor"
		if err = fsutil.WriteJSONAtomic(filepath.Join(filepath.Dir(path), "ack.json"), cursor, 0600); err != nil {
			return err
		}
	}
	log.Printf("[AGENT_OFFLINE_COPY] project=%s agent=%s session=%s event=synced revision=%d offset=%d", pair.PeerProjectID, status.Agent, session, cursor.Revision, cursor.Offset)
	stage = "handback"
	return syncCrewOfflineAppReturns(ctx, user, connection, route, record, saved.History)
}

func crewOfflineFileTailHash(file *os.File, offset int64) (string, error) {
	start := max(int64(0), offset-4096)
	raw := make([]byte, offset-start)
	if len(raw) > 0 {
		if _, err := file.ReadAt(raw, start); err != nil {
			return "", err
		}
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func sendCrewOfflineAppDelta(ctx context.Context, user string, c desktopConnection, route string, b crewOfflineBinding, cursor crewOfflineCursor, sourceSize int64, data []byte) (crewOfflineCursor, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	content := map[string][]byte{}
	version, err := chunkReader(bytes.NewReader(data), int64(len(data)), false, func(id string, data []byte) error { content[id] = bytes.Clone(data); return nil })
	if err != nil {
		return cursor, err
	}
	in := crewOfflineDelta{crewOfflineBinding: b, BaseRevision: cursor.Revision, Offset: cursor.Offset, PreviousTailHash: cursor.TailHash, Delta: version, SourceSize: sourceSize}
	var plan struct {
		Missing []string `json:"missing"`
	}
	if err = crewTeamRemoteJSON(ctx, user, c, http.MethodPost, route+"plan", in, &plan); err != nil {
		return cursor, err
	}
	query := url.Values{"provider": {b.Provider}, "session": {b.Session}, "attachment": {b.Attachment}}
	var frames bytes.Buffer
	flush := func() error {
		if frames.Len() == 0 {
			return nil
		}
		response, e := crewTeamRemoteDo(ctx, user, c, http.MethodPut, route+"chunks?"+query.Encode(), "application/octet-stream", bytes.NewReader(frames.Bytes()))
		if response != nil {
			io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
			response.Body.Close()
		}
		if e != nil {
			status := 0
			var remote *crewRemoteHTTPError
			if errors.As(e, &remote) {
				status = remote.Status
			}
			log.Printf("[AGENT_OFFLINE_COPY] session=%s event=chunks_upload_failed bytes=%d limit=%d status=%d revision=%d offset=%d retry=next_finished_turn", b.Session, frames.Len(), chunkBatchBytes, status, cursor.Revision, cursor.Offset)
		}
		frames.Reset()
		return e
	}
	for _, id := range plan.Missing {
		chunk, ok := content[id]
		if !ok {
			return cursor, fmt.Errorf("unknown source chunk requested")
		}
		if frames.Len()+36+len(chunk) > chunkBatchBytes {
			if err = flush(); err != nil {
				return cursor, err
			}
		}
		if err = writeChunkFrame(&frames, id, chunk); err != nil {
			return cursor, err
		}
	}
	if err = flush(); err != nil {
		return cursor, err
	}
	var ack crewOfflineCursor
	err = crewTeamRemoteJSON(ctx, user, c, http.MethodPost, route+"commit", in, &ack)
	return ack, err
}
