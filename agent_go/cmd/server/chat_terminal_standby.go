package server

// A paired standby must remember the same exact conversation is out, even
// after the Mac disconnects. This is not an agent-wide availability switch.
import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

const chatTerminalPeerNotice = "This chat is open in its owner's terminal right now; try again later or ask the owner."

type chatTerminalStandby struct {
	Peer         string            `json:"peer"`
	Key          string            `json:"key"`
	Conversation string            `json:"conversation"`
	State        chatTerminalState `json:"state"`
}

func terminalStandbyPath(project, person string) (string, error) {
	root, e := crewroot.StateRoot()
	if e != nil {
		return "", e
	}
	return filepath.Join(root, "chat-terminal-standby", crewTransferDigest(project+"\x00"+person)+".json"), nil
}
func readTerminalStandby(project, person string) (map[string]chatTerminalStandby, error) {
	p, e := terminalStandbyPath(project, person)
	if e != nil {
		return nil, e
	}
	raw, e := os.ReadFile(p)
	if os.IsNotExist(e) {
		return map[string]chatTerminalStandby{}, nil
	}
	if e != nil {
		return nil, e
	}
	var entries map[string]chatTerminalStandby
	e = json.Unmarshal(raw, &entries)
	if entries == nil && e == nil {
		entries = map[string]chatTerminalStandby{}
	}
	return entries, e
}
func writeTerminalStandby(project, person string, entries map[string]chatTerminalStandby) error {
	p, e := terminalStandbyPath(project, person)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		return e
	}
	return fsutil.WriteJSONAtomic(p, entries, 0600)
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
	_, e = desktopCrewSyncCall(ctx, claims.UserID, &connection, binding.PeerProjectID, crewSyncRequest{Action: "terminal_marker", PairID: binding.PairID, Terminal: marker})
	if e != nil {
		log.Printf("[CHAT_TERMINAL_RESUME] chat=%s session=%s action=refused reason=standby_marker_unacknowledged", c.ConversationKey, state.Native)
	}
	return e
}

// The ordinary pair/member admission is retained. Incoming identities select
// only that person's conversation, never another member's native home.
func (api *StreamingAPI) receiveTerminalStandby(ctx context.Context, claims *UserClaims, project string, in crewSyncRequest) (crewSyncReply, error) {
	m := in.Terminal
	if m == nil || m.Conversation == "" || !strings.HasPrefix(m.Conversation, "conversation-") || !productConversationKeyPattern.MatchString(m.Conversation) || m.State.Receipt == "" || m.State.Revision <= 0 {
		return crewSyncReply{}, fmt.Errorf("choose your own exact conversation")
	}
	_, binding, e := api.crewStandbyAdmit(ctx, claims, project, in.PairID, m.Peer)
	if e != nil {
		return crewSyncReply{}, e
	}
	if binding == nil || crewStandbyKeyTab(binding.PeerProjectID, m.Key) == "" {
		return crewSyncReply{}, fmt.Errorf("choose a conversation in your paired project")
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
	if prior.State.Revision > m.State.Revision {
		return crewSyncReply{}, fmt.Errorf("this handoff was superseded; recover the chat in AgentWorks")
	}
	if prior.State.Revision == m.State.Revision && prior.State.Receipt != m.State.Receipt {
		return crewSyncReply{}, fmt.Errorf("this handoff changed; recover the chat in AgentWorks")
	}
	// Admission and saving share the existing standby lock with lazy import and
	// takeover. Fence every already-imported copy of precisely this conversation.
	store := defaultProductConversationRegistryStore()
	doc, e := store.loadDocument(ctx, productConversationRegistryPath(claims.UserID))
	if e != nil {
		return crewSyncReply{}, e
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
	return crewSyncReply{ProjectID: project}, nil
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
		old, e := readChatTerminal(c.SessionID)
		if e != nil {
			return e
		}
		if old.Revision > state.Revision {
			continue
		}
		if e = saveChatTerminal(state); e != nil {
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
		return "This chat's terminal handoff could not be checked; ask its owner."
	}
	for _, m := range entries {
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
			m.State.Revision = time.Now().UnixNano()
			entries[id] = m
		}
	}
	return writeTerminalStandby(state.MirrorProject, state.Owner, entries)
}
