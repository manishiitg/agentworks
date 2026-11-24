package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// Agents of an online project in a Team (#3664). The project is a Team member under its own identity, project:<id>
// (never its owner's account), and its agents are Team agent rows owned by that identity. The Team's people reach them
// through the same rows, admission and delivery as a teammate's server agents; a teammate's ask runs in the agent's own
// chat like another person's turn, in the project's main owner's namespace (the 15 Oct rule: a server project's agents
// stay its main owner's). No co-owner grant is involved.

// crewTeamMemberProjectID is the online project of a project member identity.
func crewTeamMemberProjectID(id string) (string, bool) {
	project, ok := strings.CutPrefix(id, crewTeamProjectMemberPrefix)
	return project, ok && project != ""
}

// memberOrProject: id is the Team's owner, a joined person, or an online project in it while the Team is on.
func (t crewTeam) memberOrProject(id string) bool {
	if project, ok := crewTeamMemberProjectID(id); ok {
		return t.On && t.hasProject(project)
	}
	return t.member(id)
}

// crewTeamMemberCurrent is the one member check for a Team agent row's owner (person or project): a member now, with an
// active account, or a joined online project that still lives on this server with an active main owner.
func crewTeamMemberCurrent(t crewTeam, id string) bool {
	if !t.memberOrProject(id) {
		return false
	}
	if project, ok := crewTeamMemberProjectID(id); ok {
		owner := ownerOfCrewProjectID(project)
		return owner != "" && crewLivesOnServer(project) && crewTeamAccountActive(owner)
	}
	return crewTeamAccountActive(id)
}

// crewTeamRowRunsAs is the account a row's chat runs as: its person, or the online project's main owner.
func crewTeamRowRunsAs(owner string) string {
	if project, ok := crewTeamMemberProjectID(owner); ok {
		return ownerOfCrewProjectID(project)
	}
	return owner
}

// crewTeamRowProject is the project a row's chat lives in: the Team's own, or the online project's.
func crewTeamRowProject(t crewTeam, owner string) string {
	if project, ok := crewTeamMemberProjectID(owner); ok {
		return project
	}
	return t.ProjectID
}

// crewTeamsJoinedBy are the Teams on this server that have the online project as a member now.
func crewTeamsJoinedBy(project string) []crewTeam {
	if project == "" {
		return nil
	}
	state, err := crewroot.StateRoot()
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(state, "team-folders"))
	if err != nil {
		return nil
	}
	out := []crewTeam{}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == project {
			continue
		}
		if t, err := readCrewTeam(entry.Name()); err == nil && t.On && t.hasProject(project) {
			out = append(out, t)
		}
	}
	return out
}

// crewTeamOfAgent is the Team that registered agent id for a chat in project: the project's own Team, or a Team the
// project joined (then the row must be the project's own).
func crewTeamOfAgent(project, id string) (crewTeam, bool) {
	if t, err := readCrewTeam(project); err == nil {
		if a, ok := t.Agents[id]; ok && crewTeamRowProject(t, a.OwnerID) == project {
			return t, true
		}
	}
	for _, t := range crewTeamsJoinedBy(project) {
		if a, ok := t.Agents[id]; ok && a.OwnerID == crewTeamProjectMemberID(project) {
			return t, true
		}
	}
	return crewTeam{}, false
}

// crewTeamProjectPeople are the online projects of a Team as members in the Team's agent list: their Main and named
// agents (owner, 24 Nov: Main plus named tabs by default), on the server and always available.
func crewTeamProjectPeople(ctx context.Context, t crewTeam, caller string) []crewTeamAgentsPerson {
	out := []crewTeamAgentsPerson{}
	if !t.On {
		return out
	}
	for _, p := range t.withProjectNames().Projects {
		member := crewTeamProjectMemberID(p.ProjectID)
		if !crewTeamMemberCurrent(t, member) {
			continue
		}
		out = append(out, crewTeamAgentsPerson{UserID: member, Name: "Project: " + firstNonEmptyTrimmed(p.Name, "Project"), Active: true, ActiveLabel: "On the server", Agents: crewTeamProjectAgents(ctx, t, p.ProjectID)})
	}
	return out
}

// crewTeamProjectAgents are the online project's Main and its named agents (the main owner's tabs there, as its agent
// list shows them). Unnamed side chats, function agents and hidden tabs are not agents of the project.
func crewTeamProjectAgents(ctx context.Context, t crewTeam, project string) []crewTeamAgent {
	out := []crewTeamAgent{}
	owner := ownerOfCrewProjectID(project)
	if owner == "" {
		return out
	}
	store := defaultProductConversationRegistryStore()
	path := productConversationRegistryPath(owner)
	mu := productConversationRegistryMutex(path)
	mu.Lock()
	doc, err := store.loadDocument(ctx, path)
	mu.Unlock()
	if err != nil {
		return out
	}
	member := crewTeamProjectMemberID(project)
	mainKey := crewOwnerMainChatKey(project)
	pending := map[string]time.Time{}
	for _, request := range virtualtools.GetHumanFeedbackStore().ListPending(time.Now()) {
		if previous, ok := pending[request.SessionID]; !ok || request.CreatedAt.Before(previous) {
			pending[request.SessionID] = request.CreatedAt
		}
	}
	prefix := productConversationRegistryEntryKey(crewProfileID, project) + codeChatSideMarker
	for entryKey, record := range doc.Entries {
		if !strings.HasPrefix(entryKey, prefix) || strings.Contains(strings.TrimPrefix(entryKey, prefix), ":") {
			continue
		}
		if record.SessionID == "" || hiddenFromTabStrip(record.ConversationKey) || crewFunctionTabKey(record.ConversationKey) {
			continue
		}
		_, tab, side := strings.Cut(record.ConversationKey, codeChatSideMarker)
		if !side {
			continue
		}
		identity := doc.Agents[entryKey]
		name := strings.TrimSpace(identity.Name)
		if record.ConversationKey == mainKey {
			name, tab = "Main", macMainTab
		} else if name == "" || identity.GeneratedName || strings.EqualFold(name, "Main") {
			continue // an unnamed chat is not one of the project's agents
		}
		activity, label := "", ""
		if crewServerChatRunning(record.SessionID) {
			activity = "running"
		}
		since, waiting := pending[record.SessionID]
		if waiting {
			activity, label = "waiting", crewTeamWaitingLabel("its project", false)
		}
		out = append(out, crewTeamAgent{Tab: tab, Name: name, Purpose: identity.Purpose, Examples: identity.Examples, Place: "server", ChatKey: record.ConversationKey, sessionID: record.SessionID, lastWorkedAt: crewServerChatLastWorkedAt(record.SessionID), InTeam: t.agentInTeam(member, record.ConversationKey), Activity: activity, waitingSince: since, StatusLabel: label, PlaceLabel: crewServerAgentPlaceLabel})
	}
	return out
}
