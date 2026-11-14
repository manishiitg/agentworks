package server

// A paired standby must remember the same exact conversation is out, even
// after the Mac disconnects. This is not an agent-wide availability switch.
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

const chatTerminalPeerNotice = "This chat is open in its owner's terminal right now; try again later or ask the owner."

type chatTerminalStandby struct {
	Peer         string            `json:"peer"`
	Key          string            `json:"key"`
	Conversation string            `json:"conversation"`
	State        chatTerminalState `json:"state"`
	corruptFile  string
}

func terminalStandbyPath(project, person string) (string, error) {
	root, e := crewroot.StateRoot()
	if e != nil {
		return "", e
	}
	return filepath.Join(root, "chat-terminal-standby", crewTransferDigest(project+"\x00"+person)), nil
}
func readTerminalStandby(project, person string) (map[string]chatTerminalStandby, error) {
	p, e := terminalStandbyPath(project, person)
	if e != nil {
		return nil, e
	}
	files, e := os.ReadDir(p)
	if os.IsNotExist(e) {
		return map[string]chatTerminalStandby{}, nil
	}
	if e != nil {
		return nil, e
	}
	entries := map[string]chatTerminalStandby{}
	for _, file := range files {
		if file.IsDir() {
			continue
		}
		var m chatTerminalStandby
		raw, err := os.ReadFile(filepath.Join(p, file.Name()))
		if err != nil || json.Unmarshal(raw, &m) != nil || m.Key == "" || file.Name() != crewTransferDigest(m.Key)+".json" {
			log.Printf("[CHAT_TERMINAL_RESUME] chat=%s session= action=refused reason=standby_marker_unreadable", file.Name())
			entries["broken:"+file.Name()] = chatTerminalStandby{corruptFile: file.Name()}
			continue
		}
		entries[m.Conversation] = m
	}
	return entries, nil
}
func writeTerminalStandby(project, person string, entries map[string]chatTerminalStandby) error {
	p, e := terminalStandbyPath(project, person)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(p, 0700); e != nil {
		return e
	}
	for _, m := range entries {
		if m.corruptFile != "" {
			continue
		}
		if e = fsutil.WriteJSONAtomic(filepath.Join(p, crewTransferDigest(m.Key)+".json"), m, 0600); e != nil {
			return e
		}
	}
	return nil
}

var errTerminalStandbySuperseded = errors.New("this handoff was superseded; the server's current state was adopted")

func adoptTerminalStandbyReply(c ProductConversationRecord, local chatTerminalState, reply *chatTerminalStandby) error {
	if reply == nil || reply.Conversation != c.ConversationID {
		return fmt.Errorf("the server did not acknowledge this exact conversation")
	}
	changed := reply.State.Receipt != local.Receipt || reply.State.Out != local.Out
	local.Revision = reply.State.Revision
	if changed {
		local.Receipt = reply.State.Receipt
		local.Out = reply.State.Out
	}
	if e := saveChatTerminal(local); e != nil {
		return e
	}
	if changed {
		log.Printf("[CHAT_TERMINAL_RESUME] chat=%s session=%s action=resumed reason=standby_superseded", c.ConversationKey, local.Native)
		return errTerminalStandbySuperseded
	}
	return nil
}

// Called only after own-chat admission and while holding that chat's input
// lane. Fetch may not launch a terminal until this synchronous acknowledgement.
func (api *StreamingAPI) syncTerminalStandby(ctx context.Context, c ProductConversationRecord, state chatTerminalState) error {
	if !IsLocalMode() {
		return nil
	}
	project := crewResourceProjectID(c.ConversationKey)
	b, e := crewroot.ReadSync(project)
	if e != nil {
		return e
	}
	if b == nil || b.Side != "local" || b.Mode != "relay" {
		return nil
	}
	claims, binding, connection, e := api.desktopCrewChatBinding(ctx, project)
	if e != nil {
		return e
	}
	key := crewStandbyWireKey(project, c.ConversationKey)
	marker := &chatTerminalStandby{Peer: project, Key: key, Conversation: c.ConversationID, State: state}
	reply, e := desktopCrewSyncCall(ctx, claims.UserID, &connection, binding.PeerProjectID, crewSyncRequest{Action: "terminal_marker", PairID: binding.PairID, Terminal: marker})
	if e != nil {
		log.Printf("[CHAT_TERMINAL_RESUME] chat=%s session=%s action=refused reason=standby_marker_unacknowledged", c.ConversationKey, state.Native)
	}
	if e != nil {
		return e
	}
	return adoptTerminalStandbyReply(c, state, reply.Terminal)
}

