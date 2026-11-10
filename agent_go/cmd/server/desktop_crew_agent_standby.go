package server

// Always on, step 1, the Mac's side (crew_agent_standby.go has the server's): after a turn of an In team agent ends
// (the owner's own tab or a teammate's thread on it), wait until the agent has been idle for a while, then refresh the
// server's standby copy of each changed conversation through Publish's capture and native sync. Only the Crew owner's
// Mac sends, only while Mac online and the Team are on; the server decides which agents are In team. Routing does not
// change: the Mac keeps answering.

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// crewStandbyDelay is how long an agent stays idle after its last finished turn before its copy is sent.
var crewStandbyDelay = 30 * time.Second

type crewStandbyAgent struct {
	timer *time.Timer
	// dirty: sessions with a finished turn not yet copied. Primed: every session was offered once since the app
	// started, so later sends offer only dirty ones (the server still skips unchanged copies).
	dirty  map[string]bool
	primed bool
}

var crewStandbyMu sync.Mutex
var crewStandbyAgents = map[string]*crewStandbyAgent{}
var crewStandbySending sync.Map

// crewStandbyMacTeam: this Mac Crew takes part in Always on, whoever's it is (owner, 6 Nov: Team members are equal):
// the owner's Crew with the Team on, or a member's working copy of a Team's Crew. The server decides which agents are
// eligible; this only keeps the loop away from Crews that are in no Team.
func crewStandbyMacTeam(project string) bool {
	state := readCrewTeamState(project)
	return state.TeamOn || state.MemberOf != ""
}

// noteCrewStandbyTurn runs when a session's turn ends (updateSessionStatus). It only remembers the session and
// (re)starts the agent's wait; all checks and work run when the wait ends.
func (api *StreamingAPI) noteCrewStandbyTurn(session, workspace string) {
	if !IsLocalMode() || session == "" || !strings.HasPrefix(canonicalCrewWorkspaceRoot(workspace), "Crew/") {
		return
	}
	go func() {
		roots, found, err := crewStorageRoots(canonicalCrewWorkspaceRoot(workspace))
		if err != nil || !found || roots.Owner != GetDefaultUserID() {
			return
		}
		if b, err := crewroot.ReadSync(roots.ID); err != nil || b == nil || b.Side != "local" || b.Mode != "relay" {
			return
		}
		if !crewStandbyMacTeam(roots.ID) {
			return
		}
		tab := ""
		for _, record := range crewStandbyRecords(roots) {
			if record.SessionID == session && !isUserViewConversationKey(record.ConversationKey) {
				tab = crewAgentPublishTab(crewStandbyWireKey(roots.ID, record.ConversationKey))
			}
		}
		if tab == "" {
			return
		}
		api.scheduleCrewStandby(roots.ID, tab, session, crewStandbyDelay)
	}()
}

func (api *StreamingAPI) scheduleCrewStandby(project, tab, session string, wait time.Duration) {
	crewStandbyMu.Lock()
	defer crewStandbyMu.Unlock()
	key := project + "\x00" + tab
	agent := crewStandbyAgents[key]
	if agent == nil {
		agent = &crewStandbyAgent{dirty: map[string]bool{}}
		crewStandbyAgents[key] = agent
	}
	if session != "" {
		agent.dirty[session] = true
	}
	if agent.timer != nil {
		agent.timer.Stop()
	}
	agent.timer = time.AfterFunc(wait, func() { api.sendCrewStandby(project, tab) })
}

// crewStandbyStartupDelay lets Mac online connect before the startup catch-up offers copies.
var crewStandbyStartupDelay = time.Minute

// catchUpCrewStandby runs once when the Mac app starts: every agent of this Mac's Crews with Mac online and the Team
// on is offered once (the server says which copies are behind and which agents are shared), so a copy that was waiting
// when the app stopped still goes up.
func (api *StreamingAPI) catchUpCrewStandby() {
	state, err := workflowCLIStateRoot()
	if err != nil {
		return
	}
	entries, _ := os.ReadDir(filepath.Join(state, "crew-sync"))
	for _, entry := range entries {
		id, ok := strings.CutSuffix(entry.Name(), ".json")
		if _, e := uuid.Parse(id); entry.IsDir() || !ok || e != nil {
			continue
		}
		b, e := crewroot.ReadSync(id)
		if e != nil || b == nil || b.Side != "local" || b.Mode != "relay" {
			continue
		}
		if !crewStandbyMacTeam(id) {
			continue
		}
		claims := &UserClaims{UserID: GetDefaultUserID(), Username: GetDefaultUserID()}
		roots, e := api.crewSyncRoots(context.WithValue(context.Background(), UserContextKey, claims), claims, id)
		if e != nil || roots.Owner != claims.UserID {
			continue
		}
		tabs := map[string]bool{}
		for _, record := range crewStandbyRecords(roots) {
			if tab := crewAgentPublishTab(crewStandbyWireKey(roots.ID, record.ConversationKey)); tab != "" && !isUserViewConversationKey(record.ConversationKey) {
				tabs[tab] = true
			}
		}
		for tab := range tabs {
			api.scheduleCrewStandby(roots.ID, tab, "", 0)
		}
		log.Printf("[ALWAYS_ON] project=%s event=startup_offer agents=%d", roots.ID, len(tabs))
	}
}

