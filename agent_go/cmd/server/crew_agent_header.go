package server

import (
	"os"
	"path/filepath"
	"time"
)

// The online copy's state, projected only into the source owner's own row.
type crewAgentHeaderCopy struct {
	State    string     `json:"state"`
	SyncedAt *time.Time `json:"synced_at,omitempty"`
}

func crewAgentHeaderStatus(t crewTeam, row crewTeamRegisteredAgent) *crewAgentHeaderCopy {
	if row.Place != "mac" && row.Kind != "terminal" {
		return nil
	}
	ref, bound, err := crewOfflineCopyBound(t, row)
	if err != nil || !bound {
		return nil
	}
	dir, err := crewOfflineDirectory(t.ProjectID, row.ID)
	if err != nil {
		return nil
	}
	history, err := readCrewOfflineHistory(dir, ref.Binding)
	if err != nil {
		return nil
	}
	ready := history.Revision > 0 && history.Offset == history.SourceSize && ref.Revision == history.Revision
	copy := &crewAgentHeaderCopy{State: "preparing"}
	if ready {
		copy.State = "ready"
		if info, err := os.Stat(filepath.Join(dir, "history.json")); err == nil {
			at := info.ModTime().UTC()
			copy.SyncedAt = &at
		}
	}
	return copy
}
