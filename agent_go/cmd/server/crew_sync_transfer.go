package server

// PLAT-856-509: immutable binary artifacts are private pending-transfer data,
// not file contents in a JSON RPC. The existing snapshot refs and receipts still
// decide what was applied. They travel as chunks (chunk_transfer.go): a retry or
// the next move sends only the chunks the server's store lacks.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// crewSyncPayloadBytes bounds one payload: the one sync limit plus a bundle's own overhead.
const crewSyncPayloadBytes = crewsync.SyncMaxBytes + 8<<20
const crewSyncMetadataBytes int64 = 48 << 20 // includes the existing bounded chat archive

func crewTransferDigest(value string) string {
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:])
}

func crewTransferDir(id, pair, transfer, direction string) (string, error) {
	if pair == "" || transfer == "" || len(pair) > 128 || len(transfer) > 128 || direction != "upload" {
		return "", fmt.Errorf("invalid sync transfer")
	}
	p, err := crewSyncManifestPath(id)
	if err != nil {
		return "", err
	}
	// This directory is in app-private state, never in the Crew's working folder.
	root := p + ".transfers"
	dir := root
	for _, part := range []string{"", crewTransferDigest(pair), direction, crewTransferDigest(transfer)} {
		if part != "" {
			dir = filepath.Join(dir, part)
		}
		if err = agentworksclient.EnsurePrivateDirectory(dir); err != nil {
			return "", err
		}
	}
	return dir, nil
}
func crewPayloadName(name string) bool {
	return name == "content" || name == "git" || name == "index" || name == "state" || name == "database"
}
func crewPayloadValid(p crewsync.Payload) bool {
	b, e := hex.DecodeString(p.Hash)
	return e == nil && len(b) == 32 && p.Hash == strings.ToLower(p.Hash) && p.Size >= 0 && p.Size <= crewSyncPayloadBytes
}
func crewPayloadAllowed(name string, p crewsync.Payload) bool {
	if !crewPayloadValid(p) {
		return false
	}
	switch name {
	case "content", "state", "database":
		return true
	case "git":
		return true
	case "index":
		return p.Size <= 64<<20
	}
	return false
}
func checkLocalCrewTransfer(id string, expected *crewroot.SyncBinding) error {
	current, e := crewroot.ReadSync(id)
	if e != nil {
		return e
	}
	if current == nil || current.PairID != expected.PairID || current.Side != "local" || current.ConnectionID != expected.ConnectionID || current.PeerProjectID != expected.PeerProjectID || current.Editing != expected.Editing {
		return fmt.Errorf("sync state changed; retry from the current paired connection")
	}
	return nil
}
func crewPayloadPaths(s *crewsync.Snapshot) map[string]*string {
	out := map[string]*string{}
	if s.DatabasePath != "" || s.Payloads["database"].Hash != "" {
		out["database"] = &s.DatabasePath
	}
	if s.StatePath != "" || s.Payloads["state"].Hash != "" {
		out["state"] = &s.StatePath
	}
	if s.Content != nil {
		out["content"] = &s.Content.BundlePath
	}
	if s.Git != nil {
		out["git"] = &s.Git.BundlePath
		out["index"] = &s.Git.IndexPath
	}
	return out
}

func crewSyncStagingRoot(id string) (string, error) {
	p, err := crewSyncManifestPath(id)
	if err != nil {
		return "", err
	}
	p += ".staging"
	if err = agentworksclient.EnsurePrivateDirectory(p); err != nil {
		return "", err
	}
	return p, nil
}
func crewCheckPayloadFile(path string, p crewsync.Payload) error {
	info, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if !info.Mode().IsRegular() || info.Size() != p.Size {
		return fmt.Errorf("sync artifact is incomplete")
	}
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return e
	}
	if hex.EncodeToString(h.Sum(nil)) != p.Hash {
		return fmt.Errorf("sync artifact checksum differs")
	}
	return nil
}

