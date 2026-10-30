package filesnapshots

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// A Git checkout that appears during a turn (a clone, or a linked worktree the
// agent added) is not thousands of edits: its checked-out files came from
// commits that existed before the turn. PLAT-746-165: one new worktree showed
// as 3,081 added files. So the after-capture records, for each new checkout,
// a baseline: the commit it was checked out at (startCommit). Commits the
// agent made in it during the turn stay changes. Comparisons, the
// chat's combined changes and Undo then put that baseline in the checkout's
// folder on the "before" side, so only real edits, additions and deletions
// show, committed or not. A checkout with no commit from before the turn (the
// agent ran git init and committed) is the agent's own work: it is not
// recorded, its files are additions and Undo treats them as usual. A new
// checkout whose baseline cannot be read keeps its files as additions and
// Undo leaves that folder alone.
//
// A checkout whose folder is gone after the turn (worktree removed, folder
// renamed) shows no deletions: its saved files stand on both sides.

// baselineRef holds the baseline trees of a reply's new checkouts.
func baselineRef(id string) string { return "refs/turns/" + id + "/baseline" }

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// subtree returns the tree at folder p of a saved capture, "" when absent.
func (s Store) subtree(ctx context.Context, commit, p string) string {
	if commit == "" || p == "" {
		return ""
	}
	out, err := s.command(ctx, "rev-parse", "--verify", "-q", commit+":"+p).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

type baselineEntry struct {
	mode, oid, path string // path is Crew-relative
	dir             string // the checkout holding the blob
}

// repoBaselines fills r.NewRepos, r.GoneRepos and r.BaselineSkipped from the
// checkouts the after-capture listed. Called by Finish under the Crew gate.
// Failures leave a checkout without a baseline (its files show as added and
// Undo leaves them alone); they never fail the reply's capture.
func (s Store) repoBaselines(ctx context.Context, r *Record, after map[string]string) {
	before := map[string]bool{}
	for _, p := range r.BeforeRepos {
		before[p] = true
	}
	for _, p := range r.BeforeRepos {
		if _, still := after[p]; p == "" || still {
			continue
		}
		if _, err := os.Lstat(filepath.Join(s.Root, filepath.FromSlash(p))); errors.Is(err, fs.ErrNotExist) {
			r.GoneRepos = append(r.GoneRepos, p)
		}
	}
	// Older records (no BeforeRepos) cannot tell new checkouts apart.
	if r.BeforeRepos == nil {
		return
	}
	newRepos := map[string]string{}
	var entries []baselineEntry
	until := "--until=@" + strconv.FormatInt(r.StartedAt.Unix()-1, 10)
	for _, p := range sortedKeys(after) {
		// A folder that had files before (for example one turned into a
		// checkout by git init) compares as usual.
		if p == "" || before[p] || s.subtree(ctx, r.Before, p) != "" {
			continue
		}
		dir := after[p]
		base, err := startCommit(ctx, s.Root, dir, r.StartedAt, until)
		if err == nil && base == "" {
			log.Printf("[FILE_SNAPSHOT] phase=new_repo snapshot=%s repo=%q baseline=own_work", r.ID, p)
			continue
		}
		newRepos[p] = ""
		if err != nil {
			log.Printf("[FILE_SNAPSHOT] phase=new_repo snapshot=%s repo=%q baseline=none error=%v", r.ID, p, err)
			continue
		}
		list, err := gitIn(ctx, s.Root, dir, "ls-tree", "-r", "-l", "-z", "--full-tree", base).Output()
		if err != nil {
			log.Printf("[FILE_SNAPSHOT] phase=new_repo snapshot=%s repo=%q baseline=unreadable error=%v", r.ID, p, err)
			continue
		}
		newRepos[p] = base // replaced by its tree below
		if r.BeforeHeads == nil {
			r.BeforeHeads = map[string]string{}
		}
		r.BeforeHeads[p] = base
		for _, row := range bytes.Split(list, []byte{0}) {
			meta, name, ok := strings.Cut(string(row), "\t")
			fields := strings.Fields(meta)
			if !ok || len(fields) != 4 || fields[1] != "blob" {
				continue // submodule links and malformed rows
			}
			full := p + "/" + name
			if excluded(full, false) {
				continue
			}
			if size, err := strconv.ParseInt(fields[3], 10, 64); err != nil || size > MaxFileBytes {
				r.BaselineSkipped = append(r.BaselineSkipped, full)
				continue
			}
			entries = append(entries, baselineEntry{mode: fields[0], oid: fields[2], path: full, dir: dir})
		}
	}
	if len(newRepos) == 0 {
		return
	}
	r.NewRepos = newRepos
	if err := s.importBaselines(ctx, r, entries); err != nil {
		log.Printf("[FILE_SNAPSHOT] phase=new_repo snapshot=%s baseline=failed error=%v", r.ID, err)
		for p := range newRepos {
			newRepos[p] = ""
		}
		return
	}
	for p, base := range newRepos {
		if base == "" {
			continue
		}
		newRepos[p] = s.subtree(ctx, baselineRef(r.ID), p)
		log.Printf("[FILE_SNAPSHOT] phase=new_repo snapshot=%s repo=%q baseline_commit=%s baseline_tree=%s", r.ID, p, base, newRepos[p])
	}
}

// startCommit is the commit a checkout that appeared during the turn was
// checked out at. Git's own record comes first: the oldest entry of the
// checkout's HEAD reflog, written by worktree add (or clone) during the turn,
// so a worktree added at a commit the agent made earlier in the same turn
// compares against that commit. An oldest entry that is itself a commit
// (git init, then commit) is the agent's work, not a checkout. Without such a
// reflog (none, disabled, or older than the turn) the fallback is a best
// guess by commit date: the newest first-parent commit of HEAD committed
// before the turn started; "" means every commit is from the turn (the
// agent's own repository). Read-only.
func startCommit(ctx context.Context, root, dir string, started time.Time, until string) (string, error) {
	if out, err := gitIn(ctx, root, dir, "reflog", "show", "--date=unix", "--format=%H %gd %gs", "HEAD").Output(); err == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		oldest := strings.SplitN(lines[len(lines)-1], " ", 3)
		if len(oldest) >= 2 && strings.HasPrefix(oldest[1], "HEAD@{") {
			at, perr := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(oldest[1], "HEAD@{"), "}"), 10, 64)
			message := ""
			if len(oldest) == 3 {
				message = oldest[2]
			}
			if perr == nil && at >= started.Unix() && len(oldest[0]) >= 40 && !strings.HasPrefix(message, "commit") {
				return oldest[0], nil
			}
		}
	}
	out, err := gitIn(ctx, root, dir, "rev-list", "--first-parent", "--max-count=1", until, "HEAD").Output()
	return strings.TrimSpace(string(out)), err
}

