package server

import (
	"context"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"log"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// A Team is a shared room (owner, 5 Nov, the decisions log): every member sees the agents each person put in the team,
// grouped by person, with whether that person's Mac is on. Agents stay their maker's; others only watch them and reach
// them from their own agents.

type crewTeamAgent struct {
	ID          string `json:"id"`
	sessionID   string
	lastMessage crewTeamMessagePreview
	Tab         string `json:"tab"`
	Name        string `json:"name"`
	Purpose     string `json:"purpose,omitempty"`
	// Place is where it runs: "mac" (its maker's Mac) or "server" (published).
	Place       string `json:"place"`
	ChatKey     string `json:"chat_key"`
	StatusLabel string `json:"status_label,omitempty"`
	// PlaceLabel is where an agent of the owner's Mac answers now and where its server copy stands (Always on,
	// crewAlwaysOnPlaceLabel); PlaceTone colours it ("ok", "wait", "server" or ""); PlaceServer is set when the
	// server answers for it.
	PlaceLabel  string `json:"place_label,omitempty"`
	PlaceTone   string `json:"place_tone,omitempty"`
	PlaceServer bool   `json:"place_server,omitempty"`
	// InTeam: everyone in the Team sees it (its maker chose In team); false only on the viewer's own Only me agents.
	InTeam bool `json:"in_team"`
	// Activity is "running" while a turn is going in it, "waiting" while it waits for its person (crew_team_activity.go),
	// "" otherwise.
	Activity     string `json:"activity,omitempty"`
	waitingSince time.Time
	// Address is how another agent messages it (send_message target), written here so no screen builds it.
	Address string `json:"address,omitempty"`
}

// crewMacSocketOnline is whether this person's Mac serving the pair is connected, and the app version it said.
func crewMacSocketOnline(owner, project, pair string) (bool, string) {
	sock := crewMacSocketFor(owner, project, pair)
	if sock == nil {
		return false, ""
	}
	return true, sock.version
}

// crewTeamWaitingLabel is the row's line while an agent waits for its person to answer it.
func crewTeamWaitingLabel(name string, you bool) string {
	if you {
		return "Waiting for you"
	}
	return "Waiting for " + firstNonEmptyTrimmed(name, "its owner")
}

// crewTeamAgentAddress is the send_message target of one of the Team's agents in this Crew's online copy; on a Mac
// send_message maps it to the Crew paired with that copy (crew_team_message.go).
func crewTeamAgentAddress(project, chatKey string) string {
	if project == "" || chatKey == "" {
		return ""
	}
	return "workspace:" + crewProfileID + ":" + project + "/agent/" + chatKey
}

type crewTeamAgentsPerson struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
	You    bool   `json:"you,omitempty"`
	Owner  bool   `json:"owner,omitempty"`
	MacOn  bool   `json:"mac_on"`
	// AppVersion is the Mac app version their connected Mac reported ("" when off or from an older app).
	AppVersion string `json:"app_version,omitempty"`
	// Active is the people dot, ActiveLabel its words ("Active now · website", "Mac app connected", "Last seen 2 h ago · Windows app").
	Active      bool            `json:"active"`
	ActiveLabel string          `json:"active_label"`
	Agents      []crewTeamAgent `json:"agents"`
}

// teamAgentsByPerson lists the team's in-team agents, the owner first: the owner's from this Crew's Mac, each member's
// from their own tabs in the server copy (they run on the server; Only me ones are left out for everyone else).
func teamAgentsByPerson(ctx context.Context, t crewTeam, caller string) []crewTeamAgentsPerson {
	return teamAgentsByPersonWithMain(ctx, t, caller, crewOwnerMainChatKey(t.ProjectID))
}

