package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// In team / Only me (owner, 5 Nov, "A Team is a shared room"): every agent of every person in a Team Crew is In team
// unless its own person chose Only me, or turned their own "All my agents in team" off and never chose for it. Each
// person's Main is always In team. The choice is kept in one
// place, the server's Team record (crewTeam.AgentChoices), for every Crew kind: a Crew on the server, the owner's Mac
// Crew and a member's Mac copy. Every reader asks the same function: the Team tab and agents list, watching,
// notifications, messages and the tab strips.
//
// An agent's key in AgentChoices: an agent on a Mac by its tab ("main" or the Mac tab id), an agent on the server by
// its chat key.

// crewTeamAllMine is the AgentChoices key of a person's own "All my agents in team" (owner, 5 Nov): it applies only to
// their own agents they never chose for. Unset is In team.
const crewTeamAllMine = "*"

// crewTeamMainAgent: the key is the person's Main, which is always In team so each person shares at least one agent
// (owner, 5 Nov): a Mac Main tab, their default chat, the owner's Crew Main, or a root tab (named Main in the list).
func crewTeamMainAgent(t crewTeam, person, key string) bool {
	if key == macMainTab || key == crewOwnerDefaultChatKey(t.ProjectID, person) || person == t.OwnerID && key == t.ProjectID {
		return true
	}
	tab, ok := strings.CutPrefix(key, t.ProjectID+codeChatSideMarker)
	return ok && strings.HasPrefix(tab, "root-")
}

// agentInTeam: a Main is always In team; otherwise the person's own choice for the agent, else their own All my
// agents in team.
func (t crewTeam) agentInTeam(person, key string) bool {
	if crewTeamMainAgent(t, person, key) {
		return true
	}
	if in, ok := t.AgentChoices[person][key]; ok {
		return in
	}
	if all, ok := t.AgentChoices[person][crewTeamAllMine]; ok {
		return all
	}
	return true
}

// allMine is the person's own All my agents in team.
func (t crewTeam) allMine(person string) bool {
	all, ok := t.AgentChoices[person][crewTeamAllMine]
	return !ok || all
}

// errCrewTeamMainShared: a Main cannot be Only me.
var errCrewTeamMainShared = errors.New("Main is always shared with the team.")

// migrateCrewTeamRecord moves a record saved before per-person defaults (owner, 5 Nov): the Team-wide All agents in
// team off becomes the owner's own, and an Only me on a Main is dropped (Main is always In team). Delete it with
// AgentsOnlyMe once every server has saved its Teams again.
func migrateCrewTeamRecord(t *crewTeam) {
	if t.AgentsOnlyMe {
		if t.AgentChoices == nil {
			t.AgentChoices = map[string]map[string]bool{}
		}
		if t.AgentChoices[t.OwnerID] == nil {
			t.AgentChoices[t.OwnerID] = map[string]bool{}
		}
		if _, set := t.AgentChoices[t.OwnerID][crewTeamAllMine]; !set {
			t.AgentChoices[t.OwnerID][crewTeamAllMine] = false
		}
		t.AgentsOnlyMe = false
	}
	for person, choices := range t.AgentChoices {
		for key := range choices {
			if key != crewTeamAllMine && crewTeamMainAgent(*t, person, key) {
				delete(choices, key)
			}
		}
	}
}

// seesAgent: the viewer may see, watch and hear about this person's agent. Their own always; someone else's only
// while both are in the Team (Team on) and the agent is In team.
func (t crewTeam) seesAgent(viewer, person, key string) bool {
	if viewer == "" || person == "" {
		return false
	}
	return viewer == person || t.member(viewer) && t.member(person) && t.agentInTeam(person, key)
}

// crewAgentInTeam is the same answer for a server agent by its chat key, from the Crew's Team record. A Crew with no
// Team record shares nothing by choice, so nothing of anyone's is hidden by it.
func crewAgentInTeam(project, person, chatKey string) bool {
	t, err := readCrewTeam(project)
	if err != nil {
		return true
	}
	return t.agentInTeam(person, strings.TrimSpace(chatKey))
}

// crewTeamAgentOnMac: this person's agents in the Crew work on a Mac (the owner's Mac Crew, or a member who chose
// "Add to my Mac").
func crewTeamAgentOnMac(t crewTeam, person string) bool {
	if person == t.OwnerID {
		return crewRelayTwin(t.ProjectID)
	}
	_, onMac := t.memberMac(person)
	return onMac
}

