package crewsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/chunks"
)

// CachedFile remembers a stable local file version, never its contents or a
// destination's acknowledgement. The caller still applies its capture policy.
type CachedFile struct {
	File        File           `json:"file"`
	Stamp       Stamp          `json:"stamp"`
	ReadStarted time.Time      `json:"read_started"`
	Chunks      []chunks.Chunk `json:"chunks"`
}

type Cache map[string]CachedFile

// ErrFileChanged asks a handover caller to retry the file, not omit it.
var ErrFileChanged = errors.New("file changed while being read")

func LoadCache(path string) (Cache, error) {
	c := Cache{}
	raw, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(raw, &c) != nil || c == nil {
		return Cache{}, nil // derived metadata can always be rebuilt
	}
	return c, nil
}

func (c Cache) Save(path string) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".sync-cache-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(raw)
	closed := f.Close()
	if err != nil {
		return err
	}
	if closed != nil {
		return closed
	}
	return os.Rename(f.Name(), path) // CreateTemp is 0600
}

// A rooted file-open boundary also lets the regression count actual content
// opens, separately from metadata checks.
var openCachedFile = (*os.Root).Open

// Read reuses only a stable local version, otherwise computes the raw sha256
// and content-defined chunks together in one bounded pass. It stages no bytes.
func (c Cache) Read(ctx context.Context, root, rel, key string) (CachedFile, bool, error) {
	if err := ctx.Err(); err != nil {
		return CachedFile{}, false, err
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		return CachedFile{}, false, err
	}
	defer dir.Close()
	info, err := dir.Lstat(rel)
	if err != nil {
		return CachedFile{}, false, err
	}
	limit := SyncLimits(ctx).FileBytes
	if !info.Mode().IsRegular() || info.Size() > limit {
		return CachedFile{}, false, fmt.Errorf("unsupported file or file exceeds sync limit: %s", key)
	}
	current := StampOf(info)
	known, had := c[key]
	version := chunks.File{Hash: known.File.Hash, Size: known.File.Size, Exec: known.File.Executable, Chunks: known.Chunks}
	if had && known.File.Size == current.Size && version.Valid() && known.Stamp.Reusable(current, known.ReadStarted) {
		return known, false, nil
	}
	entry := CachedFile{ReadStarted: time.Now(), Stamp: current}
	f, err := openCachedFile(dir, rel)
	if err != nil {
		return entry, false, err
	}
	defer f.Close()
	opened, err := f.Stat()
	changed := func() (CachedFile, bool, error) {
		return entry, true, fmt.Errorf("%w: %s", ErrFileChanged, key)
	}
	if err != nil || !os.SameFile(info, opened) || StampOf(opened) != current {
		return changed()
	}
	executable := info.Mode()&0111 != 0
	if runtime.GOOS == "windows" && had {
		executable = known.File.Executable
	}
	version, err = chunks.Split(&contextReader{ctx, io.LimitReader(f, limit+1)}, info.Size(), executable, func(string, []byte) error { return nil })
	if err != nil {
		if ctx.Err() != nil {
			return entry, true, ctx.Err()
		}
		return changed()
	}
	after, err := f.Stat()
	if err != nil || StampOf(after) != current {
		return changed()
	}
	pathInfo, err := dir.Lstat(rel)
	if err != nil || !os.SameFile(info, pathInfo) || StampOf(pathInfo) != current {
		return changed()
	}
	entry.File = File{Hash: version.Hash, Size: version.Size, Executable: version.Exec}
	entry.Chunks = version.Chunks
	if c != nil {
		c[key] = entry
	}
	return entry, true, nil
}
