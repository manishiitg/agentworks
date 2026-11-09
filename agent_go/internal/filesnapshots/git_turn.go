package filesnapshots

// Reply history uses Git-reported paths inside checkouts and private raw
// snapshots for ordinary files. Project Git stays read-only.
import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type gitTurnRepo struct {
	Label CheckoutLabel `json:"label"`
	Head  string        `json:"head"`
	Dirty []string      `json:"dirty"`
}
type turnBlob struct {
	mode string
	data []byte
	oid  string
}

// Reuse stored blob IDs without reading their bytes back out of private Git.
func (s Store) turnTree(ctx context.Context, ref string) (map[string]turnBlob, error) {
	out, err := s.command(ctx, "ls-tree", "-r", "-z", ref).Output()
	if err != nil {
		return nil, err
	}
	files := map[string]turnBlob{}
	for _, row := range bytes.Split(out, []byte{0}) {
		meta, p, ok := strings.Cut(string(row), "\t")
		fields := strings.Fields(meta)
		if ok && len(fields) == 3 && fields[1] == "blob" {
			files[p] = turnBlob{mode: fields[0], oid: fields[2]}
		}
	}
	return files, nil
}

// The ordinary-file and checkout dirty-path candidates share the existing
// private indexes. Only changed stat entries import bytes; all other versions
// remain blob IDs throughout capture and comparison.
func (s Store) captureTurnFiles(ctx context.Context, ref, boundary string, loose []string, repoPaths map[string][]string, stats *CaptureStats, skippedState map[string]string) (map[string]turnBlob, []string, []string, error) {
	started := time.Now()
	stats.WalkedFiles = len(loose)
	files, omitted, err := s.filterLoosePaths(ctx, loose)
	if err != nil {
		return nil, nil, nil, err
	}
	checkouts := []*checkout{{dir: s.Root, key: "loose", loose: true, files: files}}
	paths := len(files)
	for _, root := range turnKeys(repoPaths) {
		dir := filepath.Join(s.Root, filepath.FromSlash(root))
		c := &checkout{dir: dir, prefix: root, key: checkoutKey(root, dir)}
		unique := map[string]bool{}
		for _, p := range repoPaths[root] {
			if validTurnPath(p) && validTurnPath(turnPath(root, p)) {
				unique[p] = true
			}
		}
		c.files = turnKeys(unique)
		paths += len(c.files)
		checkouts = append(checkouts, c)
	}
	tree, skipped, _, err := s.captureListed(ctx, ref, "", stats, skippedState, maxImportBytes, checkouts, omitted)
	log.Printf("[FILE_SNAPSHOT] phase=reply_capture ref=%s boundary=%s paths=%d walked=%d reused=%d imported_files=%d imported_bytes=%d budget_skipped=%d elapsed_ms=%d success=%t", ref, boundary, paths, len(loose), stats.Reused, stats.Changed, stats.ImportedBytes, stats.BudgetSkipped, time.Since(started).Milliseconds(), err == nil)
	if err != nil {
		return nil, skipped, omitted, err
	}
	blobs, err := s.turnTree(ctx, tree)
	return blobs, skipped, omitted, err
}

// Do not walk checkout contents: Git reports their changed paths. A .git file
// marks a linked worktree too; turnRepos validates it through safegit.CommandIn.
// Ordinary folders have no user Git and use our private before/after copies.
func (s Store) turnPaths(ctx context.Context, stats *CaptureStats) (dirs, loose []string, err error) {
	err = filepath.WalkDir(s.Root, func(p string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(s.Root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if p != s.Root && excluded(rel, entry.IsDir()) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if _, err := os.Lstat(filepath.Join(p, ".git")); err == nil {
				dirs = append(dirs, p)
				return filepath.SkipDir
			} else if !os.IsNotExist(err) {
				return err
			}
		} else if validTurnPath(rel) {
			loose = append(loose, rel)
		}
		return nil
	})
	return
}

