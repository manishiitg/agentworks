package server

import (
	"context"
	"log"
	"os"
	"path/filepath"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// applyCrewTeamOnMac makes this Mac follow its Crew's Team record from the company server (owner, 5 Nov: the server
// decides): Team on lists every agent on the online copy (who sees which is the record's In team / Only me) and makes
// the shared folders the server chose; Team off keeps only agents public on AgentBridge (Mac online's choice). It runs
// when this Mac reads the record: its Team sync loop, its screen and its chat. It returns whether a folder was made.
func (api *StreamingAPI) applyCrewTeamOnMac(ctx context.Context, project string, t crewTeam) bool {
	if !IsLocalMode() {
		return false
	}
	madeFolder := false
	// Every shared folder the record names is made here when missing: team/ on Team on, and a folder a member added
	// by name from the website (owner, 5 Nov).
	if t.On && len(t.Folder.Folders) > 0 {
		if roots, err := api.crewSyncRoots(ctx, &UserClaims{UserID: GetDefaultUserID(), Username: GetDefaultUserID()}, project); err == nil {
			for _, folder := range t.Folder.Folders {
				dir := filepath.Join(roots.Files, filepath.FromSlash(folder))
				if _, err := os.Lstat(dir); os.IsNotExist(err) {
					if err = os.MkdirAll(dir, 0o755); err != nil {
						log.Printf("[CREW_TEAM] local project=%s event=team_folder_failed folder=%q reason=%q", project, folder, err)
					} else {
						madeFolder = true
					}
				}
			}
		}
	}
	before := readCrewTeamState(project).TeamOn
	if err := writeCrewTeamState(project, func(s *crewTeamLocalState) { s.TeamOn = t.On }); err != nil {
		log.Printf("[CREW_TEAM] local project=%s team state not saved: %v", project, err)
	}
	if before != t.On || madeFolder {
		api.refreshMacSharedPurposes(ctx, project)
		dropCrewAccessCache(project, t.ProjectID) // the Access row says the new state at once
		log.Printf("[CREW_TEAM] local project=%s server_project=%s event=applied on=%t folder=%q folder_created=%t", project, t.ProjectID, t.On, t.Folder.display(), madeFolder)
	}
	return madeFolder
}

// crewLocalProjectForTeam is this Mac's own Crew whose online copy (on this connection) is the Team's project, or "".
func crewLocalProjectForTeam(owner string, c desktopConnection, serverProject string) string {
	records, err := defaultProjectOwners().All()
	if err != nil {
		return ""
	}
	for _, rec := range records {
		if rec.OwnerID != owner || rec.Product != crewProfileID {
			continue
		}
		if pair, err := crewroot.ReadSync(rec.ProjectID); err == nil && pair != nil && pair.Side == "local" && pair.ConnectionID == c.ID && pair.PeerProjectID == serverProject {
			return rec.ProjectID
		}
	}
	return ""
}
