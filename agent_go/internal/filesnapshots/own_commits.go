package filesnapshots

// A reply's file card lists only the files the reply itself changed (#2922
// task 1). When a checkout's HEAD moved during the reply, diffing start..end
// also counted every file a pull or merge brought in, and a rebase (start no
// longer an ancestor) left no list at all. Here a commit counts as the reply's
// own when it is new on the branch and this checkout's HEAD reflog shows it
// was created here during the reply (commit, amend, cherry-pick, revert, am,
// rebase pick). Fast-forwards, merges, resets and checkouts move HEAD without
// making work, so the commits they bring in are left out. A rebase that only
// moved commits made before the reply is recognised by patch ID and left out.
// Merge commits are skipped, so conflict resolutions are not listed.
import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
)

const maxOwnCommits = 500

var errTooManyCommits = errors.New("too many new commits to sort out for this reply")

// ownCommitPaths returns the checkout-relative files changed by the commits
// this reply made between start and end.
func (s Store) ownCommitPaths(ctx context.Context, dir, start, end string, started time.Time) ([]string, error) {
	added, err := s.revList(ctx, dir, end, start)
	if err != nil || len(added) == 0 {
		return nil, err
	}
	made, err := s.madeHere(ctx, dir, started)
	if err != nil {
		return nil, err
	}
	var own []string
	for _, c := range added {
		if made == nil { // No reflog: fall back to the commit's own time.
			if s.committedSince(ctx, dir, c, started) {
				own = append(own, c)
			}
		} else if made[c] {
			own = append(own, c)
		}
	}
	if len(own) == 0 {
		return nil, nil
	}
	// A rebase rewrites commits made before the reply too; their patch is
	// unchanged, so match them against the commits that left the branch.
	if gone, err := s.revList(ctx, dir, start, end); err != nil {
		return nil, err
	} else if len(gone) > 0 {
		ids, err := s.patchIDs(ctx, dir, gone)
		if err != nil {
			return nil, err
		}
		before := map[string]bool{}
		for _, id := range ids {
			before[id] = true
		}
		after, err := s.patchIDs(ctx, dir, own)
		if err != nil {
			return nil, err
		}
		kept := own[:0]
		for _, c := range own {
			if id, ok := after[c]; !ok || !before[id] {
				kept = append(kept, c)
			}
		}
		own = kept
	}
	seen := map[string]bool{}
	var paths []string
	for _, c := range own {
		raw, err := gitIn(ctx, s.Root, dir, "diff-tree", "--no-commit-id", "-r", "--root", "--name-only", "--no-renames", "-z", c).Output()
		if err != nil {
			return nil, err
		}
		for _, p := range bytes.Split(raw, []byte{0}) {
			if len(p) > 0 && !seen[string(p)] {
				seen[string(p)] = true
				paths = append(paths, string(p))
			}
		}
	}
	return paths, nil
}

// revList lists non-merge commits reachable from include and not from exclude.
func (s Store) revList(ctx context.Context, dir, include, exclude string) ([]string, error) {
	raw, err := gitIn(ctx, s.Root, dir, "rev-list", "--no-merges", "--max-count="+strconv.Itoa(maxOwnCommits+1), include, "--not", exclude, "--").Output()
	if err != nil {
		return nil, err
	}
	commits := strings.Fields(string(raw))
	if len(commits) > maxOwnCommits {
		return nil, errTooManyCommits
	}
	return commits, nil
}

// madeHere returns the commits this checkout's HEAD reflog records as created
// here since started, or nil when the checkout keeps no reflog.
func (s Store) madeHere(ctx context.Context, dir string, started time.Time) (map[string]bool, error) {
	raw, err := gitIn(ctx, s.Root, dir, "reflog", "show", "--date=unix", "--format=%H%x1f%gd%x1f%gs", "HEAD", "--").Output()
	if err != nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	made := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		parts := strings.SplitN(line, "\x1f", 3)
		if len(parts) != 3 {
			continue
		}
		at, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(parts[1], "HEAD@{"), "}"), 10, 64)
		if err != nil {
			continue
		}
		if at < started.Unix() {
			break // newest first
		}
		if createdWork(parts[2]) {
			made[parts[0]] = true
		}
	}
	return made, nil
}

// createdWork reports whether a reflog subject ("action: message") made a new
// commit, as opposed to moving HEAD to commits that already existed.
func createdWork(subject string) bool {
	action, _, _ := strings.Cut(subject, ": ")
	switch {
	case strings.HasPrefix(action, "commit"), strings.HasPrefix(action, "cherry-pick"), strings.HasPrefix(action, "revert"), action == "am":
		return true
	case strings.Contains(action, "rebase"):
		return !strings.Contains(action, "(start)") && !strings.Contains(action, "(finish)") && !strings.Contains(action, "(abort)")
	}
	return false
}

func (s Store) committedSince(ctx context.Context, dir, commit string, started time.Time) bool {
	raw, err := gitIn(ctx, s.Root, dir, "show", "-s", "--format=%ct", commit, "--").Output()
	if err != nil {
		return false
	}
	at, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	return err == nil && at >= started.Unix()
}

// patchIDs maps each commit to its stable patch ID.
func (s Store) patchIDs(ctx context.Context, dir string, commits []string) (map[string]string, error) {
	patches, err := gitIn(ctx, s.Root, dir, append([]string{"show", "--no-color", "--no-ext-diff", "--no-textconv", "--format=commit %H", "-p"}, append(commits, "--")...)...).Output()
	if err != nil {
		return nil, err
	}
	cmd := gitIn(ctx, s.Root, dir, "patch-id", "--stable")
	cmd.Stdin = bytes.NewReader(patches)
	raw, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if id, commit, ok := strings.Cut(line, " "); ok {
			out[commit] = id
		}
	}
	return out, nil
}
