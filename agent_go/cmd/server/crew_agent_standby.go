package server

// Always on, step 1 (owner, 5 Nov: every In team agent is always on): the owner's Mac keeps a standby copy of each
// In team agent's conversation on its Crew's online copy — the native session of the owner's tab and of every teammate
// thread on that agent, with its AgentWorks history. The Mac still runs the agent; nothing here routes a turn to the
// server (step 2 does). The copy arrives through the same native sync as Publish (handleCrewNativeSync) and is admitted
// by the same owner and pair checks (crewTransferBinding).
//
// The standby record is kept apart from the Publish placements on purpose: every placement reader treats an entry as
// "published" (routing, runtime folder, callers, reach off), and a standby agent is not published.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
)

// crewStandbyRequest: standby_check names the agent (and, per session, the fingerprint the Mac has now);
// standby_store carries exactly one session whose native bundle was just synced to this server's native stage.
type crewStandbyRequest struct {
	Tab string `json:"tab"`
	// Peer is the sending Mac's own project for this Crew. The owner's Mac is known from the Crew's pair; a member's
	// Mac is not (the Team record keeps only its pair), so it names itself, and only for that member's own rows.
	Peer     string                `json:"peer,omitempty"`
	Sessions []crewStandbySendItem `json:"sessions,omitempty"`
	// MacOnly (standby_check): what this Crew uses that only the owner's Mac has, names only (crewTeamMacOnly), for the
	// note a server turn gets (crewStandbyServerNote). Nil keeps the last list.
	MacOnly []crewTeamMacOnlyItem `json:"mac_only"`
}

type crewStandbySendItem struct {
	// Key is the Mac conversation key: the owner's tab, or a teammate's thread on it (sharedThreadKey).
	Key          string                   `json:"key"`
	Fingerprint  string                   `json:"fingerprint"`
	Conversation *crewHandoffConversation `json:"conversation,omitempty"`
	// Diverged (handback_done): the Mac had changed this conversation too, so it kept its own and saved the server's
	// aside (crew_agent_handback.go).
	Diverged bool `json:"diverged,omitempty"`
}

type crewStandbyReply struct {
	// Eligible: the agent is In team in a Team that is on, and not published. Not eligible drops any copy.
	Eligible bool     `json:"eligible"`
	Reason   string   `json:"reason,omitempty"`
	Need     []string `json:"need,omitempty"`  // standby_check: keys whose copy differs from the Mac's fingerprint
	Event    string   `json:"event,omitempty"` // standby_store: stored, unchanged, diverged or server_writer
	Revision uint64   `json:"revision,omitempty"`
	// Writer is "server" while the server answers for the agent from its copy (step 2, crew_agent_always_on.go): the
	// Mac then neither runs the agent nor sends copies of it until it is handed back.
	Writer string `json:"writer,omitempty"`
	// Hand back (Always on, step 3, crew_agent_handback.go). Busy: a server turn of the agent is running, ask again
	// later. Keys (handback_pull without a session): the agent's conversations on the server, as the Mac names them.
	// Handback (handback_pull with one key): that conversation, its native bundle staged for the pull.
	Busy     bool                 `json:"busy,omitempty"`
	Keys     []string             `json:"keys,omitempty"`
	Handback *crewStandbySendItem `json:"handback,omitempty"`
}

