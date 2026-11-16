package server

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// Metadata only, projected exclusively into the source owner's existing row.
// Open names the admitted source, never the copy's private journal/session key.
type crewAgentHeaderCopy struct {
	State    string     `json:"state"`
	SyncedAt *time.Time `json:"synced_at,omitempty"`
}
type crewAgentHeaderOpen struct {
	ProjectID string `json:"project_id"`
	Address   string `json:"address"`
}
type crewAgentHeaderPublished struct {
	State string               `json:"state"`
	Open  *crewAgentHeaderOpen `json:"open,omitempty"`
}

func crewAgentHeaderStatus(ctx context.Context, t crewTeam, row crewTeamRegisteredAgent) (*crewAgentHeaderCopy, *crewAgentHeaderPublished) {
	if row.Place != "mac" && row.Kind != "terminal" {
		return nil, nil
	}
	ref, bound, err := crewOfflineCopyBound(t, row)
	if err != nil {
		return nil, nil
	}
	if !bound {
		if binding, ok := crewOfflineRowBinding(t, row); ok && ref.PutOnline && ref.Home == t.OnlineHome && ref.Binding == binding {
			return nil, &crewAgentHeaderPublished{State: "publishing"}
		}
		if ref.Session == "" {
			return nil, &crewAgentHeaderPublished{State: "not_published"}
		}
		return nil, nil
	}
	published := &crewAgentHeaderPublished{State: "not_published"}
	if ref.PutOnline {
		published.State = "publishing"
	}
	dir, err := crewOfflineDirectory(t.ProjectID, row.ID)
	if err != nil {
		return nil, nil
	}
	history, err := readCrewOfflineHistory(dir, ref.Binding)
	if err != nil {
		return nil, nil
	}
	ready := history.Revision > 0 && history.Offset == history.SourceSize && ref.Revision == history.Revision
	copy := &crewAgentHeaderCopy{State: "preparing"}
	if ready {
		copy.State = "ready"
		if info, err := os.Stat(filepath.Join(dir, "history.json")); err == nil {
			at := info.ModTime().UTC()
			copy.SyncedAt = &at
		}
		if ref.PutOnline {
			published.State = "published"
			address := crewTeamAgentBridgeAddress(row.OwnerID, row.ID)
			if row.Kind == "terminal" {
				address = ""
				if ctx != nil {
					if named, err := currentCrewTeamTerminal(ctx, t, row); err == nil {
						address = "agent:" + shareOwnerLabel(row.OwnerID) + "/" + named.Name
					}
				}
			}
			if address != "" {
				published.Open = &crewAgentHeaderOpen{ProjectID: t.ProjectID, Address: address}
			}
		}
	}
	return copy, published
}
