package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
)

// This is a private continuation checkpoint, never a mailbox or a source of
// message authority. Each wake re-reads the real inbox through the exact saved
// login and current physical recipient record.
func (api *StreamingAPI) desktopTaskPath(session string) (string, error) {
	api.lastQueryMu.RLock()
	req, ok := api.lastQueryRequests[session]
	api.lastQueryMu.RUnlock()
	if !ok || req.userID == "" {
		return "", fmt.Errorf("chat owner unavailable")
	}
	return desktopTaskPathFor(req.userID, session)
}

// desktopTaskPathFor is the same checkpoint file for a known owner; the startup scan has the owner from the restart
// record, before any turn has filled lastQueryRequests.
func desktopTaskPathFor(userID, session string) (string, error) {
	root, err := workflowCLIStateRoot()
	if err != nil {
		return "", err
	}
	key := sha256.Sum256([]byte(userID + "\x00" + session))
	return filepath.Join(root, "agent-task-checkpoints", fmt.Sprintf("%x.json", key)), nil
}
func (api *StreamingAPI) readContinueTaskStore(session string) (agentMessageStore, error) {
	if !IsLocalMode() {
		return readAgentMessageStore(context.Background())
	}
	// Same-laptop requests are authoritative in the ordinary mailbox, not
	// saved-network checkpoints. Read both; never copy mailbox rows into the
	// checkpoint file (which would hide later replies).
	store, err := readAgentMessageStore(context.Background())
	if err != nil {
		return store, err
	}
	path, err := api.desktopTaskPath(session)
	if err != nil {
		return store, err
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return store, nil
	}
	var checkpoints agentMessageStore
	if err == nil {
		err = json.Unmarshal(raw, &checkpoints)
	}
	if err == nil {
		store.Conversations = append(store.Conversations, checkpoints.Conversations...)
	}
	return store, err
}
func (api *StreamingAPI) saveContinueTaskStore(session string, store agentMessageStore) error {
	if !IsLocalMode() {
		return saveAgentMessageStore(context.Background(), store)
	}
	mailbox := store
	mailbox.Conversations = nil
	var checkpoints agentMessageStore
	for _, c := range store.Conversations {
		if strings.HasPrefix(c.ID, "network:") {
			checkpoints.Conversations = append(checkpoints.Conversations, c)
		} else {
			mailbox.Conversations = append(mailbox.Conversations, c)
		}
	}
	path, err := api.desktopTaskPath(session)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err = fsutil.WriteJSONAtomic(path, checkpoints, 0600); err != nil {
		return err
	}
	return saveAgentMessageStore(context.Background(), mailbox)
}
func (api *StreamingAPI) checkpointDesktopAgentTask(ctx context.Context, owner, connection string, record ProductConversationRecord, message desktopAppIncoming) bool {
	thread := "network:" + connection + ":" + message.Inbox
	note := func(stage string) {
		log.Printf("[AGENT_CONTINUE] reason=open_task event=checkpoint_%s thread=%s task=%s session=%s kind=%s", stage, thread, message.ID, record.SessionID, desktopTaskMessageKind(message.MessageType))
	}
	if message.MessageType != "request" || message.ID == "" {
		note("skipped")
		return false
	}
	// Never take a session or path from a sender's JSON.
	current, err := desktopAppAgentRecipient(ctx, owner, record)
	if err != nil {
		note("recipient_refused")
		return false
	}
	agentMessageMu.Lock()
	defer agentMessageMu.Unlock()
	store, err := api.readContinueTaskStore(current.SessionID)
	if err != nil {
		note("read_failed")
		return false
	}
	inbox := "network:" + connection + ":" + message.Inbox
	for _, c := range store.Conversations {
		for _, m := range c.Messages {
			if c.ID == inbox && m.ID == message.ID {
				note("exists")
				return true
			}
		}
	}
	progress := agentTaskProgress{Session: current.SessionID, LocalOwner: owner, LocalRecord: &current, NextWake: time.Now().Add(time.Second)}
	store.Conversations = append(store.Conversations, agentConversation{ID: inbox, Endpoints: [2]agentMessageEndpoint{{UserID: message.SenderUser, Label: message.Sender}, {UserID: owner, Session: current.SessionID, ChatKey: current.ConversationKey}}, Messages: []agentMessage{{ID: message.ID, From: 0, Sequence: 1, MessageType: "request", Text: message.Text, Delivery: "delivered", TaskProgress: progress}}})
	if api.saveContinueTaskStore(current.SessionID, store) != nil {
		note("write_failed")
		return false
	}
	note("saved")
	return true
}
func desktopTaskCaller(record ProductConversationRecord) triggerLinkCaller {
	return triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerCrew, ID: record.ResourceID, ProfileID: record.ProfileID}, Label: record.Title, Chat: &codeChat{Key: record.ConversationKey, SessionID: record.SessionID}}
}
func (api *StreamingAPI) refreshDesktopTaskAuthority(c agentConversation, m agentMessage) error {
	p := m.TaskProgress
	if p.LocalRecord == nil || p.LocalOwner == "" {
		return fmt.Errorf("task checkpoint missing recipient")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, UserContextKey, &UserClaims{UserID: p.LocalOwner, Username: p.LocalOwner})
	record, err := desktopAppAgentRecipient(ctx, p.LocalOwner, *p.LocalRecord)
	if err != nil || record.SessionID != p.Session {
		return fmt.Errorf("task recipient changed")
	}
	out, err := api.desktopAgentNetworkMessage(ctx, p.LocalOwner, desktopTaskCaller(record), c.ID, desktopAgentNetworkRequest{Read: true, Limit: 1})
	if err != nil {
		return err
	}
	states, ok := desktopTaskStatesFrom(out)
	if !ok {
		return fmt.Errorf("task state unavailable")
	}
	if state, found := states[m.ID]; !found || state.State == "" {
		return fmt.Errorf("task is not currently admitted/delivered")
	}
	return applyDesktopTaskStates(p.LocalOwner, p.Session, c.ID, states)
}

