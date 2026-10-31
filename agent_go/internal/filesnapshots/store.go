// Package filesnapshots keeps turn comparisons in a private Git repository.
// It never writes to a project's files, Git directory, index or branches.
package filesnapshots

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/safegit"
	"github.com/manishiitg/coding-agent-loop/workspace/publication"
)

const MaxFileBytes = 25 << 20

// maxImportBytes bounds the new contents one capture reads and imports;
// files reused from the persisted index cost nothing. It keeps a capture whose
// turn wrote a lot (generated assets, dumps) inside its deadline (15 s for an
// after-capture) as an honest partial record with BudgetSkipped files instead
// of a timeout that saves nothing, bounds what one turn adds to the private
// store, and is the batch size of the resumable initial index build (Warm),
// so a build stopped by its limit keeps each finished batch. Measured on a
// 67k-file, 17-repo Crew: a cold 256 MiB batch of about 50k files took 16.7 s,
// mostly per-file cost; a warm capture's changed set is far below it.
const maxImportBytes = 256 << 20
const maxDiffBytes = 1 << 20

// Replies kept per Crew, across all its chats (plus reviewed ones, up to 14
// days). Unchanged files are shared, so each kept reply costs about the files
// it changed (0.05-1.5 MB per reply measured).
const keepTurns = 200

// Record contains no prompt, transcript or account credentials.
type Record struct {
	Reviewed           bool              `json:"-"`
	BeforeSkippedState map[string]string `json:"-"`
	AfterSkippedState  map[string]string `json:"-"`
	ID                 string            `json:"id"`
	ExecutionID        string            `json:"execution_id"`
	StartedAt          time.Time         `json:"started_at"`
	CompletedAt        *time.Time        `json:"completed_at,omitempty"`
	Before             string            `json:"-"`
	After              string            `json:"-"`
	Status             string            `json:"status"`
	Skipped            []string          `json:"skipped,omitempty"`
	Omitted            []string          `json:"-"`
	BeforeCapture      CaptureStats      `json:"before_capture"`
	AfterCapture       CaptureStats      `json:"after_capture"`
	// Git checkouts present at the before-capture (Crew-relative folders), and
	// what the after-capture found about checkouts that appeared or vanished
	// during the turn (repo_baselines.go).
	BeforeRepos     []string          `json:"-"`
	NewRepos        map[string]string `json:"-"`
	GoneRepos       []string          `json:"-"`
	BaselineSkipped []string          `json:"-"`
	// BeforeHeads is each checkout's HEAD commit at the before-capture (a new
	// checkout: the commit it was checked out at). A chat's earliest head of a
	// checkout is where its "work done in this chat" starts.
	BeforeHeads map[string]string        `json:"-"`
	BeforeGit   map[string]gitCheckpoint `json:"-"`
	Review      *savedReview             `json:"-"`
	diffPaths   []string
}

// CaptureStats explains partial captures without exposing file names or contents.
type CaptureStats struct {
	Eligible      int   `json:"eligible"`
	Captured      int   `json:"captured"`
	Reused        int   `json:"reused"`
	ImportedBytes int64 `json:"imported_bytes"`
	SizeSkipped   int   `json:"size_skipped"`
	Cold          bool  `json:"cold,omitempty"` // no persisted index existed: every file was read
	// Changed counts files read and imported this capture; Removed counts index
	// entries dropped (deleted, excluded or skipped). IndexesReused/IndexesNew
	// count checkouts whose persisted private index was reused or created.
	Changed       int `json:"changed"`
	Removed       int `json:"removed"`
	IndexesReused int `json:"indexes_reused"`
	IndexesNew    int `json:"indexes_new"`
	Batches       int `json:"batches,omitempty"` // background initial index build rounds
	// Deferred names why a send-path capture was left to the background index
	// build (Warm): "cold" (not attempted) or "budget" (ran out of time).
	Deferred      string `json:"deferred,omitempty"`
	BudgetSkipped int    `json:"budget_skipped"`
	ReadSkipped   int    `json:"read_skipped"`
	// Where the candidate list came from: Git checkouts list their own files
	// (RepoFiles); only paths outside any checkout, and checkouts that fell back,
	// are walked (WalkedFiles, counted before ignore filtering).
	Repos         int `json:"repos"`
	RepoFiles     int `json:"repo_files"`
	WalkedFiles   int `json:"walked_files"`
	RepoFallbacks int `json:"repo_fallbacks"`
}