// turnCandidatePaths uses index/path metadata rather than status, whose refresh
// can hash the same uncommitted file on every reply. A touched-but-unchanged file
// may remain a candidate; the private stat index decides whether bytes changed.
func (s Store) turnCandidatePaths(ctx context.Context, dir, head string) ([]string, error) {
	paths := map[string]bool{}
	rawPaths := func(args ...string) error {
		out, err := gitIn(ctx, s.Root, dir, args...).Output()
		if err != nil {
			return err
		}
		rows := bytes.Split(out, []byte{0})
		for i := 0; i < len(rows) && len(rows[i]) > 0; i += 2 {
			if rows[i][0] != ':' || i+1 >= len(rows) || len(rows[i+1]) == 0 {
				return errors.New("invalid Git path metadata")
			}
			if validTurnPath(string(rows[i+1])) {
				paths[string(rows[i+1])] = true
			}
		}
		return nil
	}
	if err := rawPaths("diff-files", "--raw", "--no-renames", "--no-ext-diff", "--no-textconv", "-z", "--"); err != nil {
		return nil, err
	}
	if head != "" {
		if err := rawPaths("diff-index", "--cached", "--raw", "--no-renames", "--no-ext-diff", "--no-textconv", "-z", head, "--"); err != nil {
			return nil, err
		}
	} else {
		out, err := gitIn(ctx, s.Root, dir, "ls-files", "-z").Output()
		if err != nil {
			return nil, err
		}
		for _, p := range bytes.Split(out, []byte{0}) {
			if len(p) > 0 && validTurnPath(string(p)) {
				paths[string(p)] = true
			}
		}
	}
	child, stop := context.WithCancel(ctx)
	defer stop()
	cmd := gitIn(child, s.Root, dir, "ls-files", "--others", "--exclude-standard", "-z")
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	reader := bufio.NewReader(pipe)
	var readErr error
	for {
		p, err := reader.ReadString(0)
		if err != nil {
			if err != io.EOF {
				readErr = err
			}
			break
		}
		candidate := strings.TrimSuffix(p, string([]byte{0}))
		if validTurnPath(candidate) {
			paths[candidate] = true
		}
	}
	_ = pipe.Close()
	waitErr := cmd.Wait()
	if readErr != nil {
		return nil, readErr
	}
	if waitErr != nil {
		return nil, waitErr
	}

	return turnKeys(paths), nil
}

// turnRepos reads each checkout's candidate paths. A checkout Git cannot read
// (a broken .git folder, an index the server account cannot open) is skipped
// and named in the log, so one bad checkout never fails the whole reply.
func (s Store) turnRepos(ctx context.Context, dirs []string, stats *CaptureStats) (map[string]gitTurnRepo, map[string]bool, error) {
	// Leave the other half of the boundary for importing and publishing healthy
	// files. One slow checkout must not consume the entire capture deadline.
	budget := 30 * time.Second
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline)/2 < budget {
		budget = time.Until(deadline) / 2
	}
	discovery, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	type result struct {
		root string
		repo gitTurnRepo
		err  error
	}
	results := make([]result, len(dirs))
	for i, dir := range dirs {
		rel, err := filepath.Rel(s.Root, dir)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, nil, errors.New("checkout outside workspace")
		}
		if rel == "." {
			rel = ""
		}
		results[i].root = filepath.ToSlash(rel)
	}
	_ = parallel(len(dirs), func(i int) error {
		started := time.Now()
		repoCtx, stop := context.WithTimeout(discovery, 10*time.Second)
		defer stop()
		results[i].repo, results[i].err = s.turnRepo(repoCtx, dirs[i], results[i].root)
		if repoCtx.Err() != nil {
			results[i].err = repoCtx.Err()
		}
		log.Printf("[FILE_SNAPSHOT] phase=checkout_discovery dir=%q elapsed_ms=%d success=%t", results[i].root, time.Since(started).Milliseconds(), results[i].err == nil)
		return nil
	})
	out, skipped := map[string]gitTurnRepo{}, map[string]bool{}
	for _, r := range results {
		if r.err != nil {
			skipped[r.root] = true
			log.Printf("[SNAPSHOT] checkout_skipped dir=%q reason=%q", r.root, gitReason(r.err))
		} else {
			out[r.root] = r.repo
		}
	}
	return out, skipped, ctx.Err()
}

