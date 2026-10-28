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
	"io"
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
const maxCaptureBytes = 256 << 20
const maxDiffBytes = 1 << 20
const keepTurns = 50

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
}

// CaptureStats explains partial captures without exposing file names or contents.
type CaptureStats struct {
	Eligible      int   `json:"eligible"`
	Captured      int   `json:"captured"`
	Reused        int   `json:"reused"`
	ImportedBytes int64 `json:"imported_bytes"`
	SizeSkipped   int   `json:"size_skipped"`
	Cold          bool  `json:"cold,omitempty"` // no file hashes were cached: every file was read
	// Deferred names why a send-path capture was left to a background cache
	// warm-up: "cold" (not attempted) or "budget" (ran out of time).
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
	Reviewed           bool              `json:"reviewed,omitempty"`
	BeforeSkippedState map[string]string `json:"before_skipped_state,omitempty"`
	AfterSkippedState  map[string]string `json:"after_skipped_state,omitempty"`
	BeforeTree         string            `json:"before_tree"`
	AfterTree          string            `json:"after_tree"`
	Omitted            []string          `json:"omitted,omitempty"`
}
type Change struct {
	Path    string `json:"path"`
	Status  string `json:"status"`
	Added   *int   `json:"added,omitempty"`
	Removed *int   `json:"removed,omitempty"`
	Binary  bool   `json:"binary,omitempty"`
}
type Comparison struct {
	UncapturedChanges int      `json:"uncaptured_changes"`
	Record            Record   `json:"snapshot"`
	Changes           []Change `json:"changes"`
	Diff              string   `json:"diff,omitempty"`
	Truncated         bool     `json:"truncated,omitempty"`
	SkippedCount      int      `json:"skipped_count"`
	Note              string   `json:"note"`
}
type Store struct{ Root, Private string }

type cachedFile struct {
	info os.FileInfo
	blob string
}
type storeState struct {
	gate    chan struct{}
	files   map[string]cachedFile
	warming atomic.Bool
}

// ErrWarming means a background cache warm-up holds this Crew's gate; a
// send-path capture is skipped rather than waiting for it.
var ErrWarming = errors.New("file snapshot cache warm-up in progress")

var states sync.Map

func (s Store) state() *storeState {
	value, _ := states.LoadOrStore(s.Private, &storeState{gate: make(chan struct{}, 1), files: map[string]cachedFile{}})
	return value.(*storeState)
}
func (s Store) lock(ctx context.Context) (func(), error) {
	gate := s.state().gate
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
	if r.Before == r.After {
		return true
	}
	return s.command(ctx, "--literal-pathspecs", "diff", "--quiet", "--no-ext-diff", "--no-textconv", r.Before, r.After, "--").Run() == nil
}
func (s Store) init(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Join(s.dir(), "turns"), 0700); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(s.gitDir(), "HEAD")); err == nil {
		return nil
	}
	cmd := s.command(ctx)
	// Bare initialization has no work tree; later operations explicitly bind it.
	cmd.Args = []string{"git", "init", "--bare", "--quiet", "--template=", s.gitDir()}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("initialize snapshot Git: %w (%s)", err, out)
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