// The ordinary pair/member admission is retained. Incoming identities select
// only that person's conversation, never another member's native home.
func (api *StreamingAPI) receiveTerminalStandby(ctx context.Context, claims *UserClaims, project string, in crewSyncRequest) (crewSyncReply, error) {
	m := in.Terminal
	if m == nil || m.Conversation == "" || !strings.HasPrefix(m.Conversation, "conversation-") || !productConversationKeyPattern.MatchString(m.Conversation) || m.State.Receipt == "" {
		return crewSyncReply{}, fmt.Errorf("choose your own exact conversation")
	}
	_, binding, e := api.crewStandbyAdmit(ctx, claims, project, in.PairID, m.Peer)
	if e != nil {
		return crewSyncReply{}, e
	}
	if binding == nil || crewStandbyKeyTab(binding.PeerProjectID, m.Key) == "" {
		return crewSyncReply{}, fmt.Errorf("choose a conversation in your paired project")
	}
	// Create the recoverable mirror before publishing its pause, never a
	// marker whose only recovery path would itself be refused before import.
	if m.State.Out {
		if eligible, _ := crewStandbyEligible(project, claims.UserID, crewStandbyKeyTab(binding.PeerProjectID, m.Key)); !eligible {
			return crewSyncReply{ProjectID: project, Terminal: m}, nil
		}
		doc, err := defaultProductConversationRegistryStore().loadDocument(ctx, productConversationRegistryPath(claims.UserID))
		if err != nil {
			return crewSyncReply{}, err
		}
		mirror := false
		for _, c := range doc.Entries {
			if c.ProfileID == crewProfileID && c.ConversationID == m.Conversation && crewResourceProjectID(c.ConversationKey) == project {
				mirror = true
				break
			}
		}
		if !mirror {
			if err = api.importCrewStandby(ctx, claims.UserID, project, crewStandbyKeyTab(binding.PeerProjectID, m.Key), claims.UserID); err != nil {
				return crewSyncReply{}, fmt.Errorf("this conversation's standby copy is not ready yet; keep using it here")
			}
		}
	}
	m.State.Owner = claims.UserID // The paired server sign-in, never the Mac login name.
	lock := crewStandbyLock(project, claims.UserID)
	lock.Lock()
	defer lock.Unlock()
	entries, e := readTerminalStandby(project, claims.UserID)
	if e != nil {
		return crewSyncReply{}, e
	}
	prior := entries[m.Conversation]
	if prior.State.Receipt == m.State.Receipt && prior.State.Out == m.State.Out {
		return crewSyncReply{ProjectID: project, Terminal: &prior}, nil
	}
	if prior.State.Receipt != "" && prior.State.Revision != m.State.Revision {
		// Return authoritative state, not a retryable failure. The Mac adopts
		// it and invalidates its old terminal receipt without comparing clocks.
		return crewSyncReply{ProjectID: project, Terminal: &prior}, nil
	}
	m.State.Revision = prior.State.Revision + 1 // Assigned by this server under its standby lock.
	// Admission and saving share the existing standby lock with lazy import and
	// takeover. Fence every already-imported copy of precisely this conversation.
	store := defaultProductConversationRegistryStore()
	doc, e := store.loadDocument(ctx, productConversationRegistryPath(claims.UserID))
	if e != nil {
		return crewSyncReply{}, e
	}
	mirror := false
	for _, c := range doc.Entries {
		if c.ProfileID == crewProfileID && c.ConversationID == m.Conversation && crewResourceProjectID(c.ConversationKey) == project {
			mirror = true
			break
		}
	}
	if m.State.Out && !mirror {
		return crewSyncReply{}, fmt.Errorf("the server has not imported this conversation yet; keep using it here until its standby copy is ready")
	}
	var releases []func()
	defer func() {
		for _, release := range releases {
			release()
		}
	}()
	for _, c := range doc.Entries {
		if c.ProfileID != crewProfileID || c.ConversationID != m.Conversation || crewResourceProjectID(c.ConversationKey) != project {
			continue
		}
		release, locked := api.tryLockSessionInputLane(c.SessionID, 0)
		if !locked {
			return crewSyncReply{}, fmt.Errorf("wait for this conversation's current turn to finish")
		}
		releases = append(releases, release)
		if api.chatTerminalWorkRunning(c.SessionID) || api.conversationTurnHasBacklog(claims.UserID, c.SessionID) {
			return crewSyncReply{}, fmt.Errorf("wait for this conversation's background work and queued messages to finish")
		}
	}
	entries[m.Conversation] = *m
	if e = writeTerminalStandby(project, claims.UserID, entries); e != nil {
		return crewSyncReply{}, e
	}
	if e = applyTerminalStandby(ctx, project, claims.UserID, *m); e != nil {
		return crewSyncReply{}, e
	}
	log.Printf("[CHAT_TERMINAL_RESUME] chat=%s session=%s action=written reason=standby_marker out=%t", m.Key, m.State.Native, m.State.Out)
	return crewSyncReply{ProjectID: project, Terminal: m}, nil
}