func teamAgentsByPersonWithMain(ctx context.Context, t crewTeam, caller, mainKey string) []crewTeamAgentsPerson {
	labels := crewTeamPersonLabels(t)
	owner := crewTeamAgentsPerson{UserID: t.OwnerID, Name: labels[t.OwnerID], Owner: true, You: t.OwnerID == caller, Agents: []crewTeamAgent{}}
	onMac := crewRelayTwin(t.ProjectID)
	// Website tabs use the Main conversation's physical key, while AgentBridge
	// addresses it by project. Return the actual tab identity to the browser.
	if b, err := crewroot.ReadSync(t.ProjectID); err == nil && b != nil && b.Mode == "relay" && b.Side == "server" {
		owner.MacOn, owner.AppVersion = crewMacSocketOnline(t.OwnerID, t.ProjectID, b.PairID)
	}
	if !onMac {
		// A Crew that lives on the server (owner, 5 Nov): its owner's agents are server tabs, In team unless Only me.
		owner.Agents = memberTeamAgentsWithMain(ctx, t, t.OwnerID, caller, mainKey)
	}
	// The Mac conversations of the owner's agents, by tab, for what is running in them.
	ownerConversations := map[string]string{}
	ownerPreviews := map[string]crewTeamMessagePreview{}
	if onMac {
		for _, tab := range crewMacTabsFor(ctx, t.OwnerID, t.ProjectID, caller) {
			ownerConversations[tab.Tab] = tab.ConversationID
			ownerPreviews[tab.Tab] = tab.crewTeamMessagePreview
		}
	}
	for _, agent := range readMacSharedAgents(t.ProjectID) {
		if !onMac || !t.seesAgent(caller, t.OwnerID, agent.Tab) {
			continue
		}
		place := "server"
		if onMac && !crewPublishedTarget(t.ProjectID, agent.Tab) {
			place = "mac"
		}
		// Named like the left menu: its own name, Main for the Crew's Main, never an email.
		name := firstNonEmptyTrimmed(agent.Name, "Agent")
		if agent.Tab == macMainTab {
			name = "Main" // never the saved chat title "<person> · Builder"
		}
		row := crewTeamAgent{lastMessage: ownerPreviews[agent.Tab], Tab: agent.Tab, Name: name, Purpose: agent.Purpose, Place: place, ChatKey: macSharedAgentChatKey(t.ProjectID, agent.Tab), InTeam: t.agentInTeam(t.OwnerID, agent.Tab)}
		if agent.Tab == macMainTab {
			row.ChatKey = mainKey
		}
		if place == "mac" {
			// Always on: an In team agent answers from the server while the Mac is off; an Only me one waits for it.
			row.PlaceLabel, row.PlaceTone = crewAlwaysOnPlaceLabel(t.ProjectID, t.OwnerID, agent.Tab, owner.MacOn, caller == t.OwnerID, row.InTeam)
			row.PlaceServer = row.PlaceTone == "server"
			if !owner.MacOn && !row.PlaceServer {
				row.StatusLabel = "Mac off"
			}
		}
		if place == "mac" {
			if activity, since := crewMacConversationActivitySince(t.OwnerID, t.ProjectID, ownerConversations[agent.Tab]); owner.MacOn && activity != "" {
				row.Activity, row.waitingSince = activity, since
				if activity == "waiting" {
					row.StatusLabel = crewTeamWaitingLabel(owner.Name, owner.You)
				}
			} else if crewStandbyServedRunning(t.OwnerID, t.ProjectID, agent.Tab) {
				row.Activity = "running"
			}
		}
		owner.Agents = append(owner.Agents, row)
	}
	owner.Active, owner.ActiveLabel = teamActive(owner.UserID, owner.MacOn || (onMac && IsLocalMode()))
	out := []crewTeamAgentsPerson{owner}
	for _, p := range t.People {
		if p.Invited || !crewTeamAccountActive(p.UserID) {
			continue
		}
		// Mac on only from their Mac's own connection: the server copy's sync is not the person being there.
		person := crewTeamAgentsPerson{UserID: p.UserID, Name: labels[p.UserID], You: p.UserID == caller}
		if pair, onMac := t.memberMac(p.UserID); onMac {
			// Their agents here work on their Mac (crew_team_where.go): listed from it, marked when it is off.
			person.MacOn, person.AppVersion = crewMacSocketOnline(p.UserID, t.ProjectID, pair)
			person.Agents = []crewTeamAgent{}
			for _, tab := range crewMacTabsFor(ctx, p.UserID, t.ProjectID, caller) {
				name := firstNonEmptyTrimmed(tab.Name, "Agent")
				if tab.Tab == macMainTab {
					name = "Main"
				}
				row := crewTeamAgent{lastMessage: tab.crewTeamMessagePreview, Tab: tab.Tab, Name: name, Place: "mac", ChatKey: crewMacTabRow(t.ProjectID, tab).ConversationKey, InTeam: !tab.OnlyMe}
				// The same chip as the owner's Mac agents (owner, 6 Nov: Team members are equal).
				row.PlaceLabel, row.PlaceTone = crewAlwaysOnPlaceLabel(t.ProjectID, p.UserID, tab.Tab, person.MacOn, caller == p.UserID, row.InTeam)
				row.PlaceServer = row.PlaceTone == "server"
				if !person.MacOn && !row.PlaceServer {
					row.StatusLabel = "Mac off"
				} else if activity, since := crewMacConversationActivitySince(p.UserID, t.ProjectID, tab.ConversationID); person.MacOn && activity != "" {
					row.Activity, row.waitingSince = activity, since
					if activity == "waiting" {
						row.StatusLabel = crewTeamWaitingLabel(person.Name, person.You)
					}
				}
				person.Agents = append(person.Agents, row)
			}
		} else {
			person.Agents = memberTeamAgentsWithMain(ctx, t, p.UserID, caller, mainKey)
		}
		person.Active, person.ActiveLabel = teamActive(p.UserID, person.MacOn)
		out = append(out, person)
	}
	if err := assignCrewTeamAgentIDsWithMain(t, out, mainKey); err != nil {
		log.Printf("[CREW_TEAM] project=%s event=agent_registry_unavailable reason=%q", t.ProjectID, err)
		return nil
	}
	now := time.Now()
	waits := crewTeamMessageWaitStarts(ctx, t.ProjectID, out)
	for i := range out {
		for j := range out[i].Agents {
			agent := &out[i].Agents[j]
			if since, ok := waits[agent.ChatKey]; ok && agent.InTeam && agent.Activity == "" {
				agent.Activity, agent.waitingSince = "waiting", since
				agent.StatusLabel = "Waiting for a teammate"
			}
			before := agent.Activity
			agent.Activity = crewTeamLiveActivity(before, agent.waitingSince, out[i].UserID == caller, now)
			if before == "waiting" && agent.Activity == "" {
				agent.StatusLabel = ""
			}
		}
		if out[i].UserID != caller {
			out[i].Agents = slices.DeleteFunc(out[i].Agents, func(a crewTeamAgent) bool { return !a.InTeam })
		}
	}
	return out
}

