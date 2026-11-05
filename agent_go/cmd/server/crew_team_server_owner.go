package server

import (
	"context"
	"log"
	"os"
	"slices"

	"github.com/google/uuid"
)

// Team for a Crew that lives on the server (owner, 5 Nov): people on the website work in the Crew's own folder, like
// co-owners, so nothing is copied for them. The Team store exists only for members who chose "Add to my Mac": the
// Crew's own folder is the owner's copy of it (as the owner's Mac folder is for a Mac Crew), synced in process as the
// owner, so their Macs sync against it. Changes made on the website are captured into the store by its next round
// like any change, and Mac members' changes land in the Crew folder, with the store's history and clash handling.

// serverCrewTeamStoreNeeded: a Crew on the server with its Team on, a shared folder, and someone working on their Mac.
func serverCrewTeamStoreNeeded(t crewTeam) bool {
	if IsLocalMode() || !t.On || t.Folder.ID == "" || crewRelayTwin(t.ProjectID) {
		return false
	}
	return slices.ContainsFunc(t.People, func(p crewTeamPerson) bool { _, onMac := t.memberMac(p.UserID); return onMac })
}

// syncServerCrewTeamStore runs one round of the owner's copy: the Crew folder and the Team store, both ways, and the
// Crew's setup (team-setup.json, crew_team_setup.go).
func (api *StreamingAPI) syncServerCrewTeamStore(ctx context.Context, t crewTeam) {
	if !serverCrewTeamStoreNeeded(t) {
		return
	}
	owner := t.OwnerID
	claims := &UserClaims{UserID: owner, Username: owner}
	roots, err := api.crewSyncRoots(context.WithValue(ctx, UserContextKey, claims), claims, t.ProjectID)
	if err != nil {
		log.Printf("[CREW_TEAM_SERVER] project=%s event=owner_copy_unavailable reason=%q", t.ProjectID, err)
		return
	}
	dir, err := crewTeamServerCopyDir(owner, t.ProjectID, t.Folder.ID)
	if err != nil {
		return
	}
	b, err := readCrewTeamLocal(dir)
	if os.IsNotExist(err) {
		b = crewTeamLocal{FolderID: t.Folder.ID, Connection: "server", Project: t.ProjectID, LocalProject: t.ProjectID, RemoteUser: owner, Client: uuid.NewString(),
			Root: roots.Files, Status: "pending"}
		if err = os.MkdirAll(dir, 0o700); err == nil {
			err = saveCrewTeamLocal(dir, b)
		}
		if err != nil {
			log.Printf("[CREW_TEAM_SERVER] project=%s event=owner_copy_failed reason=%q", t.ProjectID, err)
			return
		}
		log.Printf("[CREW_TEAM_SERVER] project=%s folder=%q event=owner_copy_created", t.ProjectID, t.Folder.display())
	} else if err != nil {
		return
	}
	c := desktopConnection{ID: "server", Server: crewTeamServerHost, User: UserInfo{ID: owner}}
	round := context.WithValue(ctx, crewTeamTransportKey{}, crewTeamInProcess{handler: api.crewTeamServerHandler(), claims: claims})
	api.reconcileCrewTeamSetup(ctx, owner, t.ProjectID, b.Root, dir, b.Downloaded)
	if err = syncCrewTeamLocal(round, owner, c, dir, &b); err != nil {
		log.Printf("[CREW_TEAM_SERVER] project=%s folder=%q event=owner_copy_sync_failed reason=%q", t.ProjectID, t.Folder.display(), err)
		return
	}
	api.reconcileCrewTeamSetup(ctx, owner, t.ProjectID, b.Root, dir, b.Downloaded)
}