// crewStandbyWireKey is a Mac chat key as the Team and the server name it: the Crew's Main lives under its physical
// root key on the Mac (originalMainChatKey), but is the project itself everywhere else.
func crewStandbyWireKey(project, key string) string {
	if side, ok := strings.CutPrefix(key, project+codeChatSideMarker); ok && strings.HasPrefix(side, "root-") {
		return project
	}
	return key
}

// crewStandbyRecords are the owner's conversations in this Crew, from this Mac's chat registry.
func crewStandbyRecords(roots crewroot.Roots) []ProductConversationRecord {
	out, err := defaultProductConversationRegistryStore().crewWorkspaceChats(context.Background(), roots.Owner, roots.Virtual)
	if err != nil {
		return nil
	}

	sort.Slice(out, func(i, j int) bool { return out[i].ConversationKey < out[j].ConversationKey })
	return out
}

// sendCrewStandby refreshes one agent's standby copy: its own tab first, then each teammate thread on it, one at a
// time (an owner's tab and its threads share a native session id, so each uses the native stage in turn).
func (api *StreamingAPI) sendCrewStandby(project, tab string) {
	running, _ := crewStandbySending.LoadOrStore(project, &sync.Mutex{})
	lock := running.(*sync.Mutex)
	if !lock.TryLock() {
		api.scheduleCrewStandby(project, tab, "", crewStandbyDelay)
		return
	}
	defer lock.Unlock()
	crewStandbyMu.Lock()
	agent := crewStandbyAgents[project+"\x00"+tab]
	if agent == nil {
		crewStandbyMu.Unlock()
		return
	}
	dirty, primed := agent.dirty, agent.primed
	agent.dirty = map[string]bool{}
	crewStandbyMu.Unlock()
	retry := func(session string) {
		crewStandbyMu.Lock()
		agent.dirty[session] = true
		crewStandbyMu.Unlock()
	}
	skip := func(reason string) {
		log.Printf("[ALWAYS_ON] project=%s tab=%s event=skipped reason=%s", project, tab, reason)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	claims, binding, connection, err := api.desktopCrewChatBinding(ctx, project)
	if err != nil || binding.Mode != "relay" {
		return // Mac online is off for this Crew
	}
	if !crewStandbyMacTeam(project) {
		return
	}
	member := readCrewTeamState(project).MemberOf != ""
	if placements, err := readCrewAgentPlacements(project); !member && (err != nil || placements[tab].Tab != "") {
		return // a published agent already runs on the server
	}
	roots, err := api.crewSyncRoots(ctx, claims, project)
	if err != nil {
		return
	}
	// What only this Mac has goes with the first check, so the server's note for its turns names it (names only; a
	// list that cannot be read now keeps the server's last one).
	var macOnly []crewTeamMacOnlyItem
	if !member {
		macOnly, _ = api.crewTeamMacOnly(ctx, claims.UserID, roots, connection, binding)
	}
	call := func(action string, sessions []crewStandbySendItem) (*crewStandbyReply, error) {
		// Peer names this Mac's own project: the server knows the owner's Mac from the Crew's pair, a member's only
		// from what it names (crewStandbyRequest.Peer).
		request := &crewStandbyRequest{Tab: tab, Sessions: sessions, Peer: project}
		if action == "standby_check" && sessions == nil {
			request.MacOnly = macOnly
		}
		reply, err := desktopCrewSyncCall(ctx, claims.UserID, &connection, binding.PeerProjectID, crewSyncRequest{Action: action, PairID: binding.PairID, Standby: request})
		if err != nil {
			return nil, err
		}
		if reply.Standby == nil {
			return nil, fmt.Errorf("server_not_updated")
		}
		return reply.Standby, nil
	}
	// The server decides: Team on, In team, not published. Not eligible drops the server's copy.
	if answer, err := call("standby_check", nil); err != nil {
		for session := range dirty {
			retry(session)
		}
		skip("server_unavailable")
		return
	} else if noteCrewStandbyWriter(project, tab, answer.Writer); answer.Writer == "server" {
		// The server answers for this agent (Always on, step 2): its chats there are newer than this Mac's, so no copy
		// is sent until it is handed back (step 3, which this Mac starts now).
		skip("server_writer")
		go api.handBackCrewStandby(project, tab)
		return
	} else if !answer.Eligible {
		skip("not_in_team")
		return
	}
	failed := false
	for _, record := range crewStandbyRecords(roots) {
		key := crewStandbyWireKey(roots.ID, record.ConversationKey)
		if crewAgentPublishTab(key) != tab || primed && !dirty[record.SessionID] {
			continue
		}
		started := time.Now()
		// Between turns only: the native writer has finished this session's turn and nothing runs in it.
		if api.crewAgentTurnRunning(record.SessionID) {
			retry(record.SessionID)
			api.scheduleCrewStandby(project, tab, "", crewStandbyDelay)
			continue
		}
		conversation, err := api.captureCrewHandoffConversation(ctx, claims, roots, record, true)
		if err != nil || conversation == nil || conversation.Native == nil {
			log.Printf("[ALWAYS_ON] project=%s tab=%s session=%s event=skipped reason=no_native_session error=%q", project, tab, record.SessionID, fmt.Sprint(err))
			continue
		}
		if api.crewAgentTurnRunning(record.SessionID) {
			retry(record.SessionID)
			api.scheduleCrewStandby(project, tab, "", crewStandbyDelay)
			continue
		}
		item := crewStandbySendItem{Key: key, Fingerprint: crewStandbyFingerprint(conversation)}
		answer, err := call("standby_check", []crewStandbySendItem{item})
		if err != nil {
			retry(record.SessionID)
			failed = true
			skip("server_unavailable")
			break
		}
		if noteCrewStandbyWriter(project, tab, answer.Writer); answer.Writer == "server" {
			skip("server_writer")
			go api.handBackCrewStandby(project, tab)
			return
		}
		if !answer.Eligible {
			skip("not_in_team")
			return
		}
		if len(answer.Need) == 0 {
			log.Printf("[ALWAYS_ON] project=%s tab=%s event=unchanged bytes=0 ms=%d", project, tab, time.Since(started).Milliseconds())
			continue
		}
		var bytes int64
		for _, file := range conversation.Native.Files {
			bytes += file.Size
		}
		if err = bindCrewNativeStage(roots, conversation.Native); err == nil {
			err = desktopCrewNativeSync(ctx, roots, connection, binding, conversation.Native)
		}
		if err == nil {
			item.Conversation = conversation
			answer, err = call("standby_store", []crewStandbySendItem{item})
		}
		if err != nil {
			retry(record.SessionID)
			failed = true
			skip("send_failed")
			break
		}
		log.Printf("[ALWAYS_ON] project=%s tab=%s event=standby_sent bytes=%d ms=%d result=%s revision=%d", project, tab, bytes, time.Since(started).Milliseconds(), answer.Event, answer.Revision)
	}
	if !failed {
		crewStandbyMu.Lock()
		agent.primed = true
		crewStandbyMu.Unlock()
	}
}

// Always on, step 2, the Mac's side: while the server answers for an agent from its standby copy (writer=server),
// this Mac does not run that agent. The server's answer is kept for a short while so a turn costs at most one check.
type crewStandbyWriterSeen struct {
	writer string
	at     time.Time
}

const crewStandbyWriterFresh = 15 * time.Second

var crewStandbyWriters sync.Map // project + "\x00" + tab -> crewStandbyWriterSeen

func noteCrewStandbyWriter(project, tab, writer string) {
	crewStandbyWriters.Store(project+"\x00"+tab, crewStandbyWriterSeen{writer: writer, at: time.Now()})
}

// crewStandbyMacRefusal is the plain line for a turn this Mac must not run because the server answers for the agent
// now, or "". Only the Crew owner's Mac with Mac online and the Team on asks; a server it cannot reach lets the turn run.
func (api *StreamingAPI) crewStandbyMacRefusal(ctx context.Context, profileID, key, source string) string {
	if !IsLocalMode() || profileID != crewProfileID {
		return ""
	}
	project := crewResourceProjectID(key)
	tab := crewAgentPublishTab(crewStandbyWireKey(project, key))
	if project == "" || tab == "" {
		return ""
	}
	if b, err := crewroot.ReadSync(project); err != nil || b == nil || b.Side != "local" || b.Mode != "relay" {
		return ""
	}
	if !crewStandbyMacTeam(project) {
		return ""
	}
	seen, ok := crewStandbyWriters.Load(project + "\x00" + tab)
	if !ok || time.Since(seen.(crewStandbyWriterSeen).at) > crewStandbyWriterFresh {
		writer := ""
		check, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if claims, binding, connection, err := api.desktopCrewChatBinding(check, project); err == nil && binding.Mode == "relay" {
			reply, err := desktopCrewSyncCall(check, claims.UserID, &connection, binding.PeerProjectID, crewSyncRequest{Action: "standby_check", PairID: binding.PairID, Standby: &crewStandbyRequest{Tab: tab, Peer: project}})
			if err == nil && reply.Standby != nil {
				writer = reply.Standby.Writer
			}
		}
		noteCrewStandbyWriter(project, tab, writer)
		seen, _ = crewStandbyWriters.Load(project + "\x00" + tab)
	}
	if seen.(crewStandbyWriterSeen).writer != "server" {
		return ""
	}
	log.Printf("[ALWAYS_ON] project=%s tab=%s event=mac_refused reason=server_writer source=%s ms=0", project, tab, source)
	go api.handBackCrewStandby(project, tab) // one at a time per agent; a running hand back makes this a no-op
	return crewStandbyMacRefusalLine
}
