package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// People in a Team watch each other's agents that work on a Mac (owner, 5 Nov, "A Team is a shared room": every
// in-team agent and its chat, live, watch only). Two kinds of Mac agents:
//   - the owner's, on the Mac that holds the Crew: only those In team; Only me is never listed or streamed;
//   - a member's, after "Add to my Mac" (crew_team_where.go): all their agents in this Crew.
// Their tab lists show them, and the relay streams those conversations, read-only, to the others in the Team.

type crewMacTeamTab struct {
	Person         string // whose Mac it is
	Tab            string // "main" or the Mac tab id
	ConversationID string // the Mac conversation, as the relay addresses it
	Name           string
	OnlyMe         bool // a member's agent they keep to themselves: only they see it
}

type crewMacTeamTabsRead struct {
	at   time.Time
	tabs []crewMacTeamTab
}

// crewMacTeamTabsCache keeps the last read per Crew and person, so the others still see the agents (and their history
// from the relay cache) while that Mac is off.
var crewMacTeamTabsCache sync.Map

// crewMacPairFor is the relay pair of this person's Mac for this Crew: the owner's from the Crew's binding, a member's
// from the Team record once their agents work on their Mac.
func crewMacPairFor(person, project string) (string, bool) {
	b, err := crewroot.ReadSync(project)
	twin := err == nil && b != nil && b.Side == "server" && b.Editing == "local" && b.PendingAction == ""
	if person == ownerOfCrewProjectID(project) {
		if !twin {
			return "", false // a Crew on the server has no owner's Mac
		}
		return b.PairID, b.PairID != ""
	}
	if !twin && (err != nil || b != nil) {
		return "", false // a Crew in the middle of a move, or unreadable
	}
	t, err := readCrewTeam(project)
	if err != nil || ownerOfCrewProjectID(project) != t.OwnerID {
		return "", false
	}
	return t.memberMac(person)
}