// diskRecord keeps Git object names private to the server.
type diskRecord struct {
	Record
	Reviewed           bool                     `json:"reviewed,omitempty"`
	BeforeSkippedState map[string]string        `json:"before_skipped_state,omitempty"`
	AfterSkippedState  map[string]string        `json:"after_skipped_state,omitempty"`
	BeforeTree         string                   `json:"before_tree"`
	AfterTree          string                   `json:"after_tree"`
	Omitted            []string                 `json:"omitted,omitempty"`
	BeforeRepos        []string                 `json:"before_repos"` // [] when none; absent on older records
	NewRepos           map[string]string        `json:"new_repos,omitempty"`
	GoneRepos          []string                 `json:"gone_repos,omitempty"`
	BaselineSkipped    []string                 `json:"baseline_skipped,omitempty"`
	BeforeHeads        map[string]string        `json:"before_heads,omitempty"`
	BeforeGit          map[string]gitCheckpoint `json:"before_git"`
	Review             *savedReview             `json:"review,omitempty"`
}
type Change struct {
	Path     string         `json:"path"`
	Status   string         `json:"status"`
	Added    *int           `json:"added,omitempty"`
	Removed  *int           `json:"removed,omitempty"`
	Binary   bool           `json:"binary,omitempty"`
	Key      string         `json:"key,omitempty"`
	Checkout *CheckoutLabel `json:"checkout,omitempty"`
	At       string         `json:"at,omitempty"`
	Edits    []Change       `json:"edits,omitempty"`
}
type Comparison struct {
	UncapturedChanges int      `json:"uncaptured_changes"`
	Record            Record   `json:"snapshot"`
	Changes           []Change `json:"changes"`
	Diff              string   `json:"diff,omitempty"`
	Truncated         bool     `json:"truncated,omitempty"`
	SkippedCount      int      `json:"skipped_count"`
	Note              string   `json:"note"`
	// NewRepos are the checkouts that appeared during the compared turns with
	// their checked-out commit as the baseline (cards label them "new copy").
	NewRepos []string `json:"-"`
	versions map[string]savedVersion
}
type Store struct{ Root, Private string }

type storeState struct {
	gate    chan struct{}
	warming atomic.Bool
}

// ErrWarming means the background index build (Warm) holds this Crew's gate;
// a send-path capture is skipped rather than waiting for it.
var ErrWarming = errors.New("file snapshot index build in progress")

var states sync.Map

func (s Store) state() *storeState {
	value, _ := states.LoadOrStore(s.Private, &storeState{gate: make(chan struct{}, 1)})
	return value.(*storeState)
}
func (s Store) lock(ctx context.Context) (func(), error) {
	gate := s.state().gate
	// A free gate is taken even when ctx is already done, so the caller can
	// still save its record (select picks randomly between ready cases).
	select {
	case gate <- struct{}{}:
		return func() { <-gate }, nil
	default:
	}
	select {
	case gate <- struct{}{}:
		return func() { <-gate }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func ID(execution string) string {
	h := sha256.Sum256([]byte(execution))
	return hex.EncodeToString(h[:16])
}
func validID(id string) bool {
	b, e := hex.DecodeString(id)
	return e == nil && len(b) == 16 && strings.ToLower(id) == id
}
func (s Store) dir() string    { return filepath.Join(s.Private, "file-snapshots") }
func (s Store) gitDir() string { return filepath.Join(s.dir(), "objects.git") }
func (s Store) command(ctx context.Context, args ...string) *exec.Cmd {
	flags := []string{"-c", "safe.directory=" + s.Root, "-c", "core.bare=false", "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-c", "core.pager=cat", "-c", "gc.auto=0", "-c", "protocol.allow=never", "--git-dir=" + s.gitDir(), "--work-tree=" + s.Root}
	cmd := exec.CommandContext(ctx, "git", append(flags, args...)...)
	cmd.Dir = s.Root
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + s.dir(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_TERMINAL_PROMPT=0", "LC_ALL=C"}
	return cmd
}

// Unchanged reports whether the reply's saved before and after states hold the same files: such a reply has nothing
// to review, so no changed-files card is published for it (owner, 2026-10-28: "still shows 0").
func (s Store) Unchanged(ctx context.Context, r Record) bool {
	if r.Before == "" || r.After == "" {
		return false
	}
	// A file left uncaptured (too large, over budget) with a different size/time
	// on each side is a change the trees cannot show; the card reports it.
	for _, p := range r.Skipped {
		before, after := r.BeforeSkippedState[p], r.AfterSkippedState[p]
		if before != "" && after != "" && before != after {
			return false
		}
	}
	before, after, err := s.effective(ctx, r)
	if err != nil {
		return false
	}
	if before == after {
		return true
	}
	return s.command(ctx, "--literal-pathspecs", "diff", "--quiet", "--no-ext-diff", "--no-textconv", before, after, "--").Run() == nil
}
func (s Store) init(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Join(s.dir(), "turns"), 0700); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(s.gitDir(), "HEAD")); err != nil {
		cmd := s.command(ctx)
		// Bare initialization has no work tree; later operations explicitly bind it.
		cmd.Args = []string{"git", "init", "--bare", "--quiet", "--template=", s.gitDir()}
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("initialize snapshot Git: %w (%s)", err, out)
		}
	}
	// Highest-precedence attributes: no EOL, ident, encoding or filter
	// conversion of any path, whatever the work tree's .gitattributes say.
	attributes := filepath.Join(s.gitDir(), "info", "attributes")
	if current, err := os.ReadFile(attributes); err != nil || string(current) != privateAttributes {
		if err := os.MkdirAll(filepath.Dir(attributes), 0700); err != nil {
			return err
		}
		if err := os.WriteFile(attributes, []byte(privateAttributes), 0600); err != nil {
			return err
		}
	}
	return nil
}
func excluded(path string, dir bool) bool {
	// Reuse the existing credential/private-state exclusion rules. Git's own
	// matcher below additionally handles ignore files within nested repositories.
	for _, part := range strings.Split(path, "/") {
		switch strings.ToLower(part) {
		case "dist", "build", "target", "vendor", "coverage":
			return true
		}
	}
	if filepath.Base(path) == ".gitignore" || filepath.Base(path) == ".gitattributes" || filepath.Base(path) == ".dockerignore" || filepath.Base(path) == ".editorconfig" {
		return false
	}
	if dir {
		return publication.ExcludedDirectory(path)
	}
	return publication.Excluded(path)
}

