package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
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
	Purpose        string // its one line, from the Mac's tab
	OnlyMe         bool   // a member's agent they keep to themselves: only they see it
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
	owner := person == ownerOfCrewProjectID(project)
	// In team / Only me is the Team record's (crew_team_in_team.go); the owner's Mac lists only its In team agents here.
	team, _ := readCrewTeam(project)
	inTeam := map[string]bool{}
	if owner {
		for _, agent := range readMacSharedAgents(project) {
			if crewTeamInTeam(agent) && team.agentInTeam(person, agent.Tab) {
				inTeam[agent.Tab] = true
			}
		}
	}
	keep := func(tabs []crewMacTeamTab) []crewMacTeamTab {
		if !owner {
			out := slices.Clone(tabs)
			for i := range out {
				out[i].OnlyMe = !team.agentInTeam(person, out[i].Tab)
			}
			return out
		}
		return slices.DeleteFunc(slices.Clone(tabs), func(t crewMacTeamTab) bool { return !inTeam[t.Tab] })
	}
	tabs, ok := readPersonMacTabs(ctx, person, project, !owner || inTeam[macMainTab])
	if !ok {
		return nil
	}
	return keep(tabs)
}

// ownerMacTabsAll is every agent on the Crew owner's Mac, Main first, whatever the Team chose: with the Team off the
// owner still chooses each one's Public on AgentBridge from the same list.
func ownerMacTabsAll(ctx context.Context, project string) []crewMacTeamTab {
	tabs, _ := readPersonMacTabs(ctx, ownerOfCrewProjectID(project), project, true)
	if !slices.ContainsFunc(tabs, func(t crewMacTeamTab) bool { return t.Tab == macMainTab }) {
		tabs = append([]crewMacTeamTab{{Tab: macMainTab, Name: "Main"}}, tabs...)
	}
	return tabs
}

// readPersonMacTabs is the last agent list read from this person's Mac (at most 15 s old, else read again while the
// Mac is on, else the last one kept). withMain also reads its Main.
func readPersonMacTabs(ctx context.Context, person, project string, withMain bool) ([]crewMacTeamTab, bool) {
	pair, ok := crewMacPairFor(person, project)
	if !ok {
		return nil, false
	}
	key := project + "\x00" + person
	cached, _ := crewMacTeamTabsCache.Load(key)
	if v, ok := cached.(crewMacTeamTabsRead); ok && time.Since(v.at) < 15*time.Second {
		return v.tabs, true
	}
	if crewMacSocketFor(person, project, pair) == nil {
		if v, ok := cached.(crewMacTeamTabsRead); ok {
			return v.tabs, true
		}
		// After a restart nothing is in memory: the last list read from that Mac, kept on disk, so a message to its
		// agents still waits for the Mac instead of being refused as not shared.
		if tabs, ok := readSavedCrewMacTabs(project, person); ok {
			crewMacTeamTabsCache.Store(key, crewMacTeamTabsRead{tabs: tabs}) // zero time: read the Mac again once it is back
			return tabs, true
		}
		return nil, false
	}
	tabs := []crewMacTeamTab{}
	result := callCrewMacPair(ctx, person, project, pair, crewChatCall{Operation: "tabs"}, 10*time.Second)
	defer func() {
		if result.Status == http.StatusOK {
			saveCrewMacTabs(project, person, tabs)
		}
	}()
	var rows []AgentProfileSideChat
	if result.Status == http.StatusOK && json.Unmarshal(result.Body, &rows) == nil {
		for _, row := range rows {
			_, id, ok := strings.Cut(row.ConversationKey, codeChatSideMarker)
			if ok && crewMacTabID(id) && row.ConversationID != "" {
				tabs = append(tabs, crewMacTeamTab{Person: person, Tab: id, ConversationID: row.ConversationID, Name: crewMacTabName(row.Name), Purpose: strings.TrimSpace(row.Purpose)})
			}
		}
	}
	if withMain {
		main := callCrewMacPair(ctx, person, project, pair, crewChatCall{Operation: "resolve"}, 10*time.Second)
		var out struct {
			ConversationID string `json:"conversation_id"`
		}
		if main.Status == http.StatusOK && json.Unmarshal(main.Body, &out) == nil && out.ConversationID != "" {
			tabs = append(tabs, crewMacTeamTab{Person: person, Tab: macMainTab, ConversationID: out.ConversationID, Name: "Main"})
		}
	}
	crewMacTeamTabsCache.Store(key, crewMacTeamTabsRead{at: time.Now(), tabs: tabs})
	return tabs, true
}

var crewMacTabsSaveMu sync.Mutex

