package server

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
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
)

var crewNativeLocks sync.Map

func crewNativeLock(roots crewroot.Roots, id string) *sync.Mutex {
	value, _ := crewNativeLocks.LoadOrStore(roots.Private+"/"+id, &sync.Mutex{})
	return value.(*sync.Mutex)
}

func pruneCrewNativeStage(stage string, b *sessiontransfer.Bundle) error {
	return filepath.WalkDir(stage, func(path string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if path == stage || d.IsDir() {
			return nil
		}
		name, e := filepath.Rel(stage, path)
		if e != nil {
			return e
		}
		if d.Type()&os.ModeSymlink != 0 || b.Files[filepath.ToSlash(name)].Size == 0 {
			return os.Remove(path)
		}
		return nil
	})
}

// This private directory is outside both the project file snapshot and the
// CLI's writable home. Never use a path supplied by a transfer descriptor.
func crewNativeStage(roots crewroot.Roots, b *sessiontransfer.Bundle) (string, error) {
	if err := b.Validate(); err != nil {
		return "", err
	}
	var total int64
	for _, f := range b.Files {
		total += f.Size
	}
	if total > sessiontransfer.MaxBytes() {
		return "", fmt.Errorf("this native session exceeds the configured sync capacity")
	}
	root := roots.Private
	for _, part := range []string{"native-sync", b.Provider + "-" + b.SessionID, "journals"} {
		root = filepath.Join(root, part)
		if err := os.Mkdir(root, 0700); err != nil && !os.IsExist(err) {
			return "", err
		}
		info, err := os.Lstat(root)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("private native session staging is unavailable")
		}
	}
	return root, nil
}

func stageCrewNative(roots crewroot.Roots, b *sessiontransfer.Bundle) error {
	if b == nil {
		return nil
	}
	lock := crewNativeLock(roots, b.Provider+"-"+b.SessionID)
	lock.Lock()
	defer lock.Unlock()
	if err := b.ValidateFiles(); err != nil {
		return err
	}
	stage, err := crewNativeStage(roots, b)
	if err != nil {
		return err
	}
	for name := range b.Files {
		source := filepath.Join(b.Root, filepath.FromSlash(name))
		dest := filepath.Join(stage, filepath.FromSlash(name))
		if source == dest {
			continue
		}
		if err = os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
			return err
		}
		// Source files were checked by the provider exporter. Refuse any link
		// introduced after that check, including parent directory components.
		for p := source; p != b.Root; p = filepath.Dir(p) {
			i, e := os.Lstat(p)
			if e != nil || i.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("native journal changed during capture")
			}
		}
		in, e := os.Open(source)
		if e != nil {
			return e
		}
		out, e := os.CreateTemp(filepath.Dir(dest), ".capture-")
		if e != nil {
			in.Close()
			return e
		}
		_, e = io.CopyN(out, in, b.Files[name].Size)
		in.Close()
		if closeErr := out.Close(); e == nil {
			e = closeErr
		}
		if e == nil {
			e = os.Rename(out.Name(), dest)
		}
		os.Remove(out.Name())
		if e != nil {
			return e
		}
	}
	b.Root = stage
	if err = pruneCrewNativeStage(stage, b); err != nil {
		return err
	}
	if err = b.ValidateFiles(); err != nil {
		return err
	}
	return fsutil.WriteJSONAtomic(filepath.Join(filepath.Dir(stage), "manifest.json"), b, 0600)
}

func bindCrewNativeStage(roots crewroot.Roots, b *sessiontransfer.Bundle) error {
	if b == nil {
		return nil
	}
	stage, err := crewNativeStage(roots, b)
	if err != nil {
		return err
	}
	b.Root = stage
	return b.ValidateFiles()
}

// Native sessions move like the Team's shared files (owner, 6 Nov: "use what folder sync uses"): each session file is
// cut into content-defined chunks (crew_team_chunks.go), the server keeps a Crew's chunks once in its private store,
// and a transfer sends only the chunks the other side lacks. push: the Mac sends a session here (Publish, Move,
// standby copies). pull: the Mac takes back the server's newer copy of an agent it answered for while the Mac was off
// (Always on, crew_agent_handback.go); the server sends only the copy that handback_pull staged.

// crewNativeRequest names the session and, for plan and commit, each of its files' chunks.
type crewNativeRequest struct {
	Bundle sessiontransfer.Bundle         `json:"bundle"`
	Files  map[string]crewTeamFileVersion `json:"files,omitempty"`
}

