package server

import (
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// The Changes view asks for every checkout's status when it opens, on window focus and after each file tree reload.
// A Crew folder can hold dozens of agent worktrees, and one git status pass over all of them cost a Mac about 20 s of
// CPU, nearly all of it for checkouts nothing touched (#3220). So each folder the view shows gets one file watcher (the
// tree watcher Team sync uses: FSEvents on a Mac, ReadDirectoryChangesW on Windows) and each checkout keeps its last
// status until the watcher sees a change inside it or its Git folder. Git actions from the app mark their checkout
// changed at once, a checkout unchanged for workspaceGitReuseFor is read again anyway (a missed event heals), and a
// folder nobody asked about for workspaceGitWatchIdle loses its watcher. Systems without a tree watcher (Linux
// servers) read every checkout each time, as before.

const (
	workspaceGitReuseFor  = 3 * time.Minute
	workspaceGitWatchIdle = 10 * time.Minute
	// A create, remove or rename this many folders below the watched folder or less can add or drop a checkout
	// (search depth plus an agent worktree folder like .claude/worktrees/<name>).
	workspaceGitListDepth = workspaceGitSearchDepth + 3
)

// workspaceGitWatches: real folder the Changes view shows -> its watch.
var workspaceGitWatches sync.Map

type workspaceGitWatch struct {
	root string
	w    crewTeamWatcher

	mu       sync.Mutex
	closed   bool
	lastUsed time.Time
	repos    map[string]*workspaceGitCached // real checkout folder -> its last status

	listChanges, listRead uint64 // the checkout list is fresh while these match
	list                  []string
	listMore              bool
	listAt                time.Time
}

type workspaceGitCached struct {
	repo          workspaceGitRepo
	gitDir        string
	common        string
	changes, read uint64 // fresh while read == changes
	at            time.Time
	has           bool
}

// workspaceGitWatchFor is the watch of the folder at root, started on first use; nil when this system has no tree
// watcher or the folder cannot be watched. It also closes the watches no one asked for lately.
func workspaceGitWatchFor(root string) *workspaceGitWatch {
	if !crewTeamTreeWatching {
		return nil
	}
	now := time.Now()
	workspaceGitWatches.Range(func(k, v any) bool {
		if w := v.(*workspaceGitWatch); k != root && w.idleSince(now) > workspaceGitWatchIdle {
			workspaceGitWatches.Delete(k)
			w.close()
		}
		return true
	})
	if v, ok := workspaceGitWatches.Load(root); ok {
		w := v.(*workspaceGitWatch)
		w.mu.Lock()
		w.lastUsed = now
		w.mu.Unlock()
		return w
	}
	w := &workspaceGitWatch{root: root, lastUsed: now, repos: map[string]*workspaceGitCached{}, listChanges: 1}
	fw, err := newCrewTeamWatcher(root, w)
	if err != nil {
		log.Printf("[WORKSPACE_GIT] event=watch_off reason=%q", err)
		return nil
	}
	w.w = fw
	if prior, loaded := workspaceGitWatches.LoadOrStore(root, w); loaded {
		_ = fw.Close()
		return prior.(*workspaceGitWatch)
	}
	return w
}

func (w *workspaceGitWatch) idleSince(now time.Time) time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	return now.Sub(w.lastUsed)
}

func (w *workspaceGitWatch) close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.closed {
		w.closed = true
		_ = w.w.Close()
	}
}

// findRepos is workspaceGitFindRepos, reused until a create, remove or rename near the top of the folder.
func (w *workspaceGitWatch) findRepos() ([]string, bool) {
	w.mu.Lock()
	if w.listRead == w.listChanges && time.Since(w.listAt) < workspaceGitReuseFor {
		list, more := append([]string(nil), w.list...), w.listMore
		w.mu.Unlock()
		return list, more
	}
	token := w.listChanges
	w.mu.Unlock()
	list, more := workspaceGitFindRepos(w.root)
	w.mu.Lock()
	w.list, w.listMore, w.listRead, w.listAt = list, more, token, time.Now()
	w.mu.Unlock()
	return append([]string(nil), list...), more
}