// crewAgentStandby is one agent's standby copy on the server: "standby" means the server holds a current copy and the
// Mac runs the agent.
type crewAgentStandby struct {
	// User is the person whose Mac agent this copy is (owner, 6 Nov: Team members are equal): the owner or a joined
	// member on their Mac. Peer is that Mac's own project for the Crew. Rows are kept per person and tab.
	User     string                             `json:"user"`
	Peer     string                             `json:"peer,omitempty"`
	Tab      string                             `json:"tab"`
	Location string                             `json:"location"`
	PairID   string                             `json:"pair_id"`
	Revision uint64                             `json:"revision"`
	CopiedAt string                             `json:"copied_at"`
	Sessions map[string]crewAgentStandbySession `json:"sessions"`
	// Writer is "server" once the server ran a turn of the agent from this copy (Always on, step 2), with the copy's
	// revision then and the time; empty while the Mac runs it. Step 3 hands it back to the Mac.
	Writer         string `json:"writer,omitempty"`
	WriterRevision uint64 `json:"writer_revision,omitempty"`
	WriterSince    string `json:"writer_since,omitempty"`
	// KeptAt: the last hand back found the agent had answered on both the Mac and the server; both versions were kept
	// and the owner was told (crew_agent_handback.go).
	KeptAt string `json:"kept_at,omitempty"`
	// MacProgram / ServerProgram: the AI program the agent runs on the Mac and the one the server continued it on. They
	// differ when this server lacks the Mac's program and continued from saved history instead (owner, 6 Nov).
	MacProgram    string `json:"mac_program,omitempty"`
	ServerProgram string `json:"server_program,omitempty"`
}

type crewAgentStandbySession struct {
	Dir             string   `json:"dir"`
	Fingerprint     string   `json:"fingerprint"`
	ConversationID  string   `json:"conversation_id"`
	Provider        string   `json:"provider"`
	NativeSessionID string   `json:"native_session_id"`
	Bytes           int64    `json:"bytes"`
	Revision        uint64   `json:"revision"`
	StoredAt        string   `json:"stored_at"`
	Diverged        []string `json:"diverged,omitempty"` // earlier copies kept because the new one did not extend them
	// Imported is the revision of this copy the server's own chat was last made from (crew_agent_always_on.go).
	Imported uint64 `json:"imported,omitempty"`
}

var crewStandbyLocks sync.Map

// crewStandbyLock serialises one person's standby rows of one Crew.
func crewStandbyLock(project, person string) *sync.Mutex {
	value, _ := crewStandbyLocks.LoadOrStore(project+"\x00"+person, &sync.Mutex{})
	return value.(*sync.Mutex)
}

// crewStandbyDir is app-private state next to the Crew's sync state (removed and backed up with it).
func crewStandbyDir(project string) (string, error) {
	p, err := crewSyncManifestPath(project)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(p), project+"-always-on"), nil
}

// crewStandbyPersonDir holds one person's standby record and copies: people/<person>/standby.json, mac-only.json and
// <tab>/<copy>. A member's folder is never reachable with the owner's pair, nor the owner's with a member's.
func crewStandbyPersonDir(project, person string) (string, error) {
	if strings.TrimSpace(person) == "" {
		return "", fmt.Errorf("a standby copy belongs to a person")
	}
	dir, err := crewStandbyDir(project)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "people", sanitizeUserIDForPath(person)), nil
}

// crewStandbyPeople are the people who have standby rows in this Crew.
func crewStandbyPeople(project string) []string {
	dir, err := crewStandbyDir(project)
	if err != nil {
		return nil
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "people"))
	var out []string
	for _, entry := range entries {
		var rows map[string]crewAgentStandby
		raw, err := os.ReadFile(filepath.Join(dir, "people", entry.Name(), "standby.json"))
		if err != nil || json.Unmarshal(raw, &rows) != nil {
			continue
		}
		for _, row := range rows {
			if row.User != "" {
				out = append(out, row.User)
			}
			break
		}
	}
	return out
}

