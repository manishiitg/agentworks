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
	"log"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
	"github.com/manishiitg/coding-agent-loop/workspace/sqliteopen"
	"github.com/manishiitg/coding-agent-loop/workspace/teamonline"
)

const teamOnlinePreparationNeeded = "Ask your agent to prepare this dashboard for the Team."

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

// The common mapping and this component use one namespace calculation. A new
// revision changes code, not the destination item or its first-copy dataset.
func teamOnlineDashboardItemID(sourceProject, kind, itemID string) string {
	sum := sha256.Sum256([]byte(sourceProject + "\x00" + kind + "\x00" + itemID))
	return "online-" + hex.EncodeToString(sum[:12])
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
	result.DestinationItemID = teamOnlineDashboardItemID(source.CrewID, ref.Kind, ref.ItemID)
	result.Revision = item.Revision
	result.Automations = item.Dependencies.Automations
	result.OfferAutomations = ref.Seed && len(result.Automations) > 0
	var dataSchema teamonline.Schema
	// Updates never even inspect the attached data stream. Preview of a new
	// item reads its immutable source snapshot header, never a live Team DB.
	if ref.Seed {
		if len(item.DatasetParts) > 0 {
			schema, err := teamonline.DescribeParts(ref.Package, item.DatasetParts)
			if err != nil {
				return result, err
			}
			result.Tables = schema.Tables
			dataSchema = schema
		} else if len(item.Dependencies.Dataset.Tables) > 0 {
			return result, fmt.Errorf(teamOnlinePreparationNeeded)
		}
	}
	if ref.Seed && len(item.DatasetParts) > 0 {
		if guard := dashboardFileGuard(claims); guard != nil && !guard.Allows("db/db.sqlite", !preview) {
			return result, fmt.Errorf("the Team dataset is outside this connection's file access")
		}
	}
	if preview {
		if ref.Seed && len(item.DatasetParts) > 0 && destination.CrewID != "" {
			target, err = api.dashboardTarget(ctx, claims, destination.Path)
			if err != nil || !dashboardAuthoringAllowed(claims, &target) {
				return result, fmt.Errorf("this Team Crew is unavailable")
			}
			db, err := teamOnlineDatasetDatabase(destination, true)
			if err != nil {
				return result, err
			}
			if db != nil {
				err = teamonline.CheckCollisions(ctx, db, dataSchema)
				db.Close()
				if err != nil {
					return result, err
				}
			}
		}
		return result, nil
	}
	target, err = api.dashboardTarget(ctx, claims, destination.Path)
	if err != nil || !dashboardAuthoringAllowed(claims, &target) {
		return result, fmt.Errorf("this Team Crew is unavailable")
	}
	if publication, err := readPublishedProject(ctx, destination.Path); err != nil {
		return result, err
	} else if publication != nil {
		return result, fmt.Errorf("this item must be updated in its editable Team Crew")
	}
	guard := dashboardFileGuard(claims)
	current, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: destination.Path, Action: "list", IncludeDraft: true, Guard: guard})
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
	var dataset *sql.DB
	if ref.Seed && len(item.DatasetParts) > 0 {
		dataset, err = teamOnlineDatasetDatabase(destination, false)
		if err != nil {
			return result, err
		}
		if dataset == nil {
			return result, fmt.Errorf("the Team Crew's database is still being prepared; try again in a moment")
		}
		defer dataset.Close()
		if err = teamonline.CheckCollisions(ctx, dataset, dataSchema); err != nil {
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
			content = rewriteTeamOnlineAssetToken(content, source.Path+"/"+asset, destination.Path+"/"+name)
			content = rewriteTeamOnlineAssetToken(content, asset, name)
			item.Snapshot.Files[file] = content
		}
	}
	request := dashboards.Request{Root: destination.Path, ID: result.DestinationItemID, Title: item.Snapshot.Title, Artifact: &item.Snapshot.Artifact, Motion: &item.Snapshot.Motion, Files: item.Snapshot.Files, IncludeDraft: true, Action: "create", Guard: guard}
	if existing != nil {
		request.Action = "update"
		request.ExpectedRevision = existing.Revision
		old, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: destination.Path, ID: existing.ID, Action: "get", IncludeDraft: true, Guard: guard})
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
	var published dashboards.Result
	if dataset != nil {
		var seedBytes int64
		for _, part := range item.DatasetParts {
			info, statErr := ref.Package.Stat(part)
			if statErr != nil || !info.Mode().IsRegular() || info.Size() > publication.MaxFileBytes {
				return result, fmt.Errorf("the prepared dataset is unavailable or exceeds the publication limit")
			}
			seedBytes += info.Size()
			if seedBytes > publication.MaxBytes {
				return result, fmt.Errorf("the prepared dataset exceeds the publication limit")
			}
		}
		started := time.Now()
		// This same-process coordinator holds the normal publication lock until
		// the seed transaction commits, and restores its pointer on any failure.
		published, err = dashboards.InstallWithData(ctx, fsutil.WorkspaceDocsRoot(), request, func(publish func() error) error {
			return teamonline.ImportPartsWithPublication(ctx, dataset, ref.Package, item.DatasetParts, true, publish)
		})
		event := "seeded"
		if err != nil {
			event = "seed_failed"
		}
		log.Printf("[TEAM_ONLINE] event=%s person=%s source=%s destination=%s item=%s bytes=%d elapsed_ms=%d", event, claims.UserID, source.CrewID, destination.CrewID, result.DestinationItemID, seedBytes, time.Since(started).Milliseconds())
	} else {
		var updated dashboards.Result
		updated, err = dashboardWorkspaceRequest(ctx, request)
		if err == nil && updated.Dashboard != nil {
			published, err = dashboardWorkspaceRequest(ctx, dashboards.Request{Root: destination.Path, ID: result.DestinationItemID, Action: "publish", ExpectedRevision: updated.Dashboard.Revision, IncludeDraft: true, Guard: guard})
		}
	}
	if err != nil {
		return result, err
	}
	if published.Dashboard == nil || strings.TrimSpace(published.Dashboard.Published) == "" {
		return result, fmt.Errorf("the online item could not be published")
	}

	result.Updated = existing != nil
	return result, nil
}