// crewTeamOwnerMainKey is the chat key the website uses for the Crew owner's Main.
func crewTeamOwnerMainKey(ctx context.Context, t crewTeam) string {
	if records, err := defaultProductConversationRegistryStore().projectChats(ctx, t.OwnerID, crewProfileID, t.ProjectID); err == nil {
		for _, record := range records {
			if record.IsMainChat || record.ConversationKey == crewOwnerDefaultChatKey(t.ProjectID, t.OwnerID) {
				return record.ConversationKey
			}
		}
	}
	return t.ProjectID
}

// crewTeamAgentKey is how AgentChoices names one of person's agents from any chat key a screen or tool sends: the
// Mac tab for an agent on a Mac (crewMacTabRow, macSharedAgentChatKey or the owner's Main), else the chat key. "" when
// it names none of theirs.
func crewTeamAgentKey(ctx context.Context, t crewTeam, person, key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	if !crewTeamAgentOnMac(t, person) {
		return key
	}
	if key == t.ProjectID || person == t.OwnerID && key == crewTeamOwnerMainKey(ctx, t) {
		return macMainTab
	}
	rest, ok := strings.CutPrefix(key, t.ProjectID+codeChatSideMarker)
	if !ok {
		return ""
	}
	if person != t.OwnerID {
		rest = strings.TrimPrefix(rest, "mac-"+sanitizeUserIDForPath(person)+"-") // the Team tab's row key for it
	}
	if rest == "mac-main" || rest == macMainTab {
		return macMainTab
	}
	if crewMacTabID(rest) {
		return rest
	}
	return ""
}

// errCrewTeamAgentNotYours: the agent is not one of the caller's own in this Team (or they are not in it).
var errCrewTeamAgentNotYours = errors.New("choose one of your own agents in this Team")

// crewTeamOwnAgent reads the Team (the caller holds its lock) and names one of person's own agents in it by its
// AgentChoices key: only the agent's own person changes its settings; a server agent must be one of their chats here.
func crewTeamOwnAgent(ctx context.Context, project, person, key string) (crewTeam, string, error) {
	t, err := readCrewTeam(project)
	if err != nil || !t.member(person) || ownerOfCrewProjectID(project) != t.OwnerID {
		return t, "", errCrewTeamAgentNotYours
	}
	stored := crewTeamAgentKey(ctx, t, person, key)
	if stored == "" || stored == crewTeamAllMine {
		return t, "", errCrewTeamAgentNotYours
	}
	if !crewTeamAgentOnMac(t, person) {
		records, err := defaultProductConversationRegistryStore().projectChats(ctx, person, crewProfileID, project)
		if err != nil {
			return t, "", fmt.Errorf("your agents could not be read; try again")
		}
		if !slices.ContainsFunc(records, func(record ProductConversationRecord) bool { return record.ConversationKey == stored }) {
			return t, "", errCrewTeamAgentNotYours
		}
	}
	return t, stored, nil
}

// setCrewTeamAgentChoice saves the person's own In team / Only me for one of their agents, or with key "*" their own All
// my agents in team. Only the agent's own person chooses (owner, 5 Nov: Only me is per person); a server agent must be
// one of their chats in this Crew; a Main stays In team.
func setCrewTeamAgentChoice(ctx context.Context, project, person, key string, inTeam bool) error {
	dir, err := crewTeamDir(project)
	if err != nil {
		return err
	}
	unlock, err := wf.LockWorkspace(ctx, dir)
	if err != nil {
		return err
	}
	defer unlock()
	t, err := readCrewTeam(project)
	if err != nil || !t.member(person) || ownerOfCrewProjectID(project) != t.OwnerID {
		return errCrewTeamAgentNotYours
	}
	stored := crewTeamAllMine
	if key != crewTeamAllMine {
		stored = crewTeamAgentKey(ctx, t, person, key)
	}
	if stored == "" {
		return errCrewTeamAgentNotYours
	}
	if crewTeamMainAgent(t, person, stored) {
		if !inTeam {
			return errCrewTeamMainShared
		}
		return nil
	}
	if stored != crewTeamAllMine && !crewTeamAgentOnMac(t, person) {
		records, err := defaultProductConversationRegistryStore().projectChats(ctx, person, crewProfileID, project)
		if err != nil {
			return fmt.Errorf("your agents could not be read; try again")
		}
		if !slices.ContainsFunc(records, func(record ProductConversationRecord) bool { return record.ConversationKey == stored }) {
			return errCrewTeamAgentNotYours
		}
	}
	if t.AgentChoices == nil {
		t.AgentChoices = map[string]map[string]bool{}
	}
	if t.AgentChoices[person] == nil {
		t.AgentChoices[person] = map[string]bool{}
	}
	t.AgentChoices[person][stored] = inTeam
	t.Revision++
	if err = writeCrewTeam(t); err != nil {
		return err
	}
	crewMacTeamTabsCache.Delete(project + "\x00" + person)
	log.Printf("[CREW_TEAM] project=%s event=agent_visibility user=%s agent=%s in_team=%t revision=%d", project, logUsernameForUserID(person), stored, inTeam, t.Revision)
	return nil
}