// readCrewStandby is one person's rows of this Crew, by agent tab.
func readCrewStandby(project, person string) (map[string]crewAgentStandby, error) {
	dir, err := crewStandbyPersonDir(project, person)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, "standby.json"))
	if os.IsNotExist(err) {
		return map[string]crewAgentStandby{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]crewAgentStandby{}
	if err = json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// crewStandbyHistoryEvents is the saved dialogue of the stored copy of one of this Crew's Mac agents' own
// conversations, as chat events, for reading it while the Mac is off. A teammate's private thread with an agent is
// never served here, whoever asks.
func crewStandbyHistoryEvents(project, conversationID string) []json.RawMessage {
	// The owner's Mac conversations (members' are served in a later step).
	owner := ownerOfCrewProjectID(project)
	rows, err := readCrewStandby(project, owner)
	if err != nil || conversationID == "" {
		return nil
	}
	dir, err := crewStandbyPersonDir(project, owner)
	if err != nil {
		return nil
	}
	for tab, row := range rows {
		for key, session := range row.Sessions {
			if session.ConversationID != conversationID || isSharedThreadKey(key) {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(dir, tab, session.Dir, "conversation.json"))
			if err != nil {
				return nil
			}
			var copied crewHandoffConversation
			if json.Unmarshal(raw, &copied) != nil {
				return nil
			}
			// The chat screen shows only events of the session it opened: the Mac chat's address on this server.
			data, _ := json.Marshal(map[string]any{"session_id": crewChatSessionID(project, conversationID), "conversation_history": copied.History, "updated_at": session.StoredAt})
			var log builderConversationLog
			if json.Unmarshal(data, &log) != nil {
				return nil
			}
			return builderConversationToRawEventsLimit(log, 0)
		}
	}
	return nil
}

// crewStandbyServedEvents is the server's own chat of a Mac conversation while the server answers for its agent
// (writer "server"): the copy imported as the owner's chat with the same conversation id, with the turns answered here.
func (api *StreamingAPI) crewStandbyServedEvents(owner, project, conversationID string) []json.RawMessage {
	rows, err := readCrewStandby(project, owner)
	if err != nil || conversationID == "" || api.eventStore == nil {
		return nil
	}
	serving := false
	for _, row := range rows {
		for key, session := range row.Sessions {
			serving = serving || row.Writer == "server" && session.ConversationID == conversationID && !isSharedThreadKey(key)
		}
	}
	if !serving {
		return nil
	}
	doc, err := defaultProductConversationRegistryStore().loadDocument(context.Background(), productConversationRegistryPath(owner))
	if err != nil {
		return nil
	}
	for _, record := range doc.Entries {
		if record.ProfileID != crewProfileID || record.ConversationID != conversationID || crewResourceProjectID(record.ConversationKey) != project {
			continue
		}
		// Shown under the Mac chat's address on this server, the session the screen opened.
		address := crewChatSessionID(project, conversationID)
		out := []json.RawMessage{}
		for _, event := range api.eventStore.GetAllEventsRaw(record.SessionID) {
			raw, err := json.Marshal(event)
			var fields map[string]any
			if err != nil || json.Unmarshal(raw, &fields) != nil {
				continue
			}
			fields["session_id"] = address
			if raw, err = json.Marshal(fields); err == nil {
				out = append(out, raw)
			}
		}
		return out
	}
	return nil
}

// crewStandbyMacOnly is the last list of what only this person's Mac has, as their Mac sent it.
func crewStandbyMacOnly(project, person string) []crewTeamMacOnlyItem {
	dir, err := crewStandbyPersonDir(project, person)
	if err != nil {
		return nil
	}
	var items []crewTeamMacOnlyItem
	if raw, err := os.ReadFile(filepath.Join(dir, "mac-only.json")); err == nil {
		_ = json.Unmarshal(raw, &items)
	}
	return items
}

func writeCrewStandby(project, person string, rows map[string]crewAgentStandby) error {
	dir, err := crewStandbyPersonDir(project, person)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for tab, row := range rows {
		row.User = person // every row says whose it is, whoever filled it in
		rows[tab] = row
	}
	return fsutil.WriteJSONAtomic(filepath.Join(dir, "standby.json"), rows, 0o600)
}

func crewStandbyLog(project, tab, event string, bytes int64, started time.Time, extra string) {
	log.Printf("[ALWAYS_ON] project=%s tab=%s event=%s bytes=%d ms=%d%s", project, tab, event, bytes, time.Since(started).Milliseconds(), extra)
}

// crewStandbyFingerprint names one session's copy: its native files and its history. Both sides compute it from the
// content itself, so the server never trusts the Mac's value.
func crewStandbyFingerprint(c *crewHandoffConversation) string {
	if c == nil || c.Native == nil {
		return ""
	}
	history, _ := json.Marshal(c.History)
	sum := sha256.New()
	fmt.Fprintf(sum, "%s\x00%s\x00%s\x00", c.ConversationID, bundleFingerprint(c.Native), c.ModelID)
	sum.Write(history)
	return hex.EncodeToString(sum.Sum(nil))
}

// crewStandbyEligible is the server's answer to "keep a copy of this person's agent": the Crew is a Mac Crew's online
// copy, its Team is on, and the person is the owner (the agent is listed for the Team) or a joined member whose agents
// work on their Mac (the agent is in their Mac's list); the agent is In team for that person; and, for the owner's, it
// is not published (a published agent already runs here). A person only invited, or a member on the website, is not
// eligible: they have no Mac to copy from.
func crewStandbyEligible(project, person, tab string) (bool, string) {
	if !crewRelayTwin(project) {
		return false, "not_mac_crew"
	}
	t, err := readCrewTeam(project)
	if err != nil || !t.On {
		return false, "team_off"
	}
	if person == "" || t.OwnerID == "" || ownerOfCrewProjectID(project) != t.OwnerID {
		return false, "not_owner"
	}
	if person == t.OwnerID {
		if !slices.ContainsFunc(readMacSharedAgents(project), func(a macSharedAgent) bool { return a.Tab == tab && crewTeamInTeam(a) }) {
			return false, "not_listed"
		}
	} else {
		if !t.member(person) {
			return false, "not_member"
		}
		if _, onMac := t.memberMac(person); !onMac {
			return false, "not_on_mac"
		}
		if !crewMemberMacTabKnown(project, person, tab) {
			return false, "not_listed"
		}
	}
	if !t.agentInTeam(person, tab) {
		return false, "only_me"
	}
	if person != t.OwnerID {
		// Placements are the owner's tabs: a member's "main" is not the owner's published one.
		return true, ""
	}
	if placements, err := readCrewAgentPlacements(project); err != nil {
		return false, "placement_unreadable"
	} else if _, published := placements[tab]; published {
		return false, "published"
	}
	return true, ""
}

// crewMemberMacTabKnown: the tab is in the last agent list read from this member's Mac (cache, else the saved list).
func crewMemberMacTabKnown(project, person, tab string) bool {
	var tabs []crewMacTeamTab
	if v, ok := crewMacTeamTabsCache.Load(project + "\x00" + person); ok {
		tabs = v.(crewMacTeamTabsRead).tabs
	} else if saved, ok := readSavedCrewMacTabs(project, person); ok {
		tabs = saved
	}
	return slices.ContainsFunc(tabs, func(t crewMacTeamTab) bool { return t.Tab == tab })
}

// crewStandbyPairPeer is the pair of this person's Mac for this Crew and that Mac's own project: the owner's from the
// Crew's binding, a member's from the Team record (the pair) and their row (the project they named).
func crewStandbyPairPeer(project, person string, row crewAgentStandby) (pair, peer string) {
	if person == "" {
		return "", ""
	}
	if person == ownerOfCrewProjectID(project) {
		if b, err := crewroot.ReadSync(project); err == nil && b != nil {
			return b.PairID, b.PeerProjectID
		}
		return "", ""
	}
	if t, err := readCrewTeam(project); err == nil && ownerOfCrewProjectID(project) == t.OwnerID {
		pair, _ = t.memberMac(person)
	}
	return pair, row.Peer
}

// pruneCrewStandby drops every standby copy that is no longer allowed: the agent turned Only me or left the Team's
// list, the Team turned off, the person left, the agent was published, or the person's pair changed. Runs after each
// Team record and shared-agent list write, and on each standby request.
func pruneCrewStandby(project string) {
	if IsLocalMode() {
		return
	}
	for _, person := range crewStandbyPeople(project) {
		pruneCrewStandbyPerson(project, person)
	}
}

func pruneCrewStandbyPerson(project, person string) {
	dir, err := crewStandbyPersonDir(project, person)
	if err != nil {
		return
	}
	lock := crewStandbyLock(project, person)
	lock.Lock()
	defer lock.Unlock()
	rows, err := readCrewStandby(project, person)
	if err != nil || len(rows) == 0 {
		return
	}
	changed := false
	for tab, row := range rows {
		reason := ""
		if pair, _ := crewStandbyPairPeer(project, person, row); pair == "" || pair != row.PairID {
			reason = "pair_changed"
		} else if ok, why := crewStandbyEligible(project, person, tab); !ok && row.Writer != "server" {
			// A copy the server answers from holds turns the Mac does not have yet: it stays until handed back.
			reason = why
		}
		if reason == "" {
			continue
		}
		started := time.Now()
		_ = os.RemoveAll(filepath.Join(dir, tab))
		delete(rows, tab)
		changed = true
		crewStandbyLog(project, tab, "dropped", 0, started, " reason="+reason)
	}
	if changed {
		if err = writeCrewStandby(project, person, rows); err != nil {
			log.Printf("[ALWAYS_ON] project=%s event=drop_record_failed", project)
		}
	}
}

// dropCrewStandbyTab removes one agent's copy at once (a standby request for an agent that is no longer eligible).
// The caller holds the person's standby lock.
func dropCrewStandbyTab(project, person, tab, reason string) {
	rows, err := readCrewStandby(project, person)
	if err != nil {
		return
	}
	if row, ok := rows[tab]; !ok || row.Writer == "server" {
		return // a copy the server answers from stays until it is handed back to the Mac
	}
	started := time.Now()
	if dir, err := crewStandbyPersonDir(project, person); err == nil {
		_ = os.RemoveAll(filepath.Join(dir, tab))
	}
	delete(rows, tab)
	if writeCrewStandby(project, person, rows) == nil {
		crewStandbyLog(project, tab, "dropped", 0, started, " reason="+reason)
	}
}

// crewStandbyKeyTab: the Mac conversation key must be this agent's own tab or a teammate's thread on it, in the
// paired Mac Crew.
func crewStandbyKeyTab(peer, key string) string {
	if key != peer && !strings.HasPrefix(key, peer+codeChatSideMarker) {
		return ""
	}
	return crewAgentPublishTab(key)
}

// crewStandbyAdmit admits the calling person's Mac for standby copies and hand back. The Crew's owner is admitted by
// the Crew's own pair (crewTransferBinding), exactly as before. A joined Team member is admitted only by the pair the
// Team recorded for them (crewMemberTransfer); the owner's pair never reaches a member's rows or the member's the owner's.
// peer is the member Mac's own project (see crewStandbyRequest.Peer).
func (api *StreamingAPI) crewStandbyAdmit(ctx context.Context, claims *UserClaims, id, pair, peer string) (crewroot.Roots, *crewroot.SyncBinding, error) {
	roots, binding, member, err := api.crewMemberTransfer(ctx, claims, id, pair)
	if member {
		if err == nil {
			if _, e := uuid.Parse(peer); e != nil {
				return roots, nil, fmt.Errorf("name the project this Mac holds for the Crew")
			}
			binding.PeerProjectID = peer
		}
		return roots, binding, err
	}
	return api.crewTransferBinding(ctx, claims, id, pair)
}

// crewMemberTransfer: member is true when the caller is a joined Team member who is not the Crew's owner. Admitted
// only when their agents work on their Mac and pair is the one the Team recorded for them. The returned roots are the
// Crew's, narrowed to this member: their own private staging folder (native sessions and chunks never share the
// owner's) and their own copy folder.
func (api *StreamingAPI) crewMemberTransfer(ctx context.Context, claims *UserClaims, id, pair string) (roots crewroot.Roots, binding *crewroot.SyncBinding, member bool, err error) {
	if IsLocalMode() || claims == nil || claims.UserID == "" {
		return roots, nil, false, nil
	}
	t, e := readCrewTeam(id)
	if e != nil || t.OwnerID == "" || t.OwnerID == claims.UserID || ownerOfCrewProjectID(id) != t.OwnerID || !t.member(claims.UserID) {
		return roots, nil, false, nil
	}
	if failure := crewSyncClaimsGate(claims, id); failure != nil {
		return roots, nil, true, failure
	}
	recorded, onMac := t.memberMac(claims.UserID)
	if !onMac || pair == "" || recorded != pair {
		return roots, nil, true, &crewSyncFailure{Status: 409, Code: "wrong_pair", Message: "use your own Mac's connection for this Crew"}
	}
	rec, ok := crewOwnerRecord(id)
	if !ok || rec.Folder == "" {
		return roots, nil, true, fmt.Errorf("this Crew's folder is unavailable")
	}
	roots, found, e := crewStorageRoots("Crew/" + rec.Folder)
	if e != nil || !found {
		return roots, nil, true, fmt.Errorf("this Crew's folder is unavailable")
	}
	roots.Owner = claims.UserID
	roots.Private = filepath.Join(roots.Private, "members", sanitizeUserIDForPath(claims.UserID))
	if e = os.MkdirAll(roots.Private, 0o700); e != nil {
		return roots, nil, true, e
	}
	if files, e := crewTeamMemberCopyRoot(id, claims.UserID); e == nil {
		roots.Files = files
	}
	return roots, &crewroot.SyncBinding{Mode: "relay", PairID: pair, Side: "server", Editing: "local"}, true, nil
}

func (api *StreamingAPI) receiveCrewStandby(ctx context.Context, claims *UserClaims, id string, in crewSyncRequest) (crewSyncReply, error) {
	if in.Standby == nil {
		return crewSyncReply{}, fmt.Errorf("choose one of this Crew's agents")
	}
	roots, binding, err := api.crewStandbyAdmit(ctx, claims, id, in.PairID, in.Standby.Peer)
	if err != nil {
		return crewSyncReply{}, err
	}
	out := crewSyncReply{ProjectID: id, Binding: binding}
	if binding.Mode != "relay" || binding.Editing != "local" || binding.PendingAction != "" || binding.ArchiveOnly {
		return out, &crewSyncFailure{Status: 409, Code: "wrong_pair", Message: "keep a standby copy from the Mac that holds this Crew"}
	}
	if !crewMacTabID(in.Standby.Tab) {
		return out, fmt.Errorf("choose one of this Crew's agents")
	}
	tab, person := in.Standby.Tab, claims.UserID
	if person != ownerOfCrewProjectID(id) {
		// A member's agent list is the one their Mac last sent: read it now (at most every 15 s) so a new agent is
		// known before it is judged (crewMemberMacTabKnown).
		personMacTabs(ctx, person, id)
	}
	lock := crewStandbyLock(id, person)
	lock.Lock()
	defer lock.Unlock()
	reply := &crewStandbyReply{}
	out.Standby = reply
	rows, err := readCrewStandby(id, person)
	if err != nil {
		return out, err
	}
	row := rows[tab]
	if row.PairID != "" && (row.PairID != binding.PairID || row.Peer != "" && row.Peer != binding.PeerProjectID && person != ownerOfCrewProjectID(id)) {
		row = crewAgentStandby{}
	}
	reply.Writer = row.Writer
	if ok, reason := crewStandbyEligible(id, person, tab); !ok {
		reply.Reason = reason
		dropCrewStandbyTab(id, person, tab, reason)
		return out, nil
	}
	reply.Eligible = true
	reply.Revision = row.Revision
	if in.Action == "standby_check" {
		if in.Standby.MacOnly != nil {
			if dir, err := crewStandbyPersonDir(id, person); err == nil && os.MkdirAll(dir, 0o700) == nil {
				_ = fsutil.WriteJSONAtomic(filepath.Join(dir, "mac-only.json"), in.Standby.MacOnly, 0o600)
			}
		}
		for _, item := range in.Standby.Sessions {
			if crewStandbyKeyTab(binding.PeerProjectID, item.Key) != tab {
				return out, fmt.Errorf("a conversation does not belong to this agent")
			}
			if row.Sessions[item.Key].Fingerprint != item.Fingerprint {
				reply.Need = append(reply.Need, item.Key)
			}
		}
		return out, nil
	}
	if len(in.Standby.Sessions) != 1 {
		return out, fmt.Errorf("send one conversation at a time")
	}
	item := in.Standby.Sessions[0]
	started := time.Now()
	if row.Writer == "server" {
		// The server answers for this agent: its chats here are newer than the Mac's copy, which must not replace them.
		reply.Event = "server_writer"
		crewStandbyLog(id, tab, "server_writer", 0, started, "")
		return out, nil
	}
	if crewStandbyKeyTab(binding.PeerProjectID, item.Key) != tab {
		return out, fmt.Errorf("a conversation does not belong to this agent")
	}
	conversation := item.Conversation
	if conversation == nil || conversation.Native == nil {
		return out, fmt.Errorf("a standby copy needs the agent's native session")
	}
	if err = validateCrewHandoffConversation(conversation); err != nil {
		return out, err
	}
	native := conversation.Native
	if err = native.Validate(); err != nil {
		return out, err
	}
	fingerprint := crewStandbyFingerprint(conversation)
	sum := sha256.Sum256([]byte(item.Key))
	name := hex.EncodeToString(sum[:8])
	previous, had := row.Sessions[item.Key]
	if had && previous.Fingerprint == fingerprint {
		reply.Event = "unchanged"
		crewStandbyLog(id, tab, "unchanged", 0, started, " copy="+name)
		return out, nil
	}
	dir, err := crewStandbyPersonDir(id, person)
	if err != nil {
		return out, err
	}
	final := filepath.Join(dir, tab, name)
	// The bytes are the ones the native sync just staged for this session; the stage lock keeps a concurrent push
	// from changing them while they are checked and copied.
	nativeLock := crewNativeLock(roots, native.Provider+"-"+native.SessionID)
	nativeLock.Lock()
	defer nativeLock.Unlock()
	if err = bindCrewNativeStage(roots, native); err != nil {
		return out, fmt.Errorf("the standby copy did not arrive complete; send it again")
	}
	event, fresh := "stored", false
	if had {
		old, e := readCrewStandbyBundle(final)
		if e == nil {
			// A new native session id is not a fork: the program started a fresh conversation (a changed agent
			// definition cannot load into a resumed one). The old copy is kept beside the new one all the same.
			fresh = old.SessionID != native.SessionID
			e = crewStandbyAncestor(old, native)
		}
		if e != nil {
			event = "diverged"
		}
	}
	if err = os.MkdirAll(filepath.Join(dir, tab), 0o700); err != nil {
		return out, err
	}
	tmp, err := os.MkdirTemp(filepath.Join(dir, tab), "."+name+"-")
	if err != nil {
		return out, err
	}
	defer os.RemoveAll(tmp)
	var bytes int64
	for file, meta := range native.Files {
		source, e := sessiontransfer.Path(native.Root, file, false)
		if e != nil {
			return out, e
		}
		dest := filepath.Join(tmp, "native", filepath.FromSlash(file))
		if e = os.MkdirAll(filepath.Dir(dest), 0o700); e == nil {
			e = copyCrewStandbyFile(source, dest, meta.Size)
		}
		if e != nil {
			return out, e
		}
		bytes += meta.Size
	}
	stored := *native
	stored.Root = filepath.Join(tmp, "native")
	if err = stored.ValidateFiles(); err != nil {
		return out, fmt.Errorf("the standby copy changed while it was saved; send it again")
	}
	if err = fsutil.WriteJSONAtomic(filepath.Join(tmp, "native.json"), &stored, 0o600); err != nil {
		return out, err
	}
	history := *conversation
	history.Native = nil
	raw, err := json.Marshal(history)
	if err != nil {
		return out, err
	}
	bytes += int64(len(raw))
	if err = os.WriteFile(filepath.Join(tmp, "conversation.json"), raw, 0o600); err != nil {
		return out, err
	}
	now := time.Now().UTC()
	session := crewAgentStandbySession{Dir: name, Fingerprint: fingerprint, ConversationID: conversation.ConversationID, Provider: native.Provider, NativeSessionID: native.SessionID, Bytes: bytes, StoredAt: now.Format(time.RFC3339)}
	if had {
		session.Diverged = previous.Diverged
	}
	if _, e := os.Lstat(final); e == nil {
		// Diverged: the copy already here is kept beside the new one, never overwritten (owner, 5 Nov: keep both).
		aside := final + ".old"
		if event == "diverged" {
			kept := name + ".diverged-" + now.Format("20060102T150405.000000000Z")
			aside = filepath.Join(dir, tab, kept)
			session.Diverged = append(session.Diverged, kept)
		} else {
			_ = os.RemoveAll(aside) // a leftover from an interrupted earlier save
		}
		if err = os.Rename(final, aside); err != nil {
			return out, err
		}
		if event != "diverged" {
			defer os.RemoveAll(aside)
		}
	}
	if err = os.Rename(tmp, final); err != nil {
		return out, err
	}
	if row.Sessions == nil {
		row.Sessions = map[string]crewAgentStandbySession{}
	}
	row.User, row.Peer, row.Tab, row.Location, row.PairID = person, binding.PeerProjectID, tab, "standby", binding.PairID
	row.Revision++
	row.CopiedAt = session.StoredAt
	session.Revision = row.Revision
	row.Sessions[item.Key] = session
	rows[tab] = row
	if err = writeCrewStandby(id, person, rows); err != nil {
		return out, err
	}
	reply.Event, reply.Revision = event, row.Revision
	crewStandbyLog(id, tab, "standby_stored", bytes, started, fmt.Sprintf(" copy=%s revision=%d", name, row.Revision))
	if event == "diverged" {
		label := "diverged"
		if fresh {
			label = "fresh_session"
		}
		crewStandbyLog(id, tab, label, bytes, started, fmt.Sprintf(" copy=%s kept=%d", name, len(session.Diverged)))
	}
	return out, nil
}

func readCrewStandbyBundle(dir string) (*sessiontransfer.Bundle, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "native.json"))
	if err != nil {
		return nil, err
	}
	var b sessiontransfer.Bundle
	if err = json.Unmarshal(raw, &b); err != nil {
		return nil, err
	}
	b.Root = filepath.Join(dir, "native")
	return &b, nil
}

// crewStandbyAncestor: the new copy continues the stored one. Journals must extend it byte for byte
// (sessiontransfer.Ancestor). A database snapshot (Cursor) cannot be compared by prefix; it continues the stored one
// when it has the same snapshot or comes from the same last handoff (its parent).
func crewStandbyAncestor(old, next *sessiontransfer.Bundle) error {
	if strings.HasSuffix(next.Format, "sqlite") {
		if old.Provider != next.Provider || old.Format != next.Format || old.SessionID != next.SessionID {
			return sessiontransfer.ErrDiverged
		}
		if next.Snapshot == old.Snapshot || next.Parent == old.Parent || next.Parent == old.Snapshot {
			return nil
		}
		return sessiontransfer.ErrDiverged
	}
	return sessiontransfer.Ancestor(old, next)
}

func copyCrewStandbyFile(source, dest string, size int64) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = io.CopyN(out, in, size)
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	return err
}