func (s Store) turnRepo(ctx context.Context, dir, root string) (gitTurnRepo, error) {
	head, _ := gitIn(ctx, s.Root, dir, "rev-parse", "--verify", "HEAD").Output()
	dirty, err := s.turnCandidatePaths(ctx, dir, strings.TrimSpace(string(head)))
	if err != nil {
		return gitTurnRepo{}, err
	}
	branch, _ := gitIn(ctx, s.Root, dir, "symbolic-ref", "--quiet", "--short", "HEAD").Output()
	remote, _ := gitIn(ctx, s.Root, dir, "config", "--get", "remote.origin.url").Output()
	gitdir, err := gitIn(ctx, s.Root, dir, "rev-parse", "--absolute-git-dir").Output()
	if err != nil {
		return gitTurnRepo{}, err
	}
	identity, err := checkoutIdentity(strings.TrimSpace(string(gitdir)))
	if err != nil {
		return gitTurnRepo{}, err
	}
	repo := gitTurnRepo{Head: strings.TrimSpace(string(head)), Dirty: dirty, Label: CheckoutLabel{ID: identity, Root: root, Name: filepath.Base(dir), Branch: strings.TrimSpace(string(branch)), GitHubRepo: GitHubRepo(string(remote))}}
	if repo.Label.Branch == "" && len(repo.Head) >= 8 {
		repo.Label.Branch = "detached@" + repo.Head[:8]
	}
	return repo, nil
}

// gitReason is the first line of Git's error output (never file contents).
func gitReason(err error) string {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if line, _, _ := strings.Cut(strings.TrimSpace(string(exit.Stderr)), "\n"); line != "" {
			err = errors.New(line)
		}
	}
	reason := err.Error()
	if len(reason) > 160 {
		reason = reason[:160]
	}
	return reason
}
func turnPath(root, p string) string { return path.Join(root, p) }
func validTurnPath(p string) bool {
	return p != "" && p != "." && !path.IsAbs(p) && path.Clean(p) == p && p != ".." && !strings.HasPrefix(p, "../") && !excluded(p, false)
}

// writeTurnFiles creates a self-contained tree of selected paths, with no
// parent/index/workspace traversal and no attributes or external filters.
func (s Store) writeTurnFiles(ctx context.Context, ref string, files map[string]turnBlob) (string, error) {
	var input bytes.Buffer
	fmt.Fprintf(&input, "commit %s\ncommitter AgentWorks <history@example.com> 0 +0000\ndata 13\nReply changes\ndeleteall\n", ref)
	for _, p := range turnKeys(files) {
		b := files[p]
		if b.oid != "" {
			fmt.Fprintf(&input, "M %s %s %s\n", b.mode, b.oid, gitQuotePath(p))
			continue
		}
		fmt.Fprintf(&input, "M %s inline %s\ndata %d\n", b.mode, gitQuotePath(p), len(b.data))
		input.Write(b.data)
		input.WriteByte('\n')
	}
	input.WriteString("\ndone\n")
	cmd := s.command(ctx, "fast-import", "--quiet", "--force", "--date-format=raw", "--done")
	cmd.Stdin = &input
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("save changed files: %w: %s", err, out)
	}
	out, err := s.command(ctx, "rev-parse", ref).Output()
	return strings.TrimSpace(string(out)), err
}

