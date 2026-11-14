package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// PLAT-746-1025: on-demand provisioning only. The resulting user, Crew and
// Team use ordinary directory, ownership, membership and account behaviour.
func (api *StreamingAPI) ensureCrewTeamOnlineHome(ctx context.Context, project string) (string, error) {
	if IsLocalMode() {
		return "", fmt.Errorf("send this hand-off to the Team's server")
	}
	dir, err := crewTeamDir(project)
	if err != nil {
		return "", err
	}
	mu := crewTeamRecordMutex(dir)
	mu.Lock()
	defer mu.Unlock()
	team, err := readCrewTeam(project)
	person := GetUserIDFromContext(ctx)
	if err != nil || !team.On || !team.member(person) || !crewTeamAccountActive(person) || team.RosterSource != "" || ownerOfCrewProjectID(project) != team.OwnerID {
		return "", fmt.Errorf("join this Team to put work online")
	}
	user := "team-" + project
	home := uuid.NewSHA1(uuid.NameSpaceOID, []byte("agentworks/team-home/"+project)).String()
	if team.OnlineHome != "" && team.OnlineHome != home {
		return "", fmt.Errorf("this Team's online home is unavailable")
	}
	if err := ensureCrewTeamUser(user, team.Name, !team.On); err != nil {
		return "", err
	}
	projects, err := defaultProjectOwners().All()
	if err != nil {
		return "", err
	}
	found := false
	for _, p := range projects {
		if p.ProjectID == home {
			if p.OwnerID != user || p.Product != crewProfileID {
				return "", fmt.Errorf("this Team's online home is already assigned")
			}
			found = true
		}
	}
	if !found {
		claims := &UserClaims{UserID: user, Username: user}
		name := []rune("Team: " + team.Name)
		if len(name) > 60 {
			name = name[:60]
		}
		if _, err := api.createWorkspaceFromSpec(context.WithValue(ctx, UserContextKey, claims), claims, crewProfileID, crewSpec{Name: string(name)}, false, home); err != nil {
			return "", err
		}
		log.Printf("[TEAM_HOME] team=%s crew=%s user=%s actor=%s event=created", project, home, user, person)
	}
	if team.OnlineHome == "" {
		team.Revision++
	}
	team.OnlineHome = home
	if err := syncCrewTeamHomeMembersLocked(team); err != nil {
		return "", err
	}
	if err := fsutil.WriteJSONAtomic(filepath.Join(dir, "team.json"), team, 0600); err != nil {
		return "", err
	}
	publishCrewPanelChanged(project)
	return home, nil
}

func ensureCrewTeamUser(id, name string, paused bool) error {
	userDirectoryMutationMu.Lock()
	defer userDirectoryMutationMu.Unlock()
	dir, err := readUserDirectoryFile()
	if err != nil {
		return err
	}
	user := dir.byID(id)
	if user == nil {
		dir.Users = append(dir.Users, UserRecord{ID: id, Username: dir.uniqueDirectoryUsername("Team: "+name, ""), Role: UserRoleCreator, CanCreate: true, Products: []string{"work"}, CreatedAt: time.Now().UTC().Format(time.RFC3339)})
		user = &dir.Users[len(dir.Users)-1]
	} else if user.PasswordHash != "" || user.Email != "" || user.SSO != nil {
		return fmt.Errorf("this Team's account name belongs to a sign-in account")
	}
	user.Disabled = paused
	user.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	return saveUserDirectory(dir)
}

func sameCrewTeamRoster(a, b crewTeam) bool {
	return a.On == b.On && slices.EqualFunc(a.People, b.People, func(a, b crewTeamPerson) bool {
		return a.UserID == b.UserID && a.Invited == b.Invited
	})
}

