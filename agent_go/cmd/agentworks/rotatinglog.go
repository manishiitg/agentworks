package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// rotatingLog is the log file of a shared folder: each run starts a new file, a file stops growing at max bytes, and only the
// newest keep older files are kept (path.1 newest), so a long-running share cannot fill the disk.
type rotatingLog struct {
	mu   sync.Mutex
	path string
	max  int64
	keep int
	file *os.File
	size int64
}

const (
	shareLogMaxBytes = 2 << 20
	shareLogKeep     = 3
	shareLogMaxAge   = 14 * 24 * time.Hour
)

func openRotatingLog(path string, max int64, keep int) (*rotatingLog, error) {
	r := &rotatingLog{path: path, max: max, keep: keep}
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		r.shift() // a new run starts a new file; the last run stays as path.1
	}
	return r, r.open()
}

func (r *rotatingLog) open() error {
	file, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	r.file, r.size = file, 0
	return nil
}

// shift renames path -> path.1 -> path.2 ... and drops what is beyond keep.
func (r *rotatingLog) shift() {
	_ = os.Remove(fmt.Sprintf("%s.%d", r.path, r.keep))
	for i := r.keep - 1; i >= 1; i-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", r.path, i), fmt.Sprintf("%s.%d", r.path, i+1))
	}
	_ = os.Rename(r.path, r.path+".1")
}

func (r *rotatingLog) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return 0, os.ErrClosed
	}
	if r.size > 0 && r.size+int64(len(p)) > r.max {
		_ = r.file.Close()
		r.shift()
		if err := r.open(); err != nil {
			return 0, err
		}
	}
	n, err := r.file.Write(p)
	r.size += int64(n)
	return n, err
}

func (r *rotatingLog) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return nil
	}
	err := r.file.Close()
	r.file = nil
	return err
}

// pruneStaleLogs removes logs and crash files in the shares folder that nothing has written for shareLogMaxAge (a folder that is
// no longer shared), so they do not pile up.
func pruneStaleLogs(dir string, running map[string]shareState) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.Contains(name, ".log") && !strings.HasSuffix(name, ".crash") {
			continue
		}
		key := strings.SplitN(name, ".", 2)[0]
		if _, alive := running[key]; alive {
			continue
		}
		if info, err := entry.Info(); err == nil && time.Since(info.ModTime()) > shareLogMaxAge {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}