// callCrewMacPair sends one command to this person's Mac for this Crew.
func callCrewMacPair(ctx context.Context, person, project, pair string, call crewChatCall, timeout time.Duration) crewChatResult {
	sock := crewMacSocketFor(person, project, pair)
	if sock == nil {
		data, _ := json.Marshal(map[string]any{"error": crewMacOffMessage})
		return crewChatResult{Status: 503, Body: data}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, _, err := sock.command(ctx, project, call)
	if err != nil {
		data, _ := json.Marshal(map[string]any{"error": "That Mac did not answer in time."})
		return crewChatResult{Status: 504, Body: data}
	}
	return result
}

// personMacTabs are this person's agents on their Mac that the Team may watch; a member's Only me ones are marked
// (only they see them: crewMacTabsFor).
func personMacTabs(ctx context.Context, person, project string) []crewMacTeamTab {
	pair, ok := crewMacPairFor(person, project)
	if !ok {
		return nil
	}
	owner := person == ownerOfCrewProjectID(project)
	inTeam := map[string]bool{}
	if owner {
		for _, agent := range readMacSharedAgents(project) {
			if crewTeamInTeam(agent) {
				inTeam[agent.Tab] = true
			}
		}
	}
	onlyMe := map[string]bool{}
	if !owner {
		if t, err := readCrewTeam(project); err == nil {
			if at := slices.IndexFunc(t.People, func(p crewTeamPerson) bool { return p.UserID == person }); at >= 0 {
				for _, tab := range t.People[at].OnlyMe {
					onlyMe[tab] = true
				}
			}
		}
	}
	keep := func(tabs []crewMacTeamTab) []crewMacTeamTab {
		if !owner {
			out := slices.Clone(tabs)
			for i := range out {
				out[i].OnlyMe = onlyMe[out[i].Tab]
			}
			return out
		}
		return slices.DeleteFunc(slices.Clone(tabs), func(t crewMacTeamTab) bool { return !inTeam[t.Tab] })
	}
	key := project + "\x00" + person
	cached, _ := crewMacTeamTabsCache.Load(key)
	if v, ok := cached.(crewMacTeamTabsRead); ok && time.Since(v.at) < 15*time.Second {
		return keep(v.tabs)
	}
	if crewMacSocketFor(person, project, pair) == nil {
		if v, ok := cached.(crewMacTeamTabsRead); ok {
			return keep(v.tabs)
		}
		return nil
	}
	tabs := []crewMacTeamTab{}
	result := callCrewMacPair(ctx, person, project, pair, crewChatCall{Operation: "tabs"}, 10*time.Second)
	var rows []AgentProfileSideChat
	if result.Status == http.StatusOK && json.Unmarshal(result.Body, &rows) == nil {
		for _, row := range rows {
			_, id, ok := strings.Cut(row.ConversationKey, codeChatSideMarker)
			if ok && crewMacTabID(id) && row.ConversationID != "" {
				tabs = append(tabs, crewMacTeamTab{Person: person, Tab: id, ConversationID: row.ConversationID, Name: crewMacTabName(row.Name)})
			}
		}
	}
	if !owner || inTeam[macMainTab] {
		main := callCrewMacPair(ctx, person, project, pair, crewChatCall{Operation: "resolve"}, 10*time.Second)
		var out struct {
			ConversationID string `json:"conversation_id"`
		}
		if main.Status == http.StatusOK && json.Unmarshal(main.Body, &out) == nil && out.ConversationID != "" {
			tabs = append(tabs, crewMacTeamTab{Person: person, Tab: macMainTab, ConversationID: out.ConversationID, Name: "Main"})
		}
	}
	crewMacTeamTabsCache.Store(key, crewMacTeamTabsRead{at: time.Now(), tabs: tabs})
	return keep(tabs)
}

// crewMacTabsFor are this person's Mac agents as the viewer may see them: all of their own, never another person's
// Only me ones.
func crewMacTabsFor(ctx context.Context, person, project, viewer string) []crewMacTeamTab {
	return slices.DeleteFunc(personMacTabs(ctx, person, project), func(t crewMacTeamTab) bool { return t.OnlyMe && viewer != person })
}

// crewMacPeople are the people whose Mac holds agents of this Crew: its owner, and members on their Mac.
func crewMacPeople(project string) []string {
	t, err := readCrewTeam(project)
	owner := ownerOfCrewProjectID(project)
	if err != nil || owner == "" || t.OwnerID != owner || !t.On {
		return nil
	}
	people := []string{owner}
	for _, p := range t.People {
		if _, ok := t.memberMac(p.UserID); ok && crewTeamAccountActive(p.UserID) {
			people = append(people, p.UserID)
		}
	}
	return people
}

// crewMacTeamConversation is whose Mac agent this conversation is, and which tab, if the Team may watch it.
func crewMacTeamConversation(ctx context.Context, project, conversation string) (string, string) {
	for _, person := range crewMacPeople(project) {
		for _, t := range crewMacTabsFor(ctx, person, project, "") {
			if t.ConversationID == conversation {
				return person, t.Tab
			}
		}
	}
	return "", ""
}

// crewMacTabRow is a Mac agent as a tab row for someone else in the Team: watch only, with its person.
func crewMacTabRow(project string, tab crewMacTeamTab) AgentProfileSideChat {
	id := tab.Tab
	if tab.Tab == macMainTab {
		id = "mac-main"
	}
	if tab.Person != ownerOfCrewProjectID(project) {
		id = "mac-" + sanitizeUserIDForPath(tab.Person) + "-" + id // one key per person's agent
	}
	return AgentProfileSideChat{ConversationKey: project + codeChatSideMarker + id, ConversationID: tab.ConversationID, SessionID: crewChatSessionID(project, tab.ConversationID),
		ChatLocation: "mac", Name: tab.Name, Pinned: true, TabOwnerID: tab.Person, TabOwnerName: crewOwnerDisplayName(tab.Person)}
}

// crewMacTeamMember is the caller as a joined member (not the owner) of this Mac Crew's Team, with its owner.
func crewMacTeamMember(project, caller string) (string, bool) {
	if IsLocalMode() || !crewRelayTwin(project) {
		return "", false
	}
	t, err := readCrewTeam(project)
	owner := ownerOfCrewProjectID(project)
	if err != nil || owner == "" || t.OwnerID != owner || caller == owner || !t.member(caller) {
		return "", false
	}
	return owner, true
}

// crewTeamMacMemberTabs are the Mac agents of members on their Mac, for someone else in the Team (the owner's list
// adds them through crewTeamMemberTabs).
func crewTeamMacMemberTabs(ctx context.Context, project, viewer string) []AgentProfileSideChat {
	out := []AgentProfileSideChat{}
	owner := ownerOfCrewProjectID(project)
	for _, person := range crewMacPeople(project) {
		if person == owner || person == viewer {
			continue
		}
		for _, tab := range crewMacTabsFor(ctx, person, project, viewer) {
			out = append(out, crewMacTabRow(project, tab))
		}
	}
	return out
}

// routeCrewMemberMacTabs answers a member's tab list of a Mac Crew's online copy: their own tabs (on their Mac, their
// Mac agents, watch only here), the owner's In team Mac agents, and the other members' agents, wherever they work.
func (api *StreamingAPI) routeCrewMemberMacTabs(w http.ResponseWriter, r *http.Request, project string) bool {
	caller := GetUserIDFromContext(r.Context())
	owner, ok := crewMacTeamMember(project, caller)
	if !ok {
		return false
	}
	userID, slot, ok := api.agentProfileConversationSlot(w, r, project)
	if !ok {
		return true
	}
	rows, err := api.sideChatsFor(r, userID, slot, project)
	if err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, err.Error())
		return true
	}
	onMac := crewTeamMemberOnMac(project, caller)
	out := slices.DeleteFunc(rows, func(row AgentProfileSideChat) bool {
		// The owner's agents live on the Mac; a member on their Mac has no server agents here.
		return row.TabOwnerID == owner || onMac && (row.TabOwnerID == caller || row.TabOwnerID == "")
	})
	seen := map[string]bool{}
	for _, row := range out {
		seen[row.ConversationKey] = true
	}
	add := func(row AgentProfileSideChat) {
		if !seen[row.ConversationKey] {
			seen[row.ConversationKey] = true
			out = append(out, row)
		}
	}
	for _, tab := range personMacTabs(r.Context(), owner, project) {
		add(crewMacTabRow(project, tab))
	}
	if onMac {
		for _, tab := range personMacTabs(r.Context(), caller, project) {
			add(crewMacTabRow(project, tab))
		}
	}
	for _, row := range crewTeamMemberTabs(r.Context(), project, caller) {
		add(row)
	}
	writeAgentProfileJSON(w, http.StatusOK, out)
	return true
}

