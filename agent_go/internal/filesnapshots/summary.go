package filesnapshots

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
)

// SavedChangeSummary contains immutable file metadata, never file bytes or chat
// content. Current path grants are reapplied on every read, including cache hits.
type SavedChangeSummary struct {
	Changes    []Change `json:"changes"`
	Incomplete bool     `json:"incomplete"`
}

type cachedChangeSummary struct {
	Revision string `json:"revision"`
	SavedChangeSummary
}

// SavedSummary avoids replaying every retained Git span whenever the project
// menu refreshes. The saved record's bytes invalidate the derived cache after
// a repair or metadata update. Only completed comparisons are cached.
func (s Store) SavedSummary(ctx context.Context, id string, allow func(string) bool) (SavedChangeSummary, error) {
	if err := ctx.Err(); err != nil {
		return SavedChangeSummary{}, err
	}
	if _, err := s.read(id); err != nil {
		return SavedChangeSummary{}, err
	}
	raw, err := os.ReadFile(filepath.Join(s.dir(), "turns", id+".json"))
	if err != nil {
		return SavedChangeSummary{}, err
	}
	revision := digest(string(raw))
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
		saved = cachedChangeSummary{Revision: revision, SavedChangeSummary: SavedChangeSummary{Changes: comparison.Changes, Incomplete: comparison.Note != "" || comparison.Record.Status == "partial" || comparison.UncapturedChanges > 0}}
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
	out := SavedChangeSummary{Changes: []Change{}, Incomplete: saved.Incomplete}
	for _, change := range saved.Changes {
		if allow(change.Path) {
			out.Changes = append(out.Changes, change)
		}
	}
	return out, nil
}
