package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// One-off cleanup after the Team online home was removed (#2993). Remove once every server has run it.
//
// An operator writes "dry" or "apply" to <state>/migrations/team-homes.request; the next server start reads it, writes
// the result next to it and removes the request. For every Team that still names an online home: the home (its Crew,
// backed up first, and its team-<project> account) is deleted through deleteServerCrewTeamHome and the Team record
// forgets it. The home's own Team record (one with roster_source) is backed up and removed.

const teamHomesRequest = "team-homes.request"

type teamHomeCleanupRow struct {
	Team   string `json:"team"`
	Name   string `json:"name"`
	Home   string `json:"home,omitempty"`
	Result string `json:"result"`
	Detail string `json:"detail,omitempty"`
}

func (api *StreamingAPI) runTeamHomesCleanupRequest(ctx context.Context) {
	state, err := crewroot.StateRoot()
	if err != nil {
		return
	}
	dir := filepath.Join(state, "migrations")
	request := filepath.Join(dir, teamHomesRequest)
	b, err := os.ReadFile(request)
	if err != nil {
		return
	}
	mode := strings.TrimSpace(string(b))
	_ = os.Remove(request)
	if mode != "dry" && mode != "apply" {
		log.Printf("[TEAM_HOME_CLEANUP] event=refused reason=%q", "request must say dry or apply")
		return
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	backup := filepath.Join(state, "migration-backups", "team-homes-"+stamp)
	rows, err := api.cleanupTeamHomes(ctx, state, mode == "apply", backup)
	out := map[string]any{"mode": mode, "rows": rows}
	if mode == "apply" {
		out["backup"] = backup
	}
	if err != nil {
		out["error"] = err.Error()
	}
	_ = fsutil.WriteJSONAtomic(filepath.Join(dir, "team-homes-"+mode+"-"+stamp+".json"), out, 0600)
	log.Printf("[TEAM_HOME_CLEANUP] event=finished mode=%s rows=%d error=%v", mode, len(rows), err)
}

func (api *StreamingAPI) cleanupTeamHomes(ctx context.Context, state string, apply bool, backup string) ([]teamHomeCleanupRow, error) {
	entries, err := os.ReadDir(filepath.Join(state, "team-folders"))
	if err != nil {
		return nil, err
	}
	if apply {
		if err := os.MkdirAll(backup, 0700); err != nil {
			return nil, err
		}
	}
	rows := []teamHomeCleanupRow{}
	mirrors := []crewTeam{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		team, err := readCrewTeam(entry.Name())
		if err != nil {
			continue
		}
		if team.RosterSource != "" {
			mirrors = append(mirrors, team)
			continue
		}
		if team.OnlineHome == "" {
			continue
		}
		row := teamHomeCleanupRow{Team: team.ProjectID, Name: team.Name, Home: team.OnlineHome, Result: "would_delete_home"}
		row.Detail = strings.Join(serverCrewTeamHomeFiles(team.OnlineHome), ", ")
		if apply {
			row.Result, row.Detail = "deleted_home", ""
			if err := api.cleanupOneTeamHome(ctx, team, backup); err != nil {
				row.Result, row.Detail = "failed", err.Error()
			}
		}
		log.Printf("[TEAM_HOME_CLEANUP] team=%s home=%s result=%s", team.ProjectID, team.OnlineHome, row.Result)
		rows = append(rows, row)
	}
	// A home's own record (it mirrored its Team's people) goes last, after its home is gone.
	for _, team := range mirrors {
		row := teamHomeCleanupRow{Team: team.ProjectID, Name: team.Name, Result: "would_remove_home_record"}
		if apply {
			row.Result = "removed_home_record"
			if err := removeTeamRecordWithBackup(team.ProjectID, backup); err != nil {
				row.Result, row.Detail = "failed", err.Error()
			}
		}
		log.Printf("[TEAM_HOME_CLEANUP] record=%s result=%s", team.ProjectID, row.Result)
		rows = append(rows, row)
	}
	return rows, nil
}

func (api *StreamingAPI) cleanupOneTeamHome(ctx context.Context, team crewTeam, backup string) error {
	dir, err := crewTeamDir(team.ProjectID)
	if err != nil {
		return err
	}
	if err := copyDirForBackup(dir, filepath.Join(backup, "team-folders", team.ProjectID)); err != nil {
		return err
	}
	req := httptest.NewRequest("DELETE", "/", nil).WithContext(ctx)
	if err := api.deleteServerCrewTeamHome(req, team); err != nil {
		return err
	}
	// Ordinary Team writes carry the old fields over, so forget the home in the stored record itself.
	mu := crewTeamRecordMutex(dir)
	mu.Lock()
	defer mu.Unlock()
	path := filepath.Join(dir, "team.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var record map[string]any
	if err := json.Unmarshal(raw, &record); err != nil {
		return err
	}
	delete(record, "online_home")
	delete(record, "roster_source")
	return fsutil.WriteJSONAtomic(path, record, 0600)
}

func removeTeamRecordWithBackup(project, backup string) error {
	dir, err := crewTeamDir(project)
	if err != nil {
		return err
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}
	if err := copyDirForBackup(dir, filepath.Join(backup, "team-folders", project)); err != nil {
		return err
	}
	mu := crewTeamRecordMutex(dir)
	mu.Lock()
	defer mu.Unlock()
	return os.RemoveAll(dir)
}
