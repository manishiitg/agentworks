package filesnapshots

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// SavedChangeSummary contains immutable file metadata, never file bytes or chat
// content. Current path grants are reapplied on every read, including cache hits.
type SavedChangeSummary struct {
	Changes    []Change `json:"changes"`
	Incomplete bool     `json:"incomplete"`
}

type cachedChangeSummary struct {
	Revision        string   `json:"revision"`
	Changes         []Change `json:"changes"`
	IncompletePaths []string `json:"incomplete_paths,omitempty"`
}

// SavedSummary avoids replaying every retained Git span whenever the project
// menu refreshes. The saved record's bytes invalidate the derived cache after
// a repair or metadata update. Only completed comparisons are cached.
func (s Store) SavedSummary(ctx context.Context, id string, allow func(string) bool) (SavedChangeSummary, error) {
	if err := ctx.Err(); err != nil {
		return SavedChangeSummary{}, err
	}
	record, err := s.read(id)
	if err != nil {
		return SavedChangeSummary{}, err
	}
	raw, err := os.ReadFile(filepath.Join(s.dir(), "turns", id+".json"))
	if err != nil {
		return SavedChangeSummary{}, err
	}
	revision := digest("summary-path-grants-v2\x00" + string(raw))
	dir := filepath.Join(s.dir(), "summaries")
	name := filepath.Join(dir, id+".json")
	var saved cachedChangeSummary
	data, readErr := os.ReadFile(name)
	if readErr != nil || json.Unmarshal(data, &saved) != nil || saved.Revision != revision {
		comparison, err := s.CompareSaved(ctx, []string{id}, "", "", func(string) bool { return true })
		if err != nil {
			return SavedChangeSummary{}, err
		}
		if err := ctx.Err(); err != nil {
			return SavedChangeSummary{}, err
		}
		incompletePaths, err := s.summaryIncompletePaths(ctx, record)
		if err != nil {
			return SavedChangeSummary{}, err
		}
		saved = cachedChangeSummary{Revision: revision, Changes: comparison.Changes, IncompletePaths: incompletePaths}
		data, err = json.Marshal(saved)
		if err != nil {
			return SavedChangeSummary{}, err
		}
		if err = os.MkdirAll(dir, 0700); err != nil {
			return SavedChangeSummary{}, err
		}
		tmp, err := os.CreateTemp(dir, id+"-*.tmp")
		if err != nil {
			return SavedChangeSummary{}, err
		}
		defer os.Remove(tmp.Name())
		_, err = tmp.Write(data)
		closeErr := tmp.Close()
		if err != nil {
			return SavedChangeSummary{}, err
		}
		if closeErr != nil {
			return SavedChangeSummary{}, closeErr
		}
		if err = os.Rename(tmp.Name(), name); err != nil {
			return SavedChangeSummary{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return SavedChangeSummary{}, err
	}
	out := SavedChangeSummary{Changes: []Change{}}
	for _, change := range saved.Changes {
		if allow(change.Path) {
			out.Changes = append(out.Changes, change)
		}
	}
	for _, path := range saved.IncompletePaths {
		if allow(path) {
			out.Incomplete = true
			break
		}
	}
	return out, nil
}

// Keep the paths witnessing an incomplete capture in the private cache so a
// later reader with narrower grants sees only warnings relevant to their files.
// This mirrors authorizedCheckout's saved-tree rule without replaying diffs.
func (s Store) summaryIncompletePaths(ctx context.Context, record Record) ([]string, error) {
	paths := map[string]bool{}
	for _, list := range [][]string{record.Skipped, record.Omitted, record.BaselineSkipped} {
		for _, p := range list {
			paths[p] = true
		}
	}
	if record.Review != nil && len(record.Review.IncompleteRoots) > 0 {
		roots := map[string]bool{}
		for _, root := range record.Review.IncompleteRoots {
			roots[root] = true
		}
		for _, tree := range []string{record.Before, record.After} {
			data, err := s.command(ctx, "ls-tree", "-r", "--name-only", "-z", tree).Output()
			if err != nil {
				return nil, err
			}
			for _, name := range bytes.Split(data, []byte{0}) {
				p := string(name)
				if p == "" {
					continue
				}
				if root, ok := savedRoot(p, record.Review.Roots); ok && roots[root] {
					paths[p] = true
				}
			}
		}
	}
	out := make([]string, 0, len(paths))
	for path := range paths {
		out = append(out, path)
	}
	sort.Strings(out)
	return out, nil
}