// Rewrite whole path tokens only: x.png is not a match in x.png.bak, in a
// longer pathname, or in an identifier. Query/fragment suffixes remain intact.
func rewriteTeamOnlineAssetToken(content, old, replacement string) string {
	if old == "" || old == replacement {
		return content
	}
	pathByte := func(c byte) bool {
		return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("_./%~-", rune(c)) || c >= 0x80
	}
	var out strings.Builder
	start := 0
	for search := 0; search < len(content); {
		relative := strings.Index(content[search:], old)
		if relative < 0 {
			break
		}
		at := search + relative
		end := at + len(old)
		if (at == 0 || !pathByte(content[at-1])) && (end == len(content) || !pathByte(content[end])) {
			out.WriteString(content[start:at])
			out.WriteString(replacement)
			start = end
		}
		search = end
	}
	if start == 0 {
		return content
	}
	out.WriteString(content[start:])
	return out.String()
}

// Paths come from the normal registered Team target. Database initialization
// belongs to ordinary Crew setup before the atomic item operation, not to an
// import that could later fail and leave new schema behind.
func teamOnlineDatasetDatabase(destination triggerTarget, readonly bool) (*sql.DB, error) {
	roots, found, err := crewroot.Lookup(fsutil.WorkspaceDocsRoot(), destination.Path)
	if err != nil || !found || roots.ID != destination.CrewID {
		return nil, fmt.Errorf("the Team dataset is unavailable")
	}
	full, err := crewMoveRecordPath(roots, "db/db.sqlite")
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(full); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	pragma := "&_pragma=foreign_keys(1)"
	if readonly {
		pragma = "&_pragma=query_only(true)"
	}
	db, err := sql.Open("sqlite", sqliteopen.DSN(full)+"&mode=rw"+pragma)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}