// crewMacTeamWatchContext lets someone in the Team read (only read) the relay stream of another person's Mac agent the
// Team may watch: the read runs as that person, whose Mac holds the chat. Anything else, or any other conversation,
// keeps the caller's own context.
func (api *StreamingAPI) crewMacTeamWatchContext(r *http.Request, project, conversation string) (*http.Request, bool) {
	viewer := GetUserIDFromContext(r.Context())
	if IsLocalMode() {
		return r, false
	}
	t, err := readCrewTeam(project)
	if err != nil || ownerOfCrewProjectID(project) != t.OwnerID || !t.On || !t.member(viewer) {
		return r, false
	}
	person, tab := crewMacTeamConversation(r.Context(), project, conversation)
	if person == "" || person == viewer {
		return r, false
	}
	log.Printf("[CREW_TEAM] project=%s event=member_watch user=%s of=%s tab=%s read=%s", project, logUsernameForUserID(viewer), logUsernameForUserID(person), tab, strings.TrimPrefix(r.URL.Path, "/api/sessions/"))
	return r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: person, Username: person})), true
}

// crewMemberMacTarget reads a member's Mac agent from its row key (crewMacTabRow): whose Mac, and which tab.
func crewMemberMacTarget(project, key string) (string, string, bool) {
	id := strings.TrimPrefix(strings.TrimSpace(key), project+codeChatSideMarker)
	rest, ok := strings.CutPrefix(id, "mac-")
	if !ok {
		return "", "", false
	}
	owner := ownerOfCrewProjectID(project)
	for _, person := range crewMacPeople(project) {
		if person == owner {
			continue
		}
		if tab, ok := strings.CutPrefix(rest, sanitizeUserIDForPath(person)+"-"); ok && tab != "" {
			if tab == "mac-main" {
				tab = macMainTab
			}
			return person, tab, true
		}
	}
	return "", "", false
}