// Freeze private generated files without retaining their bytes in memory.
func freezeCrewPayloads(id, pair, transfer, direction string, s *crewsync.Snapshot) error {
	dir, e := crewTransferDir(id, pair, transfer, direction)
	if e != nil {
		return e
	}
	if s.Payloads == nil {
		s.Payloads = map[string]crewsync.Payload{}
	}
	paths := crewPayloadPaths(s)
	for name, source := range paths {
		if *source == "" {
			continue
		}
		in, err := os.Open(*source)
		if err != nil {
			return err
		}
		h := sha256.New()
		size, err := io.Copy(h, in)
		if err != nil {
			in.Close()
			return err
		}
		p := crewsync.Payload{Hash: hex.EncodeToString(h.Sum(nil)), Size: size}
		if !crewPayloadAllowed(name, p) {
			in.Close()
			return fmt.Errorf("sync artifact exceeds transfer limit")
		}
		target := filepath.Join(dir, name)
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
		if os.IsExist(err) {
			err = crewCheckPayloadFile(target, p)
		} else if err == nil {
			_, err = in.Seek(0, io.SeekStart)
			if err == nil {
				_, err = io.Copy(out, in)
			}
			if err == nil {
				err = out.Sync()
			}
			ce := out.Close()
			if err == nil {
				err = ce
			}
		}
		in.Close()
		if err != nil {
			return err
		}
		s.Payloads[name] = p
		// Source is a generated temporary bundle, never a peer-supplied path.
		_ = os.Remove(*source)
		_ = os.Remove(filepath.Dir(*source))
		*source = ""
	}

	if _, ok := s.Payloads["content"]; !ok {
		return fmt.Errorf("sync content artifact is missing")
	}
	return nil
}
func hydrateCrewPayloads(id, pair, transfer, direction string, s *crewsync.Snapshot) error {
	dir, e := crewTransferDir(id, pair, transfer, direction)
	if e != nil {
		return e
	}
	fields := crewPayloadPaths(s)
	if _, ok := s.Payloads["content"]; !ok {
		return fmt.Errorf("sync content artifact is missing; restart this transfer")
	}
	for name, p := range s.Payloads {
		dst, ok := fields[name]
		if !ok || !crewPayloadName(name) || !crewPayloadAllowed(name, p) {
			return fmt.Errorf("invalid sync artifact")
		}
		file := filepath.Join(dir, name)
		if e = crewCheckPayloadFile(file, p); e != nil {
			return e
		}
		*dst = file
	}
	return nil
}
func dropCrewPayloadBytes(s *crewsync.Snapshot) {
	if s != nil {
		for _, path := range crewPayloadPaths(s) {
			*path = ""
		}
	}
}

// discardCapturedCrewPayloads is only for locally captured scratch bundles,
// including no-change scans and capture failures. Frozen transfer artifacts
// have already cleared these paths and remain until their receipt.
func discardCapturedCrewPayloads(s *crewsync.Snapshot) {
	defer s.Close()
	paths := crewPayloadPaths(s)
	for _, path := range paths {
		if *path != "" {
			_ = os.Remove(*path)
			_ = os.Remove(filepath.Dir(*path))
			*path = ""
		}
	}
}

func cleanupCrewPayloads(id, pair, transfer, direction string) {
	if d, e := crewTransferDir(id, pair, transfer, direction); e == nil {
		_ = os.RemoveAll(d)
	}
}

// Retire an obsolete transfer record, not its files, baselines or conversation.
// The next capture writes only the current descriptor format.
func moveOldCrewTransfer(path, id string) (string, error) {
	backup := path + "." + time.Now().UTC().Format("20060102T150405.000000000Z") + ".bak"
	if err := os.Rename(path, backup); err != nil {
		return "", err
	}
	log.Printf("[CREW_SYNC] project=%s old_pending=moved", id)
	return backup, nil
}

