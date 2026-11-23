package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// One-off migration (server Crews have co-owners and no Team, owner 21 Nov). Remove once every server has moved.
//
// An operator writes "dry" or "apply" to <state>/migrations/server-crew-teams.request; the next server start reads it
// before the Team startup pass, writes the result next to it and removes the request. For every Team on a Crew made on
// this server: its joined members become co-owners, its Team online home (account, Crew and slot) is deleted through the
// ordinary Crew deletion, and its Team record is moved into the backup. A Mac Crew's online copy keeps its Team.

const serverCrewTeamsRequest = "server-crew-teams.request"

type serverCrewTeamRow struct {
	Project  string   `json:"project"`
	Name     string   `json:"name"`
	Owner    string   `json:"owner"`
	CoOwners []string `json:"co_owners"`
	Home     string   `json:"home,omitempty"`
	// HomeFiles: files people made in the Team's online home (outside its .agentworks settings and seeded commands);
	// they exist only in the backup after apply, so a dry run with any here needs the owner's word first.
	HomeFiles []string `json:"home_files,omitempty"`
	Result    string   `json:"result"`
	Detail    string   `json:"detail,omitempty"`
}

func (api *StreamingAPI) runServerCrewTeamsMigrationRequest(ctx context.Context) {
	state, err := crewroot.StateRoot()
	if err != nil {
		return
	}
	dir := filepath.Join(state, "migrations")
	request := filepath.Join(dir, serverCrewTeamsRequest)
	b, err := os.ReadFile(request)
	if err != nil {
		return
	}
	mode := strings.TrimSpace(string(b))
	_ = os.Remove(request)
	if mode != "dry" && mode != "apply" {
		log.Printf("[CREW_TEAM_MIGRATION] event=refused reason=%q", "request must say dry or apply")
		return
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	backup := filepath.Join(state, "migration-backups", "server-crew-teams-"+stamp)
	rows, err := api.migrateServerCrewTeams(ctx, mode == "apply", backup)
	out := map[string]any{"mode": mode, "rows": rows}
	if mode == "apply" {
		out["backup"] = backup
	}
	if err != nil {
		out["error"] = err.Error()
	}
	_ = fsutil.WriteJSONAtomic(filepath.Join(dir, "server-crew-teams-"+mode+"-"+stamp+".json"), out, 0600)
	log.Printf("[CREW_TEAM_MIGRATION] event=finished mode=%s crews=%d error=%v", mode, len(rows), err)
}

func (api *StreamingAPI) migrateServerCrewTeams(ctx context.Context, apply bool, backup string) ([]serverCrewTeamRow, error) {
	state, err := crewroot.StateRoot()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(state, "team-folders"))
	if err != nil {
		return nil, err
	}
	if apply {
		if err := os.MkdirAll(backup, 0700); err != nil {
			return nil, err
		}
		if err := copyFileForBackup(defaultProjectOwners().path(), filepath.Join(backup, "projects.json")); err != nil {
			return nil, fmt.Errorf("back up the owner registry: %w", err)
		}
	}
	rows := []serverCrewTeamRow{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		team, err := readCrewTeam(entry.Name())
		if err != nil || team.RosterSource != "" || !crewLivesOnServer(team.ProjectID) {
			continue
		}
		row := serverCrewTeamRow{Project: team.ProjectID, Name: team.Name, Owner: logUsernameForUserID(team.OwnerID), Home: team.OnlineHome, CoOwners: []string{}, Result: "would_migrate"}
		row.HomeFiles = serverCrewTeamHomeFiles(team.OnlineHome)
		members := []string{}
		for _, p := range team.People {
			if p.Invited || p.UserID == "" || p.UserID == team.OwnerID || strings.HasPrefix(p.UserID, "team-") || !crewTeamAccountActive(p.UserID) {
				continue
			}
			members = append(members, p.UserID)
			row.CoOwners = append(row.CoOwners, logUsernameForUserID(p.UserID))
		}
		if apply {
			if err := api.migrateServerCrewTeam(ctx, team, members, backup); err != nil {
				row.Result, row.Detail = "failed", err.Error()
				log.Printf("[CREW_TEAM_MIGRATION] project=%s event=failed reason=%q", team.ProjectID, err.Error())
			} else {
				row.Result = "migrated"
				log.Printf("[CREW_TEAM_MIGRATION] project=%s event=migrated co_owners=%d home=%s", team.ProjectID, len(members), team.OnlineHome)
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (api *StreamingAPI) migrateServerCrewTeam(ctx context.Context, team crewTeam, members []string, backup string) error {
	root := crewProjectRootByID(team.ProjectID)
	id, ok := projectIdentityOf(root)
	if root == "" || !ok {
		return fmt.Errorf("the Crew's folder was not found")
	}
	dir, err := crewTeamDir(team.ProjectID)
	if err != nil {
		return err
	}
	if err := copyDirForBackup(dir, filepath.Join(backup, "team-folders", team.ProjectID)); err != nil {
		return fmt.Errorf("back up the Team record: %w", err)
	}
	for _, m := range members {
		if _, err := defaultProjectOwners().AddCoOwner(id.Product, id.Folder, m); err != nil {
			return fmt.Errorf("add co-owner: %w", err)
		}
	}
	// The ordinary Crew deletion of the Team's online home (it backs up the home's folders itself).
	if team.OnlineHome != "" {
		req := httptest.NewRequest("DELETE", "/", nil).WithContext(ctx)
		if err := api.deleteServerCrewTeamHome(req, team); err != nil {
			return fmt.Errorf("remove the Team online home: %w", err)
		}
	}
	mu := crewTeamRecordMutex(dir)
	mu.Lock()
	defer mu.Unlock()
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove the Team record: %w", err)
	}
	publishCrewPanelChanged(team.ProjectID)
	return nil
}

func copyFileForBackup(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0600)
}

func copyDirForBackup(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		return copyFileForBackup(path, target)
	})
}

