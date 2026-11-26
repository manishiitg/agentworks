package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// Agents of an online project in a Team (#3664). The project is a Team member under its own identity, project:<id>,
// used when the project itself asks the Team. Its agents are Team agent rows owned by project:<id>/<maker>: the person
// who made the agent there (its main owner, a co-owner, or a Team member working in it as a User; owner, 26 Nov). The
// agent's chat runs as its maker in the project, starts Only me and is shared by its maker. The Team's people reach it
// through the same rows, admission and delivery as a teammate's server agents.

// crewTeamMemberProjectID is the online project of a project identity (project:<id>) or agent owner
// (project:<id>/<maker>).
func crewTeamMemberProjectID(id string) (string, bool) {
	rest, ok := strings.CutPrefix(id, crewTeamProjectMemberPrefix)
	project, _, _ := strings.Cut(rest, "/")
	return project, ok && project != ""
}

// crewTeamProjectMakerID is the owner of the agents a person made in an online project.
func crewTeamProjectMakerID(project, maker string) string {
	return crewTeamProjectMemberID(project) + "/" + maker
}

// crewTeamProjectMaker is who made an online project's agent row, or "" for the project's own identity.
func crewTeamProjectMaker(id string) string {
	rest, ok := strings.CutPrefix(id, crewTeamProjectMemberPrefix)
	if !ok {
		return ""
	}
	_, maker, _ := strings.Cut(rest, "/")
	return maker
}

// crewTeamProjectAgentPerson names an online project's agent row by its project and maker: "Project: SDE · Priya".
func crewTeamProjectAgentPerson(projectName, id string) string {
	if maker := crewTeamProjectMaker(id); maker != "" {
		return projectName + " · " + crewPersonShortName(maker)
	}
	return projectName
}

// crewTeamProjectMakerAllowed: person may make and keep agents of project in t's Team: the project's main owner, a
// co-owner, or one of the Team's people (a User of the project through the Team).
func crewTeamProjectMakerAllowed(t crewTeam, project, person string) bool {
	if person == "" || !t.On || !t.hasProject(project) {
		return false
	}
	if t.member(person) || ownerOfCrewProjectID(project) == person {
		return true
	}
	root := crewProjectRootByID(project)
	return root != "" && containsID(crewCoOwnersOnly(root), person)
}

// memberOrProject: id is the Team's owner, a joined person, or an online project in it while the Team is on.
func (t crewTeam) memberOrProject(id string) bool {
	if project, ok := crewTeamMemberProjectID(id); ok {
		return t.On && t.hasProject(project)
	}
	return t.member(id)
}

// crewTeamMemberCurrent is the one member check for a Team agent row's owner (person or project): a member now, with an
// active account, or a joined online project that still lives on this server with an active main owner (and, for an
// agent row, a maker still allowed there with an active account).
func crewTeamMemberCurrent(t crewTeam, id string) bool {
	if !t.memberOrProject(id) {
		return false
	}
	if project, ok := crewTeamMemberProjectID(id); ok {
		owner := ownerOfCrewProjectID(project)
		if owner == "" || !crewLivesOnServer(project) || !crewTeamAccountActive(owner) {
			return false
		}
		maker := crewTeamProjectMaker(id)
		return maker == "" || crewTeamProjectMakerAllowed(t, project, maker) && crewTeamAccountActive(maker)
	}
	return crewTeamAccountActive(id)
}

// crewTeamRuleActor is who changes one agent's Team settings: the caller for their own agents, or the caller's maker
// identity in an online project (project:<id>/<caller>) for the agents they made there. key is the agent's ID, chat key
// or name; the caller's own agent wins a name clash.
func crewTeamRuleActor(t crewTeam, caller, key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return caller
	}
	byName := ""
	for _, a := range t.Agents {
		if a.OwnerID == caller && strings.EqualFold(strings.TrimSpace(a.Name), key) {
			return caller
		}
		if !crewTeamProjectOwnedBy(t, a.OwnerID, caller) {
			continue
		}
		if a.ID == key || a.ChatKey == key {
			return a.OwnerID
		}
		if strings.EqualFold(strings.TrimSpace(a.Name), key) {
			byName = a.OwnerID
		}
	}
	return firstNonEmptyTrimmed(byName, caller)
}

// crewTeamProjectOwnedBy: id owns agents viewer made in an online project of t.
func crewTeamProjectOwnedBy(t crewTeam, id, viewer string) bool {
	project, ok := crewTeamMemberProjectID(id)
	return ok && viewer != "" && t.hasProject(project) && crewTeamProjectMaker(id) == viewer
}

// crewTeamRowRunsAs is the account a row's chat runs as: its person, its maker in an online project, or (for the
// project's own identity) the project's main owner.
func crewTeamRowRunsAs(owner string) string {
	if project, ok := crewTeamMemberProjectID(owner); ok {
		return firstNonEmptyTrimmed(crewTeamProjectMaker(owner), ownerOfCrewProjectID(project))
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
		if t, ok := readCrewTeamUnchanged(filepath.Join(state, "team-folders", entry.Name(), "team.json"), entry.Name()); ok && t.On && t.hasProject(project) {
			out = append(out, t)
		}
	}
	return out
}

