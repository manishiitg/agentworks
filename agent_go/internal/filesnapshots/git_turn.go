package filesnapshots

// Reply history captures only Git-reported changed paths. Project Git remains
// read-only; raw before/after bytes live in our private store for diffs/Undo.
import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
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
}

func (s Store) turnRepos(ctx context.Context, dirs []string) (map[string]gitTurnRepo, error) {
	out := map[string]gitTurnRepo{}
	for _, dir := range dirs {
		rel, err := filepath.Rel(s.Root, dir)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, errors.New("checkout outside workspace")
		}
		if rel == "." {
			rel = ""
		}
		rel = filepath.ToSlash(rel)
		raw, err := gitIn(ctx, s.Root, dir, "status", "--porcelain=v1", "-z", "--untracked-files=all").Output()
		if err != nil {
			return nil, fmt.Errorf("read checkout changes: %w", err)
		}
		head, _ := gitIn(ctx, s.Root, dir, "rev-parse", "--verify", "HEAD").Output()
		branch, _ := gitIn(ctx, s.Root, dir, "symbolic-ref", "--quiet", "--short", "HEAD").Output()
		remote, _ := gitIn(ctx, s.Root, dir, "config", "--get", "remote.origin.url").Output()
		gitdir, err := gitIn(ctx, s.Root, dir, "rev-parse", "--absolute-git-dir").Output()
		if err != nil {
			return nil, err
		}
		identity, err := checkoutIdentity(strings.TrimSpace(string(gitdir)))
		if err != nil {
			return nil, err
		}
		repo := gitTurnRepo{Head: strings.TrimSpace(string(head)), Label: CheckoutLabel{ID: identity, Root: rel, Name: filepath.Base(dir), Branch: strings.TrimSpace(string(branch)), GitHubRepo: GitHubRepo(string(remote))}}
		if repo.Label.Branch == "" && len(repo.Head) >= 8 {
			repo.Label.Branch = "detached@" + repo.Head[:8]
		}
		rows := bytes.Split(raw, []byte{0})
		for i := 0; i < len(rows); i++ {
			row := rows[i]
			if len(row) < 4 {
				continue
			}
			repo.Dirty = append(repo.Dirty, string(row[3:]))
			if bytes.ContainsAny(row[:2], "RC") && i+1 < len(rows) {
				i++
				repo.Dirty = append(repo.Dirty, string(rows[i]))
			}
		}
		out[rel] = repo
	}
	return out, nil
}
func turnPath(root, p string) string { return path.Join(root, p) }
func validTurnPath(p string) bool {
	return p != "" && p != "." && !path.IsAbs(p) && path.Clean(p) == p && p != ".." && !strings.HasPrefix(p, "../") && !excluded(p, false)
}