// crewMacTabsPath keeps, per Crew, the last agent list read from each person's Mac (survives a server restart).
func crewMacTabsPath(project string) (string, error) {
	waiting, err := crewMacWaitingPath(project)
	return strings.TrimSuffix(waiting, "-mac-waiting.json") + "-mac-tabs.json", err
}

func readSavedCrewMacTabs(project, person string) ([]crewMacTeamTab, bool) {
	p, err := crewMacTabsPath(project)
	if err != nil {
		return nil, false
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, false
	}
	saved := map[string][]crewMacTeamTab{}
	if json.Unmarshal(raw, &saved) != nil {
		return nil, false
	}
	tabs, ok := saved[person]
	return tabs, ok
}

func saveCrewMacTabs(project, person string, tabs []crewMacTeamTab) {
	p, err := crewMacTabsPath(project)
	if err != nil {
		return
	}
	crewMacTabsSaveMu.Lock()
	defer crewMacTabsSaveMu.Unlock()
	saved := map[string][]crewMacTeamTab{}
	if raw, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(raw, &saved)
	}
	if old, ok := saved[person]; ok && slices.Equal(old, tabs) {
		return
	}
	saved[person] = tabs
	if err = fsutil.WriteJSONAtomic(p, saved, 0o600); err != nil {
		log.Printf("[CREW_TEAM] project=%s event=mac_tabs_not_saved reason=%q", project, err)
	}
}

// crewMacTabsFor are this person's Mac agents as the viewer may see them: all of their own, never another person's
// Only me ones.
func crewMacTabsFor(ctx context.Context, person, project, viewer string) []crewMacTeamTab {
	return slices.DeleteFunc(personMacTabs(ctx, person, project), func(t crewMacTeamTab) bool { return t.OnlyMe && viewer != person })
}

// crewMacPeople are the people whose Mac holds agents of this Crew: its owner, and members on their Mac.
func crewMacPeople(project string) []string {
	people, _ := crewMacPeopleChecked(project)
	return people
}

// crewMacPeopleChecked is crewMacPeople with an error when the Team record or the people directory cannot be read
// now: a caller that would drop held messages for someone not listed must not act on a list that is only incomplete.
func crewMacPeopleChecked(project string) ([]string, error) {
	t, err := readCrewTeam(project)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	owner := ownerOfCrewProjectID(project)
	if err != nil || owner == "" || t.OwnerID != owner || !t.On {
		return nil, nil
	}
	people := []string{owner}
	for _, p := range t.People {
		if _, ok := t.memberMac(p.UserID); !ok {
			continue
		}
		active, err := crewTeamAccountActiveChecked(p.UserID)
		if err != nil {
			return nil, err
		}
		if active {
			people = append(people, p.UserID)
		}
	}
	return people, nil
}

