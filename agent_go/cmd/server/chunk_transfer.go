package server

// One way to move file bytes (owner, 6 Nov: "i want one sync process which is most effecient"): a file is split into
// content-defined chunks (restic/chunker, one fixed polynomial, so the same bytes always cut the same way), each chunk
// is stored once by its sha256 in its namespace's store (a Team's, a Crew's private one; never shared between them),
// and a file version is its ordered chunk list. A transfer plans first (the server answers which chunks it lacks,
// after the caller's access check for that namespace), sends only those in frames of at most chunkBatchBytes, then
// commits; the commit's caller checks the assembled files. Receiving fetches only the chunks the receiver lacks.
// Team sync (crew_team_*.go) and native sessions (crew_sync_native.go) use it.

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
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/workspace/chunks"
	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
)

// The pieces every side shares live in workspace/chunks; these names keep the server's code short.
type (
	transferChunk = chunks.Chunk
	chunkedFile   = chunks.File
)

const (
	chunkMaxBytes   = chunks.MaxBytes
	chunkBatchBytes = chunks.BatchBytes
)

var (
	chunkReader     = chunks.Split
	writeChunkFrame = chunks.WriteFrame
	readChunkFrames = chunks.ReadFrames
)

// syncFile is a version's file record (hash, size, execute bit) for Team's file state.
func syncFile(v chunkedFile) *crewsync.File {
	return &crewsync.File{Hash: v.Hash, Size: v.Size, Executable: v.Exec}
}

func chunkPath(store, id string) string { return filepath.Join(store, id[:2], id) }

// hasChunk, readChunk and putChunk refuse any id that is not a chunk hash themselves, so no caller can turn a client's
// id into another path (chunkPath joins it).
func hasChunk(store string, c transferChunk) bool {
	if !validCrewTeamHash(c.ID) {
		return false
	}
	info, err := os.Stat(chunkPath(store, c.ID))
	return err == nil && info.Mode().IsRegular() && info.Size() == c.Len
}

func putChunk(store, id string, data []byte) error {
	if !validCrewTeamHash(id) || len(data) == 0 || len(data) > chunkMaxBytes {
		return fmt.Errorf("invalid chunk")
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != id {
		return fmt.Errorf("a chunk does not match its id")
	}
	target := chunkPath(store, id)
	if info, err := os.Stat(target); err == nil && info.Size() == int64(len(data)) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(target), ".tmp-"+uuid.NewString())
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, target)
}