// crewNativeChunkKeep: a chunk no staged session uses is removed after this long, so a transfer still sending keeps
// the chunks it already sent.
const crewNativeChunkKeep = time.Hour

func crewNativeChunkDir(roots crewroot.Roots) (string, error) {
	dir := filepath.Join(roots.Private, "native-sync", "chunks")
	return dir, os.MkdirAll(dir, 0o700)
}

func crewNativeVersionsPath(stage string) string {
	return filepath.Join(filepath.Dir(stage), "versions.json")
}

// crewNativeUsedChunks are the chunks of every session staged in this Crew: the only chunks the server sends.
func crewNativeUsedChunks(roots crewroot.Roots) map[string]bool {
	used := map[string]bool{}
	paths, _ := filepath.Glob(filepath.Join(roots.Private, "native-sync", "*", "versions.json"))
	for _, p := range paths {
		var files map[string]crewTeamFileVersion
		if raw, err := os.ReadFile(p); err == nil && json.Unmarshal(raw, &files) == nil {
			for _, v := range files {
				for _, c := range v.Chunks {
					used[c.ID] = true
				}
			}
		}
	}
	return used
}

func pruneCrewNativeChunks(roots crewroot.Roots, store string) {
	used := crewNativeUsedChunks(roots)
	_ = filepath.WalkDir(store, func(p string, d os.DirEntry, e error) error {
		if e != nil || d.IsDir() || used[d.Name()] {
			return nil
		}
		if info, err := d.Info(); err == nil && time.Since(info.ModTime()) > crewNativeChunkKeep {
			_ = os.Remove(p)
		}
		return nil
	})
}

// crewNativeFilesMatch: one well-formed version for each file the session names, within the sync capacity.
func crewNativeFilesMatch(b *sessiontransfer.Bundle, files map[string]crewTeamFileVersion) error {
	if len(files) != len(b.Files) {
		return fmt.Errorf("the session's files do not match its description")
	}
	var total int64
	for name := range b.Files {
		v, ok := files[name]
		if !ok || !v.valid() {
			return fmt.Errorf("the session's files do not match its description")
		}
		total += v.Size
	}
	if total > sessiontransfer.MaxBytes() {
		return fmt.Errorf("this native session exceeds the configured sync capacity")
	}
	return nil
}

// crewNativeChunkFiles cuts each of the session's files in dir into chunks, handing each chunk to put (when set): the
// files' versions and where each chunk lies. With skipMissing, a file dir does not have is left out.
func crewNativeChunkFiles(dir string, b *sessiontransfer.Bundle, skipMissing bool, put func(id string, data []byte) error) (map[string]crewTeamFileVersion, map[string]crewTeamChunkSource, error) {
	versions, sources := map[string]crewTeamFileVersion{}, map[string]crewTeamChunkSource{}
	for name := range b.Files {
		p, err := sessiontransfer.Path(dir, name, false)
		if err != nil {
			return nil, nil, err
		}
		f, err := os.Open(p)
		if err != nil && skipMissing {
			continue
		} else if err != nil {
			return nil, nil, err
		}
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			f.Close()
			return nil, nil, fmt.Errorf("native journal changed during capture")
		}
		var offset int64
		v, err := crewTeamChunkReader(f, info.Size(), false, func(id string, data []byte) error {
			sources[id] = crewTeamChunkSource{path: name, offset: offset, size: int64(len(data))}
			offset += int64(len(data))
			if put != nil {
				return put(id, data)
			}
			return nil
		})
		f.Close()
		if err != nil {
			return nil, nil, err
		}
		versions[name] = v
	}
	return versions, sources, nil
}

// crewNativeAssemble writes each file's version into dir from chunk: every file goes to a temporary file first and is
// checked, then all are renamed into place, so a failed transfer leaves dir as it was.
func crewNativeAssemble(dir string, files map[string]crewTeamFileVersion, chunk func(c crewTeamChunk) ([]byte, error)) error {
	temps := map[string]string{}
	defer func() {
		for _, tmp := range temps {
			_ = os.Remove(tmp)
		}
	}()
	for name, v := range files {
		dest, err := sessiontransfer.Path(dir, name, false)
		if err != nil {
			return err
		}
		if err = os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
			return err
		}
		out, err := os.CreateTemp(filepath.Dir(dest), ".capture-")
		if err != nil {
			return err
		}
		temps[dest] = out.Name()
		sum := sha256.New()
		w := io.MultiWriter(out, sum)
		for _, c := range v.Chunks {
			data, e := chunk(c)
			if e == nil {
				_, e = w.Write(data)
			}
			if e != nil {
				out.Close()
				return e
			}
		}
		if err = out.Close(); err != nil {
			return err
		}
		if hex.EncodeToString(sum.Sum(nil)) != v.Hash {
			return fmt.Errorf("a session file did not arrive whole; sync again")
		}
	}
	for dest, tmp := range temps {
		if err := os.Rename(tmp, dest); err != nil {
			return err
		}
		delete(temps, dest)
	}
	return nil
}