// listRepo lists a Git checkout's own non-ignored files (tracked and untracked)
// through the hardened runner, so dependency trees the repo ignores are never
// walked. Entries ending in "/" are nested checkouts. isRepo is false when dir
// has no .git; a non-empty reason means "use the plain walk for this checkout".
func (s Store) listRepo(ctx context.Context, dir string) (entries []string, isRepo bool, reason string) {
	info, err := os.Lstat(filepath.Join(dir, ".git"))
	if err != nil {
		return nil, false, ""
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, true, "symlinked_git"
	}
	if _, err := os.Lstat(filepath.Join(dir, ".gitmodules")); err == nil {
		return nil, true, "submodules"
	}
	git := func(args ...string) *exec.Cmd { return safegit.CommandIn(ctx, s.Root, dir, nil, args...) }
	if out, _ := git("config", "--get", "core.sparseCheckout").Output(); strings.TrimSpace(string(out)) == "true" {
		return nil, true, "sparse_checkout"
	}
	if git("rev-parse", "--verify", "-q", "HEAD").Run() != nil {
		return nil, true, "no_commits"
	}
	// ls-files only reads the index; safegit adds --no-optional-locks and
	// GIT_OPTIONAL_LOCKS=0, so the user's index is never refreshed or written.
	out, err := git("ls-files", "-co", "--exclude-standard", "-z").Output()
	if err != nil {
		return nil, true, "ls_files_failed"
	}
	for _, e := range bytes.Split(out, []byte{0}) {
		if len(e) > 0 {
			entries = append(entries, string(e))
		}
	}
	return entries, true, ""
}

