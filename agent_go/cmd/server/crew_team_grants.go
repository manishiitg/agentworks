package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

// A recipient's own Mac agents receive only their own admitted working copies.
// This never mounts the source owner's Crew, the canonical server tree, or the
// private transfer/history directory. Access removal stops future grants/sync;
// already downloaded files remain on their computer.
func crewTeamNativeFolders(ctx context.Context, claims *UserClaims) []workflowtypes.WorkflowFolderGrant {
	grants, _ := crewTeamNativeFoldersChecked(ctx, claims)
	return grants
}

func crewTeamNativeFoldersChecked(ctx context.Context, claims *UserClaims) ([]workflowtypes.WorkflowFolderGrant, error) {
	return crewTeamNativeFoldersFor(ctx, claims, "")
}

// crewTeamNativeFoldersFor: with a project, a failing connection is an error only when it holds that Crew's Team.
func crewTeamNativeFoldersFor(ctx context.Context, claims *UserClaims, project string) ([]workflowtypes.WorkflowFolderGrant, error) {
	if !IsLocalMode() {
		return crewTeamServerFolders(claims), nil
	}
	if !websiteDeviceClaims(claims) {
		return nil, nil
	}
	connections, e := readDesktopConnections(claims.UserID)
	if e != nil {
		return nil, e
	}
	// Each saved server is asked at the same time: one after another they added up on every message (#3739).
	type teamsAnswer struct {
		Teams []crewTeam `json:"teams"`
	}
	answers := make([]teamsAnswer, len(connections))
	failures := make([]error, len(connections))
	var asked sync.WaitGroup
	for i, c := range connections {
		asked.Add(1)
		go func(i int, c desktopConnection) {
			defer asked.Done()
			check, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			failures[i] = crewTeamRemoteJSON(check, claims.UserID, c, http.MethodGet, "/api/crew-teams", nil, &answers[i])
		}(i, c)
	}
	asked.Wait()
	var grants []workflowtypes.WorkflowFolderGrant
	var unavailable error
	for i, c := range connections {
		out, e := answers[i], failures[i]
		if e != nil {
			// A slow or unrelated saved connection must not drop Team folders for every chat: reuse the last good list
			// for that connection (as the Team reads do), and tell the person only when a Team this Mac holds a copy of
			// lives there and there is nothing to fall back on.
			last, kept := crewTeamLastGrants(claims.UserID, c.ID)
			bound := crewTeamBoundOn(claims.UserID, c.ID, project)
			log.Printf("[CREW_TEAM] event=turn_folders_unavailable project=%s connection=%s team_here=%t kept_last=%t reason=%q", project, c.ID, bound, kept, crewTeamFailureReason(e))
			if kept {
				grants = append(grants, last...)
			} else if bound {
				unavailable = e
			}
			continue
		}
		var mine []workflowtypes.WorkflowFolderGrant
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
			mine = append(mine, workflowtypes.WorkflowFolderGrant{ID: "team-" + id, Alias: "team_" + id, Path: b.Root, Access: workflowtypes.FolderAccessReadWrite, Reason: team.Name + " · shared with the team: " + team.Folder.display()})
		}
		crewTeamKeepGrants(claims.UserID, c.ID, mine)
		grants = append(grants, mine...)
	}
	return grants, unavailable
}

// The last grant list each saved connection answered, per person, for turns while it is slow or unreachable.
// A connection's last good list is reused for at most crewTeamGrantReuse after it was read, so someone removed from a
// Team does not keep its folder while that server is unreachable; a Team copy this Mac no longer holds (left or
// removed) is dropped at once.
const crewTeamGrantReuse = 15 * time.Minute

type crewTeamGrantEntry struct {
	grants []workflowtypes.WorkflowFolderGrant
	at     time.Time
}

var crewTeamGrantCache = struct {
	sync.Mutex
	m map[string]crewTeamGrantEntry
}{m: map[string]crewTeamGrantEntry{}}

func crewTeamKeepGrants(owner, connection string, grants []workflowtypes.WorkflowFolderGrant) {
	crewTeamGrantCache.Lock()
	defer crewTeamGrantCache.Unlock()
	crewTeamGrantCache.m[owner+"\x00"+connection] = crewTeamGrantEntry{grants: append([]workflowtypes.WorkflowFolderGrant{}, grants...), at: time.Now()}
}

func crewTeamLastGrants(owner, connection string) ([]workflowtypes.WorkflowFolderGrant, bool) {
	key := owner + "\x00" + connection
	crewTeamGrantCache.Lock()
	entry, ok := crewTeamGrantCache.m[key]
	if ok && time.Since(entry.at) > crewTeamGrantReuse {
		delete(crewTeamGrantCache.m, key)
		ok = false
	}
	crewTeamGrantCache.Unlock()
	if !ok {
		return nil, false
	}
	held := crewTeamHeldRoots(owner, connection)
	kept := []workflowtypes.WorkflowFolderGrant{}
	for _, g := range entry.grants {
		if held[g.Path] {
			kept = append(kept, g)
		}
	}
	return kept, true
}

// crewTeamHeldRoots are the Team working copy folders this Mac still holds through the connection.
func crewTeamHeldRoots(owner, connection string) map[string]bool {
	held := map[string]bool{}
	root, e := crewroot.StateRoot()
	if e != nil {
		return held
	}
	files, _ := filepath.Glob(filepath.Join(root, "team-working", crewTransferDigest(owner), "*", "binding.json"))
	for _, f := range files {
		if b, e := readCrewTeamLocal(filepath.Dir(f)); e == nil && b.Connection == connection && b.Root != "" {
			if info, e := os.Lstat(b.Root); e == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
				held[b.Root] = true
			}
		}
	}
	return held
}

// crewTeamBoundOn: this Mac holds a Team working copy reached through the connection, for the project's Team when one
// is named (its Team project or the local Crew it joined into).
func crewTeamBoundOn(owner, connection, project string) bool {
	root, e := crewroot.StateRoot()
	if e != nil {
		return false
	}
	files, _ := filepath.Glob(filepath.Join(root, "team-working", crewTransferDigest(owner), "*", "binding.json"))
	for _, f := range files {
		if b, e := readCrewTeamLocal(filepath.Dir(f)); e == nil && b.Connection == connection && (project == "" || b.Project == project || b.LocalProject == project) {
			return true
		}
	}
	return false
}

// crewTeamFailureReason names the failure without URLs or response bodies.
func crewTeamFailureReason(e error) string {
	var status *crewRemoteHTTPError
	var transport *crewRemoteTransportError
	switch {
	case errors.As(e, &status):
		return fmt.Sprintf("http_%d", status.Status)
	case errors.As(e, &transport) && strings.Contains(transport.Message, "too long"),
		errors.Is(e, context.DeadlineExceeded), strings.Contains(e.Error(), "took too long"):
		return "timeout"
	case errors.As(e, &transport):
		return "unreachable"
	}
	return "error"
}