func (api *StreamingAPI) handleCrewNativeSync(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	op := mux.Vars(r)["op"]
	roots, err := api.crewSyncRoots(r.Context(), claims, mux.Vars(r)["project"])
	if err != nil {
		externalError(w, 403, "native_sync_unavailable", "Only this project's owner can sync its private session.")
		return
	}
	pair := r.URL.Query().Get("pair")
	binding, err := crewroot.ReadSync(roots.ID)
	if err != nil || binding == nil || binding.ArchiveOnly || binding.Side != "server" || binding.PairID != pair || binding.Editing != "local" {
		externalError(w, 409, "native_sync_unavailable", "Use the project's current sync connection.")
		return
	}
	store, err := crewNativeChunkDir(roots)
	if err != nil {
		externalError(w, 409, "native_sync_unavailable", "private native session staging is unavailable")
		return
	}
	start := time.Now()
	fail := func(status int, code string, err error) {
		log.Printf("[NATIVE_SYNC] project=%s op=%s status=%d error=%q", roots.ID, op, status, err)
		externalError(w, status, code, err.Error())
	}
	if op == "chunks" && r.Method == http.MethodPut {
		stored, bytes := 0, 0
		err = crewTeamReadFrames(io.LimitReader(r.Body, crewTeamChunkBatch+crewTeamChunkMax+64<<10), func(id string, data []byte) error {
			stored, bytes = stored+1, bytes+len(data)
			return crewTeamPutChunk(store, id, data)
		})
		if err != nil {
			fail(400, "native_sync_failed", err)
			return
		}
		log.Printf("[NATIVE_SYNC] project=%s op=send chunks=%d bytes=%d ms=%d", roots.ID, stored, bytes, time.Since(start).Milliseconds())
		externalJSON(w, map[string]int{"stored": stored})
		return
	}
	if op == "chunks" {
		// The chunks asked for, only of sessions staged here, at most crewTeamChunkBatch bytes; the caller asks again.
		var in struct {
			Chunks []crewTeamChunk `json:"chunks"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&in) != nil || len(in.Chunks) > 20000 {
			externalError(w, 400, "invalid_transfer", "Ask for the chunks by id and size.")
			return
		}
		used := crewNativeUsedChunks(roots)
		w.Header().Set("Content-Type", "application/octet-stream")
		sent, bytes := 0, int64(0)
		for _, c := range in.Chunks {
			if !validCrewTeamHash(c.ID) || !used[c.ID] {
				continue
			}
			if bytes > 0 && bytes+c.Len > crewTeamChunkBatch {
				break
			}
			data, err := crewTeamReadChunk(store, c)
			if err != nil {
				break
			}
			if err = crewTeamWriteFrame(w, c.ID, data); err != nil {
				return
			}
			sent, bytes = sent+1, bytes+int64(len(data))
		}
		log.Printf("[NATIVE_SYNC] project=%s op=receive chunks=%d bytes=%d ms=%d", roots.ID, sent, bytes, time.Since(start).Milliseconds())
		return
	}
	var in crewNativeRequest
	if json.NewDecoder(io.LimitReader(r.Body, 16<<20)).Decode(&in) != nil {
		externalError(w, 400, "invalid_transfer", "Send the session and its files.")
		return
	}
	b := &in.Bundle
	stage, err := crewNativeStage(roots, b)
	if err != nil {
		fail(409, crewSyncErrorCode(err), err)
		return
	}
	switch op {
	case "plan":
		if err = crewNativeFilesMatch(b, in.Files); err != nil {
			fail(400, "invalid_transfer", err)
			return
		}
		missing, seen := []string{}, map[string]bool{}
		for _, v := range in.Files {
			for _, c := range v.Chunks {
				if !seen[c.ID] && !crewTeamHasChunk(store, c) {
					missing = append(missing, c.ID)
				}
				seen[c.ID] = true
			}
		}
		log.Printf("[NATIVE_SYNC] project=%s provider=%s native_session=%s op=plan files=%d missing=%d", roots.ID, b.Provider, b.SessionID, len(in.Files), len(missing))
		externalJSON(w, map[string]any{"missing": missing})
	case "commit":
		if err = crewNativeFilesMatch(b, in.Files); err != nil {
			fail(400, "invalid_transfer", err)
			return
		}
		// Workspace, then session: the order file handoffs take. A competing transfer is retryable.
		unlock, err := wf.LockWorkspace(r.Context(), roots.Files)
		if err != nil {
			fail(409, "native_sync_busy", err)
			return
		}
		defer unlock()
		if current, e := crewroot.ReadSync(roots.ID); e != nil || current == nil || current.PairID != pair || current.Editing != "local" {
			fail(409, "native_sync_unavailable", fmt.Errorf("the active computer changed; sync again"))
			return
		}
		lock := crewNativeLock(roots, b.Provider+"-"+b.SessionID)
		if !lock.TryLock() {
			fail(409, "native_sync_busy", fmt.Errorf("this session is syncing; retry shortly"))
			return
		}
		defer lock.Unlock()
		err = crewNativeAssemble(stage, in.Files, func(c crewTeamChunk) ([]byte, error) { return crewTeamReadChunk(store, c) })
		b.Root = stage
		if err == nil {
			err = pruneCrewNativeStage(stage, b)
		}
		if err == nil {
			err = b.ValidateFiles()
		}
		if err == nil {
			err = fsutil.WriteJSONAtomic(crewNativeVersionsPath(stage), in.Files, 0600)
		}
		if err == nil {
			err = fsutil.WriteJSONAtomic(filepath.Join(filepath.Dir(stage), "manifest.json"), b, 0600)
		}
		if err != nil {
			fail(409, crewSyncErrorCode(err), err)
			return
		}
		pruneCrewNativeChunks(roots, store)
		log.Printf("[NATIVE_SYNC] project=%s provider=%s native_session=%s direction=push complete=true ms=%d", roots.ID, b.Provider, b.SessionID, time.Since(start).Milliseconds())
		externalJSON(w, map[string]bool{"complete": true})
	case "pull":
		lock := crewNativeLock(roots, b.Provider+"-"+b.SessionID)
		if !lock.TryLock() {
			fail(409, "native_sync_busy", fmt.Errorf("this session is syncing; retry shortly"))
			return
		}
		defer lock.Unlock()
		// Only the exact copy handback_pull staged is sent; a stage that changed since asks the Mac to start again.
		b.Root = stage
		if err = b.ValidateFiles(); err != nil {
			fail(409, "native_changed", fmt.Errorf("this conversation changed on the server; try again"))
			return
		}
		files, _, err := crewNativeChunkFiles(stage, b, false, func(id string, data []byte) error { return crewTeamPutChunk(store, id, data) })
		if err == nil {
			err = fsutil.WriteJSONAtomic(crewNativeVersionsPath(stage), files, 0600)
		}
		if err != nil {
			fail(409, crewSyncErrorCode(err), err)
			return
		}
		log.Printf("[NATIVE_SYNC] project=%s provider=%s native_session=%s op=pull files=%d ms=%d", roots.ID, b.Provider, b.SessionID, len(files), time.Since(start).Milliseconds())
		externalJSON(w, map[string]any{"files": files})
	default:
		externalError(w, 404, "not_found", "Unknown native sync step.")
	}
}

func desktopCrewNativeSync(ctx context.Context, roots crewroot.Roots, connection desktopConnection, binding *crewroot.SyncBinding, b *sessiontransfer.Bundle) error {
	return desktopCrewNativeTransfer(ctx, roots, connection, binding, b, "push")
}

// desktopCrewNativePull brings the server's copy of a native session (described by b, from handback_pull) into this
// Mac's native stage for it, fetching only the chunks the stage does not already hold. b.Root is the stage afterwards
// and its files match b.
func desktopCrewNativePull(ctx context.Context, roots crewroot.Roots, connection desktopConnection, binding *crewroot.SyncBinding, b *sessiontransfer.Bundle) error {
	return desktopCrewNativeTransfer(ctx, roots, connection, binding, b, "pull")
}

func desktopCrewNativeTransfer(ctx context.Context, roots crewroot.Roots, connection desktopConnection, binding *crewroot.SyncBinding, b *sessiontransfer.Bundle, direction string) error {
	if crewArchiveOnly(roots) {
		return fmt.Errorf("this computer continues from saved conversation history")
	}
	if b == nil {
		return nil
	}
	lock := crewNativeLock(roots, b.Provider+"-"+b.SessionID)
	lock.Lock()
	defer lock.Unlock()
	stage, err := crewNativeStage(roots, b)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	started := time.Now()
	route := "/api/crew-sync/" + url.PathEscape(binding.PeerProjectID) + "/native"
	query := "?" + url.Values{"pair": {binding.PairID}}.Encode()
	call := func(op string, in, out any) error {
		if err := crewTeamRemoteJSON(ctx, roots.Owner, connection, http.MethodPost, route+"/"+op+query, in, out); err != nil {
			log.Printf("[NATIVE_SYNC] project=%s native_session=%s direction=%s op=%s error=%q", roots.ID, b.SessionID, direction, op, err)
			return fmt.Errorf("the server could not sync this conversation: %w", err)
		}
		return nil
	}
	var moved int64
	if direction == "push" {
		if b.Root != stage {
			return fmt.Errorf("native export is not staged")
		}
		if err = b.ValidateFiles(); err != nil {
			return err
		}
		files, sources, err := crewNativeChunkFiles(stage, b, false, nil)
		if err != nil {
			return err
		}
		request := crewNativeRequest{Bundle: *b, Files: files}
		var plan struct {
			Missing []string `json:"missing"`
		}
		if err = call("plan", request, &plan); err != nil {
			return err
		}
		for i := 0; i < len(plan.Missing); {
			var frames bytes.Buffer
			for ; i < len(plan.Missing) && (frames.Len() == 0 || int64(frames.Len())+crewTeamChunkMax <= crewTeamChunkBatch); i++ {
				id := plan.Missing[i]
				source, ok := sources[id]
				if !ok {
					return fmt.Errorf("the server asked for a chunk this conversation does not have")
				}
				data, err := crewTeamReadSource(stage, source, id)
				if err == nil {
					err = crewTeamWriteFrame(&frames, id, data)
				}
				if err != nil {
					return err
				}
				moved += int64(len(data))
			}
			resp, err := crewTeamRemoteDo(ctx, roots.Owner, connection, http.MethodPut, route+"/chunks"+query, "application/octet-stream", &frames)
			if err != nil {
				return fmt.Errorf("the server could not sync this conversation: %w", err)
			}
			resp.Body.Close()
		}
		if err = call("commit", request, nil); err != nil {
			return err
		}
	} else {
		var pulled struct {
			Files map[string]crewTeamFileVersion `json:"files"`
		}
		if err = call("pull", crewNativeRequest{Bundle: *b}, &pulled); err != nil {
			return err
		}
		if err = crewNativeFilesMatch(b, pulled.Files); err != nil {
			return err
		}
		// What this stage already holds is read from it; only the rest is fetched, into private scratch first.
		_, local, err := crewNativeChunkFiles(stage, b, true, nil)
		if err != nil {
			return err
		}
		scratch, err := os.MkdirTemp(filepath.Dir(stage), "pull-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(scratch)
		need, seen := []crewTeamChunk{}, map[string]bool{}
		for _, v := range pulled.Files {
			for _, c := range v.Chunks {
				if _, ok := local[c.ID]; !ok && !seen[c.ID] {
					need = append(need, c)
				}
				seen[c.ID] = true
			}
		}
		gone, err := crewTeamFetchChunks(ctx, roots.Owner, connection, route, query, scratch, need)
		if err != nil {
			return fmt.Errorf("the server could not sync this conversation: %w", err)
		}
		if len(gone) > 0 {
			return fmt.Errorf("this conversation changed on the server; try again")
		}
		for _, c := range need {
			moved += c.Len
		}
		err = crewNativeAssemble(stage, pulled.Files, func(c crewTeamChunk) ([]byte, error) {
			if source, ok := local[c.ID]; ok {
				return crewTeamReadSource(stage, source, c.ID)
			}
			return os.ReadFile(filepath.Join(scratch, c.ID))
		})
		if err != nil {
			return err
		}
	}
	b.Root = stage
	if err = pruneCrewNativeStage(stage, b); err != nil {
		return err
	}
	if err = b.ValidateFiles(); err != nil {
		return err
	}
	if err = fsutil.WriteJSONAtomic(filepath.Join(filepath.Dir(stage), "manifest.json"), b, 0600); err != nil {
		return err
	}
	log.Printf("[NATIVE_SYNC] project=%s provider=%s native_session=%s direction=%s moved=%d complete=true ms=%d", roots.ID, b.Provider, b.SessionID, direction, moved, time.Since(started).Milliseconds())
	return nil
}