// importBaselines writes one commit (baselineRef) holding every baseline file
// at its Crew-relative path. Blobs the private store already has (the
// after-capture just read the same bytes) are referenced; the rest are copied
// from the checkout's own objects within maxImportBytes. A file left out is
// hidden on both sides (BaselineSkipped) rather than shown as added.
func (s Store) importBaselines(ctx context.Context, r *Record, entries []baselineEntry) error {
	if len(entries) == 0 {
		return nil
	}
	check := s.command(ctx, "cat-file", "--batch-check=%(objectname)")
	var in bytes.Buffer
	for _, e := range entries {
		in.WriteString(e.oid + "\n")
	}
	check.Stdin = &in
	out, err := check.Output()
	if err != nil {
		return fmt.Errorf("check baseline objects: %w", err)
	}
	missing := map[string][]int{} // checkout dir -> entry indexes
	have := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		if oid, state, _ := strings.Cut(line, " "); state == "missing" {
			have[oid] = false
		} else if line != "" {
			have[oid] = true
		}
	}
	for i, e := range entries {
		if !have[e.oid] {
			missing[e.dir] = append(missing[e.dir], i)
		}
	}
	cmd := s.command(ctx, "fast-import", "--quiet", "--done", "--force", "--date-format=raw")
	input, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err = cmd.Start(); err != nil {
		return err
	}
	w := bufio.NewWriterSize(input, 1<<20)
	marks := map[string]int{}
	var total int64
	dirs := make([]string, 0, len(missing))
	for dir := range missing {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		if err = s.copyBlobs(ctx, w, dir, entries, missing[dir], marks, &total); err != nil {
			break
		}
	}
	if err == nil {
		fmt.Fprintf(w, "commit %s\ncommitter AgentWorks <snapshot@example.com> %d +0000\ndata 8\nbaseline\ndeleteall\n", baselineRef(r.ID), time.Now().Unix())
		for _, e := range entries {
			ref := e.oid
			if !have[e.oid] {
				mark, ok := marks[e.oid]
				if !ok {
					r.BaselineSkipped = append(r.BaselineSkipped, e.path)
					continue
				}
				ref = ":" + strconv.Itoa(mark)
			}
			fmt.Fprintf(w, "M %s %s %s\n", e.mode, ref, strconv.Quote(e.path))
		}
		if _, err = w.WriteString("\ndone\n"); err == nil {
			err = w.Flush()
		}
	}
	input.Close()
	if err != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if err != nil {
		return err
	}
	if waitErr != nil {
		return fmt.Errorf("baseline import failed: %w (%s)", waitErr, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// copyBlobs streams the checkout's own blobs (exact stored bytes, no filters)
// into fast-import. Blobs over the budget are read past and left unmarked.
func (s Store) copyBlobs(ctx context.Context, w *bufio.Writer, dir string, entries []baselineEntry, idx []int, marks map[string]int, total *int64) error {
	var in bytes.Buffer
	asked := map[string]bool{}
	for _, i := range idx {
		if oid := entries[i].oid; !asked[oid] {
			asked[oid] = true
			in.WriteString(oid + "\n")
		}
	}
	cmd := gitIn(ctx, s.Root, dir, "cat-file", "--batch")
	cmd.Stdin = &in
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return err
	}
	// Kill first: on an early return Git may still be writing to the pipe.
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	rd := bufio.NewReaderSize(stdout, 1<<20)
	for range asked {
		header, err := rd.ReadString('\n')
		if err != nil {
			return fmt.Errorf("read baseline objects: %w", err)
		}
		fields := strings.Fields(header)
		if len(fields) != 3 {
			continue // "<oid> missing"
		}
		size, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil {
			return errors.New("read baseline objects: bad size")
		}
		if fields[1] != "blob" || *total+size > maxImportBytes {
			if _, err = io.CopyN(io.Discard, rd, size+1); err != nil {
				return err
			}
			continue
		}
		*total += size
		mark := len(marks) + 1
		marks[fields[0]] = mark
		fmt.Fprintf(w, "blob\nmark :%d\ndata %d\n", mark, size)
		if _, err = io.CopyN(w, rd, size); err != nil {
			return err
		}
		if _, err = rd.Discard(1); err != nil {
			return err
		}
		if err = w.WriteByte('\n'); err != nil {
			return err
		}
	}
	return nil
}

type overlay struct{ prefix, tree string }

// effective returns the before and after captures a comparison uses: a
// checkout that appeared has its baseline on the before side, and one whose
// folder is gone keeps its saved files on the after side. Only when the other
// side has the folder and this side has none, so a later reply that removed
// a new checkout again, or a folder that existed all along, compares as saved.
func (s Store) effective(ctx context.Context, r Record) (string, string, error) {
	var before, after []overlay
	for _, p := range sortedKeys(r.NewRepos) {
		if tree := r.NewRepos[p]; tree != "" && s.subtree(ctx, r.Before, p) == "" && s.subtree(ctx, r.After, p) != "" {
			before = append(before, overlay{p, tree})
		}
	}
	for _, p := range r.GoneRepos {
		if tree := s.subtree(ctx, r.Before, p); tree != "" && s.subtree(ctx, r.After, p) == "" {
			after = append(after, overlay{p, tree})
		}
	}
	b, err := s.view(ctx, r.Before, before)
	if err != nil {
		return "", "", err
	}
	a, err := s.view(ctx, r.After, after)
	return b, a, err
}

// view writes (once) a commit that is base with each overlay's tree at its
// folder. Views are kept under refs/views/ so their trees stay readable while
// in use; retention drops them all before cleanup and they are rebuilt on demand.
func (s Store) view(ctx context.Context, base string, overlays []overlay) (string, error) {
	if len(overlays) == 0 {
		return base, nil
	}
	sort.Slice(overlays, func(i, j int) bool {
		if len(overlays[i].prefix) != len(overlays[j].prefix) {
			return len(overlays[i].prefix) < len(overlays[j].prefix)
		}
		return overlays[i].prefix < overlays[j].prefix
	})
	h := sha256.New()
	io.WriteString(h, base+"\x00")
	for _, o := range overlays {
		io.WriteString(h, o.prefix+"\x00"+o.tree+"\x00")
	}
	ref := "refs/views/" + hex.EncodeToString(h.Sum(nil)[:16])
	if out, err := s.command(ctx, "rev-parse", "--verify", "-q", ref).Output(); err == nil {
		return strings.TrimSpace(string(out)), nil
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "commit %s\ncommitter AgentWorks <snapshot@example.com> %d +0000\ndata 4\nview\nfrom %s\n", ref, time.Now().Unix(), base)
	for _, o := range overlays {
		fmt.Fprintf(&b, "D %s\nM 040000 %s %s\n", strconv.Quote(o.prefix), o.tree, strconv.Quote(o.prefix))
	}
	b.WriteString("\ndone\n")
	cmd := s.command(ctx, "fast-import", "--quiet", "--done", "--force", "--date-format=raw")
	cmd.Stdin = &b
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("comparison view failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	out, err := s.command(ctx, "rev-parse", "--verify", ref).Output()
	return strings.TrimSpace(string(out)), err
}

// dropViews removes every cached comparison view before retention cleanup.
func (s Store) dropViews(ctx context.Context) {
	out, err := s.command(ctx, "for-each-ref", "--format=delete %(refname)", "refs/views/").Output()
	if err != nil || len(out) == 0 {
		return
	}
	cmd := s.command(ctx, "update-ref", "--stdin")
	cmd.Stdin = bytes.NewReader(out)
	_ = cmd.Run()
}

// windowRepos gathers the checkout baselines of every saved reply between two
// boundaries, across the Crew's chats (the comparison covers the whole Crew
// folder between them). The earliest baseline of a folder wins.
func (s Store) windowRepos(from, to time.Time) (Record, error) {
	out := Record{NewRepos: map[string]string{}}
	records, err := s.List()
	if err != nil {
		return out, err
	}
	gone := map[string]bool{}
	for i := len(records) - 1; i >= 0; i-- { // oldest first
		r := records[i]
		if r.StartedAt.Before(from) || r.StartedAt.After(to) || (r.Status != "complete" && r.Status != "partial") {
			continue
		}
		for p, tree := range r.NewRepos {
			if _, seen := out.NewRepos[p]; !seen {
				out.NewRepos[p] = tree
			}
		}
		for _, p := range r.GoneRepos {
			if !gone[p] {
				gone[p] = true
				out.GoneRepos = append(out.GoneRepos, p)
			}
		}
		out.BaselineSkipped = append(out.BaselineSkipped, r.BaselineSkipped...)
	}
	return out, nil
}

// newRepoWithoutBaseline returns the new checkout folder holding file when
// that checkout's baseline could not be read; Undo leaves such files alone
// rather than deleting checked-out files the reply did not write.
func newRepoWithoutBaseline(r Record, file string) string {
	for p, tree := range r.NewRepos {
		if tree == "" && strings.HasPrefix(file, p+"/") {
			return p
		}
	}
	return ""
}

// repoHeads reads each checkout's HEAD commit (read-only); a checkout without
// a commit yet is left out.
func (s Store) repoHeads(ctx context.Context, repos map[string]string) map[string]string {
	heads := map[string]string{}
	for p, dir := range repos {
		if out, err := gitIn(ctx, s.Root, dir, "rev-parse", "--verify", "-q", "HEAD").Output(); err == nil {
			if head := strings.TrimSpace(string(out)); len(head) >= 40 {
				heads[p] = head
			}
		}
	}
	return heads
}