// desktopTaskRemoteState is the network server's current state of one request (its request_tasks entry).
type desktopTaskRemoteState struct {
	State string    `json:"state"`
	Due   time.Time `json:"due"`
}

func desktopTaskStatesFrom(out map[string]any) (map[string]desktopTaskRemoteState, bool) {
	raw, _ := json.Marshal(out["request_tasks"])
	var states map[string]desktopTaskRemoteState
	if json.Unmarshal(raw, &states) != nil || states == nil {
		return nil, false
	}
	return states, true
}

// applyDesktopTaskStates saves the server's current state of every request of one thread in the chat's checkpoint.
// A request closed on the network (a done reply, or closed by its sender) otherwise stays open in the checkpoint
// and wakes the chat after every restart. It writes the checkpoint file directly, so it works before the chat's first
// turn after a restart. The caller must not hold agentMessageMu.
func applyDesktopTaskStates(owner, session, thread string, states map[string]desktopTaskRemoteState) error {
	path, err := desktopTaskPathFor(owner, session)
	if err != nil {
		return err
	}
	agentMessageMu.Lock()
	defer agentMessageMu.Unlock()
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var store agentMessageStore
	if err = json.Unmarshal(raw, &store); err != nil {
		return err
	}
	changed := 0
	for i := range store.Conversations {
		if store.Conversations[i].ID != thread {
			continue
		}
		for j := range store.Conversations[i].Messages {
			task := &store.Conversations[i].Messages[j]
			state, found := states[task.ID]
			if !found || state.State == "" || (task.TaskProgress.RemoteState == state.State && task.TaskProgress.RemoteDue.Equal(state.Due)) {
				continue
			}
			task.TaskProgress.RemoteState, task.TaskProgress.RemoteDue = state.State, state.Due
			changed++
		}
	}
	if changed == 0 {
		return nil
	}
	log.Printf("[AGENT_CONTINUE] reason=task_state_refresh session=%s thread=%s updated=%d", session, thread, changed)
	return fsutil.WriteJSONAtomic(path, store, 0600)
}