// crewMemberMacAgentFor is a member's Mac agent the caller may message: the caller is in the Team, not that member,
// and the agent is not Only me.
func crewMemberMacAgentFor(ctx context.Context, project, key, caller string) (string, crewMacTeamTab, bool) {
	person, tab, ok := crewMemberMacTarget(project, key)
	if !ok || caller == person {
		return "", crewMacTeamTab{}, false
	}
	if t, err := readCrewTeam(project); err != nil || !t.member(caller) {
		return "", crewMacTeamTab{}, false
	}
	for _, t := range crewMacTabsFor(ctx, person, project, caller) {
		if t.Tab == tab {
			return person, t, true
		}
	}
	return "", crewMacTeamTab{}, false
}

// deliverToMemberMacAgent hands an agent message to a Team member's agent on their Mac, like the owner's Mac agents:
// it runs there in the sender's own thread, the answer comes back to this inbox, and with their Mac off the message
// waits for it.
func (api *StreamingAPI) deliverToMemberMacAgent(ctx context.Context, c agentConversation, m agentMessage, source, dest agentMessageEndpoint) error {
	person, agent, ok := crewMemberMacAgentFor(ctx, dest.ID, dest.ChatKey, source.UserID)
	if !ok {
		log.Printf("[CREW_TEAM] project=%s event=member_mac_message refused user=%s", dest.ID, logUsernameForUserID(source.UserID))
		return fmt.Errorf("This agent isn't shared with you.")
	}
	pair, _ := crewMacPairFor(person, dest.ID)
	sender := shareOwnerLabel(source.UserID)
	run := func(call crewChatCall, timeout time.Duration) crewChatResult {
		return callCrewMacPair(context.Background(), person, dest.ID, pair, call, timeout)
	}
	read := func(after, wait int) (map[string]any, int) {
		raw, _ := json.Marshal(map[string]any{"sender_id": source.UserID, "after": after, "wait_seconds": wait, "submission": m.ID})
		result := run(crewChatCall{Operation: "agent_read", Tab: agent.Tab, Caller: sender, Args: raw}, time.Duration(wait+40)*time.Second)
		var out map[string]any
		_ = json.Unmarshal(result.Body, &out)
		return out, result.Status
	}
	_, senderAgent, _ := agentMessageSender(ctx, source, source.UserID)
	raw, _ := json.Marshal(map[string]any{"sender_id": source.UserID, "sender_agent": senderAgent, "sender_channel": "AgentBridge"})
	ask := crewChatCall{Operation: "agent_ask", Tab: agent.Tab, Message: m.Text, Caller: sender, Args: raw, SubmissionID: m.ID}
	from := 1 - m.From
	_, status := read(0, 0)
	if status == http.StatusOK {
		status = run(ask, 60*time.Second).Status
	}
	log.Printf("[CREW_TEAM] project=%s event=member_mac_message of=%s tab=%s user=%s mac_status=%d", dest.ID, logUsernameForUserID(person), agent.Tab, logUsernameForUserID(source.UserID), status)
	if status == http.StatusServiceUnavailable {
		if err := holdForPersonMac(dest.ID, person, "mac_off", ask, c.ID, from); err != nil {
			return fmt.Errorf("Your message could not be saved. Please try again.")
		}
		go api.appendMacAgentReply(c.ID, from, shareOwnerLabel(person)+"'s Mac is off; your message is delivered when it's back. The answer comes to this inbox.")
		return nil
	}
	if status != http.StatusOK {
		return fmt.Errorf("the agent could not take this message; try again shortly")
	}
	go api.awaitMacAgentAnswer(c.ID, m.ID, from, read)
	return nil
}

// withServerCrewMacTabs adds, on a Crew that lives on the server, the agents of members who work on their Mac: the
// viewer's own (watch only here; their server tabs give way to them) and the others', never their Only me ones.
func withServerCrewMacTabs(ctx context.Context, project, viewer string, rows []AgentProfileSideChat) []AgentProfileSideChat {
	people := crewMacPeople(project)
	if len(people) < 2 { // the owner only: nobody works on their Mac
		return rows
	}
	onMac := people[1:]
	out := slices.DeleteFunc(rows, func(row AgentProfileSideChat) bool {
		// A member on their Mac has no server agents here.
		return slices.Contains(onMac, firstNonEmptyTrimmed(row.TabOwnerID, viewer))
	})
	for _, person := range onMac {
		for _, tab := range crewMacTabsFor(ctx, person, project, viewer) {
			out = append(out, crewMacTabRow(project, tab))
		}
	}
	return out
}
