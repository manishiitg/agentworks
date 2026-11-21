package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
	"github.com/manishiitg/coding-agent-loop/workspace/publication"
	"github.com/manishiitg/coding-agent-loop/workspace/sqliteopen"
	"github.com/manishiitg/coding-agent-loop/workspace/teamonline"
)

// The common item-only transport calls this on the current source (locally or
// over its exact authenticated Mac pair). It never reads a Team mirror. Only
// this source-owned revision and declared dependencies enter the private stage.
func (api *StreamingAPI) exportTeamOnlineDashboard(ctx context.Context, source triggerTarget, ref teamOnlineDashboardRef, stage *os.Root, includeData bool) (item teamonline.Item, returnErr error) {
	step := teamOnlineDashboardSteps(ctx, "export", source.CrewID, "", ref.ItemID, &returnErr)
	defer step("")
	claims := GetUserFromContext(ctx)
	if claims == nil || source.CrewOwner != claims.UserID || stage == nil {
		return item, fmt.Errorf("choose one of your own published items")
	}
	target, err := api.dashboardTarget(ctx, claims, source.Path)
	if err != nil || !dashboardAuthoringAllowed(claims, &target) {
		return item, fmt.Errorf("this source item is unavailable")
	}
	step("publication_read")
	current, err := dashboardWorkspaceRequest(ctx, dashboards.Request{Root: source.Path, ID: ref.ItemID, Action: "get", Guard: dashboardFileGuard(claims)})
	if err != nil {
		return item, err
	}
	if current.Dashboard == nil || current.Snapshot == nil || current.Dashboard.Published != ref.Revision {
		return item, fmt.Errorf("the source publication changed; preview it again")
	}
	item = teamonline.Item{ID: ref.ItemID, Revision: ref.Revision, Snapshot: *current.Snapshot}
	if declaration, ok := item.Snapshot.Files["online.json"]; ok {
		decoder := json.NewDecoder(strings.NewReader(declaration))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&item.Dependencies); err != nil {
			return item, fmt.Errorf(teamOnlinePreparationNeeded)
		}
		if decoder.Decode(new(any)) != io.EOF {
			return item, fmt.Errorf(teamOnlinePreparationNeeded)
		}
	} else if !item.Snapshot.Artifact {
		return item, fmt.Errorf(teamOnlinePreparationNeeded)
	}
	if len(item.Dependencies.Dataset.Tables) == 0 && (len(item.Dependencies.Dataset.Views) > 0 || len(item.Dependencies.Dataset.Triggers) > 0) {
		return item, fmt.Errorf(teamOnlinePreparationNeeded)
	}
	roots, found, err := crewroot.Lookup(fsutil.WorkspaceDocsRoot(), source.Path)
	if err != nil || !found || roots.ID != source.CrewID {
		return item, fmt.Errorf("the source Crew storage is unavailable")
	}
	step("assets")
	for _, asset := range item.Dependencies.Assets {
		if guard := dashboardFileGuard(claims); guard != nil && !guard.Allows(asset, false) {
			return item, fmt.Errorf("a declared item asset is outside this connection's file access")
		}
		if _, err := publication.CleanPath(asset); err != nil || !strings.HasPrefix(asset, "db/assets/") || publication.CredentialPath(asset) {
			return item, fmt.Errorf("declare only this item's files under db/assets")
		}
		physical, bound, err := crewroot.Resolve(fsutil.WorkspaceDocsRoot(), source.Path+"/"+asset)
		if err != nil || !bound {
			return item, fmt.Errorf("a declared item asset is unavailable")
		}
		sourceFile, err := os.Open(physical)
		if err != nil {
			return item, err
		}
		info, err := sourceFile.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > publication.MaxFileBytes {
			sourceFile.Close()
			return item, fmt.Errorf("a declared item asset exceeds the normal publication limits")
		}
		parts := strings.Split(asset, "/")
		for i := 1; i < len(parts); i++ {
			if err = stage.Mkdir(strings.Join(parts[:i], "/"), 0700); err != nil && !os.IsExist(err) {
				sourceFile.Close()
				return item, err
			}
		}
		copied, err := stage.OpenFile(asset, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			sourceFile.Close()
			return item, err
		}
		_, err = io.Copy(copied, io.LimitReader(sourceFile, publication.MaxFileBytes+1))
		sourceFile.Close()
		closeErr := copied.Close()
		if err != nil {
			return item, err
		}
		if closeErr != nil {
			return item, closeErr
		}
		// Only the exporter knows the authoritative source workspace. Make its
		// qualified references relative before transport; a Mac receiver has no
		// server-local source path and must not consult a stale mirror for one.
		for file, content := range item.Snapshot.Files {
			item.Snapshot.Files[file] = rewriteTeamOnlineAssetToken(content, source.Path+"/"+asset, asset)
		}
	}
	step("dataset_export")
	if includeData && len(item.Dependencies.Dataset.Tables) > 0 {
		if guard := dashboardFileGuard(claims); guard != nil && !guard.Allows("db/db.sqlite", false) {
			return item, fmt.Errorf("the item's data is outside this connection's file access")
		}
		sourcePath, err := crewDatabasePath(roots, "db/db.sqlite")
		if err != nil {
			return item, err
		}
		db, err := sql.Open("sqlite", sqliteopen.DSN(sourcePath)+"&mode=rw&_pragma=query_only(true)")
		if err != nil {
			return item, err
		}
		db.SetMaxOpenConns(1)
		_, item.DatasetParts, err = teamonline.ExportParts(ctx, db, item.Dependencies.Dataset, stage)
		db.Close()
		if err != nil {
			return item, err
		}
	}
	step("package_write")
	descriptor, err := json.Marshal(item)
	if err != nil {
		return item, err
	}
	if int64(len(descriptor)) > publication.MaxFileBytes {
		return item, fmt.Errorf("item source exceeds the normal file limit")
	}
	file, err := stage.OpenFile(teamonline.Descriptor, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return item, err
	}
	_, err = file.Write(descriptor)
	closeErr := file.Close()
	if err != nil {
		return item, err
	}
	return item, closeErr
}

// Stages carry IDs and fixed outcome names, never paths, SQL, data or raw errors.
// A deferred finish records the precise stage that refused or failed.
func teamOnlineDashboardSteps(ctx context.Context, action, source, destination, item string, result *error) func(string) {
	phase := "source_admission"
	write := func(outcome string) {
		log.Printf("[TEAM_ONLINE] event=dashboard_%s step=%s item=%s person=%s source=%s destination=%s outcome=%s", action, phase, item, GetUserIDFromContext(ctx), source, destination, outcome)
	}
	write("started")
	return func(next string) {
		outcome := "ok"
		if *result != nil {
			outcome = "failed"
		}
		write(outcome)
		if next != "" {
			phase = next
			write("started")
		}
	}
}
