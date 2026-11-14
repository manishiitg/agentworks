package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/workspace/publication"
	"io"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
	"github.com/manishiitg/coding-agent-loop/workspace/sqliteopen"
	"github.com/manishiitg/coding-agent-loop/workspace/teamonline"
)

type teamOnlineDashboardRef struct {
	ItemID, Revision, SubmissionID, Kind string
	Package                              *os.Root `json:"-"` // Only the admitted, hash-validated item importer supplies this capability.
	Seed                                 bool     `json:"-"` // The common stable mapping decides; never a caller's field.
}
type teamOnlineDatasetTable = teamonline.Table
type teamOnlineAutomationRef = teamonline.Automation
type teamOnlineDashboardResult struct {
	DestinationItemID string
	Revision          string
	Updated           bool
	Tables            []teamOnlineDatasetTable
	Automations       []teamOnlineAutomationRef
	OfferAutomations  bool
}

// Called only by receive-online, after current Team/source admission. This
// component uses ordinary dashboard Builder checks, not a virtual-user bypass.
func (api *StreamingAPI) receiveTeamOnlineDashboard(ctx context.Context, source, destination triggerTarget, ref teamOnlineDashboardRef, preview bool) (teamOnlineDashboardResult, error) {
	result := teamOnlineDashboardResult{}
	claims := GetUserFromContext(ctx)
	if claims == nil || source.CrewOwner != claims.UserID || source.CrewID == "" || ref.Package == nil {
		return result, fmt.Errorf("choose one of your published items to put online")
	}
	target, err := api.dashboardTarget(ctx, claims, source.Path)
	if err != nil || !dashboardAuthoringAllowed(claims, &target) {
		return result, fmt.Errorf("this source item is unavailable")
	}
	file, err := ref.Package.Open(teamonline.Descriptor)
	if err != nil {
		return result, err
	}
	item, err := teamonline.DecodeItem(file)
	file.Close()
	if err != nil {
		return result, err
	}
	if item.ID != ref.ItemID || item.Revision != ref.Revision || (ref.Kind != "dashboard" && ref.Kind != "artifact") || (ref.Kind == "artifact") != item.Snapshot.Artifact {
		return result, fmt.Errorf("the source item changed; preview it again")
	}
	sum := sha256.Sum256([]byte(source.CrewID + "\x00" + ref.Kind + "\x00" + ref.ItemID))
	result.DestinationItemID = "online-" + hex.EncodeToString(sum[:12])
	result.Revision = item.Revision
	result.Automations = item.Dependencies.Automations
	result.OfferAutomations = ref.Seed && len(result.Automations) > 0
	// Updates never even inspect the attached data stream. Preview of a new
	// item reads its immutable source snapshot header, never a live Team DB.
	if ref.Seed {
		if len(item.DatasetParts) > 0 {
			schema, err := teamonline.DescribeParts(ref.Package, item.DatasetParts)
			if err != nil {
				return result, err
			}
			result.Tables = schema.Tables
		} else if len(item.Dependencies.Dataset.Tables) > 0 {
			return result, fmt.Errorf("this item's used tables were not included in its package")
		}
	}
	if preview {
		return result, nil
	}
	target, err = api.dashboardTarget(ctx, claims, destination.Path)
	if err != nil || !dashboardAuthoringAllowed(claims, &target) {
		return result, fmt.Errorf("this Team Crew is unavailable")
	}
	current, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: destination.Path, Action: "list", IncludeDraft: true})
	if err != nil {
		return result, err
	}
	var existing *dashboards.Dashboard
	for i := range current.Dashboards {
		if current.Dashboards[i].ID == result.DestinationItemID {
			existing = &current.Dashboards[i]
			break
		}
	}
	if ref.Seed && existing != nil {
		return result, fmt.Errorf("this Team item ID already exists; nothing was replaced")
	}
	if ref.Seed && len(item.DatasetParts) > 0 {
		roots, found, err := crewroot.Lookup(fsutil.WorkspaceDocsRoot(), destination.Path)
		if err != nil || !found || roots.ID != destination.CrewID {
			return result, fmt.Errorf("the Team dataset is unavailable")
		}
		full, err := crewMoveRecordPath(roots, "db/db.sqlite")
		if err != nil {
			return result, err
		}
		db, err := sql.Open("sqlite", sqliteopen.DSN(full)+"&mode=rw&_pragma=foreign_keys(1)")
		if err != nil {
			return result, err
		}
		db.SetMaxOpenConns(1)
		err = teamonline.ImportParts(ctx, db, ref.Package, item.DatasetParts, true)
		db.Close()
		if err != nil {
			return result, err
		}
	}

	// Assets are ordinary Crew files in this stable item's namespace. Replacing
	// one item cannot overwrite another item's files or any live data table.
	for _, asset := range item.Dependencies.Assets {
		if !strings.HasPrefix(asset, "db/assets/") {
			return result, fmt.Errorf("declare item assets under db/assets")
		}
		file, err := ref.Package.Open(asset)
		if err != nil {
			return result, err
		}
		data, err := io.ReadAll(io.LimitReader(file, publication.MaxFileBytes+1))
		file.Close()
		if err != nil {
			return result, err
		}
		if int64(len(data)) > publication.MaxFileBytes {
			return result, fmt.Errorf("an item asset exceeds the normal file limit")
		}
		name := path.Join("db/assets", result.DestinationItemID, strings.TrimPrefix(asset, "db/assets/"))
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, "/team-online/asset", nil)
		if err != nil {
			return result, err
		}
		buffer := &externalBufferedResponse{}
		api.externalWriteCrewFile(buffer, request, claims, map[string]any{"crew_id": destination.CrewID, "path": name, "content_base64": base64.StdEncoding.EncodeToString(data)})
		if buffer.status != http.StatusOK {
			return result, fmt.Errorf("this Team item asset could not be saved with current access")
		}
		for file, content := range item.Snapshot.Files {
			content = strings.ReplaceAll(content, source.Path+"/"+asset, destination.Path+"/"+name)
			content = strings.ReplaceAll(content, asset, name)
			item.Snapshot.Files[file] = content
		}
	}
	request := dashboards.Request{Root: destination.Path, ID: result.DestinationItemID, Title: item.Snapshot.Title, Artifact: &item.Snapshot.Artifact, Motion: &item.Snapshot.Motion, Files: item.Snapshot.Files, IncludeDraft: true, Action: "create"}
	if existing != nil {
		request.Action = "update"
		request.ExpectedRevision = existing.Revision
		old, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: destination.Path, ID: existing.ID, Action: "get", IncludeDraft: true})
		if err != nil {
			return result, err
		}
		if old.Snapshot != nil {
			for p := range old.Snapshot.Files {
				if _, keep := request.Files[p]; !keep {
					request.Remove = append(request.Remove, p)
				}
			}
		}
	}
	updated, err := dashboardWorkspaceRequest(ctx, request)
	if err != nil {
		return result, err
	}
	if updated.Dashboard == nil {
		return result, fmt.Errorf("the online item could not be saved")
	}
	published, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: destination.Path, ID: result.DestinationItemID, Action: "publish", ExpectedRevision: updated.Dashboard.Revision, IncludeDraft: true})
	if err != nil {
		return result, err
	}
	if published.Dashboard == nil || strings.TrimSpace(published.Dashboard.Published) == "" {
		return result, fmt.Errorf("the online item could not be published")
	}
	result.Updated = existing != nil
	return result, nil
}