func (s Store) BeginGitTurn(ctx context.Context, execution string) (Record, error) {
	gateStarted := time.Now()
	unlock, err := s.lock(ctx)
	if err != nil {
		return Record{}, err
	}
	defer unlock()
	if r, err := s.read(ID(execution)); err == nil {
		return r, nil
	}
	if err := s.init(ctx); err != nil {
		return Record{}, err
	}
	r := Record{ID: ID(execution), ExecutionID: execution, StartedAt: time.Now().UTC(), Status: "capturing", BeforeHeads: map[string]string{}}
	r.BeforeCapture.GateMS = time.Since(gateStarted).Milliseconds()
	discoveryStarted := time.Now()
	dirs, loose, err := s.turnPaths(ctx, &r.BeforeCapture)
	if err != nil {
		return r, err
	}
	r.GitTurn, _, err = s.turnRepos(ctx, dirs, &r.BeforeCapture)
	r.BeforeCapture.DiscoveryMS = time.Since(discoveryStarted).Milliseconds()
	if err != nil {
		return r, err
	}
	// Remember unreadable roots too: recovering one next boundary must not
	// mistake its existing committed files for new work made during this turn.
	for _, dir := range dirs {
		rel, _ := filepath.Rel(s.Root, dir)
		if rel == "." {
			rel = ""
		}
		r.BeforeRepos = append(r.BeforeRepos, filepath.ToSlash(rel))
	}
	paths := map[string][]string{}
	for _, root := range turnKeys(r.GitTurn) {
		repo := r.GitTurn[root]
		r.BeforeHeads[root] = repo.Head
		paths[root] = repo.Dirty
	}
	r.BeforeSkippedState = map[string]string{}
	files, skipped, omitted, err := s.captureTurnFiles(ctx, "refs/turns/"+r.ID+"/cache-before", "before", loose, paths, &r.BeforeCapture, r.BeforeSkippedState)
	if err != nil {
		return r, err
	}
	r.Skipped = append(r.Skipped, skipped...)
	r.Omitted = append(r.Omitted, omitted...)
	r.BeforeCapture.Repos = len(r.GitTurn)
	r.Before, err = s.writeTurnFiles(ctx, "refs/turns/"+r.ID+"/before", files)
	if err != nil {
		return r, err
	}
	return r, s.save(r)
}

