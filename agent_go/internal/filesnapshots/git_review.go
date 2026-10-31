package filesnapshots

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// CheckoutLabel is captured context, never a lookup of today's branch for an
// old file. ID identifies a local checkout, not its editable branch or remote.
type CheckoutLabel struct {
	ID         string `json:"id"`
	Root       string `json:"root"`
	Name       string `json:"name"`
	Branch     string `json:"branch,omitempty"`
	GitHubRepo string `json:"github_repo,omitempty"`
}

type gitCheckpoint struct {
	CheckoutLabel
	Head        string `json:"head"`
	Cursor      string `json:"cursor"`
	CursorCount int    `json:"cursor_count"`
}

type reviewSpan struct {
	Checkout CheckoutLabel `json:"checkout"`
	Before   string        `json:"before"`
	After    string        `json:"after"`
	Skipped  []string      `json:"skipped,omitempty"`
}

type savedReview struct {
	Roots           []string     `json:"roots"`
	Spans           []reviewSpan `json:"spans"`
	ForeignRoots    []string     `json:"foreign_roots,omitempty"`
	IncompleteRoots []string     `json:"incomplete_roots,omitempty"`
}

type savedVersion struct{ before, after, path string }

func digest(value string) string {
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:16])
}

func reflog(ctx context.Context, root, dir string, n int) []string {
	out, err := gitIn(ctx, root, dir, "reflog", "show", "--max-count="+strconv.Itoa(n), "--date=raw", "--format=%H%x1f%gD%x1f%gs", "HEAD").Output()
	if err != nil || len(bytes.TrimSpace(out)) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(out)), "\n")
}

// The hash of a bounded suffix identifies the captured reflog even when many
// moves happen in the same second. Rewritten/expired logs do not match and are
// reported incomplete. No shell commands or commit messages are persisted.
func reflogDelta(before gitCheckpoint, lines []string) ([]string, bool) {
	if before.CursorCount == 0 {
		return nil, false
	}
	match := -1
	for i := 0; i+before.CursorCount <= len(lines); i++ {
		if digest(strings.Join(lines[i:i+before.CursorCount], "\n")) == before.Cursor {
			if match >= 0 {
				return nil, false // repeated entries cannot establish the boundary
			}
			match = i
		}
	}
	if match < 0 {
		return nil, false
	}
	return lines[:match], true
}

func (s Store) gitCheckpoints(ctx context.Context, repos map[string]string, limit int) (map[string]gitCheckpoint, map[string][]string) {
	out, logs := map[string]gitCheckpoint{}, map[string][]string{}
	for _, p := range sortedKeys(repos) {
		dir := repos[p]
		raw, err := gitIn(ctx, s.Root, dir, "rev-parse", "--absolute-git-dir", "HEAD").Output()
		fields := strings.Split(strings.TrimSpace(string(raw)), "\n")
		if err != nil || len(fields) != 2 || !commitID.MatchString(fields[1]) {
			continue
		}
		gitdir, err := filepath.EvalSymlinks(fields[0])
		if err != nil {
			continue
		}
		branch, _ := gitIn(ctx, s.Root, dir, "symbolic-ref", "--short", "-q", "HEAD").Output()
		remote, _ := gitIn(ctx, s.Root, dir, "config", "--get", "remote.origin.url").Output()
		name := filepath.Base(dir)
		c := gitCheckpoint{CheckoutLabel: CheckoutLabel{ID: digest(gitdir), Root: p, Name: name, Branch: strings.TrimSpace(string(branch)), GitHubRepo: GitHubRepo(string(remote))}, Head: fields[1]}
		if c.Branch == "" {
			c.Branch = "detached@" + c.Head[:8]
		}
		lines := reflog(ctx, s.Root, dir, limit)
		logs[p] = lines
		c.CursorCount, c.Cursor = len(lines), digest(strings.Join(lines, "\n"))
		out[p] = c
	}
	names := map[string]int{}
	for _, c := range out {
		names[c.Name]++
	}
	for p, c := range out {
		if names[c.Name] > 1 && p != "" {
			c.Name = p
			out[p] = c
		}
	}
	return out, logs
}

