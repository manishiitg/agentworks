package server

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/filesnapshots"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

type crewTeamTransfer struct {
	Action     string             `json:"action"`
	Client     string             `json:"client"`
	TransferID string             `json:"transfer_id"`
	Direction  string             `json:"direction"`
	Snapshot   *crewsync.Snapshot `json:"snapshot,omitempty"`
	Base       crewsync.Manifest  `json:"base,omitempty"`
}

func crewTeamPair(project, actor, client, direction string) string {
	return project + "/" + actor + "/" + client + "/" + direction
}
func crewTeamTransferDir(project, actor, client, direction, transfer string) (string, error) {
	if client == "" || len(client) > 128 || transfer == "" || len(transfer) > 128 || (direction != "upload" && direction != "download") {
		return "", fmt.Errorf("invalid folder transfer")
	}
	d, e := crewTeamDir(project)
	if e != nil {
		return "", e
	}
	return filepath.Join(d, "transfers", crewTransferDigest(actor+"\x00"+client), direction, crewTransferDigest(transfer)), nil
}
func crewTeamSnapshotStore(project, actor, client, direction string) (filesnapshots.Store, error) {
	d, e := crewTeamDir(project)
	if e != nil {
		return filesnapshots.Store{}, e
	}
	return filesnapshots.Store{Root: filepath.Join(d, "files"), Private: filepath.Join(d, "transfers", crewTransferDigest(actor+"\x00"+client), direction)}, nil
}
func crewTeamSnapshotScope(claims *UserClaims, s *crewsync.Snapshot) error {
	if e := validateCrewTeamSnapshot(s); e != nil {
		return e
	}
	if claims.AccessToken != nil && claims.AccessToken.FileGuard != nil {
		g := claims.AccessToken.FileGuard
		for key := range s.Files {
			rel, _ := crewTeamRelative(key)
			if !g.Allows(rel, false) {
				return fmt.Errorf("this connection cannot transfer the whole shared folder")
			}
		}
		for _, c := range s.Changes {
			rel, _ := crewTeamRelative(c.Path)
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
	if _, e := crewTeamAuthorize(claims, project, ".", true, true); e != nil {
		externalError(w, 403, "team_folder_forbidden", e.Error())
		return
	}
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
	dir, _ := crewTeamDir(project)
	if e = os.MkdirAll(stage, 0700); e != nil {
		externalError(w, 409, "transfer_failed", e.Error())
		return
	}
	unlock, e := wf.LockWorkspace(r.Context(), dir)
	if e != nil {
		externalError(w, 409, "transfer_failed", e.Error())
		return
	}
	defer unlock()
	if _, e = crewTeamAuthorize(claims, project, ".", true, true); e != nil {
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
		if e = crewTeamSnapshotScope(claims, in.Snapshot); e != nil {
			fail(e)
			return
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
		externalJSON(w, in)
	case "pull":
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
		if e = validateCrewTeamSnapshot(&crewsync.Snapshot{Files: in.Base}); e != nil {
			fail(e)
			return
		}
		root := filepath.Join(dir, "files")
		if e = os.MkdirAll(root, 0700); e != nil {
			fail(e)
			return
		}
		snapshot, e = crewTeamCapture(r.Context(), root, stage, in.Base)
		if e != nil {
			fail(e)
			return
		}
		defer snapshot.Close()
		if e = crewTeamSnapshotScope(claims, &snapshot); e != nil {
			fail(e)
			return
		}
		if e = store.EncodeSync(r.Context(), pair, crewTeamRoots(root), &snapshot); e != nil {
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
		if e = crewTeamSnapshotScope(claims, &snapshot); e != nil {
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
		receipt, e := applyCrewTeamChanges(r.Context(), project, claims.UserID, in.Client+"/"+in.TransferID, digest, snapshot)
		if e != nil {
			fail(e)
			return
		}
		if e = store.AckSync(r.Context(), pair, snapshot.Content); e != nil {
			fail(e)
			return
		}
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
	if _, e := crewTeamAuthorize(claims, project, ".", r.Method != http.MethodGet, true); e != nil {
		externalError(w, 403, "team_folder_forbidden", e.Error())
		return
	}
	stage, e := crewTeamTransferDir(project, claims.UserID, client, direction, transfer)
	if e != nil {
		externalError(w, 400, "invalid_transfer", e.Error())
		return
	}
	unlock, e := wf.LockWorkspace(r.Context(), stage)
	if e != nil {
		externalError(w, 409, "transfer_failed", e.Error())
		return
	}
	defer unlock()
	if _, e = crewTeamAuthorize(claims, project, ".", r.Method != http.MethodGet, true); e != nil {
		externalError(w, 403, "team_folder_forbidden", e.Error())
		return
	}
	var s crewsync.Snapshot
	raw, e := os.ReadFile(filepath.Join(stage, "snapshot.json"))
	if e != nil || json.Unmarshal(raw, &s) != nil {
		externalError(w, 404, "transfer_missing", "Restart the shared-folder transfer.")
		return
	}
	if e = crewTeamSnapshotScope(claims, &s); e != nil {
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
	r.HandleFunc("/crew-teams/{project}/{op:seed}", api.handleCrewTeam).Methods("POST")
	r.HandleFunc("/crew-teams/{project}/{op:files}", api.handleCrewTeamFiles).Methods("GET")
	r.HandleFunc("/crew-teams/{project}/{op:file}", api.handleCrewTeamFiles).Methods("GET", "PUT", "DELETE")
	r.HandleFunc("/crew-teams/{project}/{op:events|clashes|history|compare}", api.handleCrewTeamHistory).Methods("GET")
	r.HandleFunc("/crew-teams/{project}/{op:resolve}", api.handleCrewTeamHistory).Methods("POST")
	r.HandleFunc("/crew-teams/{project}/sync", api.handleCrewTeamSync).Methods("POST")
	r.HandleFunc("/crew-teams/{project}/sync/{transfer}", api.handleCrewTeamPiece).Methods("GET", "PUT", "HEAD")
	r.HandleFunc("/desktop-connections/{connection}/crew-teams/{project}/local", api.handleDesktopTeamStatus).Methods("GET")
	r.HandleFunc("/desktop-connections/{connection}/crew-teams/{project}", api.handleDesktopCrewTeam).Methods("GET", "PUT")
}

// Keep request identifiers usable in private state without accepting a path.
func crewTeamClientID(value string) bool {
	return value != "" && len(value) <= 128 && !strings.ContainsAny(value, "/\\\x00")
}
