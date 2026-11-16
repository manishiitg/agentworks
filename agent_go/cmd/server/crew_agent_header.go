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
	// PutOnline requests a private copy; only the canonical source placement is a publication.
	published := &crewAgentHeaderPublished{State: "not_published"}
	// Legacy placements belong to the Crew owner, never a member's same-named tab.
	if row.OwnerID == t.OwnerID && row.Tab != "" && crewPublishedTarget(t.ProjectID, row.Tab) {
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
	if row.Place != "mac" && row.Kind != "terminal" {
		return nil, published
	}
	ref, bound, err := crewOfflineCopyBound(t, row)
	if err != nil || !bound {
		return nil, published
	}
	dir, err := crewOfflineDirectory(t.ProjectID, row.ID)
	if err != nil {
		return nil, published
	}
	history, err := readCrewOfflineHistory(dir, ref.Binding)
	if err != nil {
		return nil, published
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
	return copy, published
}