// candidates returns the files to capture, grouped by checkout, and the
// ignored paths omitted from comparisons. Checkouts list their own files
// (listRepo); everything else is walked and filtered with the private ignore
// check, as before, and forms the last, loose group. Secret and publication
// exclusions apply to both kinds after enumeration. This validated list is
// the only thing that decides what a capture holds.
func (s Store) candidates(ctx context.Context, stats *CaptureStats) ([]*checkout, []string, error) {
	paths := []string{} // walked files, still to be ignore-filtered
	repos := []*checkout{}
	var walk func(dir string) error
	walk = func(dir string) error {
		return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if err = ctx.Err(); err != nil {
				return err
			}
			rel, err := filepath.Rel(s.Root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if path != s.Root && excluded(rel, d.IsDir()) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				entries, isRepo, reason := s.listRepo(ctx, path)
				if !isRepo {
					return nil
				}
				if reason != "" {
					stats.RepoFallbacks++
					log.Printf("[FILE_SNAPSHOT] phase=repo_fallback repo=%q reason=%s", rel, reason)
					return nil // walk this checkout
				}
				stats.Repos++
				repo := &checkout{dir: path, key: checkoutKey(rel, path)}
				if rel != "." {
					repo.prefix = rel
				}
				var nested []string
				for _, e := range entries {
					if strings.HasSuffix(e, "/") {
						nested = append(nested, filepath.Join(path, filepath.FromSlash(strings.TrimSuffix(e, "/"))))
						continue
					}
					if !excluded(repo.full(e), false) {
						repo.files = append(repo.files, e)
					}
				}
				stats.RepoFiles += len(repo.files)
				repos = append(repos, repo)
				for _, n := range nested {
					if err := walk(n); err != nil {
						return err
					}
				}
				return filepath.SkipDir
			}
			// Count only after Git's ignore filtering. Large folders with several
			// worktrees can have more than 20,000 source files; capture already
			// bounds imported bytes, individual files and elapsed time.
			paths = append(paths, rel)
			return nil
		})
	}
	if err := walk(s.Root); err != nil {
		return nil, nil, err
	}
	stats.WalkedFiles = len(paths)
	loose := &checkout{dir: s.Root, key: "loose", loose: true}
	repos = append(repos, loose)
	if len(paths) == 0 {
		return repos, nil, nil
	}
	cmd := s.command(ctx, "check-ignore", "--no-index", "-z", "--stdin")
	cmd.Stdin = strings.NewReader(strings.Join(paths, "\x00") + "\x00")
	out, err := cmd.Output()
	var status *exec.ExitError
	if err != nil && (!errors.As(err, &status) || status.ExitCode() != 1) {
		return nil, nil, errors.New("snapshot ignore rules could not be read")
	}
	ignored := map[string]bool{}
	for _, p := range bytes.Split(out, []byte{0}) {
		ignored[string(p)] = true
	}
	omitted := []string{}
	for _, p := range paths {
		if !ignored[p] {
			loose.files = append(loose.files, p)
		} else {
			omitted = append(omitted, p)
		}
	}
	return repos, omitted, nil
}

// vanishedOnDisk lists paths saved in the prior (before) state that are not
// candidates now but still exist: newly ignored or excluded files, which a
// comparison must not report as deleted. Paths missing from disk are genuine
// deletions and stay out, so the diff shows them as deleted.
func (s Store) vanishedOnDisk(ctx context.Context, root *os.Root, prior string, have []string) []string {
	out, err := s.command(ctx, "ls-tree", "-r", "--name-only", "-z", prior).Output()
	if err != nil {
		return nil
	}
	now := make(map[string]bool, len(have))
	for _, p := range have {
		now[p] = true
	}
	var res []string
	for _, p := range bytes.Split(out, []byte{0}) {
		if len(p) == 0 || now[string(p)] {
			continue
		}
		if _, err := root.Lstat(string(p)); err == nil {
			res = append(res, string(p))
		}
	}
	return res
}

