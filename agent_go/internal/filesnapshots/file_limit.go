package filesnapshots

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
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
	mu        sync.Mutex
	signature string
	dirs      []string
	report    FileLimitReport
	valid     bool
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
// known to be over the limit and its layout has not changed.
func (s Store) cachedOverLimit(signature string) ([]string, bool) {
	c := &s.state().limit
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.valid || !c.report.Over || c.signature != signature || time.Since(c.report.CheckedAt) > fileLimitRecheck {
		return nil, false
	}
	return append([]string(nil), c.dirs...), true
}

func (s Store) rememberFileLimit(signature string, dirs []string, files int, perFolder map[string]int) {
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
	c.signature, c.dirs, c.valid = signature, append([]string(nil), dirs...), true
	c.report = FileLimitReport{Over: files > maxTurnPaths, Files: files, Limit: maxTurnPaths, Checkouts: len(dirs), Folders: folders, CheckedAt: time.Now().UTC()}
}

// layoutSignature changes when a folder is added, removed or renamed in the
// project folder or one or two levels below it, which is where checkouts and
// worktrees get created. Reading these few folders costs milliseconds.
func (s Store) layoutSignature() string {
	var b strings.Builder
	var visit func(dir, rel string, depth int)
	visit = func(dir, rel string, depth int) {
		info, err := os.Stat(dir)
		if err != nil {
			return
		}
		b.WriteString(rel)
		b.WriteByte('@')
		b.WriteString(strconv.FormatInt(info.ModTime().UnixNano(), 36))
		b.WriteByte('\n')
		if depth == 2 {
			return
		}
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
			if _, err := os.Lstat(filepath.Join(dir, entry.Name(), ".git")); err == nil {
				continue // a checkout's own folders are Git's business
			}
			visit(filepath.Join(dir, entry.Name()), child, depth+1)
		}
	}
	visit(s.Root, "", 0)
	return b.String()
}
