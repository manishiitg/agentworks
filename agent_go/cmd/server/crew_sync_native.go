package server

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
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

// crewNativeChunkKeep: a chunk no staged transfer uses is removed after this long, so a transfer still sending keeps
// the chunks it already sent (shared by the chunked uploads that prune their stores).
const crewNativeChunkKeep = time.Hour

// crewNativeFilesMatch: one well-formed version for each file the session names, within the sync capacity.
func crewNativeFilesMatch(b *sessiontransfer.Bundle, files map[string]chunkedFile) error {
	if len(files) != len(b.Files) {
		return fmt.Errorf("the session's files do not match its description")
	}
	var total int64
	for name := range b.Files {
		v, ok := files[name]
		if !ok || !v.Valid() {
			return fmt.Errorf("the session's files do not match its description")
		}
		total += v.Size
	}
	if total > sessiontransfer.MaxBytes() {
		return fmt.Errorf("this native session exceeds the configured sync capacity")
	}
	return nil
}

// crewNativeFiles cuts the session's files in dir into chunks (chunkFiles), handing each chunk to put when set.
func crewNativeFiles(dir string, b *sessiontransfer.Bundle, skipMissing bool, put func(id string, data []byte) error) (map[string]chunkedFile, map[string]chunkSource, error) {
	names := make([]string, 0, len(b.Files))
	for name := range b.Files {
		names = append(names, name)
	}
	return chunkFiles(names, func(name string) (string, error) { return sessiontransfer.Path(dir, name, false) }, skipMissing, put)
}

// crewNativeAssembleInto writes the session's files into dir (assembleFiles). A file the destination does not have
// yet is created; a link or special file in its place is refused.
func crewNativeAssembleInto(dir string, files map[string]chunkedFile, chunk func(c transferChunk) ([]byte, error)) error {
	return assembleFiles(files, func(name string) (string, error) { return sessiontransfer.Path(dir, name, true) }, chunk)
}