func readCrewTransfer(path string, out any) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	return json.NewDecoder(io.LimitReader(f, crewSyncMetadataBytes+1)).Decode(out)
}
func (api *StreamingAPI) crewTransferBinding(ctx context.Context, claims *UserClaims, id, pair string) (crewroot.Roots, *crewroot.SyncBinding, error) {
	roots, e := api.crewSyncRoots(ctx, claims, id)
	if e != nil {
		return roots, nil, e
	}
	b, e := crewroot.ReadSync(id)
	if e != nil {
		return roots, nil, e
	}
	if b == nil || b.PairID != pair || b.Side != "server" {
		return roots, nil, &crewSyncFailure{Status: 409, Code: "wrong_pair", Message: "use this Crew's paired connection"}
	}
	return roots, b, nil
}
func validateCrewPayloads(s *crewsync.Snapshot) error {
	if s == nil || s.Content == nil || len(s.Payloads) == 0 || len(s.Payloads) > 5 || len(s.Files) > crewsync.SyncMaxFiles || len(s.Changes) > crewsync.SyncMaxFiles {
		return fmt.Errorf("send a snapshot with binary artifact descriptors")
	}
	if _, ok := s.Payloads["content"]; !ok {
		return fmt.Errorf("content artifact is missing")
	}
	fields := crewPayloadPaths(s)
	for name, p := range s.Payloads {
		if _, ok := fields[name]; !ok || !crewPayloadName(name) || !crewPayloadAllowed(name, p) {
			return fmt.Errorf("invalid sync artifact descriptor")
		}
	}
	return nil
}

// Abandoned uploads are app-private scratch, not Crew files. A new stage
// prunes only old siblings, retaining this retry and the latest receipt.
func pruneCrewUploadStages(id, dir, lastTransfer string) {
	entries, err := os.ReadDir(filepath.Dir(dir))
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-24 * time.Hour)
	current, last := filepath.Base(dir), crewTransferDigest(lastTransfer)
	removed := 0
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == current || entry.Name() == last {
			continue
		}
		if name, err := hex.DecodeString(entry.Name()); err != nil || len(name) != 32 {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		if os.RemoveAll(filepath.Join(filepath.Dir(dir), entry.Name())) == nil {
			removed++
		}
	}
	if removed > 0 {
		log.Printf("[CREW_SYNC] project=%s abandoned_uploads_pruned=%d", id, removed)
	}
}

// The immutable descriptor is admitted before any bytes. A retry cannot reuse
// its transfer ID for different files, even when the previous HTTP reply was lost.
func (api *StreamingAPI) prepareCrewTransfer(ctx context.Context, claims *UserClaims, id string, in crewSyncRequest) (crewSyncReply, error) {
	roots, b, e := api.crewTransferBinding(ctx, claims, id, in.PairID)
	if e != nil {
		return crewSyncReply{}, e
	}
	out := crewSyncReply{ProjectID: id, Binding: b, TransferID: in.TransferID}
	if b.LastTransfer == in.TransferID && in.TransferID != "" {
		return out, nil
	}
	if b.Editing != "local" {
		return out, fmt.Errorf("editing is on the server")
	}
	if len(in.Native) > 4096 {
		return out, fmt.Errorf("too many native sessions")
	}
	seen := map[string]bool{}
	for _, bundle := range in.Native {
		if e = bundle.Validate(); e != nil {
			return out, e
		}
		key := bundle.Provider + "/" + bundle.SessionID
		if seen[key] {
			return out, fmt.Errorf("duplicate native session")
		}
		seen[key] = true
	}
	if e = validateCrewPayloads(in.Snapshot); e != nil {
		return out, e
	}
	dir, e := crewTransferDir(roots.ID, in.PairID, in.TransferID, "upload")
	if e != nil {
		return out, e
	}
	pruneCrewUploadStages(roots.ID, dir, b.LastTransfer)
	unlock, e := wf.LockWorkspace(ctx, dir)
	if e != nil {
		return out, e
	}
	defer unlock()
	var prior crewsync.Snapshot
	path := filepath.Join(dir, "snapshot.json")
	if e = readCrewTransfer(path, &prior); e == nil {
		a, _ := json.Marshal(prior)
		b, _ := json.Marshal(in.Snapshot)
		if !bytes.Equal(a, b) {
			return out, fmt.Errorf("transfer identity already describes different files")
		}
	} else if os.IsNotExist(e) {
		e = fsutil.WriteJSONAtomic(path, in.Snapshot, 0600)
	}
	if e == nil {
		e = saveCrewMoveProgress(id, in.PairID, in.TransferID, in.Snapshot.Payloads, in.Native)
	}
	return out, e
}
func verifyCrewTransferDescriptor(id, pair, transfer string, s *crewsync.Snapshot) error {
	dir, e := crewTransferDir(id, pair, transfer, "upload")
	if e != nil {
		return e
	}
	var prior crewsync.Snapshot
	if e = readCrewTransfer(filepath.Join(dir, "snapshot.json"), &prior); e != nil {
		return e
	}
	a, _ := json.Marshal(prior)
	b, _ := json.Marshal(s)
	if !bytes.Equal(a, b) {
		return fmt.Errorf("sync descriptor differs from the admitted transfer")
	}
	return nil
}

