package server

import (
	"context"
	"net/http"
	"os"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

// A recipient's own Mac agents receive only their own admitted working copies.
// This never mounts the source owner's Crew, the canonical server tree, or the
// private transfer/history directory. Access removal stops future grants/sync;
// already downloaded files remain on their computer.
func crewTeamNativeFolders(ctx context.Context, claims *UserClaims) []workflowtypes.WorkflowFolderGrant {
	if !IsLocalMode() || !websiteDeviceClaims(claims) {
		return nil
	}
	connections, e := readDesktopConnections(claims.UserID)
	if e != nil {
		return nil
	}
	var grants []workflowtypes.WorkflowFolderGrant
	for _, c := range connections {
		check, cancel := context.WithTimeout(ctx, 5*time.Second)
		var out struct {
			Teams []crewTeam `json:"teams"`
		}
		e = crewTeamRemoteJSON(check, claims.UserID, c, http.MethodGet, "/api/crew-teams", nil, &out)
		cancel()
		if e != nil {
			continue
		}
		for _, team := range out.Teams {
			if !team.FolderAccess {
				continue
			}
			dir, e := crewTeamLocalDir(claims.UserID, c, team.ProjectID, team.Folder.ID)
			if e != nil {
				continue
			}
			b, e := readCrewTeamLocal(dir)
			if e != nil || b.LocalProject != "" || b.RemoteUser != c.User.ID {
				continue
			}
			info, e := os.Lstat(b.Root)
			if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				continue
			}
			id := crewTransferDigest(c.Server + "\x00" + c.User.ID + "\x00" + team.ProjectID)[:12]
			grants = append(grants, workflowtypes.WorkflowFolderGrant{ID: "team-" + id, Alias: "team_" + id, Path: b.Root, Access: workflowtypes.FolderAccessReadWrite, Reason: team.Name + " · shared folder"})
		}
	}
	return grants
}