// crewTeamParsed keeps each Team record parsed while its file is unchanged. crewTeamsJoinedBy reads every Team for
// every permission check, so re-parsing all of them was the top CPU path on a server with many Teams (#2989). A Team
// write is an atomic rename, so a join or leave changes size or mtime and is read again at once.
var crewTeamParsed = struct {
	sync.Mutex
	byPath map[string]crewTeamParsedEntry
}{byPath: map[string]crewTeamParsedEntry{}}

type crewTeamParsedEntry struct {
	size  int64
	mtime int64
	team  crewTeam
	ok    bool
}

// readCrewTeamUnchanged is readCrewTeam for read-only scans: one stat, and a parse only when the file changed.
func readCrewTeamUnchanged(file, project string) (crewTeam, bool) {
	info, err := os.Stat(file)
	if err != nil {
		return crewTeam{}, false
	}
	crewTeamParsed.Lock()
	cached, hit := crewTeamParsed.byPath[file]
	crewTeamParsed.Unlock()
	if hit && cached.size == info.Size() && cached.mtime == info.ModTime().UnixNano() {
		return cached.team, cached.ok
	}
	t, err := readCrewTeam(project)
	entry := crewTeamParsedEntry{size: info.Size(), mtime: info.ModTime().UnixNano(), team: t, ok: err == nil}
	crewTeamParsed.Lock()
	crewTeamParsed.byPath[file] = entry
	crewTeamParsed.Unlock()
	return entry.team, entry.ok
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
		if a, ok := t.Agents[id]; ok && crewTeamProjectMaker(a.OwnerID) != "" && crewTeamRowProject(t, a.OwnerID) == project {
			return t, true
		}
	}
	return crewTeam{}, false
}

// crewTeamProjectPeople are the online projects of a Team in the Team's agent list, one entry per person who made agents
// there: the main owner's Main and named agents, and each co-owner's and Team person's named agents (owner, 26 Nov). The
// main owner's entry is always there so the project shows even before anyone makes an agent.
func crewTeamProjectPeople(ctx context.Context, t crewTeam, caller string) []crewTeamAgentsPerson {
	out := []crewTeamAgentsPerson{}
	if !t.On {
		return out
	}
	for _, p := range t.withProjectNames().Projects {
		if !crewTeamMemberCurrent(t, crewTeamProjectMemberID(p.ProjectID)) {
			continue
		}
		main := ownerOfCrewProjectID(p.ProjectID)
		makers := []string{main}
		if root := crewProjectRootByID(p.ProjectID); root != "" {
			makers = append(makers, crewCoOwnersOnly(root)...)
		}
		makers = append(append(makers, t.OwnerID), crewTeamJoinedPeople(t)...)
		seen := map[string]bool{}
		for _, maker := range makers {
			id := crewTeamProjectMakerID(p.ProjectID, maker)
			if maker == "" || seen[maker] || !crewTeamMemberCurrent(t, id) {
				continue
			}
			seen[maker] = true
			agents := crewTeamProjectAgents(ctx, t, p.ProjectID, maker)
			if maker != main && len(agents) == 0 {
				continue
			}
			out = append(out, crewTeamAgentsPerson{UserID: id, Name: "Project: " + firstNonEmptyTrimmed(p.Name, "Project"), Active: true, ActiveLabel: "On the server", Agents: agents})
		}
	}
	return out
}

// crewTeamProjectAgents are the named agents maker has in the online project (their tabs there, as its agent list shows
// them), plus Main for the project's main owner. Unnamed side chats, function agents and hidden tabs are not agents.
func crewTeamProjectAgents(ctx context.Context, t crewTeam, project, maker string) []crewTeamAgent {
	out := []crewTeamAgent{}
	owner := ownerOfCrewProjectID(project)
	if owner == "" || maker == "" {
		return out
	}
	store := defaultProductConversationRegistryStore()
	path := productConversationRegistryPath(maker)
	mu := productConversationRegistryMutex(path)
	mu.Lock()
	doc, err := store.loadDocument(ctx, path)
	mu.Unlock()
	if err != nil {
		return out
	}
	member := crewTeamProjectMakerID(project, maker)
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
			if maker != owner {
				continue // a co-owner's or User's own Main chat is not one of the project's agents
			}
			name, tab = defaultAgentName(identity.Name, identity.GeneratedName), macMainTab
		} else if name == "" || identity.GeneratedName || strings.EqualFold(name, "Main") {
			continue // an unnamed chat is not one of the project's agents
		}
		activity, label := "", ""
		if crewServerChatRunning(record.SessionID) {
			activity = "running"
		}
		since, waiting := pending[record.SessionID]
		if waiting {
			activity, label = "waiting", crewTeamWaitingLabel("its maker", false)
		}
		onlyMe := false // a new agent in an online project starts as Only me; its maker shares it (owner, 26 Nov)
		out = append(out, crewTeamAgent{initialInTeam: &onlyMe, Tab: tab, Name: name, Purpose: identity.Purpose, Examples: identity.Examples, Place: "server", ChatKey: record.ConversationKey, sessionID: record.SessionID, lastWorkedAt: crewServerChatLastWorkedAt(record.SessionID), InTeam: t.agentInTeam(member, record.ConversationKey), Activity: activity, waitingSince: since, StatusLabel: label, PlaceLabel: crewServerAgentPlaceLabel})
	}
	return out
}