// headView imports only the existing snapshot policy's eligible Git blobs.
// Review reads never revisit project Git; even discarded branches stay readable.
func (s Store) headView(ctx context.Context, r *Record, p, dir, head string) (string, error) {
	if !commitID.MatchString(head) {
		return "", errors.New("invalid saved HEAD")
	}
	ref := "refs/turns/" + r.ID + "/git/" + digest(p) + "/" + head
	if out, err := s.command(ctx, "rev-parse", "--verify", "-q", ref).Output(); err == nil {
		return strings.TrimSpace(string(out)), nil
	}
	raw, err := gitIn(ctx, s.Root, dir, "ls-tree", "-r", "-l", "-z", "--full-tree", head).Output()
	if err != nil {
		return "", err
	}
	var entries []baselineEntry
	for _, row := range bytes.Split(raw, []byte{0}) {
		meta, name, ok := strings.Cut(string(row), "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 4 || f[1] != "blob" {
			continue
		}
		full := name
		if p != "" {
			full = p + "/" + name
		}
		if excluded(full, false) {
			continue
		}
		if size, err := strconv.ParseInt(f[3], 10, 64); err != nil || size > MaxFileBytes {
			r.BaselineSkipped = append(r.BaselineSkipped, full)
			continue
		}
		entries = append(entries, baselineEntry{mode: f[0], oid: f[2], path: full, dir: dir})
	}
	if len(entries) == 0 {
		// Keep an empty tracked tree reachable too (a branch deleting files).
		cmd := s.command(ctx, "fast-import", "--quiet", "--done", "--date-format=raw")
		cmd.Stdin = strings.NewReader(fmt.Sprintf("commit %s\ncommitter AgentWorks <snapshot@example.com> %d +0000\ndata 8\nbaseline\ndeleteall\n\ndone\n", ref, time.Now().Unix()))
		if err := cmd.Run(); err != nil {
			return "", err
		}
		out, err := s.command(ctx, "rev-parse", "--verify", ref).Output()
		return strings.TrimSpace(string(out)), err
	}
	if err := s.importBaselines(ctx, r, entries, ref); err != nil {
		return "", err
	}
	out, err := s.command(ctx, "rev-parse", "--verify", ref).Output()
	return strings.TrimSpace(string(out)), err
}

func ownCommit(message string) bool {
	return strings.HasPrefix(message, "commit") || strings.HasPrefix(message, "cherry-pick:") || strings.HasPrefix(message, "revert:")
}

func gitMove(message string) bool {
	for _, prefix := range []string{"checkout:", "reset:", "pull", "merge ", "rebase "} {
		if strings.HasPrefix(message, prefix) {
			return true
		}
	}
	return false
}

func birthCheckpoint(r Record, end gitCheckpoint, lines []string) (gitCheckpoint, []string, bool) {
	base := r.BeforeHeads[end.Root]
	if !commitID.MatchString(base) || len(lines) == 0 {
		return gitCheckpoint{}, nil, false
	}
	first := strings.SplitN(lines[len(lines)-1], "\x1f", 3)
	if len(first) != 3 || first[0] != base || (first[2] != "" && !strings.HasPrefix(first[2], "clone:")) {
		return gitCheckpoint{}, nil, false
	}
	_, stamp, _ := strings.Cut(first[1], "@{")
	fields := strings.Fields(stamp)
	if len(fields) == 0 {
		return gitCheckpoint{}, nil, false
	}
	at, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil || at < r.StartedAt.Unix() {
		return gitCheckpoint{}, nil, false
	}
	start := end
	start.Head = base
	delta := lines[:len(lines)-1]
	// worktree add writes an initial no-op reset after creating its HEAD.
	// Its creation baseline already accounts for that setup; it is not a
	// switch away from work done in the new checkout.
	for len(delta) > 0 {
		f := strings.SplitN(delta[len(delta)-1], "\x1f", 3)
		if len(f) != 3 || f[0] != base || f[2] != "reset: moving to HEAD" {
			break
		}
		delta = delta[:len(delta)-1]
	}
	for i := len(delta) - 1; i >= 0; i-- {
		f := strings.SplitN(delta[i], "\x1f", 3)
		if len(f) == 3 && strings.HasPrefix(f[2], "checkout: moving from ") {
			from, _, _ := strings.Cut(strings.TrimPrefix(f[2], "checkout: moving from "), " to ")
			start.Branch = from
			break
		}
	}
	return start, delta, true
}

