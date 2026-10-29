package filesnapshots

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
)

// UndoItem is one file of a reply. Action is what undo does (or would do):
// restore (put the saved "before" bytes back), delete (the reply added it) or
// recreate (the reply deleted it).
type UndoItem struct {
	Path   string `json:"path"`
	Status string `json:"status"`
	Action string `json:"action"`
	Reason string `json:"reason,omitempty"`
}

// UndoPlan lists what undo restores and what it leaves alone. A skipped file
// was changed again after the reply (changed_later), is already back to its
// saved state (already_restored), or cannot be written (not_allowed,
// not_a_regular_file, too_large, write_failed).
type UndoPlan struct {
	SnapshotID        string     `json:"snapshot_id"`
	Restore           []UndoItem `json:"restore"`
	Skipped           []UndoItem `json:"skipped"`
	UncapturedChanges int        `json:"uncaptured_changes"`
	// Note says plainly what undo leaves alone as a whole, such as a new Git
	// folder the reply created from scratch.
	Note string `json:"note,omitempty"`
}

type treeEntry struct{ mode, oid string }

func (s Store) entry(ctx context.Context, tree, file string) (treeEntry, bool) {
	out, err := s.command(ctx, "--literal-pathspecs", "ls-tree", "-z", tree, "--", file).Output()
	if err != nil {
		return treeEntry{}, false
	}
	meta, name, ok := strings.Cut(strings.TrimRight(string(out), "\x00"), "\t")
	fields := strings.Fields(meta)
	if !ok || name != file || len(fields) != 3 || fields[1] != "blob" {
		return treeEntry{}, false
	}
	return treeEntry{fields[0], fields[2]}, true
}

func (s Store) blob(ctx context.Context, e treeEntry) ([]byte, error) {
	size, err := s.command(ctx, "cat-file", "-s", e.oid).Output()
	if err != nil {
		return nil, err
	}
	if n, err := strconv.ParseInt(strings.TrimSpace(string(size)), 10, 64); err != nil || n > MaxFileBytes {
		return nil, errors.New("too large")
	}
	return s.command(ctx, "cat-file", "blob", e.oid).Output()
}

// current returns the file's bytes; exists is false when it is missing.
func currentBytes(root *os.Root, file string) (data []byte, exists, regular bool, err error) {
	info, err := root.Lstat(file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, true, nil
	}
	if err != nil {
		return nil, false, false, err
	}
	if !info.Mode().IsRegular() || info.Size() > MaxFileBytes {
		return nil, true, false, nil
	}
	f, err := root.Open(file)
	if err != nil {
		return nil, true, false, err
	}
	defer f.Close()
	data, err = io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	return data, true, len(data) <= MaxFileBytes, err
}