// The source Team's mutex stays held through both ordinary Team writes.
// Source membership is the only editable roster; the home's is derived.
func syncCrewTeamHomeMembersLocked(source crewTeam) error {
	if IsLocalMode() || source.OnlineHome == "" || source.RosterSource != "" {
		return nil
	}
	home, err := readCrewTeam(source.OnlineHome)
	if os.IsNotExist(err) {
		home = crewTeam{ProjectID: source.OnlineHome, OwnerID: "team-" + source.ProjectID, RosterSource: source.ProjectID, Name: source.Name}
	} else if err != nil {
		return err
	}
	if home.OwnerID != "team-"+source.ProjectID || home.RosterSource != source.ProjectID || ownerOfCrewProjectID(home.ProjectID) != home.OwnerID {
		return fmt.Errorf("this Team's online home is unavailable")
	}
	home.On = source.On
	home.People = []crewTeamPerson{{UserID: source.OwnerID}}
	for _, person := range source.People {
		home.People = append(home.People, crewTeamPerson{UserID: person.UserID, Invited: person.Invited})
	}
	if err := ensureCrewTeamUser(home.OwnerID, source.Name, !source.On); err != nil {
		return err
	}
	if err := writeCrewTeamRecord(home, nil, true); err != nil {
		return err
	}
	log.Printf("[TEAM_HOME] team=%s crew=%s event=members_synced source_count=%d home_count=%d on=%t", source.ProjectID, home.ProjectID, len(source.People)+1, len(home.People), home.On)
	return nil
}

// Once at startup; repair existing homes only, never provision idle Teams.
func repairCrewTeamHomeMembers() {
	root, err := crewroot.StateRoot()
	if err != nil {
		return
	}
	entries, err := os.ReadDir(filepath.Join(root, "team-folders"))
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir, err := crewTeamDir(entry.Name())
		if err != nil {
			continue
		}
		mu := crewTeamRecordMutex(dir)
		mu.Lock()
		team, err := readCrewTeam(entry.Name())
		if err == nil {
			err = syncCrewTeamHomeMembersLocked(team)
		}
		mu.Unlock()
		if err != nil {
			log.Printf("[TEAM_HOME] team=%s event=members_sync_failed reason=%q", entry.Name(), err.Error())
		}
	}
}

// Lifecycle deletion calls the ordinary Crew deletion handler under the
// virtual owner's identity. No sign-in or credential is issued.
func (api *StreamingAPI) deleteCrewTeamOnlineHome(r *http.Request, project string) error {
	team, err := readCrewTeam(project)
	if os.IsNotExist(err) || err == nil && team.OnlineHome == "" {
		return nil
	}
	if err != nil {
		return err
	}
	backup := ""
	root := crewProjectRootByID(team.OnlineHome)
	if root != "" {
		roots, found, err := crewStorageRoots(root)
		if err != nil || !found || roots.Owner != "team-"+project {
			return fmt.Errorf("Team online home could not be backed up")
		}
		if backup, err = backupCrewFolders(roots); err != nil {
			return err
		}
		if err := ensureCrewTeamUser(roots.Owner, team.Name, false); err != nil {
			return err
		}
		claims := &UserClaims{UserID: roots.Owner, Username: roots.Owner}
		req := r.Clone(context.WithValue(r.Context(), UserContextKey, claims))
		status, _ := scheduleHandler(req, api.handleDeleteAgentProfileProject, http.MethodDelete, "/", map[string]string{"id": crewProfileID, "project_id": team.OnlineHome}, nil, nil)
		if status >= 300 {
			_ = ensureCrewTeamUser(roots.Owner, team.Name, !team.On)
			return fmt.Errorf("Team online home could not be removed")
		}
	}
	userDirectoryMutationMu.Lock()
	defer userDirectoryMutationMu.Unlock()
	directory, err := readUserDirectoryFile()
	if err != nil {
		return err
	}
	directory.Users = slices.DeleteFunc(directory.Users, func(u UserRecord) bool { return u.ID == "team-"+project })
	if err := saveUserDirectory(directory); err != nil {
		return err
	}
	log.Printf("[TEAM_HOME] team=%s crew=%s user=%s event=deleted backup=%s", project, team.OnlineHome, "team-"+project, backup)
	return nil
}
