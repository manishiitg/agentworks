package filesnapshots

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// RepoWatch tells a reply's capture which checkouts may have changed since it
// last read them, so the rest reuse that read instead of asking Git again
// (#3878: a project with ~20 checkouts spent 2-8 s of every reply on Git).
// The server backs it with the Changes view's file watcher (FSEvents on a
// Mac); a nil Watch reads every checkout, as on systems without one.
type RepoWatch interface {
	// Token moves whenever something that can change dir's Git state changes.
	// ok is false when dir is not watched: read it.
	Token(dir string) (token uint64, ok bool)
	// Located names dir's Git folders, so changes there (a commit in a linked
	// worktree) count for it too.
	Located(dir, gitDir, common string)
	// Settle returns once the changes made so far are counted, so an edit
	// just before a boundary is never missed.
	Settle()
}

// A checkout unchanged this long is read again anyway, so a missed event heals.
// Only the oldest few are redone per capture: checkouts first read together
// would otherwise all be redone by one reply (#3878 proof run).
const (
	repoReuseFor       = 30 * time.Minute
	repoRedoPerCapture = 2
)

type repoReads struct {
	mu    sync.Mutex
	reads map[string]repoRead
}

type repoRead struct {
	token uint64
	repo  gitTurnRepo
	at    time.Time
}

// reusedRepo is dir's last read while the watcher saw no change in it since;
// at is when that read happened.
func (s Store) reusedRepo(dir string, token uint64) (repo gitTurnRepo, at time.Time, ok bool) {
	c := &s.state().repos
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.reads[dir]
	if !ok {
		return gitTurnRepo{}, time.Time{}, false
	}
	if r.token != token {
		return gitTurnRepo{}, r.at, false
	}
	repo = r.repo
	repo.Dirty = append([]string(nil), r.repo.Dirty...)
	return repo, r.at, true
}

func (s Store) rememberRepo(dir string, token uint64, repo gitTurnRepo) {
	c := &s.state().repos
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.reads == nil {
		c.reads = map[string]repoRead{}
	}
	repo.Dirty = append([]string(nil), repo.Dirty...)
	c.reads[dir] = repoRead{token: token, repo: repo, at: time.Now()}
}

// commonGitDir is the repository's shared Git folder: a linked worktree's
// gitdir names it in its commondir file.
func commonGitDir(gitDir string) string {
	raw, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if err != nil {
		return gitDir
	}
	common := strings.TrimSpace(string(raw))
	if !filepath.IsAbs(common) {
		common = filepath.Join(gitDir, common)
	}
	return filepath.Clean(common)
}

// repoPlan is what one capture does with each checkout: reuse its last read,
// or read it and say why.
type repoPlan struct {
	token   uint64
	watched bool
	reuse   bool
	reason  string // new, changed, expired or unwatched
}

// planRepos decides which checkouts this capture reads (repo_watch.go).
func (s Store) planRepos(dirs []string) ([]repoPlan, []gitTurnRepo) {
	plans, reused := make([]repoPlan, len(dirs)), make([]gitTurnRepo, len(dirs))
	if s.Watch == nil {
		for i := range plans {
			plans[i].reason = "unwatched"
		}
		return plans, reused
	}
	s.Watch.Settle()
	type aged struct {
		i  int
		at time.Time
	}
	var expired []aged
	for i, dir := range dirs {
		token, ok := s.Watch.Token(dir)
		if !ok {
			plans[i].reason = "unwatched"
			continue
		}
		plans[i].token, plans[i].watched = token, true
		repo, at, hit := s.reusedRepo(dir, token)
		switch {
		case hit:
			plans[i].reuse, reused[i] = true, repo
			if time.Since(at) > repoReuseFor {
				expired = append(expired, aged{i, at})
			}
		case at.IsZero():
			plans[i].reason = "new"
		default:
			plans[i].reason = "changed"
		}
	}
	sort.Slice(expired, func(a, b int) bool { return expired[a].at.Before(expired[b].at) })
	for n, e := range expired {
		if n == repoRedoPerCapture {
			break
		}
		plans[e.i].reuse, plans[e.i].reason = false, "expired"
	}
	return plans, reused
}