// evaluateUndo also returns r with the comparison's before and after (a new
// checkout's baseline in place), which the writes must use.
func (s Store) evaluateUndo(ctx context.Context, r Record, allowRead, allowWrite func(string) bool) (UndoPlan, Record, error) {
	plan := UndoPlan{SnapshotID: r.ID, Restore: []UndoItem{}, Skipped: []UndoItem{}}
	comparison, err := s.compare(ctx, r, "", allowRead)
	if err != nil {
		return plan, r, err
	}
	r.Before, r.After = comparison.Record.Before, comparison.Record.After
	plan.UncapturedChanges = comparison.UncapturedChanges
	leftNew := map[string]int{}
	root, err := os.OpenRoot(s.Root)
	if err != nil {
		return plan, r, err
	}
	defer root.Close()
	for _, change := range comparison.Changes {
		item := UndoItem{Path: change.Path, Status: change.Status}
		if p := newRepoWithoutBaseline(r, change.Path); p != "" {
			leftNew[p]++
			continue
		}
		switch change.Status {
		case "A":
			item.Action = "delete"
		case "M":
			item.Action = "restore"
		case "D":
			item.Action = "recreate"
		default:
			item.Reason = "not_a_regular_file"
			plan.Skipped = append(plan.Skipped, item)
			continue
		}
		skip := func(reason string) {
			item.Reason = reason
			plan.Skipped = append(plan.Skipped, item)
		}
		if !allowWrite(change.Path) {
			skip("not_allowed")
			continue
		}
		var before, after []byte
		if item.Action != "delete" {
			e, ok := s.entry(ctx, r.Before, change.Path)
			if !ok || e.mode == "120000" {
				skip("not_a_regular_file")
				continue
			}
			if before, err = s.blob(ctx, e); err != nil {
				skip("too_large")
				continue
			}
		}
		if item.Action != "recreate" {
			e, ok := s.entry(ctx, r.After, change.Path)
			if !ok || e.mode == "120000" {
				skip("not_a_regular_file")
				continue
			}
			if after, err = s.blob(ctx, e); err != nil {
				skip("too_large")
				continue
			}
		}
		now, exists, regular, err := currentBytes(root, change.Path)
		if err != nil || !regular {
			skip("not_a_regular_file")
			continue
		}
		switch item.Action {
		case "delete":
			if !exists {
				skip("already_restored")
			} else if !bytes.Equal(now, after) {
				skip("changed_later")
			} else {
				plan.Restore = append(plan.Restore, item)
			}
		case "restore":
			if exists && bytes.Equal(now, before) {
				skip("already_restored")
			} else if !exists || !bytes.Equal(now, after) {
				skip("changed_later")
			} else {
				plan.Restore = append(plan.Restore, item)
			}
		case "recreate":
			if !exists {
				plan.Restore = append(plan.Restore, item)
			} else if bytes.Equal(now, before) {
				skip("already_restored")
			} else {
				skip("changed_later")
			}
		}
	}
	sort.Slice(plan.Restore, func(i, j int) bool { return plan.Restore[i].Path < plan.Restore[j].Path })
	sort.Slice(plan.Skipped, func(i, j int) bool { return plan.Skipped[i].Path < plan.Skipped[j].Path })
	folders := make([]string, 0, len(leftNew))
	for p := range leftNew {
		folders = append(folders, p)
	}
	sort.Strings(folders)
	for _, p := range folders {
		plan.Note += fmt.Sprintf("Undo leaves %s alone: it is a new Git folder from this reply whose starting files could not be read (%d files). Delete the folder if you don't want it. ", p, leftNew[p])
	}
	plan.Note = strings.TrimSpace(plan.Note)
	return plan, r, nil
}

// PlanUndo shows what undoing the reply would do, without writing anything.
// allowRead is the snapshot reader's guard; allowWrite is the caller's write guard.
func (s Store) PlanUndo(ctx context.Context, id string, allowRead, allowWrite func(string) bool) (UndoPlan, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return UndoPlan{}, err
	}
	defer unlock()
	r, err := s.read(id)
	if err != nil {
		return UndoPlan{}, err
	}
	plan, _, err := s.evaluateUndo(ctx, r, allowRead, allowWrite)
	return plan, err
}

// ApplyUndo re-checks every file at write time and restores only those still
// exactly as the reply left them. Files changed since are reported, never overwritten.
func (s Store) ApplyUndo(ctx context.Context, id string, allowRead, allowWrite func(string) bool) (UndoPlan, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return UndoPlan{}, err
	}
	defer unlock()
	r, err := s.read(id)
	if err != nil {
		return UndoPlan{}, err
	}
	plan, r, err := s.evaluateUndo(ctx, r, allowRead, allowWrite)
	if err != nil {
		return plan, err
	}
	root, err := os.OpenRoot(s.Root)
	if err != nil {
		return plan, err
	}
	defer root.Close()
	done := []UndoItem{}
	for _, item := range plan.Restore {
		if err := s.applyUndoItem(ctx, root, r, item); err != nil {
			item.Reason = "write_failed"
			plan.Skipped = append(plan.Skipped, item)
			continue
		}
		done = append(done, item)
	}
	plan.Restore = done
	return plan, nil
}

func (s Store) applyUndoItem(ctx context.Context, root *os.Root, r Record, item UndoItem) error {
	if item.Action == "delete" {
		if err := root.Remove(item.Path); err != nil {
			return err
		}
		for dir := path.Dir(item.Path); dir != "." && dir != "/"; dir = path.Dir(dir) {
			if root.Remove(dir) != nil {
				break
			}
		}
		return nil
	}
	e, ok := s.entry(ctx, r.Before, item.Path)
	if !ok {
		return errors.New("saved file missing")
	}
	data, err := s.blob(ctx, e)
	if err != nil {
		return err
	}
	if dir := path.Dir(item.Path); dir != "." {
		if err = root.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	perm := os.FileMode(0644)
	if e.mode == "100755" {
		perm = 0755
	}
	var suffix [6]byte
	_, _ = rand.Read(suffix[:])
	tmp := item.Path + ".undo-" + hex.EncodeToString(suffix[:])
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = root.Remove(tmp)
		return werr
	}
	if err = root.Rename(tmp, item.Path); err != nil {
		_ = root.Remove(tmp)
		return err
	}
	return nil
}
