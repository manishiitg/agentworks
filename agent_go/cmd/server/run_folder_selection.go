package server

import (
	"log"
	"sort"
	"sync"
	"time"
)

// Latest means most recently started, not the last older run to finish.
func runFolderTime(folder RunFolderInfo) (time.Time, string) {
	var latest time.Time
	reason := "no_timestamp"
	if m := folder.Metadata; m != nil {
		if !m.StartedAt.IsZero() {
			return m.StartedAt, "newest_started"
		}
		if !m.CreatedAt.IsZero() {
			return m.CreatedAt, "newest_created"
		}
	}
	return latest, reason
}

func sortRunFoldersByTime(folders []RunFolderInfo) {
	sort.SliceStable(folders, func(i, j int) bool {
		a, _ := runFolderTime(folders[i])
		b, _ := runFolderTime(folders[j])
		if !a.Equal(b) {
			return a.After(b)
		}
		// Stable ordering for legacy records without a clock and equal-time aliases.
		if a.IsZero() {
			ai, bi := extractIterationNumber(folders[i].Name), extractIterationNumber(folders[j].Name)
			if ai != bi {
				return ai > bi
			}
		}
		return folders[i].Name < folders[j].Name
	})
}

func defaultRunFolder(folders []RunFolderInfo) string {
	if len(folders) == 0 {
		return ""
	}
	return folders[0].Name
}

var runDefaultLogState = struct {
	sync.Mutex
	last map[string]string
}{last: map[string]string{}}

func logRunFolderDefault(workspace string, folders []RunFolderInfo) {
	reason := "no_runs"
	var at time.Time
	if len(folders) > 0 {
		at, reason = runFolderTime(folders[0])
	}
	chosen := defaultRunFolder(folders)
	// A reused slot is a new selection when its lifecycle clock changes.
	fingerprint := chosen + "|" + at.Format(time.RFC3339Nano) + "|" + reason
	runDefaultLogState.Lock()
	previous, seen := runDefaultLogState.last[workspace]
	runDefaultLogState.last[workspace] = fingerprint
	runDefaultLogState.Unlock()
	if !seen || previous != fingerprint {
		log.Printf("[RUNS] workflow=%s default=%s at=%s reason=%s", workspace, chosen, at.Format(time.RFC3339Nano), reason)
	}
}
