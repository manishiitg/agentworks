package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/slots"
)

// Team On provisions the shared online home. The resulting user, Crew and
// Team use ordinary directory, ownership, membership and account behaviour.
func (api *StreamingAPI) ensureCrewTeamOnlineHome(ctx context.Context, project string, reason ...string) (string, error) {
	if IsLocalMode() {
		return "", fmt.Errorf("set up this Team on its server")
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
	// Team On/startup keeps its existing admission. Only an admitted native
	// producer may create a home while Team is off. Discovery and asks never
	// pass that reason.
	ownedSource := false
	if len(reason) == 2 && reason[0] == "native_capture" {
		if a, ok := team.Agents[reason[1]]; ok {
			ownedSource = crewOfflineSourceOwner(team, person, a) == nil && a.Kind == "terminal" && a.Terminal != nil
		}
	}
	solo := ownedSource
	if err != nil || !team.On && !solo || (!team.member(person) && !crewTeamHomeOwnsSetup(team, person) && !ownedSource) || !crewTeamAccountActive(person) || team.RosterSource != "" || ownerOfCrewProjectID(project) != team.OwnerID {
		return "", fmt.Errorf("join this Team to put work online")
	}
	// The Team's own account (and its server slot) exists only while someone besides the owner has joined: an
	// owner-only Team has nobody for the Team agent to serve, and each account holds one of the server's slots.
	if !ownedSource && !crewTeamHasTeammate(team) {
		return "", fmt.Errorf("%s", crewTeamAgentWaitsForTeammate)
	}
	user := "team-" + project
	home := uuid.NewSHA1(uuid.NameSpaceOID, []byte("agentworks/team-home/"+project)).String()
	if team.OnlineHome != "" && team.OnlineHome != home {
		return "", fmt.Errorf("this Team's online home is unavailable")
	}
	if err := ensureCrewTeamUser(user, team.Name, false); err != nil {
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
		if err := api.createCrewTeamHomeMain(ctx, user, home); err != nil {
			return "", err
		}
		log.Printf("[TEAM_HOME] team=%s crew=%s user=%s actor=%s event=created reason=%s", project, home, user, person, firstNonEmptyTrimmed(strings.Join(reason, ""), "handoff"))
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
	if !found {
		homeTeam, err := readCrewTeam(home)
		if err != nil {
			return "", err
		}
		key := crewOwnerMainChatKey(home)
		teamAgentsByPersonWithMain(ctx, homeTeam, user, key)
		if err := writeCrewTeamWithAgents(homeTeam, func(agents map[string]crewTeamRegisteredAgent) {
			for id, a := range agents {
				if a.OwnerID == user && a.ChatKey == key {
					a.Rule.CanMakeChanges = true
					agents[id] = a
				}
			}
		}); err != nil {
			return "", err
		}
	}
	publishCrewPanelChanged(project)
	return home, nil
}

const crewTeamAgentWaitsForTeammate = "The Team agent starts when the first teammate joins. Invite someone to the Team first."

// crewTeamHasTeammate: someone besides the owner has joined (an open invite does not count).
func crewTeamHasTeammate(t crewTeam) bool {
	return slices.ContainsFunc(t.People, func(p crewTeamPerson) bool { return !p.Invited && p.UserID != "" && p.UserID != t.OwnerID })
}

// The virtual user owns this existing home and its Team setup. This grants no
// membership in members' source Crews and can never create a different home.
func crewTeamHomeOwnsSetup(team crewTeam, user string) bool {
	if team.OnlineHome == "" || team.RosterSource != "" || user != "team-"+team.ProjectID || ownerOfCrewProjectID(team.OnlineHome) != user {
		return false
	}
	home, err := readCrewTeam(team.OnlineHome)
	return err == nil && home.On && home.RosterSource == team.ProjectID && home.OwnerID == user
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
	// The Team agent is a teammate with a default profile picture (#3084); a picture set later is kept.
	if user.Avatar == nil {
		user.Avatar = &userAvatar{Icon: "bot", Color: "blue", Revision: "team-agent"}
	}
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
	// Online work keeps running for its owner when sharing stops. The home's
	// ordinary derived roster becomes owner-only; the source Team stays Off.
	home.On = true
	home.People = []crewTeamPerson{{UserID: source.OwnerID}}
	if source.On {
		for _, person := range source.People {
			home.People = append(home.People, crewTeamPerson{UserID: person.UserID, Invited: person.Invited})
		}
	}
	if err := ensureCrewTeamUser(home.OwnerID, source.Name, false); err != nil {
		return err
	}
	if err := writeCrewTeamRecord(home, nil, true); err != nil {
		return err
	}
	// The Team's vault (PLAT-746-1026) follows the same roster; a no-op until something has been shared.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := syncCrewTeamVault(ctx, source.ProjectID, source); err != nil {
			log.Printf("[TEAM_WAY] team=%s event=vault_members_failed reason=%q", source.ProjectID, err)
		}
	}()
	log.Printf("[TEAM_HOME] team=%s crew=%s event=members_synced source_count=%d home_count=%d source_on=%t owner_only=%t", source.ProjectID, home.ProjectID, len(source.People)+1, len(home.People), source.On, !source.On)
	return nil
}