func checkoutBranch(message, current string) string {
	if strings.HasPrefix(message, "checkout:") {
		if _, to, ok := strings.Cut(message, " to "); ok {
			if commitID.MatchString(to) {
				return "detached@" + to[:8]
			}
			return to
		}
	}
	return current
}

func (s Store) saveReview(ctx context.Context, r *Record, repos map[string]string) {
	if r.BeforeGit == nil {
		return
	}
	after, logs := s.gitCheckpoints(ctx, repos, 512)
	review := &savedReview{Roots: append([]string{}, r.BeforeRepos...), Spans: []reviewSpan{}}
	for _, p := range sortedKeys(repos) {
		found := false
		for _, old := range review.Roots {
			found = found || old == p
		}
		if !found {
			review.Roots = append(review.Roots, p)
		}
	}
	b, a, err := s.effective(ctx, *r)
	if err != nil {
		review.IncompleteRoots = append(review.IncompleteRoots, review.Roots...)
		r.Review = review
		return
	}
	for _, p := range review.Roots {
		end, exists := after[p]
		start, had := r.BeforeGit[p]
		if !exists {
			// Removing a worktree is not deleting every source file.
			if s.subtree(ctx, r.After, p) != "" || p == "" {
				review.IncompleteRoots = append(review.IncompleteRoots, p)
			}
			continue
		}
		var delta []string
		known := false
		if !had {
			if tree, newCopy := r.NewRepos[p]; newCopy {
				if tree == "" {
					review.IncompleteRoots = append(review.IncompleteRoots, p)
					continue
				}
				start, delta, known = birthCheckpoint(*r, end, logs[p])
				if !known {
					review.IncompleteRoots = append(review.IncompleteRoots, p)
					continue
				}
				had = true
			}
		}
		if !had {
			// The existing new-checkout baseline includes real commits made
			// during the turn; git-init repositories remain genuine additions.
			review.Spans = append(review.Spans, reviewSpan{Checkout: end.CheckoutLabel, Before: b, After: a})
			continue
		}
		if !known {
			delta, known = reflogDelta(start, logs[p])
		}
		if start.ID != end.ID || (!known && (start.CursorCount > 0 || start.Head != end.Head || start.Branch != end.Branch)) {
			review.IncompleteRoots = append(review.IncompleteRoots, p)
			continue
		}
		foreign := false
		proven := true
		for _, line := range delta {
			fields := strings.SplitN(line, "\x1f", 3)
			if len(fields) != 3 || (!ownCommit(fields[2]) && !gitMove(fields[2])) {
				proven = false
			}
			if len(fields) != 3 || !ownCommit(fields[2]) {
				foreign = true
			}
		}
		if !proven {
			review.IncompleteRoots = append(review.IncompleteRoots, p)
			continue
		}
		if !foreign {
			review.Spans = append(review.Spans, reviewSpan{Checkout: end.CheckoutLabel, Before: b, After: a})
			continue
		}
		review.ForeignRoots = append(review.ForeignRoots, p)
		spans, err := s.transitionSpans(ctx, r, repos[p], start, end, delta, b, a)
		if err != nil {
			review.IncompleteRoots = append(review.IncompleteRoots, p)
			log.Printf("[FILE_SNAPSHOT] phase=git_review snapshot=%s checkout=%s state=incomplete", r.ID, end.ID)
			continue
		}
		for i := range spans {
			kept := spans[i].Skipped[:0]
			for _, name := range spans[i].Skipped {
				if owner, ok := savedRoot(name, review.Roots); ok && owner == p {
					kept = append(kept, name)
				}
			}
			spans[i].Skipped = kept
		}
		review.Spans = append(review.Spans, spans...)
		for _, span := range spans {
			if len(span.Skipped) > 0 {
				review.IncompleteRoots = append(review.IncompleteRoots, p)
				break
			}
		}
	}
	// Pin derived views under the reply, independent of disposable view
	// caches and the live checkout's reflog retention.
	kept := review.Spans[:0]
	for i, span := range review.Spans {
		start, had := r.BeforeGit[span.Checkout.Root]
		end, present := after[span.Checkout.Root]
		delta, known := reflogDelta(start, logs[span.Checkout.Root])
		_, newCopy := r.NewRepos[span.Checkout.Root]
		unchanged := had && present && known && len(delta) == 0 && start.ID == end.ID && start.Head == end.Head && start.Branch == end.Branch && !newCopy
		args := []string{"--literal-pathspecs", "diff", "--quiet", "--no-ext-diff", "--no-textconv", span.Before, span.After, "--"}
		if span.Checkout.Root != "" {
			args = append(args, span.Checkout.Root)
		}
		if unchanged && s.command(ctx, args...).Run() == nil {
			continue // retain zero-diff checkout/HEAD moves and new-copy baselines
		}
		ref := "refs/turns/" + r.ID + "/review/" + strconv.Itoa(i)
		if s.command(ctx, "update-ref", ref+"/before", span.Before).Run() != nil || s.command(ctx, "update-ref", ref+"/after", span.After).Run() != nil {
			review.IncompleteRoots = append(review.IncompleteRoots, span.Checkout.Root)
			continue
		}
		kept = append(kept, span)
	}
	review.Spans = kept
	r.Review = review
}