// crewMoveChunkStore is this Crew's private chunk store for moves (chunk_transfer.go): the chunks of its last
// move stay, so the next move sends only what changed in its payloads.
func crewMoveChunkStore(id string) (string, error) {
	p, err := crewSyncManifestPath(id)
	if err != nil {
		return "", err
	}
	dir := p + ".chunks"
	return dir, agentworksclient.EnsurePrivateDirectory(dir)
}

// crewMoveRequest names a staged transfer and, for plan and commit, each payload as chunks.
type crewMoveRequest struct {
	PairID     string                 `json:"pair_id"`
	TransferID string                 `json:"transfer_id"`
	Files      map[string]chunkedFile `json:"files,omitempty"`
}

// handleCrewMoveChunks is /api/crew-sync/{project}/move/{op:plan|chunks|commit}: a move's payloads (content, Git,
// index, state, database) as chunks. Every request rechecks the actual owner, the pair, that this server side is
// receiving a move (Editing local), and for plan/commit that each file is exactly a payload the admitted transfer
// named. Commit writes the payloads into the transfer's staging folder, where push reads them as before.
func (api *StreamingAPI) handleCrewMoveChunks(w http.ResponseWriter, r *http.Request) {
	id, op := mux.Vars(r)["project"], mux.Vars(r)["op"]
	start := time.Now()
	fail := func(e error) {
		status := 409
		if f, ok := e.(*crewSyncFailure); ok && f.Status != 0 {
			status = f.Status
		}
		log.Printf("[CREW_SYNC] project=%s op=move_%s status=%d error=%q", id, op, status, e)
		externalError(w, status, crewSyncErrorCode(e), e.Error())
	}
	pair := r.URL.Query().Get("pair")
	_, b, e := api.crewTransferBinding(r.Context(), GetUserFromContext(r.Context()), id, pair)
	if e != nil {
		fail(e)
		return
	}
	if b.Editing != "local" {
		fail(fmt.Errorf("this Crew is not receiving a move"))
		return
	}
	store, e := crewMoveChunkStore(id)
	if e != nil {
		fail(e)
		return
	}
	if op == "chunks" {
		stored, size, err := receiveChunks(r.Body, store)
		if err != nil {
			fail(err)
			return
		}
		log.Printf("[CREW_SYNC] project=%s op=move_send chunks=%d bytes=%d ms=%d", id, stored, size, time.Since(start).Milliseconds())
		externalJSON(w, map[string]int{"stored": stored})
		return
	}
	var in crewMoveRequest
	if json.NewDecoder(io.LimitReader(r.Body, 16<<20)).Decode(&in) != nil || in.PairID != pair {
		fail(fmt.Errorf("send the transfer and its payloads"))
		return
	}
	dir, e := crewTransferDir(id, pair, in.TransferID, "upload")
	if e != nil {
		fail(e)
		return
	}
	unlock, e := wf.LockWorkspace(r.Context(), dir)
	if e != nil {
		fail(e)
		return
	}
	defer unlock()
	var s crewsync.Snapshot
	if e = readCrewTransfer(filepath.Join(dir, "snapshot.json"), &s); e != nil {
		fail(fmt.Errorf("sync transfer is unavailable"))
		return
	}
	if len(in.Files) != len(s.Payloads) {
		fail(fmt.Errorf("send every payload the transfer named"))
		return
	}
	files := make([]chunkedFile, 0, len(in.Files))
	for name, v := range in.Files {
		p, ok := s.Payloads[name]
		if !ok || !crewPayloadName(name) || !crewPayloadAllowed(name, p) || !v.valid() || v.Hash != p.Hash || v.Size != p.Size {
			fail(fmt.Errorf("artifact not admitted by this transfer"))
			return
		}
		files = append(files, v)
	}
	switch op {
	case "plan":
		// The planned chunks are the move's progress until the payloads are written (crewMoveBytes).
		if e = fsutil.WriteJSONAtomic(filepath.Join(dir, "chunks.json"), in.Files, 0600); e != nil {
			fail(e)
			return
		}
		missing := missingChunks(store, files)
		log.Printf("[CREW_SYNC] project=%s transfer=%s op=move_plan payloads=%d missing=%d", id, in.TransferID, len(files), len(missing))
		externalJSON(w, map[string]any{"missing": missing})
	case "commit":
		e = assembleFiles(in.Files, func(name string) (string, error) { return filepath.Join(dir, name), nil }, func(c transferChunk) ([]byte, error) { return readChunk(store, c) })
		if e != nil {
			fail(e)
			return
		}
		used := map[string]bool{}
		for _, v := range files {
			for _, c := range v.Chunks {
				used[c.ID] = true
			}
		}
		pruneChunks(store, used, crewNativeChunkKeep)
		log.Printf("[CREW_SYNC] project=%s transfer=%s op=move_commit payloads=%d ms=%d", id, in.TransferID, len(files), time.Since(start).Milliseconds())
		externalJSON(w, map[string]bool{"complete": true})
	default:
		externalError(w, 404, "not_found", "Unknown move step.")
	}
}

