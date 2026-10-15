package server

import (
	"log"
	"sort"
	"sync"
	"time"
)

// Run folders are storage slots, not a clock: iteration zero may be reused.
func runFolderTime(folder RunFolderInfo) (time.Time, string) {
	var latest time.Time
	reason := "no_timestamp"
	if m := folder.Metadata; m != nil {
		for _, candidate := range []struct {
			at     time.Time
			reason string
		}{{m.CreatedAt, "newest_created"}, {m.StartedAt, "newest_started"}} {
			if candidate.at.After(latest) {
				latest, reason = candidate.at, candidate.reason
			}
		}
		if m.CompletedAt != nil && m.CompletedAt.After(latest) {
			latest, reason = *m.CompletedAt, "newest_completed"
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