func applyTerminalStandby(ctx context.Context, project, person string, m chatTerminalStandby) error {
	doc, e := defaultProductConversationRegistryStore().loadDocument(ctx, productConversationRegistryPath(person))
	if e != nil {
		return e
	}
	for _, c := range doc.Entries {
		if c.ProfileID != crewProfileID || c.ConversationID != m.Conversation || crewResourceProjectID(c.ConversationKey) != project {
			continue
		}
		state := m.State
		state.Chat = c.ConversationKey
		state.Session = c.SessionID
		state.MirrorProject = project
		if e := saveChatTerminal(state); e != nil {
			return e
		}
		if state.Out {
			dropQueuedNativeWakes(c.SessionID, "continued_in_terminal")
		}
	}
	return nil
}

// Called under the standby lock after lazy import. An imported source must not
// briefly become writable just because its platform session ID changed.
func applyImportedTerminalStandby(ctx context.Context, project, person, conversation string) error {
	entries, e := readTerminalStandby(project, person)
	if e != nil {
		return e
	}
	if m, ok := entries[conversation]; ok {
		return applyTerminalStandby(ctx, project, person, m)
	}
	return nil
}

func terminalStandbyCallRefusal(project, person, tab, sender string, ownChat bool) string {
	entries, e := readTerminalStandby(project, person)
	if e != nil {
		log.Printf("[CHAT_TERMINAL_RESUME] chat= session= action=refused reason=standby_directory_unreadable")
		return ""
	}
	peer := ""
	if person == ownerOfCrewProjectID(project) {
		if b, err := crewroot.ReadSync(project); err == nil && b != nil {
			peer = b.PeerProjectID
		}
	} else if rows, err := readCrewStandby(project, person); err == nil {
		peer = rows[tab].Peer
	}
	for _, m := range entries {
		if m.corruptFile != "" {
			key := macSharedAgentChatKey(peer, tab)
			if !ownChat {
				key = sharedThreadKey(peer, tab, sender)
			}
			if peer != "" && m.corruptFile == crewTransferDigest(key)+".json" {
				return "This chat's terminal handoff could not be checked; ask its owner."
			}
			continue
		}
		key := macSharedAgentChatKey(m.Peer, tab)
		if !ownChat {
			key = sharedThreadKey(m.Peer, tab, sender)
		}
		if m.Key == key && m.State.Out {
			log.Printf("[CHAT_TERMINAL_RESUME] chat=%s session=%s action=refused reason=standby_continued_in_terminal", m.Conversation, m.State.Native)
			return chatTerminalPeerNotice
		}
	}
	return ""
}
func terminalStandbyAgentOut(project, person, tab string) bool {
	entries, e := readTerminalStandby(project, person)
	if e != nil {
		return false
	}
	for _, m := range entries {
		if m.State.Out && crewStandbyKeyTab(m.Peer, m.Key) == tab {
			return true
		}
	}
	doc, err := defaultProductConversationRegistryStore().loadDocument(context.Background(), productConversationRegistryPath(person))
	if err == nil {
		for _, c := range doc.Entries {
			if c.ProfileID == crewProfileID && crewResourceProjectID(c.ConversationKey) == project && crewAgentPublishTab(crewStandbyWireKey(project, c.ConversationKey)) == tab {
				if state, e := readChatTerminal(c.SessionID); e == nil && state.Out {
					return true
				}
			}
		}
	}
	return false
}

// Recovering the imported copy also supersedes the retained mirror record;
// an old Mac's repeated standby offer may not resurrect the terminal receipt.
func clearTerminalStandby(state chatTerminalState) error {
	if state.MirrorProject == "" {
		return nil
	}
	lock := crewStandbyLock(state.MirrorProject, state.Owner)
	lock.Lock()
	defer lock.Unlock()
	entries, e := readTerminalStandby(state.MirrorProject, state.Owner)
	if e != nil {
		return e
	}
	for id, m := range entries {
		if m.State.Receipt == state.Receipt {
			m.State.Out = false
			m.State.Revision++
			m.State.Receipt = uuid.NewString()
			entries[id] = m
		}
	}
	return writeTerminalStandby(state.MirrorProject, state.Owner, entries)
}