// A Team in an online project's Users (owner, 26 Nov, #4087): a Team member's ask to one of the project's agents runs
// as that person, a User of the project: working power in the project, its setup locked for them (the turn's asked_by,
// crew_setup_lock.go), on the agent's own AI login, with the cost recorded against the asker.

// crewTeamProjectAskChanges says whether a Team ask into dest, an online project's agent, works as the asker's User turn
// rather than Run: the agent is a current project row of a Team the asker is a person member of, and the asking turn is
// that person's own (a Run or guest turn stays Run).
func crewTeamProjectAskChanges(dest, source agentMessageEndpoint) bool {
	asker := publishedAsker(source)
	if dest.TeamAgentID == "" || asker == "" || source.Mode == "run" || strings.TrimSpace(source.Guest) != "" {
		return false
	}
	if _, isProject := crewTeamMemberProjectID(asker); isProject {
		return false
	}
	t, found := crewTeamOfAgent(dest.ID, dest.TeamAgentID)
	if !found {
		return false
	}
	a, ok := t.Agents[dest.TeamAgentID]
	project, isProject := crewTeamMemberProjectID(a.OwnerID)
	return ok && isProject && project == dest.ID && crewTeamMemberCurrent(t, a.OwnerID) && t.member(asker) && crewTeamAccountActive(asker)
}

// The online project as a sender (#3664; Team Module Manager rule 2): a
// Builder turn of a joined online project (its owner or a co-owner, or its own schedules, in the project's own chat)
// asks the Team's agents as the project, project:<id>. A Run person's turn, a function's, a watcher's, a product Ask or
// a turn started by another person's message never carries the project's identity.

// crewTeamBuilderProject is the online project a sending endpoint speaks for, or "".
func crewTeamBuilderProject(e agentMessageEndpoint) string {
	project := e.ID
	if e.Hosted != nil {
		project = e.Hosted.Project
	} else if e.Kind != triggerCallerCrew || e.External {
		return ""
	}
	if e.Mode != "builder" || e.Guest != "" || e.FunctionCallID != "" || e.ConditionJob != "" || e.ProductAsk || e.PublishedAgent != "" || project == "" || !crewLivesOnServer(project) {
		return ""
	}
	if root := crewProjectRootByID(project); root == "" || !crewProjectOwnedByCaller(e.UserID, root) {
		return ""
	}
	return project
}

// crewTeamAskerOf is who a sending endpoint is in the Teams: a joined online project's Builder turn is the project; every
// other endpoint its person (publishedAsker).
func crewTeamAskerOf(e agentMessageEndpoint) string {
	if project := crewTeamBuilderProject(e); project != "" && len(crewTeamsJoinedBy(project)) > 0 {
		return crewTeamProjectMemberID(project)
	}
	return publishedAsker(e)
}

// crewTeamAskerKey carries the sending turn's Team identity (crewTeamAskerOf) through one in-process tool call. No
// request or tool argument can set it.
type crewTeamAskerKey struct{}

func withCrewTeamAsker(ctx context.Context, e agentMessageEndpoint) context.Context {
	if asker := crewTeamAskerOf(e); asker != publishedAsker(e) {
		return context.WithValue(ctx, crewTeamAskerKey{}, asker)
	}
	return ctx
}

// crewTeamViewer is the identity Team admission uses for this call: the sending turn's project, else person.
func crewTeamViewer(ctx context.Context, person string) string {
	if asker, _ := ctx.Value(crewTeamAskerKey{}).(string); asker != "" {
		return asker
	}
	return person
}

// crewTeamAskerLabel is how a Team asker is named to the agent it asks.
func crewTeamAskerLabel(asker string) string {
	if project, ok := crewTeamMemberProjectID(asker); ok {
		return "Project: " + firstNonEmptyTrimmed(crewTeamProjectName(project), "Project")
	}
	return shareOwnerLabel(asker)
}

// crewTeamAskerShortName is crewPersonShortName for a Team asker, the project's name for a project.
func crewTeamAskerShortName(asker string) string {
	if _, ok := crewTeamMemberProjectID(asker); ok {
		return crewTeamAskerLabel(asker)
	}
	return crewPersonShortName(asker)
}

// crewTeamAskerActive: a person's account is active, or a project member's main owner is (the project has no account).
func crewTeamAskerActive(asker string) bool {
	return crewTeamAccountActive(crewTeamRowRunsAs(asker))
}