func (s Store) read(id string) (Record, error) {
	if !validID(id) {
		return Record{}, errors.New("invalid snapshot")
	}
	data, err := os.ReadFile(filepath.Join(s.dir(), "turns", id+".json"))
	if err != nil {
		return Record{}, err
	}
	var disk diskRecord
	if err = json.Unmarshal(data, &disk); err != nil {
		return Record{}, err
	}
	disk.Record.Reviewed = disk.Reviewed
	disk.Record.Before = disk.BeforeTree
	disk.Record.After = disk.AfterTree
	disk.Record.Omitted = disk.Omitted
	disk.Record.BeforeSkippedState = disk.BeforeSkippedState
	disk.Record.AfterSkippedState = disk.AfterSkippedState
	disk.Record.BeforeRepos = disk.BeforeRepos
	disk.Record.NewRepos = disk.NewRepos
	disk.Record.GoneRepos = disk.GoneRepos
	disk.Record.BaselineSkipped = disk.BaselineSkipped
	disk.Record.BeforeHeads = disk.BeforeHeads
	disk.Record.BeforeGit, disk.Record.Review = disk.BeforeGit, disk.Review
	return disk.Record, nil
}
func (s Store) save(r Record) error {
	data, err := json.Marshal(diskRecord{Record: r, Reviewed: r.Reviewed, BeforeTree: r.Before, AfterTree: r.After, Omitted: r.Omitted, BeforeSkippedState: r.BeforeSkippedState, AfterSkippedState: r.AfterSkippedState,
		BeforeRepos: r.BeforeRepos, NewRepos: r.NewRepos, GoneRepos: r.GoneRepos, BaselineSkipped: r.BaselineSkipped, BeforeHeads: r.BeforeHeads, BeforeGit: r.BeforeGit, Review: r.Review})
	if err != nil {
		return err
	}
	p := filepath.Join(s.dir(), "turns", r.ID+".json")
	tmp := p + ".tmp"
	if err = os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
func (s Store) Begin(ctx context.Context, execution string) (Record, error) {
	return s.begin(ctx, execution, false)
}

// BeginWithin is the reply's before-capture: waiting for the Crew gate and
// capturing together never take longer than limit. The server runs it beside
// the send without waiting (PLAT-746-163, owner decision 2026-10-29: a capture
// that finishes during the turn is still the baseline). A cold Crew (no persisted private index yet)
// reads every file, so it is not tried; it and a warm capture that runs out of
// limit are saved unavailable with BeforeCapture.Deferred set, and the caller
// runs Warm in the background. While Warm holds the gate this returns
// ErrWarming at once.
func (s Store) BeginWithin(limit time.Duration, execution string) (Record, error) {
	if s.state().warming.Load() {
		return Record{}, ErrWarming
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	return s.begin(ctx, execution, true)
}

func (s Store) begin(ctx context.Context, execution string, sendPath bool) (Record, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return Record{}, err
	}
	defer unlock()
	id := ID(execution)
	if r, err := s.read(id); err == nil {
		return r, nil
	}
	if err := s.init(ctx); err != nil {
		return Record{}, err
	}
	r := Record{ID: id, ExecutionID: execution, StartedAt: time.Now().UTC(), Status: "capturing"}
	r.BeforeSkippedState = map[string]string{}
	r.BeforeCapture.Cold = !s.hasIndexes()
	if sendPath && r.BeforeCapture.Cold {
		r.Status = "unavailable"
		r.BeforeCapture.Deferred = "cold"
		return r, s.save(r)
	}
	repos := map[string]string{}
	r.Before, r.Skipped, r.Omitted, err = s.captureRepos(ctx, "refs/turns/"+id+"/before", "", &r.BeforeCapture, r.BeforeSkippedState, maxImportBytes, repos)
	r.BeforeRepos = sortedKeys(repos)
	r.BeforeHeads = s.repoHeads(ctx, repos)
	r.BeforeGit, _ = s.gitCheckpoints(ctx, repos, 256)
	if err != nil || r.Before == "" {
		r.Status = "unavailable"
		if sendPath && ctx.Err() != nil {
			r.BeforeCapture.Deferred = "budget"
		}
	}
	saveErr := s.save(r)
	if err != nil {
		return r, err
	}
	return r, saveErr
}

// Warm is the resumable initial index build: it fills the Crew's persisted
// private indexes in the background, in batches of maxImportBytes, without a
// reply record, so later before-captures reuse them and fit their time bound.
// It stays separate from replies: a reply's before-capture runs beside the
// send and never delays it, and a Crew with no index yet gets no baseline for
// that reply while this builds one (PLAT-746-163, owner decision 2026-10-29).
// Each batch is committed (indexes plus headRef) before the next starts, so a
// build stopped by limit keeps every finished batch and the next continues
// from there. One runs at a time per Crew (ErrWarming).
func (s Store) Warm(limit time.Duration) (CaptureStats, error) {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	return s.warm(ctx, maxImportBytes, nil)
}

func (s Store) warm(ctx context.Context, batch int64, afterBatch func(CaptureStats)) (CaptureStats, error) {
	state := s.state()
	if !state.warming.CompareAndSwap(false, true) {
		return CaptureStats{}, ErrWarming
	}
	defer state.warming.Store(false)
	unlock, err := s.lock(ctx)
	if err != nil {
		return CaptureStats{}, err
	}
	defer unlock()
	if err := s.init(ctx); err != nil {
		return CaptureStats{}, err
	}
	total := CaptureStats{Cold: !s.hasIndexes()}
	for {
		stats := CaptureStats{}
		_, _, _, err := s.capture(ctx, headRef, "", &stats, map[string]string{}, batch)
		// The last batch's counts describe the index; bytes, changes and
		// batches add up over the whole build.
		sum := total
		total = stats
		total.Cold, total.ImportedBytes, total.Changed, total.Batches = sum.Cold, sum.ImportedBytes+stats.ImportedBytes, sum.Changed+stats.Changed, sum.Batches+1
		if err != nil {
			return total, err
		}
		if afterBatch != nil {
			afterBatch(stats)
		}
		// Done, or no file fits a batch (each is at most MaxFileBytes).
		if stats.BudgetSkipped == 0 || stats.Changed == 0 {
			return total, nil
		}
		if err := ctx.Err(); err != nil {
			return total, err
		}
	}
}

// Saved reports whether a reply still has its before and after state: its
// capture did not fail and retention has not removed it.
func (s Store) Saved(id string) bool {
	r, err := s.read(id)
	return err == nil && (r.Status == "complete" || r.Status == "partial")
}
func (s Store) Finish(ctx context.Context, execution string) (Record, error) {
	// A reply with no before-state (deferred or failed capture) has no card and
	// no use for an after-capture: return without waiting on the gate, which
	// the background index build may hold for minutes. Reads take no gate.
	if r, err := s.read(ID(execution)); err != nil || r.Status != "capturing" {
		return r, err
	}
	unlock, err := s.lock(ctx)
	if err != nil {
		return Record{}, err
	}
	defer unlock()
	r, err := s.read(ID(execution))
	if err != nil {
		return r, err
	}
	if r.Status != "capturing" {
		return r, nil
	}
	var skipped, omitted []string
	r.AfterSkippedState = map[string]string{}
	repos := map[string]string{}
	r.After, skipped, omitted, err = s.captureRepos(ctx, "refs/turns/"+r.ID+"/after", r.Before, &r.AfterCapture, r.AfterSkippedState, maxImportBytes, repos)
	r.Skipped = append(r.Skipped, skipped...)
	r.Omitted = append(r.Omitted, omitted...)
	if r.Before != "" {
		// PLAT-746-258: checkout creation is still known when the after
		// capture fails. Later chat comparisons need its creation baseline,
		// even though this reply itself has no usable after-state.
		baselineCtx := ctx
		cancel := func() {}
		if ctx.Err() != nil {
			baselineCtx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		}
		s.repoBaselines(baselineCtx, &r, repos)
		cancel()
	}
	now := time.Now().UTC()
	r.CompletedAt = &now
	r.Status = "complete"
	if len(r.Skipped) > 0 {
		r.Status = "partial"
	}
	if err != nil || r.Before == "" {
		r.Status = "unavailable"
	}
	if err == nil && r.Before != "" {
		s.saveReview(ctx, &r, repos)
	}
	saveErr := s.save(r)
	if err != nil {
		return r, err
	}
	if saveErr != nil {
		return r, saveErr
	}
	s.prune(ctx)
	return r, nil
}

// Abandon marks a prepared but undelivered turn without inventing an after state.
func (s Store) Abandon(execution string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	unlock, err := s.lock(ctx)
	if err != nil {
		return
	}
	defer unlock()
	r, err := s.read(ID(execution))
	if err != nil || r.Status != "capturing" {
		return
	}
	r.Status = "unavailable"
	_ = s.save(r)
}
func (s Store) List() ([]Record, error) {
	paths, err := filepath.Glob(filepath.Join(s.dir(), "turns", "*.json"))
	if err != nil {
		return nil, err
	}
	records := []Record{}
	for _, p := range paths {
		r, e := s.read(strings.TrimSuffix(filepath.Base(p), ".json"))
		if os.IsNotExist(e) {
			continue
		} // Retention may remove a record during a list read.
		if e != nil {
			return nil, e
		}
		records = append(records, r)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].StartedAt.After(records[j].StartedAt) })
	return records, nil
}
func (s Store) prune(ctx context.Context) {
	records, err := s.List()
	if err != nil {
		return
	}
	removed := false
	for i, r := range records {
		// Checkout baselines describe workspace history, independently of
		// reply retention. Older saved chat boundaries still need them.
		if r.Reviewed || len(r.NewRepos) > 0 || len(r.GoneRepos) > 0 || (r.Review != nil && len(r.Review.Spans) > 0) || (i < keepTurns && time.Since(r.StartedAt) < 14*24*time.Hour) {
			continue
		}
		refs, _ := s.command(ctx, "for-each-ref", "--format=delete %(refname)", "refs/turns/"+r.ID+"/").Output()
		cmd := s.command(ctx, "update-ref", "--stdin")
		cmd.Stdin = bytes.NewReader(refs)
		_ = cmd.Run()
		_ = os.Remove(filepath.Join(s.dir(), "turns", r.ID+".json"))
		removed = true
	}
	if removed {
		s.dropViews(ctx)
		_ = s.command(ctx, "gc", "--prune=now", "--quiet").Run()
	}
}

