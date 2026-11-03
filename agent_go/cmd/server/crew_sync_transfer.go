package server

// PLAT-856-509: immutable binary artifacts are private pending-transfer data,
// not file contents in a JSON RPC. The existing snapshot refs and receipts still
// decide what was applied. A file's durable length is its resumable offset.
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
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
	"github.com/manishiitg/coding-agent-loop/workspace/publication"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

const crewSyncPieceBytes int64 = 8 << 20
const crewSyncPayloadBytes int64 = 264 << 20 // Phase 2 retains existing limits.
const crewSyncMetadataBytes int64 = 48 << 20 // includes the existing bounded chat archive

func crewTransferDigest(value string) string {
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:])
}

func crewTransferDir(id, pair, transfer, direction string) (string, error) {
	if pair == "" || transfer == "" || len(pair) > 128 || len(transfer) > 128 || (direction != "upload" && direction != "download") {
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
func crewPayloadName(name string) bool { return name == "content" || name == "git" || name == "index" }
func crewPayloadValid(p crewsync.Payload) bool {
	b, e := hex.DecodeString(p.Hash)
	return e == nil && len(b) == 32 && p.Hash == strings.ToLower(p.Hash) && p.Size >= 0 && p.Size <= crewSyncPayloadBytes
}
func crewPayloadAllowed(name string, p crewsync.Payload) bool {
	if !crewPayloadValid(p) {
		return false
	}
	switch name {
	case "content":
		return true
	case "git":
		return p.Size <= 256<<20
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
	if current == nil || current.PairID != expected.PairID || current.Side != "local" || current.ConnectionID != expected.ConnectionID || current.PeerProjectID != expected.PeerProjectID || current.Editing != expected.Editing || current.Mode == "off" || current.Mode == "relay" {
		return fmt.Errorf("sync state changed; retry from the current paired connection")
	}
	return nil
}
func crewPayloadPaths(s *crewsync.Snapshot) map[string]*string {
	out := map[string]*string{}
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
	if b.Mode == "off" {
		return roots, nil, fmt.Errorf("file sync is off for this Crew")
	}
	return roots, b, nil
}
func validateCrewPayloads(s *crewsync.Snapshot) error {
	if s == nil || s.Content == nil || len(s.Payloads) == 0 || len(s.Payloads) > 3 || len(s.Files) > publication.MaxFiles || len(s.Changes) > publication.MaxFiles {
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

// The API already authenticates this request. Recheck actual owner, pair,
// enabled state and direction on EVERY piece, including status/resume probes.
func (api *StreamingAPI) handleCrewSyncPiece(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	id := mux.Vars(r)["project"]
	pair, transfer, direction, name := q.Get("pair"), q.Get("transfer"), q.Get("direction"), q.Get("blob")
	fail := func(e error) {
		status := 409
		if f, ok := e.(*crewSyncFailure); ok && f.Status != 0 {
			status = f.Status
		}
		externalError(w, status, crewSyncErrorCode(e), e.Error())
	}
	roots, b, e := api.crewTransferBinding(r.Context(), GetUserFromContext(r.Context()), id, pair)
	if e != nil {
		fail(e)
		return
	}
	if !crewPayloadName(name) {
		fail(fmt.Errorf("invalid sync artifact name"))
		return
	}
	dir, e := crewTransferDir(id, pair, transfer, direction)
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
	// Recheck after waiting on a concurrent piece without acquiring the working-folder lock.
	_, b, e = api.crewTransferBinding(r.Context(), GetUserFromContext(r.Context()), id, pair)
	if e != nil {
		fail(e)
		return
	}
	var snapshot *crewsync.Snapshot
	if direction == "upload" {
		if b.Editing != "local" {
			fail(fmt.Errorf("editing is on the server"))
			return
		}
		var s crewsync.Snapshot
		e = readCrewTransfer(filepath.Join(dir, "snapshot.json"), &s)
		snapshot = &s
	} else {
		p, _ := crewSyncManifestPath(roots.ID)
		var pending crewSyncReply
		e = readCrewTransfer(p+".return", &pending)
		if e == nil && (pending.TransferID != transfer || pending.Binding == nil || pending.Binding.PairID != pair || pending.Binding.Editing != b.Editing) {
			e = fmt.Errorf("this return transfer is no longer current")
		}
		snapshot = pending.Snapshot
	}
	if e != nil || snapshot == nil {
		fail(fmt.Errorf("sync transfer is unavailable"))
		return
	}
	payload, ok := snapshot.Payloads[name]
	if !ok || !crewPayloadAllowed(name, payload) {
		fail(fmt.Errorf("artifact not admitted by this transfer"))
		return
	}
	path := filepath.Join(dir, name)
	var offset int64
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Size() > payload.Size {
			fail(fmt.Errorf("invalid sync staging file"))
			return
		}
		offset = info.Size()
	} else if !os.IsNotExist(err) {
		fail(err)
		return
	}
	w.Header().Set("Upload-Offset", strconv.FormatInt(offset, 10))
	w.Header().Set("X-Sync-Size", strconv.FormatInt(payload.Size, 10))
	w.Header().Set("X-Sync-Hash", payload.Hash)
	if r.Method == http.MethodHead {
		w.WriteHeader(200)
		return
	}
	wanted, e := strconv.ParseInt(q.Get("offset"), 10, 64)
	if e != nil || wanted < 0 || wanted > payload.Size {
		fail(fmt.Errorf("invalid sync offset"))
		return
	}
	if r.Method == http.MethodGet && direction == "download" {
		if offset != payload.Size {
			fail(fmt.Errorf("sync artifact unavailable"))
			return
		}
		f, e := os.Open(path)
		if e != nil {
			fail(e)
			return
		}
		defer f.Close()
		count := min(crewSyncPieceBytes, payload.Size-wanted)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.FormatInt(count, 10))
		n, copyErr := io.CopyN(w, io.NewSectionReader(f, wanted, count), count)
		log.Printf("[CREW_SYNC_PIECE] project=%s transfer=%s direction=download bytes=%d offset=%d total=%d complete=%t", id, transfer, n, wanted+n, payload.Size, copyErr == nil)
		return
	}
	if r.Method != http.MethodPut || direction != "upload" {
		fail(fmt.Errorf("unsupported sync piece operation"))
		return
	}
	if wanted != offset {
		fail(fmt.Errorf("sync offset changed; resume from the saved offset"))
		return
	}
	if r.ContentLength <= 0 || r.ContentLength > crewSyncPieceBytes || offset+r.ContentLength > payload.Size {
		fail(fmt.Errorf("send one bounded sync piece"))
		return
	}
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(2 * time.Minute))
	f, e := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0600)
	if e != nil {
		fail(e)
		return
	}
	_, e = f.Seek(offset, io.SeekStart)
	var n int64
	if e == nil {
		n, e = io.CopyN(f, r.Body, r.ContentLength)
	}
	syncErr := f.Sync()
	closeErr := f.Close()
	if e == nil {
		e = syncErr
	}
	if e == nil {
		e = closeErr
	}
	offset += n
	w.Header().Set("Upload-Offset", strconv.FormatInt(offset, 10))
	if offset == payload.Size {
		if check := crewCheckPayloadFile(path, payload); check != nil {
			_ = os.Remove(path)
			fail(check)
			return
		}
	}
	if e != nil {
		fail(fmt.Errorf("sync piece interrupted; resume from its saved offset"))
		return
	}
	log.Printf("[CREW_SYNC_PIECE] project=%s transfer=%s direction=upload bytes=%d offset=%d total=%d", id, transfer, n, offset, payload.Size)
	w.WriteHeader(http.StatusNoContent)
}

func crewPieceRequest(ctx context.Context, c desktopConnection, project, pair, transfer, direction, name, method string, offset int64, body io.Reader, length int64) (*http.Response, error) {
	u := c.Server + "/api/crew-sync/" + url.PathEscape(project)
	q := url.Values{"pair": {pair}, "transfer": {transfer}, "direction": {direction}, "blob": {name}, "offset": {strconv.FormatInt(offset, 10)}}
	req, e := http.NewRequestWithContext(ctx, method, u+"?"+q.Encode(), body)
	if e != nil {
		return nil, e
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.ContentLength = length
	if method == http.MethodPut {
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	client := desktopHTTPClient()
	client.Timeout = 2 * time.Minute
	res, e := client.Do(req)
	if e != nil {
		return nil, fmt.Errorf("sync connection interrupted; retry resumes saved bytes")
	}
	if res.StatusCode != 200 && res.StatusCode != 204 {
		defer res.Body.Close()
		return nil, fmt.Errorf("server refused sync piece (%d); check the paired connection and retry", res.StatusCode)
	}
	return res, nil
}
func desktopCrewPayloads(ctx context.Context, id string, c desktopConnection, b *crewroot.SyncBinding, transfer string, s *crewsync.Snapshot, upload bool) error {
	if e := validateCrewPayloads(s); e != nil {
		return e
	}
	direction := "download"
	if upload {
		direction = "upload"
	}
	dir, e := crewTransferDir(id, b.PairID, transfer, direction)
	if e != nil {
		return e
	}
	for name, p := range s.Payloads {
		if e = checkLocalCrewTransfer(id, b); e != nil {
			return e
		}
		path := filepath.Join(dir, name)
		if upload {
			if e = crewCheckPayloadFile(path, p); e != nil {
				return e
			}
			res, e := crewPieceRequest(ctx, c, b.PeerProjectID, b.PairID, transfer, direction, name, http.MethodHead, 0, nil, 0)
			if e != nil {
				return e
			}
			res.Body.Close()
			offset, e := strconv.ParseInt(res.Header.Get("Upload-Offset"), 10, 64)
			if e != nil || offset < 0 || offset > p.Size || res.Header.Get("X-Sync-Hash") != p.Hash {
				return fmt.Errorf("invalid sync resume response")
			}
			f, e := os.Open(path)
			if e != nil {
				return e
			}
			for offset < p.Size {
				if e = checkLocalCrewTransfer(id, b); e != nil {
					f.Close()
					return e
				}
				count := min(crewSyncPieceBytes, p.Size-offset)
				res, e = crewPieceRequest(ctx, c, b.PeerProjectID, b.PairID, transfer, direction, name, http.MethodPut, offset, io.NewSectionReader(f, offset, count), count)
				if e != nil {
					f.Close()
					return e
				}
				res.Body.Close()
				next, err := strconv.ParseInt(res.Header.Get("Upload-Offset"), 10, 64)
				if err != nil || next != offset+count {
					f.Close()
					return fmt.Errorf("server did not confirm sync piece")
				}
				offset = next
			}
			f.Close()
		} else {
			f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
			if e != nil {
				return e
			}
			info, e := f.Stat()
			if e != nil || info.Size() > p.Size {
				f.Close()
				return fmt.Errorf("invalid partial sync artifact")
			}
			offset := info.Size()
			for offset < p.Size {
				if e = checkLocalCrewTransfer(id, b); e != nil {
					f.Close()
					return e
				}
				res, e := crewPieceRequest(ctx, c, b.PeerProjectID, b.PairID, transfer, direction, name, http.MethodGet, offset, nil, 0)
				if e != nil {
					f.Close()
					return e
				}
				count := min(crewSyncPieceBytes, p.Size-offset)
				if res.Header.Get("X-Sync-Hash") != p.Hash || res.ContentLength != count {
					res.Body.Close()
					f.Close()
					return fmt.Errorf("server returned different sync bytes")
				}
				_, e = f.Seek(offset, io.SeekStart)
				var n int64
				if e == nil {
					n, e = io.CopyN(f, res.Body, count)
				}
				res.Body.Close()
				syncErr := f.Sync()
				offset += n
				if e != nil || syncErr != nil {
					f.Close()
					return fmt.Errorf("download interrupted; retry resumes saved bytes")
				}
			}
			f.Close()
			if e = crewCheckPayloadFile(path, p); e != nil {
				_ = os.Remove(path)
				return e
			}
		}
	}
	return nil
}
