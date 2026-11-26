// Package chunks is the one way AgentWorks moves file bytes (owner, 6 Nov: "one sync process"): a file is split into
// content-defined chunks (restic/chunker, one fixed polynomial, so the same bytes always cut the same way), each
// identified by its sha256, and a file version is its ordered chunk list. The server's stores, plans and commits are in
// agent_go/cmd/server/chunk_transfer.go; the CLI and the Mac app use this package to cut and frame what they send.
package chunks

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/restic/chunker"
)

const (
	// The chunk boundaries are permanent for stored data: never change these.
	MinBytes   = 16 << 10
	AvgBits    = 16 // 64 KiB average
	MaxBytes   = 256 << 10
	Polynomial = chunker.Pol(0x3DA3358B4DC173)
	// BatchBytes bounds the chunk bytes in one request or reply, so every request finishes well inside the
	// company gateway's 20 s.
	BatchBytes = 4 << 20
)

// Chunk is one chunk of a file version, in order.
type Chunk struct {
	ID  string `json:"id"`
	Len int64  `json:"n"`
}

// File is a file's content: its whole-file sha256, size, execute bit and chunks.
type File struct {
	Hash   string  `json:"hash"`
	Size   int64   `json:"size"`
	Exec   bool    `json:"exec,omitempty"`
	Chunks []Chunk `json:"chunks"`
}

// ValidID: a lowercase hex sha256, the only thing a chunk or file id may be.
func ValidID(value string) bool {
	b, e := hex.DecodeString(value)
	return e == nil && len(b) == 32 && value == strings.ToLower(value)
}

// Valid: a well-formed version whose chunks add up to its size.
func (v File) Valid() bool {
	if !ValidID(v.Hash) || v.Size < 0 {
		return false
	}
	var total int64
	for _, c := range v.Chunks {
		if !ValidID(c.ID) || c.Len <= 0 || c.Len > MaxBytes {
			return false
		}
		total += c.Len
	}
	return total == v.Size
}

// Split splits r (size bytes) into chunks, handing each to each, and hashes the whole file in the same
// pass. A file smaller than the minimum chunk is one chunk; an empty file has none.
func Split(r io.Reader, size int64, exec bool, each func(id string, data []byte) error) (File, error) {
	whole := sha256.New()
	tee := io.TeeReader(r, whole)
	v := File{Size: size, Exec: exec, Chunks: []Chunk{}}
	emit := func(data []byte) error {
		sum := sha256.Sum256(data)
		id := hex.EncodeToString(sum[:])
		v.Chunks = append(v.Chunks, Chunk{ID: id, Len: int64(len(data))})
		return each(id, data)
	}
	var read int64
	if size < MinBytes {
		data, err := io.ReadAll(io.LimitReader(tee, MinBytes))
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
		c := chunker.New(tee, Polynomial, chunker.WithBoundaries(MinBytes, MaxBytes), chunker.WithAverageBits(AvgBits))
		buf := make([]byte, MaxBytes)
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

// Chunk frames on the wire: 32-byte sha256, 4-byte big-endian length, the bytes; repeated.
func WriteFrame(w io.Writer, id string, data []byte) error {
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

func ReadFrames(r io.Reader, each func(id string, data []byte) error) error {
	var read int64 // bytes of the stream read so far, for the error: where it stopped and why
	for frames := 0; ; frames++ {
		var head [36]byte
		got, err := io.ReadFull(r, head[:])
		read += int64(got)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("incomplete chunk frame (after %d frames, %d bytes): %w", frames, read, err)
		}
		n := binary.BigEndian.Uint32(head[32:])
		if n == 0 || n > MaxBytes {
			return fmt.Errorf("invalid chunk frame")
		}
		data := make([]byte, n)
		got, err = io.ReadFull(r, data)
		read += int64(got)
		if err != nil {
			return fmt.Errorf("incomplete chunk frame (after %d frames, %d bytes, %d of %d in this one): %w", frames, read, got, n, err)
		}
		if err := each(hex.EncodeToString(head[:32]), data); err != nil {
			return err
		}
	}
}