func (s Store) transitionSpans(ctx context.Context, r *Record, dir string, start, end gitCheckpoint, delta []string, before, after string) ([]reviewSpan, error) {
	p := start.Root
	oldHead, err := s.headView(ctx, r, p, dir, start.Head)
	if err != nil {
		return nil, err
	}
	segmentHead := oldHead
	label, base, latest, head, committed := start.CheckoutLabel, before, before, start.Head, false
	reset := false
	var spans []reviewSpan
	for i := len(delta) - 1; i >= 0; i-- {
		f := strings.SplitN(delta[i], "\x1f", 3)
		if len(f) != 3 || !commitID.MatchString(f[0]) {
			return nil, errors.New("incomplete Git transition")
		}
		tree, err := s.headView(ctx, r, p, dir, f[0])
		if err != nil {
			return nil, err
		}
		if ownCommit(f[2]) {
			latest, committed = tree, true
		} else {
			if committed {
				b, err := s.carryDirty(ctx, base, before, oldHead, segmentHead, segmentHead, p, "", nil)
				if err != nil {
					return nil, err
				}
				a, err := s.carryDirty(ctx, latest, before, oldHead, segmentHead, latest, p, "", nil)
				if err != nil {
					return nil, err
				}
				uncertain, err := s.uncertainDirtyBeforeSwitch(ctx, before, oldHead, segmentHead, latest, after, p)
				if err != nil {
					return nil, err
				}
				spans = append(spans, reviewSpan{Checkout: label, Before: b, After: a, Skipped: uncertain})
			}
			label.Branch = checkoutBranch(f[2], label.Branch)
			base, latest, committed = tree, tree, false
			segmentHead = tree
			reset = strings.HasPrefix(f[2], "reset:")
		}
		head = f[0]
	}
	if head != end.Head || label.Branch != end.Branch {
		return nil, errors.New("Git moved during capture")
	}
	// Carrying an existing dirty file to another branch is not a new edit.
	// Git permits the simple carry when both branches have the same base blob.
	var skipped []string
	if reset && !committed {
		// A reset reflog does not record --soft/--mixed/--hard. Dirty
		// destination files could be carried old contents or new edits;
		// without a later commit, do not invent their starting state.
		args := []string{"--literal-pathspecs", "diff", "--no-ext-diff", "--no-textconv", "--name-only", "-z", segmentHead, after, "--"}
		if p != "" {
			args = append(args, p)
		}
		raw, err := s.command(ctx, args...).Output()
		if err != nil {
			return nil, err
		}
		for _, name := range bytes.Split(raw, []byte{0}) {
			if len(name) > 0 {
				skipped = append(skipped, string(name))
			}
		}
	}
	base, err = s.carryDirty(ctx, base, before, oldHead, segmentHead, segmentHead, p, after, &skipped)
	if err != nil {
		return nil, err
	}
	if committed {
		kept := skipped[:0]
		for _, name := range skipped {
			saved, savedOK := s.entry(ctx, latest, name)
			actual, actualOK := s.entry(ctx, after, name)
			if saved != actual || savedOK != actualOK {
				kept = append(kept, name)
			}
		}
		skipped = kept // a known committed blob is not an uncertain carried file
	}
	spans = append(spans, reviewSpan{Checkout: label, Before: base, After: after, Skipped: skipped})
	return spans, nil
}