// candidates returns the files to capture and the ignored paths omitted from
// comparisons. Checkouts list their own files (listRepo); everything else is
// walked and filtered with the private ignore check, as before. Secret and
// publication exclusions apply to both kinds after enumeration.
func (s Store) candidates(ctx context.Context, stats *CaptureStats) ([]string, []string, error) {
	paths := []string{}     // walked files, still to be ignore-filtered
	repoFiles := []string{} // already filtered by their repo's own ignore rules
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
				var nested []string
				for _, e := range entries {
					full := e
					if rel != "." {
						full = rel + "/" + e
					}
					if strings.HasSuffix(e, "/") {
						nested = append(nested, filepath.Join(path, filepath.FromSlash(strings.TrimSuffix(e, "/"))))
						continue
					}
					if !excluded(full, false) {
						repoFiles = append(repoFiles, full)
					}
				}
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
	stats.RepoFiles, stats.WalkedFiles = len(repoFiles), len(paths)
	if len(paths) == 0 {
		return repoFiles, nil, nil
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
	kept := repoFiles
	omitted := []string{}
	for _, p := range paths {
		if !ignored[p] {
			kept = append(kept, p)
		} else {
			omitted = append(omitted, p)
		}
	}
	return kept, omitted, nil
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

// capture streams eligible files into Git's standard importer. Rooted reads
// cannot follow a replaced ancestor symlink outside the Crew. Git owns object
// hashing/deduplication; no user Git config, attributes or clean filters run.
func (s Store) capture(ctx context.Context, ref, prior string, stats *CaptureStats, skippedState map[string]string) (string, []string, []string, error) {
	paths, omitted, err := s.candidates(ctx, stats)
	if err != nil {
		return "", nil, nil, err
	}
	root, err := os.OpenRoot(s.Root)
	if err != nil {
		return "", nil, nil, err
	}
	defer root.Close()
	stats.Eligible = len(paths)
	if prior != "" {
		omitted = append(omitted, s.vanishedOnDisk(ctx, root, prior, paths)...)
	}
	// A large asset encountered first must not starve ordinary source files.
	// Stat through the confined root; recheck each file before reading below.
	skipped := []string{}
	// Metadata is evidence of an observed change, not a captured blob. Keep it
	// only for skipped regular files; unreadable/non-regular paths stay unknown.
	rememberSkipped := func(path string, info os.FileInfo) {
		if info.Mode().IsRegular() {
			skippedState[path] = fmt.Sprintf("%d:%d:%d", info.Size(), info.ModTime().UnixNano(), info.Mode())
		}
	}
	// Concurrent deletes are ordinary working-tree changes. An unreadable path
	// is omitted on both sides of the comparison, never shown as a deletion.
	skipRead := func(path string, err error) {
		if !errors.Is(err, fs.ErrNotExist) {
			skipped = append(skipped, path)
			stats.ReadSkipped++
		}
	}
	sizes := make(map[string]int64, len(paths))
	present := paths[:0]
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return "", nil, nil, err
		}
		info, err := root.Lstat(path)
		if err != nil {
			skipRead(path, err)
			continue
		}
		sizes[path] = info.Size()
		present = append(present, path)
	}
	paths = present
	sort.Slice(paths, func(i, j int) bool {
		if sizes[paths[i]] == sizes[paths[j]] {
			return paths[i] < paths[j]
		}
		return sizes[paths[i]] < sizes[paths[j]]
	})
	cmd := s.command(ctx, "fast-import", "--quiet", "--done", "--date-format=raw")
	input, err := cmd.StdinPipe()
	if err != nil {
		return "", nil, nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err = cmd.Start(); err != nil {
		return "", nil, nil, err
	}
	// Each checkpoint is independent, so deleting an old ref permits Git GC.
	_, err = fmt.Fprintf(input, "commit %s\ncommitter AgentWorks <snapshot@example.com> %d +0000\ndata 14\nFile snapshot\n\ndeleteall\n", ref, time.Now().Unix())
	captured := map[string]os.FileInfo{}
	cache := s.state().files
	var total int64
	writeData := func(mode, path string, data []byte) error {
		if _, e := fmt.Fprintf(input, "M %s inline %s\ndata %d\n", mode, strconv.Quote(path), len(data)); e != nil {
			return e
		}
		if _, e := input.Write(data); e != nil {
			return e
		}
		_, e := io.WriteString(input, "\n")
		return e
	}
	for _, path := range paths {
		if err != nil {
			break
		}
		if err = ctx.Err(); err != nil {
			break
		}
		info, e := root.Lstat(path)
		if e != nil {
			skipRead(path, e)
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, e := root.Readlink(path)
			if e != nil {
				skipRead(path, e)
				continue
			}
			err = writeData("120000", path, []byte(target))
			stats.Captured++
			continue
		}
		if !info.Mode().IsRegular() || info.Size() > MaxFileBytes {
			rememberSkipped(path, info)
			skipped = append(skipped, path)
			stats.SizeSkipped++
			continue
		}
		if previous, ok := cache[path]; ok && os.SameFile(info, previous.info) && info.Size() == previous.info.Size() && info.ModTime() == previous.info.ModTime() && info.Mode() == previous.info.Mode() {
			mode := "100644"
			if info.Mode()&0111 != 0 {
				mode = "100755"
			}
			_, err = fmt.Fprintf(input, "M %s %s %s\n", mode, previous.blob, strconv.Quote(path))
			// Stored blobs cost no read/import bytes, even once the budget is full.
			stats.Reused++
			stats.Captured++
			captured[path] = info
			continue
		}
		if total+info.Size() > maxCaptureBytes {
			rememberSkipped(path, info)
			skipped = append(skipped, path)
			stats.BudgetSkipped++
			continue
		}
		f, e := root.Open(path)
		if e != nil {
			skipRead(path, e)
			continue
		}
		// The open handle's size/type is authoritative if the file changed during enumeration.
		actual, e := f.Stat()
		if e != nil {
			f.Close()
			skipRead(path, e)
			continue
		}
		if !actual.Mode().IsRegular() || actual.Size() > MaxFileBytes {
			rememberSkipped(path, actual)
			f.Close()
			stats.SizeSkipped++
			skipped = append(skipped, path)
			continue
		}
		data, e := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
		f.Close()
		if e != nil {
			skipRead(path, e)
			continue
		}
		if len(data) > MaxFileBytes || total+int64(len(data)) > maxCaptureBytes {
			rememberSkipped(path, actual)
			if len(data) > MaxFileBytes {
				stats.SizeSkipped++
			} else {
				stats.BudgetSkipped++
			}
			skipped = append(skipped, path)
			continue
		}
		total += int64(len(data))
		stats.ImportedBytes = total
		stats.Captured++
		mode := "100644"
		if actual.Mode()&0111 != 0 {
			mode = "100755"
		}
		err = writeData(mode, path, data)
		captured[path] = actual
	}
	if err == nil {
		_, err = io.WriteString(input, "\ndone\n")
	}
	input.Close()
	if err != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if err != nil {
		return "", nil, nil, err
	}
	if waitErr != nil {
		return "", nil, nil, fmt.Errorf("snapshot import failed: %w", waitErr)
	}
	out, err := s.command(ctx, "rev-parse", "--verify", ref).Output()
	commit := strings.TrimSpace(string(out))
	if err == nil {
		tree, treeErr := s.command(ctx, "ls-tree", "-r", "-z", commit).Output()
		if treeErr == nil {
			next := map[string]cachedFile{}
			for _, entry := range bytes.Split(tree, []byte{0}) {
				meta, path, ok := strings.Cut(string(entry), "\t")
				fields := strings.Fields(meta)
				if info, found := captured[path]; ok && found && len(fields) == 3 {
					next[path] = cachedFile{info: info, blob: fields[2]}
				}
			}
			s.state().files = next
		}
	}
	return commit, skipped, omitted, err
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
	return disk.Record, nil
}
func (s Store) save(r Record) error {
	data, err := json.Marshal(diskRecord{Record: r, Reviewed: r.Reviewed, BeforeTree: r.Before, AfterTree: r.After, Omitted: r.Omitted, BeforeSkippedState: r.BeforeSkippedState, AfterSkippedState: r.AfterSkippedState})
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

// BeginWithin is the send-path before-capture: waiting for the Crew gate and
// capturing together never take longer than budget, so a message is never held
// behind a capture. A cold cache (after a restart or the first capture) reads
// every file, which a large Crew cannot do in that time, so it is not tried;
// it and a warm capture that runs out of time are saved unavailable with
// BeforeCapture.Deferred set, and the caller runs Warm in the background.
// While Warm holds the gate this returns ErrWarming at once.
func (s Store) BeginWithin(budget time.Duration, execution string) (Record, error) {
	if s.state().warming.Load() {
		return Record{}, ErrWarming
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
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
	r.BeforeCapture.Cold = len(s.state().files) == 0
	if sendPath && r.BeforeCapture.Cold {
		r.Status = "unavailable"
		r.BeforeCapture.Deferred = "cold"
		return r, s.save(r)
	}
	r.Before, r.Skipped, r.Omitted, err = s.capture(ctx, "refs/turns/"+id+"/before", "", &r.BeforeCapture, r.BeforeSkippedState)
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

// Warm reads the Crew's files into the file-hash cache without a reply record,
// so the next send-path capture reuses them and fits its budget. One runs at a
// time per Crew (ErrWarming otherwise). Its import keeps a ref so a later
// cleanup cannot remove blobs the cache points to. A warm-up stopped by limit
// leaves the cache as it was (what it read is not kept).
func (s Store) Warm(limit time.Duration) (CaptureStats, error) {
	state := s.state()
	if !state.warming.CompareAndSwap(false, true) {
		return CaptureStats{}, ErrWarming
	}
	defer state.warming.Store(false)
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	unlock, err := s.lock(ctx)
	if err != nil {
		return CaptureStats{}, err
	}
	defer unlock()
	if err := s.init(ctx); err != nil {
		return CaptureStats{}, err
	}
	stats := CaptureStats{Cold: len(state.files) == 0}
	ref := "refs/warm/" + strconv.FormatInt(time.Now().UnixNano(), 10)
	_, _, _, err = s.capture(ctx, ref, "", &stats, map[string]string{})
	// Keep only the newest successful warm-up ref: the cache points at its blobs.
	refs, listErr := s.command(context.Background(), "for-each-ref", "--format=%(refname)", "refs/warm/").Output()
	if listErr == nil {
		for _, name := range strings.Fields(string(refs)) {
			if (err == nil && name != ref) || (err != nil && name == ref) {
				_ = s.command(context.Background(), "update-ref", "-d", name).Run()
			}
		}
	}
	return stats, err
}

// Failed reports whether a retained reply's capture failed, so it saved no
// before or after state. A record that is gone (retention) is not Failed.
func (s Store) Failed(id string) bool {
	r, err := s.read(id)
	return err == nil && r.Status == "unavailable"
}
func (s Store) Finish(ctx context.Context, execution string) (Record, error) {
	// A reply with no before-state (deferred or failed capture) has no card and
	// no use for an after-capture: return without waiting on the gate, which a
	// background warm-up may hold for minutes. Reads take no gate.
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
	r.After, skipped, omitted, err = s.capture(ctx, "refs/turns/"+r.ID+"/after", r.Before, &r.AfterCapture, r.AfterSkippedState)
	r.Skipped = append(r.Skipped, skipped...)
	r.Omitted = append(r.Omitted, omitted...)
	now := time.Now().UTC()
	r.CompletedAt = &now
	r.Status = "complete"
	if len(r.Skipped) > 0 {
		r.Status = "partial"
	}
	if err != nil || r.Before == "" {
		r.Status = "unavailable"
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
		if r.Reviewed || (i < keepTurns && time.Since(r.StartedAt) < 14*24*time.Hour) {
			continue
		}
		for _, phase := range []string{"before", "after"} {
			_ = s.command(ctx, "update-ref", "-d", "refs/turns/"+r.ID+"/"+phase).Run()
		}
		_ = os.Remove(filepath.Join(s.dir(), "turns", r.ID+".json"))
		removed = true
	}
	if removed {
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
	out := Comparison{Record: r, Changes: []Change{}, Note: "Changes observed during this turn; other chats or editors may also have changed these files. Undo is not available."}
	if r.Status != "complete" && r.Status != "partial" {
		return out, errors.New("this turn has no completed file comparison")
	}
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
	raw, err := s.command(ctx, "--literal-pathspecs", "diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--name-status", "-z", r.Before, r.After, "--").Output()
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
	stats, err := s.command(ctx, "--literal-pathspecs", "diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--numstat", "-z", r.Before, r.After, "--").Output()
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
	out, err := s.compare(ctx, after, file, allow)
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
