package filesnapshots

import (
	"context"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// A project folder over the reply file limit is decided once, not on every
// reply (owner, #3878): the verdict, its checkouts and the biggest folders are
// kept until the folder's layout changes, it gets old, or someone asks to
// check again. Under the limit the walk still runs each reply, because the
// loose files themselves are the capture.
const fileLimitRecheck = 10 * time.Minute

// FolderFiles is one top-level folder and how many loose files (outside its
// Git checkouts) it holds.
type FolderFiles struct {
	Path  string `json:"path"`
	Files int    `json:"files"`
}

// FileLimitReport is the project's stored verdict, for the panel and tools.
type FileLimitReport struct {
	Over      bool          `json:"over"`
	Files     int           `json:"files"`
	Limit     int           `json:"limit"`
	Checkouts int           `json:"checkouts"`
	Folders   []FolderFiles `json:"folders,omitempty"`
	CheckedAt time.Time     `json:"checked_at"`
}

type fileLimitCache struct {
	mu      sync.Mutex
	folders map[string]bool
	dirs    []string
	report  FileLimitReport
	valid   bool
}

// FileLimit returns the last decided verdict for this project, if any.
func (s Store) FileLimit() (FileLimitReport, bool) {
	c := &s.state().limit
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.report, c.valid
}

// RecheckFileLimit forgets the verdict so the next reply decides again.
func (s Store) RecheckFileLimit() {
	c := &s.state().limit
	c.mu.Lock()
	c.valid = false
	c.mu.Unlock()
}

// cachedOverLimit returns the remembered checkouts while the project is still
// known to be over the limit. The verdict survives everyday churn (#3878 proof
// run): files saved near the root and worktrees agents add or remove keep it.
// Only removed folders' checkouts are dropped and added folders are searched
// for new ones, so a reply skips the full walk. Removing one of the biggest
// folders (Move out) or the 10-minute age decides the verdict again.
func (s Store) cachedOverLimit(ctx context.Context) ([]string, bool) {
	current := s.layoutFolders()
	c := &s.state().limit
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.valid || !c.report.Over || time.Since(c.report.CheckedAt) > fileLimitRecheck {
		return nil, false
	}
	big := map[string]bool{}
	for _, f := range c.report.Folders {
		big[f.Path] = true
	}
	var removed, added []string
	for p := range c.folders {
		if !current[p] {
			if big[p] {
				return nil, false // a big folder left: the count may be under now
			}
			removed = append(removed, p)
		}
	}
	for p := range current {
		parent := path.Dir(p)
		if !c.folders[p] && !(current[parent] && !c.folders[parent]) {
			added = append(added, p) // topmost new folder; its subtree is searched
		}
	}
	if len(removed) > 0 || len(added) > 0 {
		dirs := c.dirs[:0:0]
		for _, dir := range c.dirs {
			rel, _ := filepath.Rel(s.Root, dir)
			rel = filepath.ToSlash(rel)
			gone := false
			for _, r := range removed {
				if rel == r || strings.HasPrefix(rel, r+"/") {
					gone = true
					break
				}
			}
			if !gone {
				dirs = append(dirs, dir)
			}
		}
		known := map[string]bool{}
		for _, dir := range dirs {
			known[dir] = true
		}
		sort.Strings(added)
		for _, rel := range added {
			found, err := s.findCheckouts(ctx, rel)
			if err != nil {
				return nil, false
			}
			for _, dir := range found {
				if !known[dir] {
					known[dir] = true
					dirs = append(dirs, dir)
				}
			}
		}
		c.dirs, c.folders = dirs, current
		c.report.Checkouts = len(dirs)
	}
	return append([]string(nil), c.dirs...), true
}

// findCheckouts lists the checkouts at or below one added folder, by the same
// rules as the full walk in turnPaths.
func (s Store) findCheckouts(ctx context.Context, rel string) ([]string, error) {
	var dirs []string
	start := filepath.Join(s.Root, filepath.FromSlash(rel))
	err := filepath.WalkDir(start, func(p string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) {
				return filepath.SkipDir // removed again meanwhile
			}
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		r, err := filepath.Rel(s.Root, p)
		if err != nil {
			return err
		}
		if excluded(filepath.ToSlash(r), true) {
			return filepath.SkipDir
		}
		if _, err := os.Lstat(filepath.Join(p, ".git")); err == nil {
			dirs = append(dirs, p)
			return filepath.SkipDir
		}
		return nil
	})
	return dirs, err
}

func (s Store) rememberFileLimit(folderSet map[string]bool, dirs []string, files int, perFolder map[string]int) {
	folders := make([]FolderFiles, 0, len(perFolder))
	for p, n := range perFolder {
		folders = append(folders, FolderFiles{Path: p, Files: n})
	}
	sort.Slice(folders, func(i, j int) bool {
		if folders[i].Files != folders[j].Files {
			return folders[i].Files > folders[j].Files
		}
		return folders[i].Path < folders[j].Path
	})
	if len(folders) > 5 {
		folders = folders[:5]
	}
	c := &s.state().limit
	c.mu.Lock()
	defer c.mu.Unlock()
	c.folders, c.dirs, c.valid = folderSet, append([]string(nil), dirs...), true
	c.report = FileLimitReport{Over: files > maxTurnPaths, Files: files, Limit: maxTurnPaths, Checkouts: len(dirs), Folders: folders, CheckedAt: time.Now().UTC()}
}

// layoutFolders names the folders in the project folder and one level below
// it, checkouts included, which is where checkouts and worktrees get created.
// Names, not modification times: saving a file must not count as a change.
// Reading these few folders costs milliseconds.
func (s Store) layoutFolders() map[string]bool {
	out := map[string]bool{}
	var visit func(dir, rel string, depth int)
	visit = func(dir, rel string, depth int) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			child := entry.Name()
			if rel != "" {
				child = rel + "/" + entry.Name()
			}
			if excluded(child, true) {
				continue
			}
			out[child] = true
			if depth == 1 {
				continue
			}
			if _, err := os.Lstat(filepath.Join(dir, entry.Name(), ".git")); err == nil {
				continue // a checkout's own folders are Git's business
			}
			visit(filepath.Join(dir, entry.Name()), child, depth+1)
		}
	}
	visit(s.Root, "", 0)
	return out
}

// CheckFileLimit decides the verdict again now ("Check again" on the Git row).
func (s Store) CheckFileLimit(ctx context.Context) (FileLimitReport, error) {
	s.RecheckFileLimit()
	var stats CaptureStats
	if _, _, err := s.turnPaths(ctx, &stats); err != nil {
		return FileLimitReport{}, err
	}
	report, _ := s.FileLimit()
	return report, nil
}