// crewTeamAccountActiveChecked is crewTeamAccountActive that tells an unreadable directory from an inactive account.
func crewTeamAccountActiveChecked(user string) (bool, error) {
	directory, err := loadUserDirectory()
	if err != nil {
		return false, err
	}
	if directory == nil {
		return false, fmt.Errorf("the people directory is unavailable")
	}
	record := directory.byID(user)
	return record != nil && !record.Disabled && userAllowedProduct(&UserClaims{UserID: user}, crewProfileID), nil
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
type crewMacTeamWatchKey struct{}
type crewMacTeamWatch struct {
	viewer                                    *UserClaims
	project, conversation, person, tab, share string // share: what the person shared when this read began
}

// Retain the original viewer for the lifetime of a relay read. Switching the
// routing identity must not discard credential limits or current membership.
func crewMacTeamWatchAllowed(r *http.Request, project string) bool {
	watch, ok := r.Context().Value(crewMacTeamWatchKey{}).(crewMacTeamWatch)
	if !ok {
		return true
	}
	if watch.project != project || !crewChatWebsiteClaims(watch.viewer) || !crewTeamAccountActive(watch.viewer.UserID) {
		return false
	}
	t, err := readCrewTeam(project)
	if err != nil || ownerOfCrewProjectID(project) != t.OwnerID || !crewTeamAccountActive(t.OwnerID) || !t.On || !t.member(watch.viewer.UserID) {
		return false
	}
	person, tab := crewMacTeamConversation(r.Context(), project, watch.conversation)
	share := t.agentChatShare(r.Context(), person, tab)
	return person == watch.person && tab == watch.tab && share == watch.share && share != crewChatShareAgent
}

func (api *StreamingAPI) crewMacTeamWatchContext(r *http.Request, project, conversation string) (*http.Request, bool) {
	claims := GetUserFromContext(r.Context())
	if IsLocalMode() || !crewChatWebsiteClaims(claims) {
		return r, false
	}
	viewer := claims.UserID
	t, err := readCrewTeam(project)
	if err != nil || ownerOfCrewProjectID(project) != t.OwnerID || !crewTeamAccountActive(t.OwnerID) || !crewTeamAccountActive(viewer) || !t.On || !t.member(viewer) {
		return r, false
	}
	person, tab := crewMacTeamConversation(r.Context(), project, conversation)
	if person == "" || person == viewer {
		return r, false
	}
	log.Printf("[CREW_TEAM] project=%s event=member_watch user=%s of=%s tab=%s read=%s", project, logUsernameForUserID(viewer), logUsernameForUserID(person), tab, strings.TrimPrefix(r.URL.Path, "/api/sessions/"))
	ctx := context.WithValue(r.Context(), crewMacTeamWatchKey{}, crewMacTeamWatch{viewer: claims, project: project, conversation: conversation, person: person, tab: tab, share: t.agentChatShare(r.Context(), person, tab)})
	return r.WithContext(context.WithValue(ctx, UserContextKey, &UserClaims{UserID: person, Username: person})), true
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

// crewMemberMacOffLine is what the sender is told when the member's Mac is off and their agent cannot answer from the
// server (no copy there yet, or no AI program the server can use for it): the message waits for the Mac.
const crewMemberMacOffLine = "Their Mac is off and their agent can't answer from the server yet. Your message is waiting for their Mac."

// deliverToMemberMacAgent hands an agent message to a Team member's agent on their Mac, like the owner's Mac agents:
// it runs there in the sender's own thread, the answer comes back to this inbox. With their Mac off an In team agent
// that has a standby copy answers from the server AS THE MEMBER (crew_standby_runas.go); otherwise the message waits
// for the Mac.
func (api *StreamingAPI) deliverToMemberMacAgent(ctx context.Context, c agentConversation, m agentMessage, source, dest agentMessageEndpoint) error {
	person, agent, ok := crewMemberMacAgentFor(ctx, dest.ID, dest.ChatKey, source.UserID)
	if !ok {
		log.Printf("[CREW_TEAM] project=%s event=member_mac_message refused user=%s", dest.ID, logUsernameForUserID(source.UserID))
		return fmt.Errorf("This agent isn't shared with you.")
	}
	pair, _ := crewMacPairFor(person, dest.ID)
	sender := shareOwnerLabel(source.UserID)
	// With their Mac off, the agent answers from its standby copy on the server, as the member.
	standby := api.crewStandbyRoute(ctx, person, dest.ID, agent.Tab, source.UserID, true)
	run := func(call crewChatCall, timeout time.Duration) crewChatResult {
		if standby {
			return api.standbyAgentCall(context.Background(), person, dest.ID, call, true)
		}
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
	raw, _ := json.Marshal(withAgentRuleArgs(ctx, map[string]any{"sender_id": source.UserID, "sender_agent": senderAgent, "sender_channel": "AgentBridge"}, dest.ID, person, crewMacTabRow(dest.ID, agent).ConversationKey))
	ask := crewChatCall{Operation: "agent_ask", Tab: agent.Tab, Message: m.Text, Caller: sender, Args: raw, SubmissionID: m.ID}
	from := 1 - m.From
	_, status := read(0, 0)
	if status == http.StatusOK {
		status = run(ask, 60*time.Second).Status
	}
	// The standby copy was dropped between the route and the call (the agent turned Only me, the person left): the
	// message is not lost, it waits for the Mac like any other.
	if standby && (status == http.StatusForbidden || status == http.StatusConflict || status == http.StatusServiceUnavailable) {
		standby, status = false, http.StatusServiceUnavailable
	}
	log.Printf("[CREW_TEAM] project=%s event=member_mac_message of=%s tab=%s user=%s mac_status=%d standby=%t", dest.ID, logUsernameForUserID(person), agent.Tab, logUsernameForUserID(source.UserID), status, standby)
	if status == http.StatusServiceUnavailable && !standby {
		if err := holdForPersonMac(dest.ID, person, "mac_off", ask, c.ID, from); err != nil {
			if errors.Is(err, errCrewMacHeldFull) {
				return fmt.Errorf("Too many of your messages are already waiting for this Mac. Try again when it is back.")
			}
			return fmt.Errorf("Your message could not be saved. Please try again.")
		}
		go api.appendMacAgentReply(c.ID, from, crewMemberMacOffLine)
		return nil
	}
	if status != http.StatusOK {
		return fmt.Errorf("the agent could not take this message; try again shortly")
	}
	api.startMacAgentAnswer(macAgentAwaiting{Inbox: c.ID, Submission: m.ID, From: from, Project: dest.ID, Tab: agent.Tab, Mac: person, Member: true, Standby: standby, SenderID: source.UserID, Caller: sender})
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