type limitedOutput struct {
	bytes.Buffer
	truncated bool
}

func (w *limitedOutput) Write(p []byte) (int, error) {
	n := len(p)
	left := maxDiffBytes - w.Len()
	if len(p) > left {
		p = p[:left]
		w.truncated = true
	}
	_, _ = w.Buffer.Write(p)
	return n, nil
}

// Compare, CompareBoundaries and the file reads take no gate: a record is
// replaced by atomic rename and its Git refs are immutable until prune removes
// them, which makes a concurrent read fail cleanly instead of blocking a reply.
func (s Store) Compare(ctx context.Context, id, file string, allow func(string) bool) (Comparison, error) {
	r, err := s.read(id)
	if err != nil {
		return Comparison{}, err
	}
	return s.compare(ctx, r, file, allow)
}

func (s Store) compare(ctx context.Context, r Record, file string, allow func(string) bool) (Comparison, error) {
	if r.Review != nil {
		return s.compareReview(ctx, []Record{r}, file, "", allow)
	}
	return s.compareRaw(ctx, r, file, allow)
}

func (s Store) compareRaw(ctx context.Context, r Record, file string, allow func(string) bool) (Comparison, error) {
	out := Comparison{Record: r, Changes: []Change{}, Note: "Changes observed during this turn; other chats or editors may also have changed these files. Undo is not available."}
	if r.Status != "complete" && r.Status != "partial" {
		return out, errors.New("this turn has no completed file comparison")
	}
	// Checkouts that appeared or vanished during the turn compare against
	// their checked-out commit, not against nothing (repo_baselines.go).
	before, after, err := s.effective(ctx, r)
	if err != nil {
		return out, err
	}
	r.Before, r.After = before, after
	out.Record.Before, out.Record.After = before, after
	for p, tree := range r.NewRepos {
		if tree != "" {
			out.NewRepos = append(out.NewRepos, p)
		}
	}
	sort.Strings(out.NewRepos)
	skipped := map[string]bool{}
	for _, p := range r.Skipped {
		skipped[p] = true
	}
	out.Record.Skipped = nil
	for p := range skipped {
		if allow(p) {
			out.Record.Skipped = append(out.Record.Skipped, p)
			before, after := r.BeforeSkippedState[p], r.AfterSkippedState[p]
			if before == "" || after == "" || before != after {
				out.UncapturedChanges++
			}
		}
	}
	sort.Strings(out.Record.Skipped)
	out.SkippedCount = len(out.Record.Skipped)
	// The full list remains in the private record and filter above. Cards
	// need a count and bounded examples, not megabytes of skipped paths.
	if len(out.Record.Skipped) > 50 {
		out.Record.Skipped = out.Record.Skipped[:50]
	}
	for _, p := range r.Omitted {
		skipped[p] = true
	}
	for _, p := range r.BaselineSkipped {
		skipped[p] = true
	}
	paths := append([]string{"--"}, r.diffPaths...)
	raw, err := s.command(ctx, append([]string{"--literal-pathspecs", "diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--name-status", "-z", r.Before, r.After}, paths...)...).Output()
	if err != nil {
		return out, err
	}
	parts := bytes.Split(raw, []byte{0})
	for i := 0; i+1 < len(parts); i += 2 {
		p := string(parts[i+1])
		if !skipped[p] && allow(p) {
			out.Changes = append(out.Changes, Change{Path: p, Status: string(parts[i])})
		}
	}
	stats, err := s.command(ctx, append([]string{"--literal-pathspecs", "diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--numstat", "-z", r.Before, r.After}, paths...)...).Output()
	if err != nil {
		return out, err
	}
	byPath := map[string]*Change{}
	for i := range out.Changes {
		byPath[out.Changes[i].Path] = &out.Changes[i]
	}
	for _, row := range bytes.Split(stats, []byte{0}) {
		fields := bytes.SplitN(row, []byte{'\t'}, 3)
		if len(fields) != 3 {
			continue
		}
		change := byPath[string(fields[2])]
		if change == nil {
			continue
		}
		if string(fields[0]) == "-" || string(fields[1]) == "-" {
			change.Binary = true
			continue
		}
		added, aerr := strconv.Atoi(string(fields[0]))
		removed, rerr := strconv.Atoi(string(fields[1]))
		if aerr == nil && rerr == nil {
			change.Added, change.Removed = &added, &removed
		}
	}
	if file != "" {
		found := false
		for _, c := range out.Changes {
			if c.Path == file {
				found = true
			}
		}
		if !found {
			return out, errors.New("file not found in this comparison")
		}
		cmd := s.command(ctx, "--literal-pathspecs", "diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--no-color", r.Before, r.After, "--", file)
		var buf limitedOutput
		cmd.Stdout = &buf
		if err = cmd.Run(); err != nil {
			return out, err
		}
		out.Diff = buf.String()
		out.Truncated = buf.truncated
	}
	return out, nil
}

