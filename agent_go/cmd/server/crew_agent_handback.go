package server

// Always on, step 3, the server's side (owner, 5 Nov): an In team agent the server answered for while its owner's Mac
// was off (writer=server, crew_agent_always_on.go) goes back to the Mac once the Mac is online again. The Mac drives it
// (desktop_crew_agent_handback.go): it asks for the agent's conversations here (handback_pull), takes each one's native
// session through the native sync's pull direction with its history, and then asks to flip the writer back
// (handback_done). Until that flip every turn of the agent, a teammate's new ask included, still runs here, so nothing
// is lost: the flip is refused while a server turn runs or when a conversation here changed after the Mac took it, and
// the Mac then starts again. If the Mac goes off midway nothing changes and the next reconnect retries.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// crewHandbackCaptureForTest stands in for the server's native capture (it needs a provider runtime) in tests.
var crewHandbackCaptureForTest func(project string, record ProductConversationRecord) (*crewHandoffConversation, error)

// crewHandbackKeptFor is how long the Team card says an agent's two versions were kept.
const crewHandbackKeptFor = 72 * time.Hour

func crewHandbackLog(project, tab, event, reason string, bytes int64, started time.Time) {
	log.Printf("[ALWAYS_ON] project=%s tab=%s event=%s reason=%s bytes=%d ms=%d", project, tab, event, reason, bytes, time.Since(started).Milliseconds())
}

// crewHandbackSession is one of the agent's conversations on the server, with the key its Mac knows it by.
type crewHandbackSession struct {
	record ProductConversationRecord
	macKey string
}

// crewHandbackSessions are the agent's conversations in the owner's chats on this server: the owner's own chat with
// it and every teammate's thread on it, owner's chat first.
func (api *StreamingAPI) crewHandbackSessions(ctx context.Context, roots crewroot.Roots, binding *crewroot.SyncBinding, owner, tab string) []crewHandbackSession {
	claims := &UserClaims{UserID: owner, Username: owner}
	mainKey := ""
	if main, err := api.crewHandoffMain(context.WithValue(ctx, UserContextKey, claims), claims, roots.ID); err == nil {
		mainKey = main.ConversationKey
	}
	out := []crewHandbackSession{}
	for _, record := range crewStandbyRecords(roots) {
		if isUserViewConversationKey(record.ConversationKey) {
			continue
		}
		macKey := crewHandbackMacKey(roots.ID, binding.PeerProjectID, mainKey, record.ConversationKey)
		if macKey == "" || crewStandbyKeyTab(binding.PeerProjectID, macKey) != tab {
			continue
		}
		out = append(out, crewHandbackSession{record: record, macKey: macKey})
	}
	ownerKey := macSharedAgentChatKey(binding.PeerProjectID, tab)
	slices.SortStableFunc(out, func(a, b crewHandbackSession) int {
		if (a.macKey == ownerKey) != (b.macKey == ownerKey) {
			if a.macKey == ownerKey {
				return -1
			}
			return 1
		}
		return strings.Compare(a.macKey, b.macKey)
	})
	return out
}

// crewHandbackMacKey is a server chat key in the Mac's naming: the owner's Main is the Mac Crew itself, a side tab or a
// thread keeps its part after the project.
func crewHandbackMacKey(project, peer, mainKey, key string) string {
	if mainKey != "" && key == mainKey {
		return peer
	}
	side, ok := strings.CutPrefix(key, project+codeChatSideMarker)
	if !ok || side == "" || strings.HasPrefix(side, "owner-") {
		return ""
	}
	return peer + codeChatSideMarker + side
}