func (s Store) FinishGitTurn(ctx context.Context, execution string) (Record, error) {
	gateStarted := time.Now()
	unlock, err := s.lock(ctx)
	if err != nil {
		return Record{}, err
	}
	defer unlock()
	r, err := s.read(ID(execution))
	r.AfterCapture.GateMS = time.Since(gateStarted).Milliseconds()
	if err != nil || r.Status != "capturing" {
		return r, err
	}
	discoveryStarted := time.Now()
	dirs, loose, err := s.turnPaths(ctx, &r.AfterCapture)
	if err != nil {
		return r, err
	}
	repos, unreadable, err := s.turnRepos(ctx, dirs, &r.AfterCapture)
	r.AfterCapture.DiscoveryMS = time.Since(discoveryStarted).Milliseconds()
	if err != nil {
		return r, err
	}
	before, after := map[string]turnBlob{}, map[string]turnBlob{}
	skipped := map[string]bool{}
	for _, p := range r.Skipped {
		skipped[p] = true
	}
	r.Review = &savedReview{Roots: turnKeys(repos), Spans: []reviewSpan{}, IncompleteRoots: turnKeys(unreadable)}
	presentBefore := map[string]bool{}
	for _, root := range r.BeforeRepos {
		presentBefore[root] = true
	}
	previous, err := s.turnTree(ctx, r.Before)
	if err != nil {
		return r, err
	}
	starts := map[string]gitTurnRepo{}
	paths := map[string][]string{}
	changedRoots := map[string]bool{}
	for _, root := range turnKeys(repos) {
		end := repos[root]
		paths[root] = nil
		start, existed := r.GitTurn[root]
		dir := filepath.Join(s.Root, filepath.FromSlash(root))
		if !existed && presentBefore[root] {
			// A discovered checkout whose before-capture failed has no trusted
			// baseline. Never call pre-existing dirty files new reply edits.
			r.Review.IncompleteRoots = append(r.Review.IncompleteRoots, root)
			continue
		}
		if !existed { // A new checkout's existing contents are not edits.
			base, e := startCommit(ctx, s.Root, dir, r.StartedAt, "--until=@"+strconv.FormatInt(r.StartedAt.Unix()-1, 10))
			if e != nil {
				r.Review.IncompleteRoots = append(r.Review.IncompleteRoots, root)
				continue
			}
			start = gitTurnRepo{Head: base}
		}
		candidates := map[string]bool{}
		for _, p := range start.Dirty {
			candidates[p] = true
		}
		for _, p := range end.Dirty {
			candidates[p] = true
		}
		if start.Head != "" && end.Head != "" && start.Head != end.Head {
			if gitIn(ctx, s.Root, dir, "merge-base", "--is-ancestor", start.Head, end.Head).Run() != nil {
				r.Review.IncompleteRoots = append(r.Review.IncompleteRoots, root)
				continue
			}
			raw, e := gitIn(ctx, s.Root, dir, "diff", "--name-only", "--no-renames", "--no-ext-diff", "--no-textconv", "-z", start.Head, end.Head, "--").Output()
			if e != nil {
				r.Review.IncompleteRoots = append(r.Review.IncompleteRoots, root)
				continue
			}
			for _, p := range bytes.Split(raw, []byte{0}) {
				if len(p) > 0 {
					candidates[string(p)] = true
				}
			}
		}
		if start.Head == "" && end.Head != "" {
			raw, e := gitIn(ctx, s.Root, dir, "ls-tree", "-r", "--name-only", "-z", end.Head).Output()
			if e != nil {
				r.Review.IncompleteRoots = append(r.Review.IncompleteRoots, root)
				continue
			}
			for _, p := range bytes.Split(raw, []byte{0}) {
				if len(p) > 0 {
					candidates[string(p)] = true
				}
			}
		}
		starts[root] = start
		paths[root] = turnKeys(candidates)
	}
	r.AfterSkippedState = map[string]string{}
	current, cacheSkipped, omitted, err := s.captureTurnFiles(ctx, "refs/turns/"+r.ID+"/cache-after", "after", loose, paths, &r.AfterCapture, r.AfterSkippedState)
	if err != nil {
		return r, err
	}
	r.Omitted = append(r.Omitted, omitted...)
	for _, p := range cacheSkipped {
		skipped[p] = true
	}
	for _, root := range turnKeys(starts) {
		start := starts[root]
		dir := filepath.Join(s.Root, filepath.FromSlash(root))
		dirty := map[string]bool{}
		for _, p := range start.Dirty {
			dirty[p] = true
		}
		for _, p := range paths[root] {
			full := turnPath(root, p)
			if !validTurnPath(p) || !validTurnPath(full) || skipped[full] {
				continue
			}
			b, present := previous[full]
			if !dirty[p] && start.Head != "" {
				b, present, err = s.committedTurnFile(ctx, dir, start.Head, p)
				if err == nil && present {
					b, err = s.importTurnBlob(ctx, b, &r.AfterCapture)
				}
				if err != nil {
					skipped[full] = true
					continue
				}
			}
			a, exists := current[full]
			if present != exists || b.mode != a.mode || b.oid != a.oid {
				changedRoots[root] = true
			}
			if present {
				before[full] = b
			}
			if exists {
				after[full] = a
			}
		}
	}

	// The before tree also names deleted ordinary files. Exclude both starting
	// and current checkout roots so creating/switching a checkout cannot make
	// its existing contents look like ordinary-file additions or deletions.
	roots := append([]string(nil), r.BeforeRepos...)
	for _, dir := range dirs {
		rel, _ := filepath.Rel(s.Root, dir)
		if rel == "." {
			rel = ""
		}
		roots = append(roots, filepath.ToSlash(rel))
	}
	ignored := map[string]bool{}
	for _, p := range r.Omitted {
		ignored[p] = true
	}
	candidates := map[string]bool{}
	for _, p := range append(turnKeys(current), turnKeys(previous)...) {
		if _, repo := savedRoot(p, roots); !repo && validTurnPath(p) && !ignored[p] {
			candidates[p] = true
		}
	}
	for _, p := range turnKeys(candidates) {
		if skipped[p] {
			continue
		}
		b, present := previous[p]
		a, exists := current[p]
		if present == exists && b.mode == a.mode && b.oid == a.oid {
			continue
		}
		if present {
			before[p] = b
		}
		if exists {
			after[p] = a
		}
	}
	r.Skipped = turnKeys(skipped)
	r.Before, err = s.writeTurnFiles(ctx, "refs/turns/"+r.ID+"/before", before)
	if err != nil {
		return r, err
	}
	r.After, err = s.writeTurnFiles(ctx, "refs/turns/"+r.ID+"/after", after)
	if err != nil {
		return r, err
	}
	for _, root := range turnKeys(changedRoots) {
		r.Review.Spans = append(r.Review.Spans, reviewSpan{Checkout: repos[root].Label, Before: r.Before, After: r.After})
	}
	now := time.Now().UTC()
	r.CompletedAt = &now
	r.Status = "complete"
	if len(r.Skipped) > 0 || len(r.Review.IncompleteRoots) > 0 {
		r.Status = "partial"
	}
	r.AfterCapture.Repos = len(repos)
	r.AfterCapture.Captured = len(after)
	err = s.save(r)
	if err == nil {
		s.prune(ctx)
	}
	return r, err
}

