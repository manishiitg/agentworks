package server

// Team sync moves content-defined chunks (owner, 5 Nov, PLAT-856-535): a file is split into chunks by its content
// (restic/chunker, one fixed polynomial, so the same bytes always cut the same way), each chunk is stored once in the
// Team's chunk store by its sha256, and a file version is its ordered chunk list. A round sends only the chunks the
// server lacks and receives only the chunks the copy lacks, in requests of at most a few MB; a change of a few bytes in
// a large file moves one or two chunks, and history costs only new chunks. One path for every file size.

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
	"github.com/restic/chunker"
)

const (
	// The chunk boundaries are permanent for stored data: never change these.
	crewTeamChunkMin     = 16 << 10
	crewTeamChunkAvgBits = 16 // 64 KiB average
	crewTeamChunkMax     = 256 << 10
	crewTeamChunkPol     = chunker.Pol(0x3DA3358B4DC173)
	// crewTeamChunkBatch bounds the chunk bytes in one request or reply; crewTeamCommitBatch the files in one commit,
	// so every request finishes well inside the company gateway's 20 s.
	crewTeamChunkBatch  = 4 << 20
	crewTeamCommitBatch = 2000
	// The Team's own limits, not Publish's one-upload limit (owner, 6 Nov): sync moves only changed chunks after
	// the first copy, so a bigger folder costs a longer first download, not every round. Each file keeps
	// publication.MaxFileBytes.
	crewTeamMaxFiles       = 50000
	crewTeamMaxBytes int64 = 4 << 30
	// crewTeamSyncFormat names the chunked layout: a Team's content and every copy's binding start fresh with it.
	crewTeamSyncFormat = "chunks-1"
)

// crewTeamChunk is one chunk of a file version, in order.
type crewTeamChunk struct {
	ID  string `json:"id"`
	Len int64  `json:"n"`
}

// crewTeamFileVersion is a file's content: its whole-file sha256, size, execute bit and chunks.
type crewTeamFileVersion struct {
	Hash   string          `json:"hash"`
	Size   int64           `json:"size"`
	Exec   bool            `json:"exec,omitempty"`
	Chunks []crewTeamChunk `json:"chunks"`
}

func (v crewTeamFileVersion) file() *crewsync.File {
	return &crewsync.File{Hash: v.Hash, Size: v.Size, Executable: v.Exec}
}

// valid: a well-formed version whose chunks add up to its size.
func (v crewTeamFileVersion) valid() bool {
	if !validCrewTeamHash(v.Hash) || v.Size < 0 {
		return false
	}
	var total int64
	for _, c := range v.Chunks {
		if !validCrewTeamHash(c.ID) || c.Len <= 0 || c.Len > crewTeamChunkMax {
			return false
		}
		total += c.Len
	}
	return total == v.Size
}

// crewTeamChunkReader splits r (size bytes) into chunks, handing each to each, and hashes the whole file in the same
// pass. A file smaller than the minimum chunk is one chunk; an empty file has none.
func crewTeamChunkReader(r io.Reader, size int64, exec bool, each func(id string, data []byte) error) (crewTeamFileVersion, error) {
	whole := sha256.New()
	tee := io.TeeReader(r, whole)
	v := crewTeamFileVersion{Size: size, Exec: exec, Chunks: []crewTeamChunk{}}
	emit := func(data []byte) error {
		sum := sha256.Sum256(data)
		id := hex.EncodeToString(sum[:])
		v.Chunks = append(v.Chunks, crewTeamChunk{ID: id, Len: int64(len(data))})
		return each(id, data)
	}
	var read int64
	if size < crewTeamChunkMin {
		data, err := io.ReadAll(io.LimitReader(tee, crewTeamChunkMin))
		if err != nil {
			return v, err
		}
		read = int64(len(data))
		if len(data) > 0 {
			if err = emit(data); err != nil {
				return v, err
			}
		}
	} else {
		c := chunker.New(tee, crewTeamChunkPol, chunker.WithBoundaries(crewTeamChunkMin, crewTeamChunkMax), chunker.WithAverageBits(crewTeamChunkAvgBits))
		buf := make([]byte, crewTeamChunkMax)
		for {
			ch, err := c.Next(buf)
			if err == io.EOF {
				break
			}
			if err != nil {
				return v, err
			}
			read += int64(len(ch.Data))
			if err = emit(ch.Data); err != nil {
				return v, err
			}
		}
	}
	if read != size {
		return v, fmt.Errorf("the file changed while it was read")
	}
	v.Hash = hex.EncodeToString(whole.Sum(nil))
	return v, nil
}

// The server's chunk store: <content>/chunks/<id[:2]>/<id>, written once, verified on write and on read.
func crewTeamChunkStore(project string) (string, error) {
	d, e := crewTeamContentDir(project)
	if e != nil {
		return "", e
	}
	return filepath.Join(d, "chunks"), nil
}

func crewTeamChunkPath(store, id string) string { return filepath.Join(store, id[:2], id) }

func crewTeamHasChunk(store string, c crewTeamChunk) bool {
	info, err := os.Stat(crewTeamChunkPath(store, c.ID))
	return err == nil && info.Mode().IsRegular() && info.Size() == c.Len
}

func crewTeamPutChunk(store, id string, data []byte) error {
	if !validCrewTeamHash(id) || len(data) == 0 || len(data) > crewTeamChunkMax {
		return fmt.Errorf("invalid chunk")
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != id {
		return fmt.Errorf("a chunk does not match its id")
	}
	target := crewTeamChunkPath(store, id)
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

func crewTeamReadChunk(store string, c crewTeamChunk) ([]byte, error) {
	data, err := os.ReadFile(crewTeamChunkPath(store, c.ID))
	if err != nil {
		return nil, err
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != c.ID || int64(len(data)) != c.Len {
		return nil, fmt.Errorf("a stored chunk is damaged")
	}
	return data, nil
}

// crewTeamAssemble writes a version's bytes from the chunk store to w.
func crewTeamAssemble(store string, v crewTeamFileVersion, w io.Writer) error {
	for _, c := range v.Chunks {
		data, err := crewTeamReadChunk(store, c)
		if err != nil {
			return err
		}
		if _, err = w.Write(data); err != nil {
			return err
		}
	}
	return nil
}

// Chunk frames on the wire: 32-byte sha256, 4-byte big-endian length, the bytes; repeated.
func crewTeamWriteFrame(w io.Writer, id string, data []byte) error {
	raw, err := hex.DecodeString(id)
	if err != nil || len(raw) != 32 {
		return fmt.Errorf("invalid chunk id")
	}
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(data)))
	if _, err = w.Write(raw); err == nil {
		_, err = w.Write(size[:])
	}
	if err == nil {
		_, err = w.Write(data)
	}
	return err
}

func crewTeamReadFrames(r io.Reader, each func(id string, data []byte) error) error {
	for {
		var head [36]byte
		if _, err := io.ReadFull(r, head[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("incomplete chunk frame")
		}
		n := binary.BigEndian.Uint32(head[32:])
		if n == 0 || n > crewTeamChunkMax {
			return fmt.Errorf("invalid chunk frame")
		}
		data := make([]byte, n)
		if _, err := io.ReadFull(r, data); err != nil {
			return fmt.Errorf("incomplete chunk frame")
		}
		if err := each(hex.EncodeToString(head[:32]), data); err != nil {
			return err
		}
	}
}