// CompareBetween compares two retained turn boundaries, never the working tree.
// Callers resolve the bounds from the authorized chat, not client-supplied Git refs.
func (s Store) CompareBetween(ctx context.Context, first, last, file string, allow func(string) bool) (Comparison, error) {
	return s.CompareBoundaries(ctx, first, last, "before", file, allow)
}

// KeepReviewBoundaries keeps only the selected review points in the existing
// snapshot records. Ordinary replies retain their normal bounded retention.
func (s Store) KeepReviewBoundaries(ctx context.Context, ids ...string) error {
	unlock, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	for _, id := range ids {
		if id == "" {
			continue
		}
		r, err := s.read(id)
		if err != nil {
			return err
		}
		if r.After == "" {
			return errors.New("review boundary is unavailable")
		}
		r.Reviewed = true
		if err := s.save(r); err != nil {
			return err
		}
	}
	return nil
}

// CompareBoundaries also permits an accepted after-state as the next baseline.
func (s Store) CompareBoundaries(ctx context.Context, first, last, firstSide, file string, allow func(string) bool) (Comparison, error) {
	before, err := s.read(first)
	if err != nil {
		return Comparison{}, err
	}
	after, err := s.read(last)
	if err != nil {
		return Comparison{}, err
	}
	window, err := s.windowRepos(before.StartedAt, after.StartedAt)
	if err != nil {
		return Comparison{}, err
	}
	if firstSide == "after" {
		before.Before = before.After
		before.BeforeSkippedState = before.AfterSkippedState
		if before.CompletedAt != nil {
			before.StartedAt = *before.CompletedAt
		}
	} else if firstSide != "before" {
		return Comparison{}, errors.New("invalid saved boundary")
	}
	if before.Before == "" || after.After == "" || (before.Status != "complete" && before.Status != "partial") || (after.Status != "complete" && after.Status != "partial") {
		return Comparison{}, errors.New("a saved comparison boundary is unavailable")
	}
	after.Before = before.Before
	after.BeforeSkippedState = before.BeforeSkippedState
	after.StartedAt = before.StartedAt
	if first != last {
		after.Skipped = append(after.Skipped, before.Skipped...)
		after.Omitted = append(after.Omitted, before.Omitted...)
	}
	if before.Status == "partial" {
		after.Status = "partial"
	}
	after.NewRepos, after.GoneRepos, after.BaselineSkipped = window.NewRepos, window.GoneRepos, window.BaselineSkipped
	out, err := s.compareRaw(ctx, after, file, allow)
	out.Note = "Net changes between this chat's saved boundaries. Other chats or editors may contribute. Undo is not available."
	return out, err
}