// desktopCrewPayloads sends a frozen transfer's payloads as chunks (owner: this computer's signed-in person): plan, only the chunks the server's store lacks,
// then commit, rechecking this Mac's pairing between steps.
func desktopCrewPayloads(ctx context.Context, owner, id string, c desktopConnection, b *crewroot.SyncBinding, transfer string, s *crewsync.Snapshot) error {
	if e := validateCrewPayloads(s); e != nil {
		return e
	}
	dir, e := crewTransferDir(id, b.PairID, transfer, "upload")
	if e != nil {
		return e
	}
	names := make([]string, 0, len(s.Payloads))
	for name, p := range s.Payloads {
		if e = crewCheckPayloadFile(filepath.Join(dir, name), p); e != nil {
			return e
		}
		names = append(names, name)
	}
	started := time.Now()
	files, sources, e := chunkFiles(names, func(name string) (string, error) { return filepath.Join(dir, name), nil }, false, nil)
	if e != nil {
		return e
	}
	route := "/api/crew-sync/" + url.PathEscape(b.PeerProjectID) + "/move"
	query := "?" + url.Values{"pair": {b.PairID}}.Encode()
	request := crewMoveRequest{PairID: b.PairID, TransferID: transfer, Files: files}
	call := func(op string, out any) error {
		if err := checkLocalCrewTransfer(id, b); err != nil {
			return err
		}
		if err := crewTeamRemoteJSON(ctx, owner, c, http.MethodPost, route+"/"+op+query, request, out); err != nil {
			return fmt.Errorf("server refused the move (%s): %w", op, err)
		}
		return nil
	}
	var plan struct {
		Missing []string `json:"missing"`
	}
	if e = call("plan", &plan); e != nil {
		return e
	}
	if e = checkLocalCrewTransfer(id, b); e != nil {
		return e
	}
	sent, size, e := sendMissingChunks(ctx, owner, c, route+"/chunks"+query, dir, sources, plan.Missing, nil)
	if e != nil {
		return fmt.Errorf("sync connection interrupted; retry sends only what is missing: %w", e)
	}
	if e = call("commit", nil); e != nil {
		return e
	}
	all := make([]chunkedFile, 0, len(files))
	for _, v := range files {
		all = append(all, v)
	}
	logChunkTransfer("move", id, "up", len(files), distinctChunks(all), sent, size, started)
	return nil
}
