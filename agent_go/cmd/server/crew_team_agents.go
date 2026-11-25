package server

import (
	"context"
	"log"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// A Team is a shared room (owner, 5 Nov, the decisions log): every member sees the agents each person put in the team,
// grouped by person, with whether that person's Mac is on. Agents stay their maker's; others only watch them and reach
// them from their own agents.

type crewTeamAgent struct {
	initialInTeam *bool
	ID            string `json:"id"`
	sessionID     string
	lastMessage   crewTeamMessagePreview
	Tab           string   `json:"tab"`
	Name          string   `json:"name"`
	Purpose       string   `json:"purpose,omitempty"`
	Examples      []string `json:"examples,omitempty"`
	// Place is where it runs: "mac" (its maker's Mac) or "server" (published).
	Place       string `json:"place"`
	ChatKey     string `json:"chat_key"`
	StatusLabel string `json:"status_label,omitempty"`
	// PlaceLabel is where an agent of the owner's Mac answers now and where its server copy stands (Always on,
	// crewMacPlaceLabel); PlaceTone colours it ("ok", "wait", "server" or ""); PlaceServer is set when the
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
	lastWorkedAt time.Time
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

// One AgentBridge address works from a native CLI or an app chat.
// An online project's row is addressed under the account its chat runs as (its main owner), like its other agents.
func crewTeamAgentBridgeAddress(owner, id string) string {
	owner = crewTeamRowRunsAs(owner)
	if owner == "" || id == "" {
		return ""
	}
	return "agent:" + shareOwnerLabel(owner) + "/" + id
}

// crewTeamAgentAddress is the address of agent id, by its Team's project or by the online project its chat lives in.
func crewTeamAgentAddress(project, id string) string {
	if t, err := readCrewTeam(project); err == nil {
		if a, ok := t.Agents[id]; ok {
			return crewTeamAgentBridgeAddress(a.OwnerID, id)
		}
	}
	if t, ok := crewTeamOfAgent(project, id); ok {
		return crewTeamAgentBridgeAddress(t.Agents[id].OwnerID, id)
	}
	return ""
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

// defaultAgentName is a person's default agent (Main) as the Team names it, like any agent (owner, 25 Nov, #4046): the
// name its person gave it, else "Main"; never an import label or the automatic chat title "<person> · Builder".
func defaultAgentName(name string, generated bool) string {
	name = strings.TrimSpace(name)
	if name == "" || generated || name == "Builder" || strings.HasSuffix(name, " · Builder") {
		return "Main"
	}
	return name
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
		place := "mac"
		// Named like the left menu: its own name, never an email or a saved chat title.
		name := firstNonEmptyTrimmed(agent.Name, "Agent")
		if agent.Tab == macMainTab {
			name = defaultAgentName(agent.Name, false)
		}
		row := crewTeamAgent{lastMessage: ownerPreviews[agent.Tab], Tab: agent.Tab, Name: name, Purpose: agent.Purpose, Examples: agent.Examples, Place: place, ChatKey: macSharedAgentChatKey(t.ProjectID, agent.Tab), InTeam: t.agentInTeam(t.OwnerID, agent.Tab)}
		if agent.Tab == macMainTab {
			row.ChatKey = mainKey
		}
		if place == "mac" {
			// A Mac agent is available only while its Mac is connected.
			row.PlaceLabel, row.PlaceTone = crewMacPlaceLabel(owner.MacOn, caller == t.OwnerID)
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
			}
		}
		row.lastWorkedAt = crewMacConversationLastWorkedAt(t.OwnerID, t.ProjectID, ownerConversations[agent.Tab])
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
					name = defaultAgentName(tab.Name, false)
				}
				row := crewTeamAgent{initialInTeam: tab.InitialInTeam, lastMessage: tab.crewTeamMessagePreview, Tab: tab.Tab, Name: name, Place: "mac", ChatKey: crewMacTabRow(t.ProjectID, tab).ConversationKey, InTeam: !tab.OnlyMe}
				// The same chip as the owner's Mac agents (owner, 6 Nov: Team members are equal).
				row.PlaceLabel, row.PlaceTone = crewMacPlaceLabel(person.MacOn, caller == p.UserID)
				row.PlaceServer = row.PlaceTone == "server"
				if !person.MacOn && !row.PlaceServer {
					row.StatusLabel = "Mac off"
				} else if activity, since := crewMacConversationActivitySince(p.UserID, t.ProjectID, tab.ConversationID); person.MacOn && activity != "" {
					row.Activity, row.waitingSince = activity, since
					if activity == "waiting" {
						row.StatusLabel = crewTeamWaitingLabel(person.Name, person.You)
					}
				}
				row.lastWorkedAt = crewMacConversationLastWorkedAt(p.UserID, t.ProjectID, tab.ConversationID)
				person.Agents = append(person.Agents, row)
			}
		} else {
			person.Agents = memberTeamAgentsWithMain(ctx, t, p.UserID, caller, mainKey)
		}
		person.Active, person.ActiveLabel = teamActive(p.UserID, person.MacOn)
		out = append(out, person)
	}
	out = append(out, crewTeamProjectPeople(ctx, t, caller)...) // online projects in the Team (#3664)
	if err := assignCrewTeamAgentIDsWithMain(t, out, mainKey); err != nil {
		log.Printf("[CREW_TEAM] project=%s event=agent_registry_unavailable reason=%q", t.ProjectID, err)
		return nil
	}
	terminalStates := map[string]struct {
		named namedMessageAgent
		err   error
	}{}
	// The registry owns identity and admission. A cold/offline Mac discovery
	// read may omit runtime metadata, but must not remove a registered teammate.
	for i := range out {
		seen := map[string]bool{}
		for _, a := range out[i].Agents {
			seen[a.ID] = true
		}
		missing := []crewTeamRegisteredAgent{}
		for _, a := range t.Agents {
			if a.OwnerID == out[i].UserID && !seen[a.ID] && (a.InTeam || a.OwnerID == caller) && !crewFunctionTabKey(a.ChatKey) {
				missing = append(missing, a)
			}
		}
		slices.SortFunc(missing, func(a, b crewTeamRegisteredAgent) int { return strings.Compare(a.ID, b.ID) })
		for _, a := range missing {
			row := crewTeamAgent{ID: a.ID, Tab: a.Tab, ChatKey: a.ChatKey, Name: a.Name, Purpose: a.Purpose, Examples: a.Examples, Place: a.Place, InTeam: a.InTeam, Address: crewTeamAgentAddress(t.ProjectID, a.ID)}
			if a.Kind == "terminal" {
				named, bindingErr := currentCrewTeamTerminal(ctx, t, a)
				if bindingErr != nil && caller != a.OwnerID {
					continue
				}
				row.Address = ""
				if bindingErr == nil {
					row.Address = "agent:" + shareOwnerLabel(a.OwnerID) + "/" + named.Name
					row.Examples = named.Examples
				}
				terminalStates[a.ID] = struct {
					named namedMessageAgent
					err   error
				}{named, bindingErr}
				row.StatusLabel, row.Activity = crewTeamTerminalState(named, bindingErr, false, out[i].Name, out[i].UserID == caller)
				row.PlaceLabel = "Mac · offline"
				if row.StatusLabel != "Offline" {
					row.PlaceLabel, row.PlaceTone = "Mac · online", "ok"
				}
			}
			out[i].Agents = append(out[i].Agents, row)
		}
	}
	now := time.Now()
	waits := crewTeamMessageWaitStarts(ctx, t.ProjectID, out)
	ownerOf := map[string]string{} // chat key -> its person's name, to say who a wait is on
	for _, person := range out {
		for _, agent := range person.Agents {
			ownerOf[agent.ChatKey] = person.Name
		}
	}
	for i := range out {
		for j := range out[i].Agents {
			agent := &out[i].Agents[j]
			if terminal, ok := terminalStates[agent.ID]; ok {
				if terminal.err == nil {
					agent.lastWorkedAt = terminal.named.Attachment.ActivityAt
				}
				wait, waiting := waits[agent.ChatKey]
				agent.StatusLabel, agent.Activity = crewTeamTerminalState(terminal.named, terminal.err, waiting && agent.InTeam && crewTeamLiveActivity("waiting", wait.Since, out[i].UserID == caller, now) == "waiting", out[i].Name, out[i].UserID == caller)
				if agent.Activity == "waiting" {
					agent.waitingSince = wait.Since
				}
				crewTeamPresenceNote(t.ProjectID, out[i].UserID, out[i].UserID == caller, *agent)
				continue
			}
			if wait, ok := waits[agent.ChatKey]; ok && agent.InTeam && agent.Activity == "" {
				agent.Activity, agent.waitingSince = "waiting", wait.Since
				agent.StatusLabel = "Waiting for a teammate's reply"
				if name := ownerOf[wait.On]; name != "" && name != out[i].Name {
					agent.StatusLabel = "Waiting for " + name + "'s reply"
				}
			}
			before := agent.Activity
			agent.Activity = crewTeamLiveActivity(before, agent.waitingSince, out[i].UserID == caller, now)
			if before == "waiting" && agent.Activity == "" {
				agent.StatusLabel = ""
			}
			crewTeamPresenceNote(t.ProjectID, out[i].UserID, out[i].UserID == caller, *agent)
		}
		if out[i].UserID != caller {
			person := out[i].UserID
			out[i].Agents = slices.DeleteFunc(out[i].Agents, func(a crewTeamAgent) bool {
				return !a.InTeam || !t.agentRule(person, crewTeamRuleKey(ctx, t, person, a.ChatKey)).chosen(caller)
			})
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
		// Named like the left menu (sideChatsFor): every agent by its own name, their default one "Main" until renamed;
		// never a raw chat title or an email.
		name := firstNonEmptyTrimmed(identity.Name, "Agent")
		if record.ConversationKey == mainKey {
			name, tab = defaultAgentName(identity.Name, identity.GeneratedName), macMainTab
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
		out = append(out, crewTeamAgent{Tab: tab, Name: name, Purpose: identity.Purpose, Examples: identity.Examples, Place: "server", ChatKey: record.ConversationKey, sessionID: record.SessionID, lastWorkedAt: crewServerChatLastWorkedAt(record.SessionID), InTeam: t.agentInTeam(member, record.ConversationKey), Activity: activity, waitingSince: since, StatusLabel: label, PlaceLabel: crewServerAgentPlaceLabel})
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
// agent, with no automatic server stand-in for a Mac agent.
const crewServerAgentPlaceLabel = "Runs on the server, always available"

// crewMacPlaceLabel never claims a server copy is answering for a Mac agent.
func crewMacPlaceLabel(on, yours bool) (string, string) {
	if on {
		if yours {
			return "On your Mac", "ok"
		}
		return "On their Mac", "ok"
	}
	return "Mac off", ""
}

// crewTeamPresenceLast is each Team agent's last projected activity, so [TEAM_PRESENCE] logs transitions only (the
// projection runs on every Team read). Bounded by the number of Team agents on the server.
var crewTeamPresenceLast sync.Map

// crewTeamPresenceNote logs when a Team agent's projected working state changes, and from which source, so a report
// like "No teammates working now" can be traced against the Mac's [CREW_RELAY] turn lines. IDs only, no text.
func crewTeamPresenceNote(project, person string, mine bool, a crewTeamAgent) {
	source := "server_turn"
	switch {
	case a.StatusLabel == "Mac off" || a.StatusLabel == "Offline":
		source = "mac_off"
	case strings.HasPrefix(a.PlaceLabel, "Mac · "): // a joined terminal's state (crewTeamTerminalState)
		source = "terminal_hook"
	case a.Place == "mac":
		source = "mac_relay"
	}
	if a.Activity == "waiting" && strings.HasPrefix(a.StatusLabel, "Waiting for") {
		source = "team_message"
	}
	// The maker keeps seeing their own old wait (crewTeamLiveActivity), so their view is tracked apart.
	key := project + "\x00" + firstNonEmptyTrimmed(a.ID, a.ChatKey) + "\x00" + strconv.FormatBool(mine)
	state := firstNonEmptyTrimmed(a.Activity, "idle") + "/" + source
	if last, ok := crewTeamPresenceLast.Swap(key, state); ok && last == state {
		return
	}
	log.Printf("[TEAM_PRESENCE] project=%s person=%s agent=%s activity=%s source=%s maker_view=%t", project, logUsernameForUserID(person), firstNonEmptyTrimmed(a.ID, a.ChatKey), firstNonEmptyTrimmed(a.Activity, "idle"), source, mine)
}