// refreshDesktopThreadStates asks the network server for one thread's current request states on behalf of the chat
// that received them (its recipient record is rechecked), for the startup scan, which runs before any turn.
func (api *StreamingAPI) refreshDesktopThreadStates(owner string, local ProductConversationRecord, session, thread string) error {
	if api.desktopThreadStates != nil {
		states, err := api.desktopThreadStates(owner, local, thread)
		if err != nil {
			return err
		}
		return applyDesktopTaskStates(owner, session, thread, states)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, UserContextKey, &UserClaims{UserID: owner, Username: owner})
	record, err := desktopAppAgentRecipient(ctx, owner, local)
	if err != nil || record.SessionID != session {
		return fmt.Errorf("task recipient changed")
	}
	out, err := api.desktopAgentNetworkMessage(ctx, owner, desktopTaskCaller(record), thread, desktopAgentNetworkRequest{Read: true, Limit: 1, serverRefresh: true})
	if err != nil {
		return err
	}
	states, ok := desktopTaskStatesFrom(out)
	if !ok {
		return fmt.Errorf("task state unavailable")
	}
	return applyDesktopTaskStates(owner, session, thread, states)
}
func (api *StreamingAPI) recheckContinueTask(c agentConversation, m agentMessage) error {
	if IsLocalMode() && strings.HasPrefix(c.ID, "network:") {
		return api.refreshDesktopTaskAuthority(c, m)
	}
	return api.recheckAgentMessageAdmission(context.Background(), c, m)
}

func (api *StreamingAPI) forwardDesktopTaskFinal(c agentConversation, request agentMessage, event, text string) {
	p := request.TaskProgress
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, UserContextKey, &UserClaims{UserID: p.LocalOwner, Username: p.LocalOwner})
	if api.refreshDesktopTaskAuthority(c, request) != nil {
		return
	}
	record, err := desktopAppAgentRecipient(ctx, p.LocalOwner, *p.LocalRecord)
	if err != nil {
		return
	}
	label := record.Title
	_, err = api.desktopAgentNetworkMessage(ctx, p.LocalOwner, desktopTaskCaller(record), c.ID, desktopAgentNetworkRequest{AutomaticFor: request.ID, Status: "in_progress", SubmissionID: "task-final:" + request.ID + ":" + event, Message: "Auto-forwarded from " + label + "'s chat (not a reply)\n\n" + text})
	if err == nil {
		log.Printf("[AGENT-MESSAGE] event=final_text_forwarded thread=%s task=%s", c.ID, request.ID)
	}
}

func (api *StreamingAPI) publishDesktopTaskStalled(session, thread, id string) {
	agentMessageMu.Lock()
	store, err := api.readContinueTaskStore(session)
	agentMessageMu.Unlock()
	if err != nil {
		return
	}
	for _, c := range store.Conversations {
		for _, m := range c.Messages {
			p := m.TaskProgress
			if c.ID != thread || m.ID != id || p.NoProgress < 2 || p.LocalRecord == nil {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			ctx = context.WithValue(ctx, UserContextKey, &UserClaims{UserID: p.LocalOwner, Username: p.LocalOwner})
			_, _ = api.desktopAgentNetworkMessage(ctx, p.LocalOwner, desktopTaskCaller(*p.LocalRecord), thread, desktopAgentNetworkRequest{ProgressTask: id})
			cancel()
		}
	}
}

func desktopTaskMessageKind(kind string) string {
	switch kind {
	case "request", "notification":
		return kind
	}
	return "unknown"
}

// A query may finish before this receipt, or may still be queued/running.
// Check only after the server committed delivery; the normal busy/Stop/current
// request gates decide whether to run now or wait for the real turn to end.
func (api *StreamingAPI) desktopTaskDeliveryAcknowledged(ctx context.Context, owner, connection string, record ProductConversationRecord, message desktopAppIncoming) {
	if message.MessageType != "request" {
		return
	}
	if api.checkpointDesktopAgentTask(ctx, owner, connection, record, message) {
		api.scheduleAgentTaskCheck(record.SessionID, time.Now())
	}
}