func readChunk(store string, c transferChunk) ([]byte, error) {
	if !validCrewTeamHash(c.ID) {
		return nil, fmt.Errorf("invalid chunk")
	}
	data, err := os.ReadFile(chunkPath(store, c.ID))
	if err != nil {
		return nil, err
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != c.ID || int64(len(data)) != c.Len {
		return nil, fmt.Errorf("a stored chunk is damaged")
	}
	return data, nil
}

// assembleChunked writes a version's bytes from the chunk store to w.
func assembleChunked(store string, v chunkedFile, w io.Writer) error {
	for _, c := range v.Chunks {
		data, err := readChunk(store, c)
		if err != nil {
			return err
		}
		if _, err = w.Write(data); err != nil {
			return err
		}
	}
	return nil
}

// chunkSource is where a chunk's bytes are in this copy: a file and the range inside it.
type chunkSource struct {
	path   string
	offset int64
	size   int64
}

// readChunkSource reads one chunk back from this copy and checks it is still the same bytes.
func readChunkSource(root string, source chunkSource, id string) ([]byte, error) {
	f, err := os.Open(filepath.Join(root, filepath.FromSlash(source.path)))
	if err != nil {
		return nil, fmt.Errorf("a file changed while it was sent; it goes next round")
	}
	defer f.Close()
	data := make([]byte, source.size)
	if _, err = f.ReadAt(data, source.offset); err != nil {
		return nil, fmt.Errorf("a file changed while it was sent; it goes next round")
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != id {
		return nil, fmt.Errorf("a file changed while it was sent; it goes next round")
	}
	return data, nil
}

// fetchChunks asks the server for chunks until all have arrived (each reply holds at most chunkBatchBytes
// bytes) and stages each, checked, by its id. It returns the chunks the server no longer serves: their file's version
// moved on since the page was read.
func fetchChunks(ctx context.Context, owner string, c desktopConnection, route, query, stage string, chunks []transferChunk) (map[string]bool, error) {
	var received int64
	moved := map[string]bool{}
	for len(chunks) > 0 {
		body, _ := json.Marshal(map[string]any{"chunks": chunks})
		resp, err := crewTeamRemoteDo(ctx, owner, c, http.MethodPost, route+"/chunks"+query, "application/json", bytes.NewReader(body))
		if err != nil {
			return moved, crewTeamStep("receive", err)
		}
		got := map[string]bool{}
		err = readChunkFrames(resp.Body, func(id string, data []byte) error {
			if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != id {
				return fmt.Errorf("a received chunk does not match its id")
			}
			got[id] = true
			received += int64(len(data))
			crewTeamRoundBytes(ctx, received)
			return os.WriteFile(filepath.Join(stage, id), data, 0o600)
		})
		resp.Body.Close()
		if err != nil {
			return moved, crewTeamStep("receive", err)
		}
		if len(got) == 0 {
			for _, ch := range chunks {
				moved[ch.ID] = true
			}
			return moved, nil
		}
		chunks = slices.DeleteFunc(chunks, func(ch transferChunk) bool { return got[ch.ID] })
	}
	return moved, nil
}

// chunkFiles cuts each named file into chunks, handing each chunk to put (when set): the files' versions and where
// each chunk lies (chunkSource.path is the name). pathOf gives a name's file; with skipMissing, a file that is not
// there is left out.
func chunkFiles(names []string, pathOf func(name string) (string, error), skipMissing bool, put func(id string, data []byte) error) (map[string]chunkedFile, map[string]chunkSource, error) {
	versions, sources := map[string]chunkedFile{}, map[string]chunkSource{}
	for _, name := range names {
		p, err := pathOf(name)
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
			return nil, nil, fmt.Errorf("a file changed while it was read")
		}
		var offset int64
		v, err := chunkReader(f, info.Size(), false, func(id string, data []byte) error {
			sources[id] = chunkSource{path: name, offset: offset, size: int64(len(data))}
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

// assembleFiles writes each file's version from chunk to the file destOf names: every file goes to a temporary file
// beside it first and is checked, then all are renamed into place, so a failed transfer changes nothing.
func assembleFiles(files map[string]chunkedFile, destOf func(name string) (string, error), chunk func(c transferChunk) ([]byte, error)) error {
	temps := map[string]string{}
	defer func() {
		for _, tmp := range temps {
			_ = os.Remove(tmp)
		}
	}()
	for name, v := range files {
		dest, err := destOf(name)
		if err != nil {
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
			return fmt.Errorf("a file did not arrive whole; sync again")
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

// pruneChunks removes the store's chunks no current version uses, once they are older than keep (a transfer still
// sending keeps the chunks it already sent).
func pruneChunks(store string, used map[string]bool, keep time.Duration) {
	_ = filepath.WalkDir(store, func(p string, d os.DirEntry, e error) error {
		if e != nil || d.IsDir() || used[d.Name()] {
			return nil
		}
		if info, err := d.Info(); err == nil && time.Since(info.ModTime()) > keep {
			_ = os.Remove(p)
		}
		return nil
	})
}

// missingChunks is the plan's answer: the chunks of files the store lacks, each once, in order. The caller has
// checked the request against its namespace's access rules first.
func missingChunks(store string, files []chunkedFile) []string {
	missing, seen := []string{}, map[string]bool{}
	for _, v := range files {
		for _, c := range v.Chunks {
			if !seen[c.ID] && !hasChunk(store, c) {
				missing = append(missing, c.ID)
			}
			seen[c.ID] = true
		}
	}
	return missing
}

// distinctChunks counts the different chunks of files: a transfer's planned chunks.
func distinctChunks(files []chunkedFile) int {
	seen := map[string]bool{}
	for _, v := range files {
		for _, c := range v.Chunks {
			seen[c.ID] = true
		}
	}
	return len(seen)
}

// receiveChunks stores the frames of a PUT .../chunks body (verified, each once).
func receiveChunks(body io.Reader, store string) (stored, size int, err error) {
	err = readChunkFrames(io.LimitReader(body, chunkBatchBytes+chunkMaxBytes+64<<10), func(id string, data []byte) error {
		stored, size = stored+1, size+len(data)
		return putChunk(store, id, data)
	})
	return stored, size, err
}

// serveChunks answers a POST .../chunks {chunks} with the frames of the ones readable allows, at most chunkBatchBytes
// (the caller asks again for the rest). readable is the caller's namespace rule: only chunks of what this person may
// read now, never any stored chunk by its hash. ok is false when the request was refused (the reply is written).
func serveChunks(w http.ResponseWriter, r *http.Request, store string, readable func() (map[string]bool, error)) (sent int, size int64, ok bool) {
	var in struct {
		Chunks []transferChunk `json:"chunks"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&in) != nil || len(in.Chunks) > 20000 {
		externalError(w, 400, "invalid_transfer", "Ask for the chunks by id and size.")
		return 0, 0, false
	}
	allowed, err := readable()
	if err != nil {
		externalError(w, 409, "transfer_failed", err.Error())
		return 0, 0, false
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	for _, c := range in.Chunks {
		if !validCrewTeamHash(c.ID) || !allowed[c.ID] {
			continue // not part of a file the caller may read now (its version moved on): the caller skips that file
		}
		if size > 0 && size+c.Len > chunkBatchBytes {
			break
		}
		data, err := readChunk(store, c)
		if err != nil {
			break // the caller asks again for what did not arrive
		}
		if err = writeChunkFrame(w, c.ID, data); err != nil {
			return sent, size, true
		}
		sent, size = sent+1, size+int64(len(data))
	}
	return sent, size, true
}

// errChunkNotOffered: the server asked for a chunk this transfer did not plan.
var errChunkNotOffered = errors.New("the server asked for a chunk this transfer did not send")

// sendMissingChunks PUTs the chunks the plan named to target (a .../chunks URL path with its query), read back from
// root through sources and checked, in frames of at most chunkBatchBytes. progress, when set, gets the bytes sent so
// far after each request.
func sendMissingChunks(ctx context.Context, owner string, c desktopConnection, target, root string, sources map[string]chunkSource, missing []string, progress func(sent int64)) (int, int64, error) {
	chunks, sent := 0, int64(0)
	for i := 0; i < len(missing); {
		var frames bytes.Buffer
		for ; i < len(missing) && (frames.Len() == 0 || int64(frames.Len())+chunkMaxBytes <= chunkBatchBytes); i++ {
			id := missing[i]
			source, ok := sources[id]
			if !ok {
				return chunks, sent, errChunkNotOffered
			}
			data, err := readChunkSource(root, source, id)
			if err == nil {
				err = writeChunkFrame(&frames, id, data)
			}
			if err != nil {
				return chunks, sent, err
			}
			chunks, sent = chunks+1, sent+int64(len(data))
		}
		resp, err := crewTeamRemoteDo(ctx, owner, c, http.MethodPut, target, "application/octet-stream", &frames)
		if err != nil {
			return chunks, sent, err
		}
		resp.Body.Close()
		if progress != nil {
			progress(sent)
		}
	}
	return chunks, sent, nil
}

// logChunkTransfer is the one line per transfer: which kind of store, what for, how many files, the chunks it planned,
// sent (or fetched) and reused, the bytes moved and how long it took.
func logChunkTransfer(kind, project, direction string, files, planned, moved int, size int64, started time.Time) {
	log.Printf("[CHUNK_TRANSFER] kind=%s project=%s direction=%s files=%d chunks=%d moved=%d reused=%d bytes=%d ms=%d",
		kind, project, direction, files, planned, moved, max(planned-moved, 0), size, time.Since(started).Milliseconds())
}