// cached is the checkout's last status while nothing changed in it since it was read.
func (w *workspaceGitWatch) cached(dir string) (workspaceGitRepo, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	c := w.repos[dir]
	if c == nil || !c.has || c.read != c.changes || time.Since(c.at) > workspaceGitReuseFor {
		return workspaceGitRepo{}, false
	}
	return workspaceGitCloneRepo(c.repo), true
}

// begin marks the start of a read of dir; store keeps the result unless a change arrived meanwhile.
func (w *workspaceGitWatch) begin(dir string) uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	c := w.repos[dir]
	if c == nil {
		c = &workspaceGitCached{}
		w.repos[dir] = c
	}
	return c.changes
}

func (w *workspaceGitWatch) store(dir string, token uint64, repo workspaceGitRepo, gitDir, common string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	c := w.repos[dir]
	if c == nil {
		return
	}
	c.repo, c.read, c.at, c.has = workspaceGitCloneRepo(repo), token, time.Now(), true
	c.gitDir, c.common = filepath.Clean(gitDir), filepath.Clean(common)
}

// workspaceGitCloneRepo copies the file list: callers filter it in place.
func workspaceGitCloneRepo(repo workspaceGitRepo) workspaceGitRepo {
	repo.Files = append([]workspaceGitFile(nil), repo.Files...)
	return repo
}

// note: a change at name (a full path under the watched folder) makes stale the checkouts it can affect.
func (w *workspaceGitWatch) note(name string, structural bool) {
	name = filepath.Clean(name)
	w.mu.Lock()
	defer w.mu.Unlock()
	if structural {
		if rel, err := filepath.Rel(w.root, name); err == nil && strings.Count(filepath.ToSlash(rel), "/") < workspaceGitListDepth {
			w.listChanges++
		}
	}
	for dir, c := range w.repos {
		if workspaceGitChangeAffects(dir, c, name) {
			c.changes++
		}
	}
}

// workspaceGitChangeAffects: a change at name can alter the status of the checkout at dir. Inside the checkout's own
// folder it always can. Inside the shared Git folder of its repository only some parts matter: a linked worktree's
// own folder (worktrees/<name>) affects that worktree, branches and remotes affect every checkout of the repository,
// and the rest (index, HEAD, config) only its main checkout; objects and logs affect none.
func workspaceGitChangeAffects(dir string, c *workspaceGitCached, name string) bool {
	if c.common == "" || !workspaceGitUnder(name, c.common) {
		return workspaceGitUnder(name, dir)
	}
	if c.gitDir != c.common && workspaceGitUnder(name, c.gitDir) {
		return true // this linked worktree's own index and HEAD
	}
	rel := filepath.ToSlash(strings.TrimPrefix(strings.TrimPrefix(name, c.common), string(filepath.Separator)))
	first, _, _ := strings.Cut(rel, "/")
	switch first {
	case "objects", "logs", "worktrees":
		return false
	case "refs", "packed-refs", "FETCH_HEAD":
		return true
	}
	return c.gitDir == c.common
}

func workspaceGitUnder(name, dir string) bool {
	return name == dir || strings.HasPrefix(name, dir+string(filepath.Separator))
}

// overflow: the system dropped events, so every checkout is read again.
func (w *workspaceGitWatch) overflow() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.listChanges++
	for _, c := range w.repos {
		c.changes++
	}
}

// workspaceGitStatusChanged marks a checkout, and every checkout sharing its repository, changed: called after a Git
// action from the app so the next status shows it without waiting for the watcher.
func workspaceGitStatusChanged(dir string) {
	workspaceGitWatches.Range(func(_, v any) bool {
		w := v.(*workspaceGitWatch)
		w.mu.Lock()
		if c := w.repos[dir]; c != nil {
			common := c.common
			for _, other := range w.repos {
				if other == c || (common != "" && other.common == common) {
					other.changes++
				}
			}
		}
		w.mu.Unlock()
		return true
	})
}
