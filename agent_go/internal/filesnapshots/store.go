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
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/publication"
)

const MaxFileBytes = 25 << 20
const maxCaptureBytes = 256 << 20
const maxFiles = 20000
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
	BudgetSkipped int   `json:"budget_skipped"`
	ReadSkipped   int   `json:"read_skipped"`
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
	gate  chan struct{}
	files map[string]cachedFile
}

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
func (s Store) candidates(ctx context.Context) ([]string, []string, error) {
	paths := []string{}
	err := filepath.WalkDir(s.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if path == s.Root {
			return nil
		}
		rel, err := filepath.Rel(s.Root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if excluded(rel, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			paths = append(paths, rel)
			if len(paths) > maxFiles {
				return errors.New("snapshot file limit exceeded")
			}
		}
		return nil
	})
	if err != nil || len(paths) == 0 {
		return paths, nil, err
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
	kept := []string{}
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

// capture streams eligible files into Git's standard importer. Rooted reads
// cannot follow a replaced ancestor symlink outside the Crew. Git owns object
// hashing/deduplication; no user Git config, attributes or clean filters run.
func (s Store) capture(ctx context.Context, ref string, stats *CaptureStats, skippedState map[string]string) (string, []string, []string, error) {
	paths, omitted, err := s.candidates(ctx)
	if err != nil {
		return "", nil, nil, err
	}
	root, err := os.OpenRoot(s.Root)
	if err != nil {
		return "", nil, nil, err
	}
	defer root.Close()
	stats.Eligible = len(paths)
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
	return s.begin(ctx, ctx, execution, 0, 0)
}

// BeginWithin waits up to lockWait for the Crew gate, then captures within a
// budget chosen by cache state: cold when no file hashes are cached (after a
// restart or the first capture, every file is read), warm otherwise. The wait
// does not use up the capture budget.
func (s Store) BeginWithin(lockWait, warm, cold time.Duration, execution string) (Record, error) {
	lockCtx, cancel := context.WithTimeout(context.Background(), lockWait)
	defer cancel()
	return s.begin(lockCtx, context.Background(), execution, warm, cold)
}

func (s Store) begin(lockCtx, ctx context.Context, execution string, warm, cold time.Duration) (Record, error) {
	unlock, err := s.lock(lockCtx)
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
	if budget := captureBudget(r.BeforeCapture.Cold, warm, cold); budget > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, budget)
		defer cancel()
	}
	r.Before, r.Skipped, r.Omitted, err = s.capture(ctx, "refs/turns/"+id+"/before", &r.BeforeCapture, r.BeforeSkippedState)
	if err != nil || r.Before == "" {
		r.Status = "unavailable"
	}
	saveErr := s.save(r)
	if err != nil {
		return r, err
	}
	return r, saveErr
}

func captureBudget(isCold bool, warm, cold time.Duration) time.Duration {
	if isCold {
		return cold
	}
	return warm
}

// Failed reports whether a retained reply's capture failed, so it saved no
// before or after state. A record that is gone (retention) is not Failed.
func (s Store) Failed(id string) bool {
	r, err := s.read(id)
	return err == nil && r.Status == "unavailable"
}
func (s Store) Finish(ctx context.Context, execution string) (Record, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return Record{}, err
	}
	defer unlock()
	r, err := s.read(ID(execution))
	if err != nil {
		return r, err
	}
	if r.Status != "capturing" && !(r.Status == "unavailable" && r.Before == "") {
		return r, nil
	}
	var skipped, omitted []string
	r.AfterSkippedState = map[string]string{}
	r.After, skipped, omitted, err = s.capture(ctx, "refs/turns/"+r.ID+"/after", &r.AfterCapture, r.AfterSkippedState)
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
