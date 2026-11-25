package filesnapshots

import (
	"os"
	"path/filepath"
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
const repoReuseFor = 3 * time.Minute

type repoReads struct {
	mu    sync.Mutex
	reads map[string]repoRead
}

type repoRead struct {
	token uint64
	repo  gitTurnRepo
	at    time.Time
}

func (s Store) reusedRepo(dir string, token uint64) (gitTurnRepo, bool) {
	c := &s.state().repos
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.reads[dir]
	if !ok || r.token != token || time.Since(r.at) > repoReuseFor {
		return gitTurnRepo{}, false
	}
	repo := r.repo
	repo.Dirty = append([]string(nil), r.repo.Dirty...)
	return repo, true
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