func serverCrewTeamHomeFiles(home string) []string {
	root := crewProjectRootByID(home)
	if home == "" || root == "" {
		return nil
	}
	dir := filepath.Join(fsutil.WorkspaceDocsRoot(), filepath.FromSlash(root))
	files := []string{}
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, ".agentworks/") || strings.HasPrefix(rel, "commands/") {
			return nil
		}
		files = append(files, rel)
		return nil
	})
	return files
}

// deleteServerCrewTeamHome removes a Team's online home (its Crew, backed up first, and its team-<project> account)
// through the ordinary Crew deletion under that account's identity. No sign-in or credential is issued.
func (api *StreamingAPI) deleteServerCrewTeamHome(r *http.Request, team crewTeam) error {
	account := "team-" + team.ProjectID
	backup := ""
	if root := crewProjectRootByID(team.OnlineHome); root != "" {
		roots, found, err := crewStorageRoots(root)
		if err != nil || !found || roots.Owner != account {
			return fmt.Errorf("Team online home could not be backed up")
		}
		if backup, err = backupCrewFolders(roots); err != nil {
			return err
		}
		claims := &UserClaims{UserID: account, Username: account}
		req := r.Clone(context.WithValue(r.Context(), UserContextKey, claims))
		status, _ := scheduleHandler(req, api.handleDeleteAgentProfileProject, http.MethodDelete, "/", map[string]string{"id": crewProfileID, "project_id": team.OnlineHome}, nil, nil)
		if status >= 300 {
			return fmt.Errorf("Team online home could not be removed")
		}
	}
	userDirectoryMutationMu.Lock()
	defer userDirectoryMutationMu.Unlock()
	directory, err := readUserDirectoryFile()
	if err != nil {
		return err
	}
	directory.Users = slices.DeleteFunc(directory.Users, func(u UserRecord) bool { return u.ID == account })
	if err := saveUserDirectory(directory); err != nil {
		return err
	}
	log.Printf("[TEAM_HOME] team=%s crew=%s user=%s event=deleted backup=%s", team.ProjectID, team.OnlineHome, account, backup)
	return nil
}