// memberTeamAgents are one person's own tabs in this Crew's server copy that the caller may see.
func memberTeamAgents(ctx context.Context, t crewTeam, member, caller string) []crewTeamAgent {
	return memberTeamAgentsWithMain(ctx, t, member, caller, crewOwnerMainChatKey(t.ProjectID))
}

func memberTeamAgentsWithMain(ctx context.Context, t crewTeam, member, caller, ownerMainKey string) []crewTeamAgent {
	project := t.ProjectID
	out := []crewTeamAgent{}
	// A single registry snapshot supplies both chat bindings and display identities.
	// Main was already validated by the request; no per-chat manifest/action loads.
	store := defaultProductConversationRegistryStore()
	path := productConversationRegistryPath(member)
	mu := productConversationRegistryMutex(path)
	mu.Lock()
	doc, err := store.loadDocument(ctx, path)
	mu.Unlock()
	if err != nil {
		return out
	}
	mainKey := crewOwnerDefaultChatKey(project, member)
	if member == t.OwnerID {
		mainKey = ownerMainKey
	}
	// Read the existing authoritative queue once for these registered chats;
	// never transfer the question text or options into Team activity.
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
		identity := doc.Agents[entryKey]
		if !t.seesAgent(caller, member, record.ConversationKey) || record.SessionID == "" || hiddenFromTabStrip(record.ConversationKey) || crewFunctionTabKey(record.ConversationKey) {
			continue
		}
		_, tab, side := strings.Cut(record.ConversationKey, codeChatSideMarker)
		if !side {
			continue // the Crew's own Main record and Builder chats are not this member's agents
		}
		// Named like the left menu (sideChatsFor, owner 5 Nov): their default chat is their Main, a tab its own name;
		// never a raw chat title or an email.
		name := firstNonEmptyTrimmed(identity.Name, "Agent")
		if record.ConversationKey == mainKey {
			name, tab = "Main", macMainTab
		}
		activity := ""
		if crewServerChatRunning(record.SessionID) {
			activity = "running"
		}
		since, waiting := pending[record.SessionID]
		label := ""
		if waiting {
			activity, label = "waiting", crewTeamWaitingLabel(crewPersonShortName(member), member == caller)
		}
		out = append(out, crewTeamAgent{Tab: tab, Name: name, Purpose: identity.Purpose, Place: "server", ChatKey: record.ConversationKey, sessionID: record.SessionID, InTeam: t.agentInTeam(member, record.ConversationKey), Activity: activity, waitingSince: since, StatusLabel: label, PlaceLabel: crewServerAgentPlaceLabel})
	}
	return out
}

// handleCrewTeamAgents is the team's agents for one of its members (invited people and others get nothing).
func (api *StreamingAPI) handleCrewTeamAgents(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	project := mux.Vars(r)["project"]
	if !crewTeamClaims(claims, project, false) {
		externalError(w, 403, "team_forbidden", "Use your own account to open this team.")
		return
	}
	t, err := readCrewTeam(project)
	if err != nil || !t.member(claims.UserID) || ownerOfCrewProjectID(project) != t.OwnerID || !crewTeamAccountActive(t.OwnerID) {
		externalError(w, 403, "team_forbidden", "This team is not shared with you. Ask its owner to add you.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	externalJSON(w, map[string]any{"project_id": project, "name": t.Name, "people": teamAgentsByPerson(r.Context(), t, claims.UserID)})
}

// crewPersonShortName is how a person is named in a Team: their short name (tester-b), never their email.
func crewPersonShortName(userID string) string {
	return strings.SplitN(strings.TrimSpace(shareOwnerLabel(userID)), "@", 2)[0]
}

// crewServerAgentPlaceLabel is the status of an agent that lives on the server: it answers any time (the same row as a Mac
// agent's "Backup on the server for when the Mac is off · updated …", owner 6 Nov: one row for every own agent).
const crewServerAgentPlaceLabel = "Runs on the server, always available"