// crewHandbackMark names a server conversation's state cheaply: its session and its saved history, which every
// finished turn rewrites. A changed mark at the flip means a turn ran here after the Mac took the conversation.
func crewHandbackMark(owner string, record ProductConversationRecord) string {
	sum := sha256.New()
	fmt.Fprintf(sum, "%s\x00", record.SessionID)
	if raw, err := ReadChatHistoryConversation(owner, record.SessionID, record.WorkspacePath); err == nil {
		sum.Write(raw)
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// crewHandbackBusy: a server turn of the agent is running or queued, or a call routed to its server copy has not
// reached either yet (crewStandbyRoute's in-flight mark).
func (api *StreamingAPI) crewHandbackBusy(project, owner, tab string, sessions []crewHandbackSession) bool {
	return crewStandbyInFlight(project, owner, tab) || slices.ContainsFunc(sessions, func(s crewHandbackSession) bool {
		return api.crewAgentTurnRunning(s.record.SessionID) || api.conversationTurnHasBacklog(owner, s.record.SessionID)
	})
}

func (api *StreamingAPI) receiveCrewHandback(ctx context.Context, claims *UserClaims, id string, in crewSyncRequest) (crewSyncReply, error) {
	if in.Standby == nil {
		return crewSyncReply{}, fmt.Errorf("choose one of this Crew's agents")
	}
	// owner is the person whose Mac agent this is, the Crew's owner or a joined member: each is admitted only by
	// their own pair (crewStandbyAdmit) and reads only their own rows.
	roots, binding, err := api.crewStandbyAdmit(ctx, claims, id, in.PairID, in.Standby.Peer)
	if err != nil {
		return crewSyncReply{}, err
	}
	out := crewSyncReply{ProjectID: id, Binding: binding}
	if binding.Mode != "relay" || binding.Editing != "local" || binding.PendingAction != "" || binding.ArchiveOnly {
		return out, &crewSyncFailure{Status: 409, Code: "wrong_pair", Message: "hand an agent back only to the Mac that holds this Crew"}
	}
	if !crewMacTabID(in.Standby.Tab) {
		return out, fmt.Errorf("choose one of this Crew's agents")
	}
	tab, owner := in.Standby.Tab, claims.UserID
	reply := &crewStandbyReply{}
	out.Standby = reply
	rows, err := readCrewStandby(id, owner)
	if err != nil {
		return out, err
	}
	if row, ok := rows[tab]; !ok || row.Writer != "server" || row.PairID != binding.PairID || owner != ownerOfCrewProjectID(id) && row.Peer != binding.PeerProjectID {
		return out, nil // the Mac runs this agent already
	}
	reply.Writer = "server"
	if crewMacSocketFor(owner, id, binding.PairID) == nil {
		return out, &crewSyncFailure{Status: 409, Code: "mac_offline", Message: "the agent goes back to your Mac once it is online"}
	}
	started := time.Now()
	sessions := api.crewHandbackSessions(ctx, roots, binding, owner, tab)
	if in.Action == "handback_pull" {
		if api.crewHandbackBusy(id, owner, tab, sessions) {
			reply.Busy = true
			return out, nil
		}
		if len(in.Standby.Sessions) == 0 {
			for _, s := range sessions {
				reply.Keys = append(reply.Keys, s.macKey)
			}
			crewHandbackLog(id, tab, "handback_listed", fmt.Sprintf("conversations_%d", len(sessions)), 0, started)
			return out, nil
		}
		if len(in.Standby.Sessions) != 1 {
			return out, fmt.Errorf("take one conversation at a time")
		}
		at := slices.IndexFunc(sessions, func(s crewHandbackSession) bool { return s.macKey == in.Standby.Sessions[0].Key })
		if at < 0 {
			return out, &crewSyncFailure{Status: 409, Code: "handback_changed", Message: "this agent's conversations changed on the server; start again"}
		}
		s := sessions[at]
		item := &crewStandbySendItem{Key: s.macKey, Fingerprint: crewHandbackMark(owner, s.record)}
		reply.Handback = item
		var conversation *crewHandoffConversation
		if crewHandbackCaptureForTest != nil {
			conversation, err = crewHandbackCaptureForTest(id, s.record)
		} else if _, e := ReadChatHistoryConversation(owner, s.record.SessionID, s.record.WorkspacePath); !errors.Is(e, errChatHistoryConversationNotFound) {
			ownerClaims := &UserClaims{UserID: owner, Username: owner}
			ownerCtx := context.WithValue(ctx, UserContextKey, ownerClaims)
			conversation, err = api.captureCrewHandoffConversation(ownerCtx, ownerClaims, roots, s.record, true)
			if err != nil || conversation == nil || conversation.Native == nil {
				// A chat this server ran from saved history (it lacks the Mac's program), or one that never ran here, has no
				// native session: its history goes back, and the Mac continues it on its own program.
				log.Printf("[ALWAYS_ON] project=%s tab=%s event=handback_history reason=%q", id, tab, fmt.Sprint(err))
				conversation, err = api.captureCrewHandoffConversation(ownerCtx, ownerClaims, roots, s.record)
			}
		}
		if err == nil && conversation == nil {
			// Nothing to take (a thread whose first turn never finished): it does not hold the flip.
			reply.Event = "no_native"
			crewHandbackLog(id, tab, "handback_skipped", "no_saved_conversation", 0, started)
			return out, nil
		}
		if err != nil {
			// The server keeps answering for the agent rather than lose this conversation; the next try takes it again.
			crewHandbackLog(id, tab, "handback_failed", "capture_failed", 0, started)
			log.Printf("[ALWAYS_ON] project=%s tab=%s capture_error=%q", id, tab, err)
			return out, &crewSyncFailure{Status: 409, Code: "handback_capture", Message: "this agent's server conversation could not be read yet; it stays on the server for now"}
		}
		item.Conversation = conversation
		var bytes int64
		if conversation.Native != nil {
			for _, file := range conversation.Native.Files {
				bytes += file.Size
			}
		}
		crewHandbackLog(id, tab, "handback_staged", "", bytes, started)
		return out, nil
	}

	// handback_done: flip the writer back to the Mac, only if nothing ran here since the Mac took the conversations.
	lock := crewStandbyLock(id, owner)
	lock.Lock()
	defer lock.Unlock()
	if rows, err = readCrewStandby(id, owner); err != nil {
		return out, err
	}
	row, ok := rows[tab]
	if !ok || row.Writer != "server" {
		reply.Writer = row.Writer
		return out, nil
	}
	// Under the lock crewStandbyRoute decides under: a call routed here before this point is in flight, running or
	// queued, and one routed after it sees the flip and goes to the Mac.
	sessions = api.crewHandbackSessions(ctx, roots, binding, owner, tab)
	if api.crewHandbackBusy(id, owner, tab, sessions) {
		reply.Busy = true
		crewHandbackLog(id, tab, "handback_retry", "server_busy", 0, started)
		return out, nil
	}
	taken := map[string]crewStandbySendItem{}
	for _, item := range in.Standby.Sessions {
		taken[item.Key] = item
	}
	for _, s := range sessions {
		if item, ok := taken[s.macKey]; !ok || item.Fingerprint != crewHandbackMark(owner, s.record) {
			reply.Event = "changed"
			crewHandbackLog(id, tab, "handback_retry", "server_changed", 0, started)
			return out, nil
		}
	}
	diverged := 0
	for _, s := range sessions {
		if !taken[s.macKey].Diverged {
			continue
		}
		diverged++
		// The Mac kept its own version. The server's stays in this chat's earlier conversations; the chat itself starts
		// from the Mac's next copy, so a later import is not checked against a version the Mac does not have.
		if err := rotateCrewHandbackRecord(ctx, owner, binding.PairID, s.record); err != nil {
			log.Printf("[ALWAYS_ON] project=%s tab=%s event=handback_rotate_failed", id, tab)
		}
		if session, ok := row.Sessions[s.macKey]; ok {
			session.Imported = 0
			row.Sessions[s.macKey] = session
		}
	}
	row.Writer, row.WriterRevision, row.WriterSince = "", 0, ""
	if diverged > 0 {
		row.KeptAt = time.Now().UTC().Format(time.RFC3339)
	}
	rows[tab] = row
	if err = writeCrewStandby(id, owner, rows); err != nil {
		return out, err
	}
	reply.Writer, reply.Event = "", "done"
	crewHandbackLog(id, tab, "handback_done", fmt.Sprintf("conversations_%d", len(sessions)), 0, started)
	if diverged > 0 {
		crewHandbackLog(id, tab, "handback_diverged", fmt.Sprintf("conversations_%d", diverged), 0, started)
		notifyCrewHandbackKept(id, owner, tab)
	}
	// An agent that turned Only me or left the Team while the server answered for it: handed back, its copy goes.
	if ok, reason := crewStandbyEligible(id, owner, tab); !ok {
		dropCrewStandbyTab(id, owner, tab, reason)
	}
	return out, nil
}

// rotateCrewHandbackRecord starts the server's chat afresh after a hand back that kept both versions: the server's
// version moves to the chat's earlier conversations (its history stays on disk).
func rotateCrewHandbackRecord(ctx context.Context, owner, pair string, record ProductConversationRecord) error {
	store := defaultProductConversationRegistryStore()
	path := productConversationRegistryPath(owner)
	lock := productConversationRegistryMutex(path)
	lock.Lock()
	defer lock.Unlock()
	doc, err := store.loadDocument(ctx, path)
	if err != nil {
		return err
	}
	entry := productConversationRegistryEntryKey(crewProfileID, record.ConversationKey)
	current, ok := doc.Entries[entry]
	if !ok || current.SessionID != record.SessionID {
		return nil
	}
	rememberPreviousConversation(&doc, entry, current)
	now := time.Now().UTC()
	current.SessionID = "product-" + uuid.NewSHA1(uuid.NameSpaceOID, []byte(pair+"/handback-kept/"+record.ConversationKey+"/"+now.Format(time.RFC3339Nano))).String()
	current.UpdatedAt = now.Format(time.RFC3339Nano)
	doc.Entries[entry] = current
	return store.writeDocument(ctx, path, doc)
}

func crewHandbackAgentName(project, person, tab string) string {
	if person != ownerOfCrewProjectID(project) {
		// A member's agents are named by the list their Mac last sent.
		if saved, ok := readSavedCrewMacTabs(project, person); ok {
			for _, t := range saved {
				if t.Tab == tab && strings.TrimSpace(t.Name) != "" {
					return strings.TrimSpace(t.Name)
				}
			}
		}
	}
	for _, agent := range readMacSharedAgents(project) {
		if person == ownerOfCrewProjectID(project) && agent.Tab == tab && strings.TrimSpace(agent.Name) != "" {
			return strings.TrimSpace(agent.Name)
		}
	}
	if tab == macMainTab {
		return "Main"
	}
	return "This agent"
}

// notifyCrewHandbackKept tells the agent's person (owner is the owner of that Mac agent, the Crew's owner or a member)
// once, in plain words, that the agent answered in both places.
func notifyCrewHandbackKept(project, owner, tab string) {
	name := crewHandbackAgentName(project, owner, tab)
	notifyEnqueue(notifyJob{event: "always_on_kept", direct: []notifyDirect{{user: owner, msg: notifyMessage{
		Title: name + " answered in two places",
		Body:  "While your Mac was off, " + name + " also answered on the server. Both versions are kept; your Mac's continues, and the server's is saved on your Mac.",
		Route: "/crew/projects/" + url.PathEscape(project),
	}}}})
}

// crewHandbackKeptNote is the Team card's line for the agent's person after a hand back that kept both versions.
func crewHandbackKeptNote(project, person, tab string) string {
	rows, err := readCrewStandby(project, person)
	if err != nil {
		return ""
	}
	at, err := time.Parse(time.RFC3339, rows[tab].KeptAt)
	if err != nil || time.Since(at) > crewHandbackKeptFor {
		return ""
	}
	return "While your Mac was off, it also answered on the server. Both versions are kept; your Mac's continues"
}

// offerCrewHandbacks tells a Mac that just connected which of its agents the server answers for, so it takes them
// back now (desktopCrewRelaySession, "handback").
func offerCrewHandbacks(sock *crewMacSocket) {
	sock.mu.Lock()
	twins := map[string]string{}
	for project, pair := range sock.twins {
		twins[project] = pair
	}
	sock.mu.Unlock()
	for project, pair := range twins {
		// The connecting person's own rows, by the pair this connection was admitted with (owner or joined member).
		rows, err := readCrewStandby(project, sock.owner)
		if err != nil {
			continue
		}
		for tab, row := range rows {
			if row.User != sock.owner || row.Writer != "server" || row.PairID != pair {
				continue
			}
			if sock.send != nil && sock.send(crewRelayMessage{Type: "handback", Project: project, State: tab}) == nil {
				crewHandbackLog(project, tab, "handback_offered", "mac_online", 0, time.Now())
			}
		}
	}
}
