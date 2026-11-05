package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/filesnapshots"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

type crewTeamTransfer struct {
	Unchanged  bool               `json:"unchanged,omitempty"`
	FolderID   string             `json:"folder_id"`
	Action     string             `json:"action"`
	Client     string             `json:"client"`
	TransferID string             `json:"transfer_id"`
	Direction  string             `json:"direction"`
	Snapshot   *crewsync.Snapshot `json:"snapshot,omitempty"`
	Base       crewsync.Manifest  `json:"base,omitempty"`
	// OnPurpose: on upload, the files the uploader chose to sync despite credential-looking names; on download, the
	// files the shared folder holds on purpose (owner, 5 Nov, "Sync it anyway").
	OnPurpose []string `json:"on_purpose,omitempty"`
}

// crewTeamTransferOnPurpose is what a transfer may carry on purpose: the folder's on-purpose files and, for an upload,
// the uploader's own choices saved with that transfer.
func crewTeamTransferOnPurpose(project, stage string) (crewTeamOnPurpose, []string) {
	state, _ := readCrewTeamFileState(project)
	var declared []string
	if raw, err := os.ReadFile(filepath.Join(stage, "on_purpose.json")); err == nil {
		_ = json.Unmarshal(raw, &declared)
	}
	return state.onPurpose().with(declared), declared
}

func crewTeamPair(project, actor, client, direction string) string {
	return project + "/" + actor + "/" + client + "/" + direction
}
func crewTeamTransferDir(project, actor, client, direction, transfer string) (string, error) {
	if client == "" || len(client) > 128 || transfer == "" || len(transfer) > 128 || (direction != "upload" && direction != "download") {
		return "", fmt.Errorf("invalid folder transfer")
	}
	d, e := crewTeamContentDir(project)
	if e != nil {
		return "", e
	}
	return filepath.Join(d, "transfers", crewTransferDigest(actor+"\x00"+client), direction, crewTransferDigest(transfer)), nil
}
func crewTeamSnapshotStore(project, actor, client, direction string) (filesnapshots.Store, error) {
	d, e := crewTeamContentDir(project)
	if e != nil {
		return filesnapshots.Store{}, e
	}
	root := filepath.Join(d, "files")
	if e = os.MkdirAll(root, 0700); e != nil {
		return filesnapshots.Store{}, e
	}
	return filesnapshots.Store{Root: root, Private: filepath.Join(d, "transfers", crewTransferDigest(actor+"\x00"+client), direction)}, nil
}
func crewTeamSnapshotScope(claims *UserClaims, s *crewsync.Snapshot, on ...crewTeamOnPurpose) error {
	if e := validateCrewTeamSnapshot(s, on...); e != nil {
		return e
	}
	if claims.AccessToken != nil && claims.AccessToken.FileGuard != nil {
		g := claims.AccessToken.FileGuard
		for key := range s.Files {
			rel, _ := crewTeamRelative(key, on...)
			if !g.Allows(rel, false) {
				return fmt.Errorf("this connection cannot transfer the whole shared folder")
			}
		}
		for _, c := range s.Changes {
			rel, _ := crewTeamRelative(c.Path, on...)
			if !g.Allows(rel, true) {
				return fmt.Errorf("this connection cannot change that shared file")
			}
		}
	}
	return nil
}