// ReadComparedFile uses only the authorized paths of a server-produced comparison.
func (s Store) ReadComparedFile(ctx context.Context, comparison Comparison, file, side string) ([]byte, error) {
	found := false
	for _, change := range comparison.Changes {
		if change.Path == file {
			found = true
		}
	}
	if !found || (side != "before" && side != "after") {
		return nil, errors.New("choose a historical file and side")
	}
	return s.readComparedFile(ctx, comparison, file, side)
}

// ReadFile returns a saved blob only when this exact historical path still
// passes today's file guard. Git reads the blob itself, never a symlink target.
func (s Store) ReadFile(ctx context.Context, id, file, side string, allow func(string) bool) ([]byte, error) {
	if file == "" || (side != "before" && side != "after") {
		return nil, errors.New("choose a historical file and side")
	}
	comparison, err := s.Compare(ctx, id, file, allow)
	if err != nil {
		return nil, err
	}
	return s.readComparedFile(ctx, comparison, file, side)
}

func (s Store) readComparedFile(ctx context.Context, comparison Comparison, file, side string) ([]byte, error) {
	tree := comparison.Record.After
	if side == "before" {
		tree = comparison.Record.Before
	}
	if comparison.versions != nil {
		var err error
		tree, err = s.reviewVersion(comparison, file, side)
		if err != nil {
			return nil, err
		}
	}
	object := tree + ":" + file
	size, err := s.command(ctx, "cat-file", "-s", object).Output()
	if err != nil {
		return nil, err
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(size)), 10, 64)
	if err != nil || n < 0 || n > MaxFileBytes {
		return nil, errors.New("historical file is too large to preview")
	}
	return s.command(ctx, "cat-file", "blob", object).Output()
}
