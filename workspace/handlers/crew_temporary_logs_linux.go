package handlers

import (
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const crewTemporaryLogBytes int64 = 50 << 20

// Background programs may redirect stdout themselves, bypassing shell capture.
// Sweep only Crew .tmp/*.log trees, never project data or provider journals.
func sweepCrewTemporaryLogs(docsDir string) {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("AGENTWORKS_DEPLOYMENT_MODE")), "local") {
		return
	}
	started := time.Now()
	root, err := os.OpenRoot(docsDir)
	if err != nil {
		return
	}
	defer root.Close()
	crewInfo, err := root.Lstat("Crew")
	if err != nil || !crewInfo.IsDir() || crewInfo.Mode()&os.ModeSymlink != 0 {
		return
	}
	crew, err := root.OpenRoot("Crew")
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		log.Printf("[DISK_CLEANUP] kind=crew_tmp_logs event=error reason=%q", err)
		return
	}
	defer crew.Close()
	openedCrew, err := crew.Stat(".")
	if err != nil || !os.SameFile(crewInfo, openedCrew) {
		return
	}
	entries, err := fs.ReadDir(crew.FS(), ".")
	if err != nil {
		return
	}
	count, scanned := 0, 0
	var freed int64
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		tmp := entry.Name() + "/.tmp"
		before, err := crew.Lstat(tmp)
		if err != nil || !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
			continue
		}
		tempRoot, err := crew.OpenRoot(tmp)
		if err != nil {
			continue
		}
		opened, err := tempRoot.Stat(".")
		if err != nil || !os.SameFile(before, opened) {
			tempRoot.Close()
			continue
		}
		fs.WalkDir(tempRoot.FS(), ".", func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if d.IsDir() {
				switch d.Name() {
				case "node_modules", "node-compile-cache", ".git", ".cache", "__pycache__":
					return fs.SkipDir
				}
				if strings.HasPrefix(d.Name(), "node-compile-cache-") {
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(d.Name(), ".log") {
				return nil
			}
			scanned++
			n, err := rotateCrewTemporaryLog(tempRoot, path, crewTemporaryLogBytes)
			if err != nil {
				log.Printf("[DISK_CLEANUP] kind=crew_tmp_logs event=error crew=%q log=%q reason=%q", entry.Name(), path, err)
				return nil
			}
			if n >= 0 {
				count++
				freed += n
			}
			return nil
		})
		tempRoot.Close()
	}
	log.Printf("[DISK_CLEANUP] kind=crew_tmp_logs event=done files=%d bytes_freed=%d limit_bytes=%d keep=2 scanned_logs=%d elapsed_ms=%d", count, freed, crewTemporaryLogBytes, scanned, time.Since(started).Milliseconds())
}

// Copytruncate retains the inode held by a running writer. Rotation is a size
// threshold checked by the existing sweep, not a per-write hard limit.
// A negative count means skipped; no caller may infer that any process stopped.
func rotateCrewTemporaryLog(root *os.Root, path string, limit int64) (freed int64, err error) {
	before, err := root.Lstat(path)
	if err != nil {
		return -1, err
	}
	if !before.Mode().IsRegular() || before.Size() <= limit {
		return -1, nil
	}
	original, err := root.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return -1, err
	}
	defer original.Close()
	opened, err := original.Stat()
	if err != nil {
		return -1, err
	}
	stat, ok := opened.Sys().(*syscall.Stat_t)
	if !os.SameFile(before, opened) || !ok || stat.Nlink != 1 {
		return -1, fmt.Errorf("log inode changed or is hard-linked")
	}
	footprint := func() int64 {
		var n int64
		for _, p := range []string{path, path + ".1", path + ".2"} {
			if st, e := root.Lstat(p); e == nil && st.Mode().IsRegular() {
				if raw, ok := st.Sys().(*syscall.Stat_t); ok {
					n += raw.Blocks * 512
				}
			}
		}
		return n
	}
	bytesBefore := footprint()
	tmp := filepath.ToSlash(filepath.Join(filepath.Dir(path), fmt.Sprintf(".log-rotation-%d-%d", os.Getpid(), time.Now().UnixNano())))
	archive, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return -1, err
	}
	defer root.Remove(tmp)
	size := opened.Size()
	offset := size - limit
	if offset < 0 {
		offset = 0
	}
	_, copyErr := io.Copy(archive, io.NewSectionReader(original, offset, size-offset))
	closeErr := archive.Close()
	if copyErr != nil {
		return -1, copyErr
	}
	if closeErr != nil {
		return -1, closeErr
	}
	// Rename never follows the old archive's symlink target. The new archive was
	// created exclusively through the rooted handle with private permissions.
	if err = root.Rename(path+".1", path+".2"); err != nil && !os.IsNotExist(err) {
		return -1, err
	}
	if err = root.Rename(tmp, path+".1"); err != nil {
		return -1, err
	}
	if err = original.Truncate(0); err != nil {
		return -1, err
	}
	freed = bytesBefore - footprint()
	if freed < 0 {
		freed = 0
	}
	return freed, nil
}