// A commit omits unstaged files. If an initially dirty file is clean in that
// commit and different at the final capture, the reflog cannot establish
// whether it was restored before the switch or discarded by the switch.
func (s Store) uncertainDirtyBeforeSwitch(ctx context.Context, before, oldHead, segmentHead, committed, after, root string) ([]string, error) {
	args := []string{"--literal-pathspecs", "diff", "--no-ext-diff", "--no-textconv", "--name-only", "-z", oldHead, before, "--"}
	if root != "" {
		args = append(args, root)
	}
	raw, err := s.command(ctx, args...).Output()
	if err != nil {
		return nil, err
	}
	var skipped []string
	for _, name := range bytes.Split(raw, []byte{0}) {
		p := string(name)
		if p == "" {
			continue
		}
		old, oldOK := s.entry(ctx, oldHead, p)
		segment, segmentOK := s.entry(ctx, segmentHead, p)
		saved, savedOK := s.entry(ctx, committed, p)
		initial, initialOK := s.entry(ctx, before, p)
		final, finalOK := s.entry(ctx, after, p)
		if old == segment && oldOK == segmentOK && segment == saved && segmentOK == savedOK && (initial != final || initialOK != finalOK) {
			skipped = append(skipped, p)
		}
	}
	return skipped, nil
}

func inCheckout(file, root string) bool { return root == "" || strings.HasPrefix(file, root+"/") }

// No live branch or repository lookup participates in a saved comparison.
func savedRoot(file string, roots []string) (string, bool) {
	root, found := "", false
	for _, p := range roots {
		if inCheckout(file, p) && (!found || len(p) > len(root)) {
			root, found = p, true
		}
	}
	return root, found
}

func (s Store) carryDirty(ctx context.Context, base, before, oldHead, segmentHead, newHead, root, observedAfter string, uncertain *[]string) (string, error) {
	raw, err := s.command(ctx, "diff", "--no-ext-diff", "--no-textconv", "--name-only", "-z", oldHead, before).Output()
	if err != nil {
		return "", err
	}
	var edits []string
	for _, name := range bytes.Split(raw, []byte{0}) {
		p := string(name)
		if p == "" || !inCheckout(p, root) {
			continue
		}
		old, oldOK := s.entry(ctx, oldHead, p)
		segment, segmentOK := s.entry(ctx, segmentHead, p)
		now, nowOK := s.entry(ctx, newHead, p)
		if old != segment || oldOK != segmentOK || old != now || oldOK != nowOK {
			if observedAfter != "" && uncertain != nil {
				actual, actualOK := s.entry(ctx, observedAfter, p)
				if actual != now || actualOK != nowOK {
					*uncertain = append(*uncertain, p)
				}
			}
			continue
		}
		if observedAfter != "" {
			actual, actualOK := s.entry(ctx, observedAfter, p)
			if actual == now && actualOK == nowOK {
				continue // a reset/forced checkout discarded this old dirty file
			}
		}
		if e, ok := s.entry(ctx, before, p); ok {
			edits = append(edits, fmt.Sprintf("M %s %s %s\n", e.mode, e.oid, strconv.Quote(p)))
		} else {
			edits = append(edits, "D "+strconv.Quote(p)+"\n")
		}
	}
	if len(edits) == 0 {
		return base, nil
	}
	sort.Strings(edits)
	ref := "refs/views/" + digest(base+strings.Join(edits, ""))
	input := fmt.Sprintf("commit %s\ncommitter AgentWorks <snapshot@example.com> %d +0000\ndata 4\nview\nfrom %s\n%s\ndone\n", ref, time.Now().Unix(), base, strings.Join(edits, ""))
	cmd := s.command(ctx, "fast-import", "--quiet", "--done", "--force", "--date-format=raw")
	cmd.Stdin = strings.NewReader(input)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("carried file baseline: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	out, err := s.command(ctx, "rev-parse", "--verify", ref).Output()
	return strings.TrimSpace(string(out)), err
}