func (api *StreamingAPI) handleCrewTeamSync(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	project := mux.Vars(r)["project"]
	if _, e := crewTeamRequestAuthorize(r, claims, project, ".", true, true); e != nil {
		externalError(w, 403, "team_folder_forbidden", e.Error())
		return
	}
	noteTeamMacSeen(project, claims.UserID) // each member's Mac syncs the team folder every few seconds while it is on
	var in crewTeamTransfer
	if json.NewDecoder(io.LimitReader(r.Body, 16<<20)).Decode(&in) != nil {
		externalError(w, 400, "invalid_transfer", "Send a shared-folder transfer.")
		return
	}
	if in.Action == "prepare" || in.Action == "apply" {
		in.Direction = "upload"
	}
	if in.Action == "pull" {
		in.Direction = "download"
	}
	stage, e := crewTeamTransferDir(project, claims.UserID, in.Client, in.Direction, in.TransferID)
	if e != nil {
		externalError(w, 400, "invalid_transfer", e.Error())
		return
	}
	// A step runs as a job that outlives the request: the request waits up to crewTeamSyncWait, then answers
	// "still working" and the caller asks again with the same transfer (Showcase, 5 Nov: a 7,184-file apply ran past the
	// 20 s the company gateway and the Mac allow, was cut, and the Mac looped). Each finished result is handed out once.
	key := stage + "\x00" + in.Action
	job := &crewTeamSyncJob{done: make(chan struct{})}
	if running, loaded := crewTeamSyncJobs.LoadOrStore(key, job); loaded {
		job = running.(*crewTeamSyncJob)
	} else {
		detached := r.WithContext(context.WithoutCancel(r.Context()))
		go func() {
			start, result := time.Now(), httptest.NewRecorder()
			api.crewTeamSyncStep(result, detached, claims, project, in, stage)
			files, changes := 0, 0
			if in.Snapshot != nil {
				files, changes = len(in.Snapshot.Files), len(in.Snapshot.Changes)
			}
			log.Printf("[CREW_TEAM_SYNC] project=%s actor=%s action=%s files=%d changes=%d base=%d status=%d ms=%d", project, logUsernameForUserID(claims.UserID), in.Action, files, changes, len(in.Base), result.Code, time.Since(start).Milliseconds())
			job.result = result
			close(job.done)
		}()
	}
	select {
	case <-job.done:
		crewTeamSyncJobs.CompareAndDelete(key, job)
		for name, values := range job.result.Header() {
			w.Header()[name] = values
		}
		w.WriteHeader(job.result.Code)
		_, _ = w.Write(job.result.Body.Bytes())
	case <-time.After(crewTeamSyncWait):
		w.Header().Set(crewTeamWorkingHeader, "1")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"working":true}`))
	case <-r.Context().Done():
	}
}

// crewTeamSyncWait is how long a sync request waits for its step before answering "still working".
const crewTeamSyncWait = 10 * time.Second

// crewTeamWorkingHeader marks a 202 that means "the step is still running; ask again with the same transfer".
const crewTeamWorkingHeader = "X-Team-Working"

type crewTeamSyncJob struct {
	done   chan struct{}
	result *httptest.ResponseRecorder
}

// crewTeamSyncJobs are the running (or finished, not yet handed out) sync steps, by transfer folder and action.
var crewTeamSyncJobs sync.Map

// crewTeamSyncStep is one sync step under the team lock: prepare, pull, apply or ack.
func (api *StreamingAPI) crewTeamSyncStep(w http.ResponseWriter, r *http.Request, claims *UserClaims, project string, in crewTeamTransfer, stage string) {
	var e error
	dir, _ := crewTeamContentDir(project)
	if e = os.MkdirAll(stage, 0700); e != nil {
		externalError(w, 409, "transfer_failed", e.Error())
		return
	}
	lockDir, _ := crewTeamDir(project)
	unlock, e := wf.LockWorkspace(r.Context(), lockDir)
	if e != nil {
		externalError(w, 409, "transfer_failed", e.Error())
		return
	}
	defer unlock()
	if _, e = crewTeamRequestAuthorize(r, claims, project, ".", true, true); e != nil {
		externalError(w, 403, "team_folder_forbidden", e.Error())
		return
	}
	store, e := crewTeamSnapshotStore(project, claims.UserID, in.Client, in.Direction)
	if e != nil {
		externalError(w, 409, "transfer_failed", e.Error())
		return
	}
	pair := crewTeamPair(project, claims.UserID, in.Client, in.Direction)
	meta := filepath.Join(stage, "snapshot.json")
	fail := func(e error) { externalError(w, 409, "transfer_failed", e.Error()) }
	switch in.Action {
	case "prepare":
		state, _ := readCrewTeamFileState(project)
		if e = crewTeamSnapshotScope(claims, in.Snapshot, state.onPurpose().with(in.OnPurpose)); e != nil {
			fail(e)
			return
		}
		// The uploader's on-purpose choices stay with this transfer for its apply.
		if len(in.OnPurpose) > 0 {
			if e = fsutil.WriteJSONAtomic(filepath.Join(stage, "on_purpose.json"), in.OnPurpose, 0600); e != nil {
				fail(e)
				return
			}
		}
		if in.Snapshot.Content == nil || len(in.Snapshot.Payloads) != 1 || !crewPayloadValid(in.Snapshot.Payloads["content"]) {
			fail(fmt.Errorf("send one verified file-content bundle"))
			return
		}
		encoded, _ := json.Marshal(in.Snapshot)
		if old, err := os.ReadFile(meta); err == nil {
			var saved crewsync.Snapshot
			if json.Unmarshal(old, &saved) != nil {
				fail(fmt.Errorf("saved transfer is unavailable"))
				return
			}
			before, _ := json.Marshal(saved)
			if string(before) != string(encoded) {
				fail(fmt.Errorf("this transfer already has different contents"))
				return
			}
		} else if !os.IsNotExist(err) {
			fail(err)
			return
		} else if e = fsutil.WriteJSONAtomic(meta, in.Snapshot, 0600); e != nil {
			fail(e)
			return
		}
		// The uploader already has its snapshot; echoing it back cost a 3 MB reply every round for 7,000 files.
		externalJSON(w, map[string]bool{"prepared": true})
	case "pull":
		state, _ := readCrewTeamFileState(project)
		op := state.onPurpose()
		in.OnPurpose = sortedOnPurpose(op)
		var snapshot crewsync.Snapshot
		if raw, err := os.ReadFile(meta); err == nil {
			if e = json.Unmarshal(raw, &snapshot); e != nil {
				fail(e)
				return
			}
			in.Snapshot = &snapshot
			externalJSON(w, in)
			return
		} else if !os.IsNotExist(err) {
			fail(err)
			return
		}
		// The caller's base may still name a file someone stopped syncing on purpose: it is only compared against,
		// never sent, so it is not refused (the reply no longer has it).
		baseOp := crewTeamOnPurpose{}.with(sortedOnPurpose(op))
		for key := range in.Base {
			if rel, ok := strings.CutPrefix(key, "resources/"); ok {
				baseOp[rel] = true
			}
		}
		if e = validateCrewTeamSnapshot(&crewsync.Snapshot{Files: in.Base}, baseOp); e != nil {
			fail(e)
			return
		}
		// Nothing changed since the caller's last download: the team's own record says so, without reading and hashing
		// every shared file again (11-14 s for 7,000 files, under the team lock, every few seconds).
		if len(in.Base) > 0 && maps.Equal(in.Base, state.manifest()) {
			_ = os.RemoveAll(stage)
			in.Snapshot, in.Base, in.Unchanged = nil, nil, true
			externalJSON(w, in)
			return
		}
		root := filepath.Join(dir, "files")
		if e = os.MkdirAll(root, 0700); e != nil {
			fail(e)
			return
		}
		team, _ := readCrewTeam(project)
		snapshot, e = crewTeamCapture(r.Context(), root, stage, in.Base, team.Folder, op)
		if e != nil {
			fail(e)
			return
		}
		defer snapshot.Close()
		if e = crewTeamSnapshotScope(claims, &snapshot, op); e != nil {
			fail(e)
			return
		}
		if len(snapshot.Changes) == 0 {
			snapshot.Close()
			_ = os.RemoveAll(stage)
			in.Snapshot = nil
			in.Base = nil
			in.Unchanged = true
			externalJSON(w, in)
			return
		}
		if e = store.EncodeFolderSync(r.Context(), pair, crewTeamRoots(root), &snapshot); e != nil {
			fail(e)
			return
		}
		generated := snapshot.Content.BundlePath
		defer os.RemoveAll(filepath.Dir(generated))
		if e = copyCrewTeamBundle(generated, filepath.Join(stage, "content")); e != nil {
			fail(e)
			return
		}
		payload, e := crewTeamPayload(filepath.Join(stage, "content"))
		if e != nil {
			fail(e)
			return
		}
		snapshot.Payloads = map[string]crewsync.Payload{"content": payload}
		snapshot.Content.BundlePath = ""
		if e = fsutil.WriteJSONAtomic(meta, snapshot, 0600); e != nil {
			fail(e)
			return
		}
		in.Snapshot = &snapshot
		in.Base = nil
		externalJSON(w, in)
	case "apply", "ack":
		var snapshot crewsync.Snapshot
		raw, err := os.ReadFile(meta)
		if err != nil {
			fail(err)
			return
		}
		if e = json.Unmarshal(raw, &snapshot); e != nil {
			fail(e)
			return
		}
		op, declared := crewTeamTransferOnPurpose(project, stage)
		if e = crewTeamSnapshotScope(claims, &snapshot, op); e != nil {
			fail(e)
			return
		}
		if in.Action == "ack" {
			if in.Direction != "download" {
				fail(fmt.Errorf("only a downloaded folder can be acknowledged"))
				return
			}
			if e = store.AckSync(r.Context(), pair, snapshot.Content); e != nil {
				fail(e)
				return
			}
			completeCrewTeamTransfer(stage)
			externalJSON(w, map[string]bool{"acknowledged": true})
			return
		}
		digestData, _ := json.Marshal(snapshot)
		digest := crewTransferDigest(string(digestData))
		state, err := readCrewTeamFileState(project)
		if err != nil {
			fail(err)
			return
		}
		if receipt, ok := state.Receipts[crewTransferDigest(claims.UserID+"\x00"+in.Client+"/"+in.TransferID)]; ok {
			if receipt.Digest != digest {
				fail(fmt.Errorf("transfer receipt does not match"))
				return
			}
			if e = store.AckSync(r.Context(), pair, snapshot.Content); e != nil {
				fail(e)
				return
			}
			completeCrewTeamTransfer(stage)
			externalJSON(w, receipt)
			return
		}
		snapshot.Content.BundlePath = filepath.Join(stage, "content")
		if e = crewCheckPayloadFile(snapshot.Content.BundlePath, snapshot.Payloads["content"]); e != nil {
			fail(e)
			return
		}
		if e = store.DecodeSync(r.Context(), pair, &snapshot); e != nil {
			fail(e)
			return
		}
		defer snapshot.Close()
		receipt, e := applyCrewTeamChanges(r.Context(), project, claims.UserID, in.Client+"/"+in.TransferID, digest, snapshot, declared...)
		if e != nil {
			fail(e)
			return
		}
		if e = store.AckSync(r.Context(), pair, snapshot.Content); e != nil {
			fail(e)
			return
		}
		completeCrewTeamTransfer(stage)
		externalJSON(w, receipt)
	default:
		externalError(w, 400, "invalid_action", "Choose prepare, pull, apply or ack for this folder.")
	}
}

func crewTeamPayload(file string) (crewsync.Payload, error) {
	f, e := os.Open(file)
	if e != nil {
		return crewsync.Payload{}, e
	}
	defer f.Close()
	h := sha256.New()
	n, e := io.Copy(h, f)
	if e != nil {
		return crewsync.Payload{}, e
	}
	return crewsync.Payload{Size: n, Hash: fmt.Sprintf("%x", h.Sum(nil))}, nil
}
func copyCrewTeamBundle(src, dst string) error {
	in, e := os.Open(src)
	if e != nil {
		return e
	}
	defer in.Close()
	out, e := os.CreateTemp(filepath.Dir(dst), "bundle-")
	if e != nil {
		return e
	}
	defer os.Remove(out.Name())
	_, e = io.Copy(out, in)
	if e == nil {
		e = out.Sync()
	}
	ce := out.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	return os.Rename(out.Name(), dst)
}

// Chunk offsets and hashes reuse the Crew transfer protocol. Folder authority,
// actor/client staging and allowed payloads are deliberately separate.
func (api *StreamingAPI) handleCrewTeamPiece(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	project := mux.Vars(r)["project"]
	q := r.URL.Query()
	client, direction := q.Get("client"), q.Get("direction")
	transfer := mux.Vars(r)["transfer"]
	if _, e := crewTeamRequestAuthorize(r, claims, project, ".", r.Method != http.MethodGet, true); e != nil {
		externalError(w, 403, "team_folder_forbidden", e.Error())
		return
	}
	stage, e := crewTeamTransferDir(project, claims.UserID, client, direction, transfer)
	if e != nil {
		externalError(w, 400, "invalid_transfer", e.Error())
		return
	}
	lockDir, _ := crewTeamDir(project)
	unlock, e := wf.LockWorkspace(r.Context(), lockDir)
	if e != nil {
		externalError(w, 409, "transfer_failed", e.Error())
		return
	}
	defer unlock()
	if _, e = crewTeamRequestAuthorize(r, claims, project, ".", r.Method != http.MethodGet, true); e != nil {
		externalError(w, 403, "team_folder_forbidden", e.Error())
		return
	}
	var s crewsync.Snapshot
	raw, e := os.ReadFile(filepath.Join(stage, "snapshot.json"))
	if e != nil || json.Unmarshal(raw, &s) != nil {
		externalError(w, 404, "transfer_missing", "Restart the shared-folder transfer.")
		return
	}
	op, _ := crewTeamTransferOnPurpose(project, stage)
	if e = crewTeamSnapshotScope(claims, &s, op); e != nil {
		externalError(w, 403, "team_folder_forbidden", e.Error())
		return
	}
	payload := s.Payloads["content"]
	if !crewPayloadValid(payload) {
		externalError(w, 400, "invalid_transfer", "Transfer metadata is unavailable.")
		return
	}
	file := filepath.Join(stage, "content")
	offset := int64(0)
	if info, e := os.Lstat(file); e == nil {
		if !info.Mode().IsRegular() || info.Size() > payload.Size {
			externalError(w, 409, "invalid_transfer", "Transfer bytes do not match.")
			return
		}
		offset = info.Size()
	} else if !os.IsNotExist(e) {
		externalError(w, 409, "transfer_failed", "Transfer bytes are unavailable.")
		return
	}
	if direction == "upload" {
		if _, err := os.Stat(filepath.Join(stage, "complete")); err == nil {
			offset = payload.Size
		}
	}
	w.Header().Set("Upload-Offset", strconv.FormatInt(offset, 10))
	w.Header().Set("X-Sync-Size", strconv.FormatInt(payload.Size, 10))
	w.Header().Set("X-Sync-Hash", payload.Hash)
	if r.Method == http.MethodHead {
		w.WriteHeader(200)
		return
	}
	if direction == "download" && r.Method == http.MethodGet {
		f, e := os.Open(file)
		if e != nil {
			externalError(w, 404, "transfer_missing", "Prepare the folder download first.")
			return
		}
		defer f.Close()
		info, _ := f.Stat()
		w.Header().Set("Content-Type", "application/octet-stream")
		http.ServeContent(w, r, "content.bundle", info.ModTime(), f)
		return
	}
	if direction != "upload" || r.Method != http.MethodPut {
		externalError(w, 405, "invalid_action", "Use the matching upload or download operation.")
		return
	}
	wanted, e := strconv.ParseInt(q.Get("offset"), 10, 64)
	if e != nil || wanted != offset || r.ContentLength <= 0 || r.ContentLength > crewSyncPieceBytes || offset+r.ContentLength > payload.Size {
		externalError(w, 409, "transfer_offset", "Resume from the saved offset with a bounded piece.")
		return
	}
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(2 * time.Minute))
	f, e := os.OpenFile(file, os.O_CREATE|os.O_WRONLY, 0600)
	if e != nil {
		externalError(w, 409, "transfer_failed", "Could not save the file transfer.")
		return
	}
	_, e = f.Seek(offset, io.SeekStart)
	var n int64
	if e == nil {
		n, e = io.CopyN(f, r.Body, r.ContentLength)
	}
	se := f.Sync()
	ce := f.Close()
	if e == nil {
		e = se
	}
	if e == nil {
		e = ce
	}
	offset += n
	w.Header().Set("Upload-Offset", strconv.FormatInt(offset, 10))
	if offset == payload.Size {
		if check := crewCheckPayloadFile(file, payload); check != nil {
			_ = os.Remove(file)
			externalError(w, 409, "transfer_checksum", "The upload checksum did not match. Retry this transfer.")
			return
		}
	}
	if e != nil {
		externalError(w, 409, "transfer_interrupted", "Resume this folder upload from its saved offset.")
		return
	}
	w.WriteHeader(204)
}

func (api *StreamingAPI) registerCrewTeamRoutes(r *mux.Router) {
	r.HandleFunc("/crew-teams", api.handleCrewTeam).Methods("GET")
	r.HandleFunc("/crew-teams/{project}", api.handleCrewTeam).Methods("GET", "PUT")
	r.HandleFunc("/crew-teams/{project}/{op:invite}", api.handleCrewTeam).Methods("POST")
	r.HandleFunc("/crew-teams/{project}/agents", api.handleCrewTeamAgents).Methods("GET")
	r.HandleFunc("/crew-teams/{project}/where", api.handleCrewTeamWhere).Methods("POST")
	r.HandleFunc("/crew-teams/{project}/{op:leave|agent-visibility}", api.handleCrewTeamMemberAction).Methods("POST")
	r.HandleFunc("/crew-teams/{project}/{op:files}", api.handleCrewTeamFiles).Methods("GET")
	r.HandleFunc("/crew-teams/{project}/{op:file}", api.handleCrewTeamFiles).Methods("GET", "PUT", "DELETE")
	r.HandleFunc("/crew-teams/{project}/{op:events|clashes|history|compare}", api.handleCrewTeamHistory).Methods("GET")
	r.HandleFunc("/crew-teams/{project}/{op:resolve}", api.handleCrewTeamHistory).Methods("POST")
	r.HandleFunc("/crew-teams/{project}/sync", api.handleCrewTeamSync).Methods("POST")
	r.HandleFunc("/crew-teams/{project}/sync/{transfer}", api.handleCrewTeamPiece).Methods("GET", "PUT", "HEAD")
	r.HandleFunc("/desktop-connections/{connection}/crew-teams/{project}/folders", api.handleDesktopTeamFolders).Methods("GET")
	r.HandleFunc("/desktop-connections/{connection}/crew-teams/{project}/local", api.handleDesktopTeamStatus).Methods("GET")
	r.HandleFunc("/desktop-connections/{connection}/crew-teams/{project}/sync-anyway", api.handleDesktopTeamSyncAnyway).Methods("POST")
	r.HandleFunc("/desktop-connections/{connection}/crew-teams/{project}", api.handleDesktopCrewTeam).Methods("GET", "PUT")
	r.HandleFunc("/desktop-connections/{connection}/crew-teams/{project}/switch", api.handleDesktopCrewTeamSwitch).Methods("POST")
	r.HandleFunc("/desktop-connections/{connection}/crew-teams/{project}/join", api.handleDesktopCrewTeamJoin).Methods("GET", "POST")
	r.HandleFunc("/desktop-connections/{connection}/crew-teams/{project}/remove-from-mac", api.handleDesktopCrewTeamRemoveFromMac).Methods("POST")
	r.HandleFunc("/desktop-connections/{connection}/crew-teams/{project}/add-to-mac", api.handleDesktopCrewTeamAddToMac).Methods("POST")
}

// Keep request identifiers usable in private state without accepting a path.
func crewTeamClientID(value string) bool {
	return value != "" && len(value) <= 128 && !strings.ContainsAny(value, "/\\\x00")
}

// Supported clients keep one pending transfer per direction. Keep two completed
// metadata receipts for retries, remove verified payloads after acknowledgement.
func completeCrewTeamTransfer(stage string) {
	if os.WriteFile(filepath.Join(stage, "complete"), []byte("ok"), 0600) != nil {
		return
	}
	_ = os.Remove(filepath.Join(stage, "content"))
	type done struct {
		path string
		at   time.Time
	}
	completed := []done{}
	entries, _ := os.ReadDir(filepath.Dir(stage))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		p := filepath.Join(filepath.Dir(stage), entry.Name())
		if info, err := os.Stat(filepath.Join(p, "complete")); err == nil {
			completed = append(completed, done{p, info.ModTime()})
		}
	}
	sort.Slice(completed, func(i, j int) bool { return completed[i].at.After(completed[j].at) })
	for i := 2; i < len(completed); i++ {
		_ = os.RemoveAll(completed[i].path)
	}
}

func sortedOnPurpose(op crewTeamOnPurpose) []string {
	out := make([]string, 0, len(op))
	for p := range op {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