// A committed before-image not already in private Git is imported once. Dirty
// files use the saved blob IDs directly and never enter this content path.
func (s Store) importTurnBlob(ctx context.Context, b turnBlob, stats *CaptureStats) (turnBlob, error) {
	if b.oid != "" {
		return b, nil
	}
	if stats.ImportedBytes+int64(len(b.data)) > maxImportBytes {
		stats.BudgetSkipped++
		return turnBlob{}, errors.New("capture size limit exceeded")
	}
	cmd := s.command(ctx, "hash-object", "-w", "--stdin", "--no-filters")
	cmd.Stdin = bytes.NewReader(b.data)
	out, err := cmd.Output()
	if err != nil {
		return turnBlob{}, err
	}
	stats.ImportedBytes += int64(len(b.data))
	stats.Changed++
	return turnBlob{mode: b.mode, oid: strings.TrimSpace(string(out))}, nil
}

// A clean path's committed bytes are a valid before-image only when Git does
// not transform them in the worktree. Never turn a canonicalized blob into Undo.
func (s Store) committedTurnFile(ctx context.Context, dir, head, p string) (turnBlob, bool, error) {
	auto, _ := gitIn(ctx, s.Root, dir, "config", "--get", "core.autocrlf").Output()
	if a := strings.TrimSpace(string(auto)); a != "" && a != "false" {
		return turnBlob{}, false, errors.New("converted worktree bytes unavailable")
	}
	attrs, err := gitIn(ctx, s.Root, dir, "check-attr", "-z", "text", "eol", "ident", "filter", "working-tree-encoding", "--", p).Output()
	if err != nil {
		return turnBlob{}, false, err
	}
	fields := bytes.Split(attrs, []byte{0})
	for i := 2; i < len(fields); i += 3 {
		v := string(fields[i])
		if v != "unspecified" && v != "unset" {
			return turnBlob{}, false, errors.New("converted worktree bytes unavailable")
		}
	}
	return s.treeTurnFile(ctx, gitIn(ctx, s.Root, dir, "ls-tree", "-z", head, "--", p), func(oid string) *exec.Cmd { return gitIn(ctx, s.Root, dir, "cat-file", "blob", oid) })
}
func (s Store) treeTurnFile(ctx context.Context, list *exec.Cmd, blob func(string) *exec.Cmd) (turnBlob, bool, error) {
	raw, err := list.Output()
	if err != nil {
		return turnBlob{}, false, err
	}
	if len(raw) == 0 {
		return turnBlob{}, false, nil
	}
	meta, _, ok := strings.Cut(string(raw), "\t")
	fields := strings.Fields(meta)
	if !ok || len(fields) != 3 || fields[1] != "blob" {
		return turnBlob{}, false, errors.New("not a regular blob")
	}
	check := s.command(ctx, "cat-file", "--batch-check=%(objecttype) %(objectsize)")
	check.Stdin = strings.NewReader(fields[2] + "\n")
	if out, e := check.Output(); e == nil {
		cached := strings.Fields(string(out))
		if len(cached) == 2 && cached[0] == "blob" {
			if size, e := strconv.ParseInt(cached[1], 10, 64); e == nil && size <= MaxFileBytes {
				return turnBlob{mode: fields[0], oid: fields[2]}, true, nil
			}
		}
	}
	cmd := blob(fields[2])
	out, err := cmd.StdoutPipe()
	if err != nil {
		return turnBlob{}, false, err
	}
	if err = cmd.Start(); err != nil {
		return turnBlob{}, false, err
	}
	data, readErr := io.ReadAll(io.LimitReader(out, MaxFileBytes+1))
	if len(data) > MaxFileBytes {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return turnBlob{}, false, errors.New("file too large")
	}
	waitErr := cmd.Wait()
	if readErr != nil {
		return turnBlob{}, false, readErr
	}
	return turnBlob{mode: fields[0], data: data}, true, waitErr
}

func turnKeys[T any](items map[string]T) []string {
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
