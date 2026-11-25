package server

import (
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/filesnapshots"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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

var workspaceGitWatchGen atomic.Uint64

type workspaceGitWatch struct {
	root string
	w    crewTeamWatcher
	gen  uint64 // tells this watch's counters from an earlier, closed watch's

	mu       sync.Mutex
	closed   bool
	lastUsed time.Time
	repos    map[string]*workspaceGitCached // real checkout folder -> its last status

	wideLogAt time.Time

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
	w := &workspaceGitWatch{root: root, gen: workspaceGitWatchGen.Add(1), lastUsed: now, repos: map[string]*workspaceGitCached{}, listChanges: 1}
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

// workspaceGitCloneRepo copies the file list: callers filter it in place. A
// clean checkout keeps an empty list, never null: the Files view reads it.
func workspaceGitCloneRepo(repo workspaceGitRepo) workspaceGitRepo {
	repo.Files = append([]workspaceGitFile{}, repo.Files...)
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
	marked := 0
	for dir, c := range w.repos {
		if workspaceGitChangeAffects(dir, c, name) {
			c.changes++
			marked++
		}
	}
	if marked > 3 {
		w.logWide("change", name, marked)
	}
}

// logWide names a change that made several checkouts read again (#3878 proof run: every checkout lost its reuse at
// once), at most every 10 s per watch.
func (w *workspaceGitWatch) logWide(kind, name string, marked int) {
	if time.Since(w.wideLogAt) < 10*time.Second {
		return
	}
	w.wideLogAt = time.Now()
	rel, err := filepath.Rel(w.root, name)
	if err != nil || strings.HasPrefix(rel, "..") {
		rel = filepath.Base(name)
	}
	log.Printf("[WORKSPACE_GIT] event=reread_many kind=%s path=%q checkouts=%d", kind, filepath.ToSlash(rel), marked)
}

// workspaceGitChangeAffects: a change at name can alter the status of the checkout at dir. Inside the checkout's own
// folder it always can. Inside the shared Git folder of its repository only some parts matter: a linked worktree's
// own folder (worktrees/<name>) affects that worktree, a branch ref only the checkouts on that branch or tracking it
// (agents commit and fetch in one repository all the time, and most of a Crew folder's checkouts are its worktrees),
// packed-refs every checkout, and the rest (index, HEAD, config) only its main checkout; objects, logs and FETCH_HEAD
// affect none.
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
	case "objects", "logs", "worktrees", "FETCH_HEAD":
		return false
	case "refs":
		ref := strings.TrimSuffix(rel, ".lock")
		r := c.repo
		if r.Detached || r.Branch == "" {
			return false
		}
		return ref == "refs/heads/"+r.Branch ||
			(r.Upstream != "" && (ref == "refs/remotes/"+r.Upstream || ref == "refs/heads/"+r.Upstream))
	case "packed-refs":
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
	w.logWide("dropped_events", w.root, len(w.repos))
}

// workspaceGitStatusChanged marks a checkout, and every checkout sharing its repository, changed: called after a Git
// action from the app so the next status shows it without waiting for the watcher.
func workspaceGitStatusChanged(dir string) {
	workspaceGitWatches.Range(func(_, v any) bool {
		w := v.(*workspaceGitWatch)
		w.mu.Lock()
		if c := w.repos[dir]; c != nil {
			common, marked := c.common, 0
			for _, other := range w.repos {
				if other == c || (common != "" && other.common == common) {
					other.changes++
					marked++
				}
			}
			if marked > 3 {
				w.logWide("git_action", dir, marked)
			}
		}
		w.mu.Unlock()
		return true
	})
}

// Reply captures share the Changes view's watch (#3878): a checkout nothing touched since a capture read it reuses
// that read. crewSnapshotWatch adapts the watch to filesnapshots.RepoWatch.
type crewSnapshotWatch struct{ w *workspaceGitWatch }

// newCrewSnapshotWatch is the watch for a project folder; nil when this system has no tree watcher (Linux servers),
// so every checkout is read as before.
func newCrewSnapshotWatch(root string) filesnapshots.RepoWatch {
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil
	}
	w := workspaceGitWatchFor(real)
	if w == nil {
		return nil
	}
	return crewSnapshotWatch{w: w}
}

func (c crewSnapshotWatch) Token(dir string) (uint64, bool) {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return 0, false
	}
	w := c.w
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || !workspaceGitUnder(real, w.root) {
		return 0, false
	}
	e := w.repos[real]
	if e == nil {
		e = &workspaceGitCached{}
		w.repos[real] = e
	}
	return w.gen<<40 | e.changes, true
}

func (c crewSnapshotWatch) Located(dir, gitDir, common string) {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return
	}
	w := c.w
	w.mu.Lock()
	defer w.mu.Unlock()
	if e := w.repos[real]; e != nil && e.common == "" {
		if g, err := filepath.EvalSymlinks(gitDir); err == nil {
			gitDir = g
		}
		if g, err := filepath.EvalSymlinks(common); err == nil {
			common = g
		}
		e.gitDir, e.common = filepath.Clean(gitDir), filepath.Clean(common)
	}
}

func (c crewSnapshotWatch) Settle() {
	if s, ok := c.w.w.(interface{ settle() }); ok {
		s.settle()
		return
	}
	time.Sleep(100 * time.Millisecond) // Windows reports at once; let its reader note what arrived
}