// setOwnAgentInTeam is an agent's own In team / Only me from its chat (set_my_agent_identity in_team). On a Mac the
// Team record is on the company server, reached through this Crew's Mac online connection; the agent is its Mac tab.
func (api *StreamingAPI) setOwnAgentInTeam(ctx context.Context, user, chatKey string, inTeam bool) error {
	project := crewResourceProjectID(chatKey)
	if !IsLocalMode() {
		return setCrewTeamAgentChoice(ctx, project, user, chatKey, inTeam)
	}
	b, err := crewroot.ReadSync(project)
	if err != nil || b == nil || b.Side != "local" {
		return fmt.Errorf("turn on this Crew's Team first; it works through Mac online")
	}
	c, err := desktopConnectionFor(user, b.ConnectionID)
	if err != nil {
		return err
	}
	tab := macSharedAgentTab(project, chatKey)
	if tab == "" {
		return fmt.Errorf("choose one of this Crew's agents")
	}
	body := map[string]any{"agent_key": macSharedAgentChatKey(b.PeerProjectID, tab), "in_team": inTeam}
	return crewTeamRemoteJSON(ctx, user, c, http.MethodPost, "/api/crew-teams/"+url.PathEscape(b.PeerProjectID)+"/agent-visibility", body, nil)
}

// migrateCrewTeamAgentChoices moves, once per choice, the Only me choices kept before the Team record held them: a
// server agent's Only me in its person's chat registry, a member's Mac agents (People[].only_me), and the owner's Mac
// agents shared only on AgentBridge (no people audience). Each changed team.json is backed up first. Runs at server
// start; delete it, and ProductAgentIdentity.OnlyMe, once every server has started on it.
func migrateCrewTeamAgentChoices() {
	if IsLocalMode() {
		return
	}
	state, err := crewroot.StateRoot()
	if err != nil {
		return
	}
	files, _ := filepath.Glob(filepath.Join(state, "team-folders", "*", "team.json"))
	ctx := context.Background()
	store := defaultProductConversationRegistryStore()
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		var old struct {
			People []struct {
				UserID string   `json:"user_id"`
				OnlyMe []string `json:"only_me"`
			} `json:"people"`
		}
		t, err := readCrewTeam(filepath.Base(filepath.Dir(file)))
		if err != nil || json.Unmarshal(raw, &old) != nil {
			continue
		}
		changed := 0
		onlyMe := func(person, key string) {
			if t.AgentChoices == nil {
				t.AgentChoices = map[string]map[string]bool{}
			}
			if t.AgentChoices[person] == nil {
				t.AgentChoices[person] = map[string]bool{}
			}
			if _, chosen := t.AgentChoices[person][key]; !chosen {
				t.AgentChoices[person][key] = false
				changed++
			}
		}
		hadOld := false
		for _, p := range old.People {
			hadOld = hadOld || len(p.OnlyMe) > 0
			for _, tab := range p.OnlyMe {
				onlyMe(p.UserID, tab)
			}
		}
		people := []string{t.OwnerID}
		for _, p := range t.People {
			people = append(people, p.UserID)
		}
		for _, person := range people {
			identities, err := store.agentIdentities(ctx, person, crewProfileID, t.ProjectID)
			if err != nil {
				continue
			}
			for key, identity := range identities {
				if identity.OnlyMe {
					onlyMe(person, key)
				}
			}
		}
		if crewRelayTwin(t.ProjectID) {
			for _, agent := range readMacSharedAgents(t.ProjectID) {
				if agent.Audience == "" {
					onlyMe(t.OwnerID, agent.Tab)
				}
			}
		}
		if changed == 0 && !hadOld {
			continue
		}
		if os.WriteFile(file+"."+time.Now().UTC().Format("20060102T150405Z")+".bak", raw, 0o600) != nil {
			continue
		}
		if err := writeCrewTeam(t); err != nil {
			log.Printf("[CREW_TEAM] project=%s event=migrate_agent_choices result=failed reason=%q", t.ProjectID, err)
			continue
		}
		log.Printf("[CREW_TEAM] project=%s event=migrate_agent_choices only_me=%d", t.ProjectID, changed)
	}
}