// Once at startup: enabled source Teams always have their normal online home.
func (api *StreamingAPI) repairCrewTeamHomeMembers(ctx context.Context) {
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
		team, err := readCrewTeam(entry.Name())
		if err == nil && team.On && team.RosterSource == "" && team.OnlineHome == "" && crewTeamHasTeammate(team) && crewTeamAccountActive(team.OwnerID) {
			ownerCtx := internalBotRequestContext(context.WithValue(ctx, UserContextKey, (*UserClaims)(nil)), team.OwnerID)
			if _, err = api.ensureCrewTeamOnlineHome(ownerCtx, team.ProjectID, "team_on"); err != nil {
				log.Printf("[TEAM_HOME] team=%s event=create_failed reason=team_on error=%q", team.ProjectID, err)
			}
			continue
		}
		mu := crewTeamRecordMutex(dir)
		mu.Lock()
		team, err = readCrewTeam(entry.Name())
		if err == nil {
			err = syncCrewTeamHomeMembersLocked(team)
		}
		mu.Unlock()
		// One repair pass per startup, not a recursive walk on every roster write.
		// Ordinary creation already applies this policy when a slot exists.
		if err == nil && team.RosterSource == "" && team.OnlineHome != "" {
			if home, e := readCrewTeam(team.OnlineHome); e == nil && home.RosterSource == team.ProjectID && home.OwnerID == "team-"+team.ProjectID && ownerOfCrewProjectID(home.ProjectID) == home.OwnerID {
				repairCrewTeamHomeSlotAccess(home)
			}
		}
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

const crewTeamHomeSetupPending = "The Team agent is being set up on the server; the owner's next update finishes it."

// No provisioning or privilege escalation: only the existing host slot table.
// Profile admission uses this before a model or native program can be started.
func requireCrewTeamHomeSlot(project string) error {
	if IsLocalMode() || cliHostOS != "linux" || !slots.Enabled() {
		return nil
	}
	owner := ownerOfCrewProjectID(project)
	home, err := readCrewTeam(project)
	derived := err == nil && home.RosterSource != "" && home.OwnerID == owner
	source, named := strings.CutPrefix(owner, "team-")
	canonical := named && source != "" && project == uuid.NewSHA1(uuid.NameSpaceOID, []byte("agentworks/team-home/"+source)).String()
	if !derived && !canonical {
		return nil
	}
	// An unavailable protected home record must not re-enable the ordinary
	// user's app-account fallback for a known deterministic execution home.
	if !derived {
		log.Printf("[TEAM_HOME] event=slot_missing team=%s crew=%s user=%s reason=home_unavailable", source, project, owner)
		log.Printf("[TEAM_SLOT] outcome=refused project=%s", project)
		return fmt.Errorf("%s", crewTeamHomeSetupPending)
	}
	slot, enabled, err := slots.For(home.OwnerID)
	if err != nil || !enabled || slot == "" {
		log.Printf("[TEAM_HOME] event=slot_missing team=%s crew=%s user=%s", home.RosterSource, project, home.OwnerID)
		log.Printf("[TEAM_SLOT] outcome=refused project=%s", project)
		return fmt.Errorf("%s", crewTeamHomeSetupPending)
	}
	return nil
}

func repairCrewTeamHomeSlotAccess(home crewTeam) {
	if cliHostOS != "linux" || !slots.Enabled() {
		return
	}
	slot, enabled, err := slots.For(home.OwnerID)
	if err != nil || !enabled || slot == "" {
		log.Printf("[TEAM_HOME] event=slot_missing team=%s crew=%s user=%s", home.RosterSource, home.ProjectID, home.OwnerID)
		return
	}
	record, ok := crewOwnerRecord(home.ProjectID)
	if !ok || record.OwnerID != home.OwnerID {
		return
	}
	group, err := user.LookupGroup(slot)
	if err != nil {
		log.Printf("[TEAM_HOME] event=slot_access_failed team=%s crew=%s slot=%s reason=group_unavailable", home.RosterSource, home.ProjectID, slot)
		return
	}
	gid, err := strconv.Atoi(group.Gid)
	if err == nil {
		// Healthy homes need only two root stats, not a recursive walk on
		// every restart (their own slots may now own descendant files).
		docs := fsutil.WorkspaceDocsRoot()
		root := filepath.Join(docs, "Crew", record.Folder)
		ready := crewFolderHasSlotAccess(root, gid)
		if roots, found, e := crewroot.LookupForRemoval(docs, "Crew/"+record.Folder); e != nil {
			ready = false
		} else if found && roots.Private != "" {
			ready = ready && crewFolderHasSlotAccess(roots.Private, gid)
		}
		if ready {
			return
		}
		err = ensureSharedCrewFolderAccessForGroup(record.Folder, gid)
	}
	if err != nil {
		log.Printf("[TEAM_HOME] event=slot_access_failed team=%s crew=%s slot=%s reason=folder_access", home.RosterSource, home.ProjectID, slot)
		return
	}
	log.Printf("[TEAM_HOME] event=slot_access_ready team=%s crew=%s slot=%s", home.RosterSource, home.ProjectID, slot)
}