func (s Store) rawTurnFile(p string) (turnBlob, bool, error) {
	root, err := os.OpenRoot(s.Root)
	if err != nil {
		return turnBlob{}, false, err
	}
	defer root.Close()
	info, err := root.Lstat(p)
	if os.IsNotExist(err) {
		return turnBlob{}, false, nil
	}
	if err != nil {
		return turnBlob{}, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		data, err := root.Readlink(p)
		return turnBlob{"120000", []byte(data)}, true, err
	}
	if !info.Mode().IsRegular() || info.Size() > MaxFileBytes {
		return turnBlob{}, false, errors.New("file cannot be captured")
	}
	f, err := root.Open(p)
	if err != nil {
		return turnBlob{}, false, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if len(data) > MaxFileBytes {
		return turnBlob{}, false, errors.New("file too large")
	}
	mode := "100644"
	if info.Mode()&0111 != 0 {
		mode = "100755"
	}
	return turnBlob{mode, data}, true, err
}

// writeTurnFiles creates a self-contained tree of selected paths, with no
// parent/index/workspace traversal and no attributes or external filters.
func (s Store) writeTurnFiles(ctx context.Context, ref string, files map[string]turnBlob) (string, error) {
	var input bytes.Buffer
	fmt.Fprintf(&input, "commit %s\ncommitter AgentWorks <history@example.com> 0 +0000\ndata 13\nReply changes\ndeleteall\n", ref)
	for _, p := range turnKeys(files) {
		b := files[p]
		fmt.Fprintf(&input, "M %s inline %s\ndata %d\n", b.mode, strconv.Quote(p), len(b.data))
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

func (s Store) BeginGitTurn(ctx context.Context, execution string, dirs []string) (Record, error) {
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
	r.GitTurn, err = s.turnRepos(ctx, dirs)
	if err != nil {
		return r, err
	}
	files := map[string]turnBlob{}
	var total int64
	for _, root := range turnKeys(r.GitTurn) {
		repo := r.GitTurn[root]
		r.BeforeRepos = append(r.BeforeRepos, root)
		r.BeforeHeads[root] = repo.Head
		for _, p := range repo.Dirty {
			full := turnPath(root, p)
			if !validTurnPath(full) {
				continue
			}
			b, present, e := s.rawTurnFile(full)
			if e != nil || total+int64(len(b.data)) > maxImportBytes {
				r.Skipped = append(r.Skipped, full)
				continue
			}
			if present {
				files[full] = b
				total += int64(len(b.data))
			}
		}
	}
	r.BeforeCapture = CaptureStats{Repos: len(r.GitTurn), Captured: len(files), ImportedBytes: total, Changed: len(files)}
	r.Before, err = s.writeTurnFiles(ctx, "refs/turns/"+r.ID+"/before", files)
	if err != nil {
		return r, err
	}
	return r, s.save(r)
}

func (s Store) FinishGitTurn(ctx context.Context, execution string, dirs []string) (Record, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return Record{}, err
	}
	defer unlock()
	r, err := s.read(ID(execution))
	if err != nil || r.Status != "capturing" {
		return r, err
	}
	repos, err := s.turnRepos(ctx, dirs)
	if err != nil {
		return r, err
	}
	before, after := map[string]turnBlob{}, map[string]turnBlob{}
	skipped := map[string]bool{}
	for _, p := range r.Skipped {
		skipped[p] = true
	}
	r.Review = &savedReview{Roots: turnKeys(repos), Spans: []reviewSpan{}}
	var total int64
	changedRoots := map[string]bool{}
	for _, root := range turnKeys(repos) {
		end := repos[root]
		start, existed := r.GitTurn[root]
		dir := filepath.Join(s.Root, filepath.FromSlash(root))
		if !existed { // A new checkout's existing contents are not edits.
			base, e := startCommit(ctx, s.Root, dir, r.StartedAt, "--until=@"+strconv.FormatInt(r.StartedAt.Unix()-1, 10))
			if e != nil {
				r.Review.IncompleteRoots = append(r.Review.IncompleteRoots, root)
				continue
			}
			start = gitTurnRepo{Head: base}
		}
		candidates := map[string]bool{}
		dirty := map[string]bool{}
		for _, p := range start.Dirty {
			candidates[p] = true
			dirty[p] = true
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
				return r, e
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
				return r, e
			}
			for _, p := range bytes.Split(raw, []byte{0}) {
				if len(p) > 0 {
					candidates[string(p)] = true
				}
			}
		}
		for _, p := range turnKeys(candidates) {
			full := turnPath(root, p)
			if !validTurnPath(p) || !validTurnPath(full) || skipped[full] {
				continue
			}
			var b turnBlob
			var present bool
			var e error
			if dirty[p] {
				b, present, e = s.savedTurnFile(ctx, r.Before, full)
			} else if start.Head != "" {
				b, present, e = s.committedTurnFile(ctx, dir, start.Head, p)
			}
			if e != nil {
				skipped[full] = true
				continue
			}
			a, exists, e := s.rawTurnFile(full)
			if e != nil {
				skipped[full] = true
				continue
			}
			if total+int64(len(b.data)+len(a.data)) > maxImportBytes {
				skipped[full] = true
				continue
			}
			if present != exists || b.mode != a.mode || !bytes.Equal(b.data, a.data) {
				changedRoots[root] = true
			}
			total += int64(len(b.data) + len(a.data))
			if present {
				before[full] = b
			}
			if exists {
				after[full] = a
			}
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
	r.AfterCapture = CaptureStats{Repos: len(repos), Captured: len(after), Changed: len(after), ImportedBytes: total}
	err = s.save(r)
	if err == nil {
		s.prune(ctx)
	}
	return r, err
}

func (s Store) savedTurnFile(ctx context.Context, tree, p string) (turnBlob, bool, error) {
	return s.treeTurnFile(ctx, s.command(ctx, "ls-tree", "-z", tree, "--", p), func(oid string) *exec.Cmd { return s.command(ctx, "cat-file", "blob", oid) })
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
	return turnBlob{fields[0], data}, true, waitErr
}

func turnKeys[T any](items map[string]T) []string {
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
