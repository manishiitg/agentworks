package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

// Conversations have addresses and messages, never request/result settlement.
// The store is durable through the workspace backend; transport acknowledgements
// do not claim that a recipient read, answered, or completed any work.
const agentMessagesPath = "_system/agent_messages/conversations.json"
const agentMessageKeep = 1000
const agentConversationKeep = 2000

var agentMessageMu sync.Mutex
var agentMessageWorkers sync.Map
var agentMessageLanes sync.Map

type agentMessageEndpoint struct {
	UserID         string `json:"user_id"`
	Kind           string `json:"kind"`
	ID             string `json:"id"`
	Profile        string `json:"profile,omitempty"`
	Path           string `json:"path,omitempty"`
	Label          string `json:"label"`
	Session        string `json:"session,omitempty"`
	ChatKey        string `json:"chat_key,omitempty"`
	External       bool   `json:"external,omitempty"`
	Mode           string `json:"mode,omitempty"`
	Guest          string `json:"guest,omitempty"`
	FunctionCallID string `json:"function_call_id,omitempty"`
}
type agentMessage struct {
	ID         string    `json:"message_id"`
	Sequence   int       `json:"sequence"`
	From       int       `json:"from"`
	Text       string    `json:"message"`
	SentAt     time.Time `json:"sent_at"`
	Submission string    `json:"submission_id,omitempty"`
	Delivery   string    `json:"delivery"`
	Error      string    `json:"delivery_error,omitempty"`
	Wakeup     bool      `json:"wakeup,omitempty"`
}
type agentConversation struct {
	ID        string                  `json:"inbox_id"`
	Endpoints [2]agentMessageEndpoint `json:"endpoints"`
	Messages  []agentMessage          `json:"messages"`
	Next      int                     `json:"next_sequence"`
}
type agentMessageStore struct {
	Conversations []agentConversation `json:"conversations"`
	Wakeups       []agentWakeup       `json:"wakeups,omitempty"`
}

func readAgentMessageStore(ctx context.Context) (agentMessageStore, error) {
	var s agentMessageStore
	raw, exists, err := readFileFromWorkspace(ctx, agentMessagesPath)
	if err != nil {
		return s, err
	}
	if exists {
		err = json.Unmarshal([]byte(raw), &s)
	}
	return s, err
}
func saveAgentMessageStore(ctx context.Context, s agentMessageStore) error {
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return writeFileToWorkspace(ctx, agentMessagesPath, string(raw))
}
func callerMessageEndpoint(userID string, c triggerLinkCaller) agentMessageEndpoint {
	e := agentMessageEndpoint{UserID: userID, Kind: c.Stamp.Type, ID: c.Stamp.ID, Profile: normalizeInternalProfileID(c.Stamp.ProfileID), Path: agentProfileRuntimeWorkspace(userID, c.Path), Label: c.Label, External: c.Stamp.Type == triggerCallerUser}
	if c.Chat != nil {
		e.Session = c.Chat.SessionID
		e.ChatKey = c.Chat.Key
	}
	return e
}
func targetMessageEndpoint(userID string, t triggerTarget) agentMessageEndpoint {
	e := agentMessageEndpoint{UserID: userID, Kind: t.Kind, ID: t.stampID(), Profile: normalizeInternalProfileID(t.CrewProfile), Path: crewFunctionRoot(context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: userID}), t), Label: t.Label}
	if t.Kind == triggerCallerCrew {
		e.UserID = t.ownerOr(userID)
	}
	if t.Chat != nil {
		e.Session = t.Chat.SessionID
		e.ChatKey = t.Chat.Key
	}
	return e
}
func messageEndpointMatches(e agentMessageEndpoint, userID string, c triggerLinkCaller) bool {
	if e.UserID != userID || e.Kind != c.Stamp.Type || e.ID != c.Stamp.ID {
		return false
	}
	if e.External {
		return true
	}
	if normalizeInternalProfileID(c.Stamp.ProfileID) != e.Profile {
		return false
	}
	if e.Session != "" {
		return c.Chat != nil && e.Session == c.Chat.SessionID
	}
	return false // an undelivered target cannot impersonate a receiving conversation
}
func endpointTarget(e agentMessageEndpoint) triggerTarget {
	t := triggerTarget{Kind: e.Kind, Path: e.Path, Label: e.Label, CrewID: e.ID, CrewProfile: e.Profile, CrewOwner: e.UserID}
	if e.Kind == triggerCallerWorkflow {
		t.Manifest = &WorkflowManifest{ID: e.ID}
	}
	if e.Session != "" {
		t.Chat = &codeChat{Key: e.ChatKey, ID: e.ChatKey, SessionID: e.Session, Name: e.Label}
	}
	return t
}
func validateMessageDestination(ctx context.Context, e agentMessageEndpoint) error {
	if e.External {
		return nil
	}
	t := endpointTarget(e)
	if e.Kind == triggerCallerCrew && e.Profile != codeproduct.ProfileID && crewFreeTextAskOff(ctx, t) {
		return fmt.Errorf("agent messaging is disabled for this Crew; use a declared function")
	}
	if e.Profile == codeproduct.ProfileID && e.Session == "" {
		return fmt.Errorf("Code messages require an authorized chat of your own Code")
	}
	return nil
}

// sendAgentMessage creates an address for a contact, or sends in a previously
// authorized conversation. Only the two exact endpoints can use that address.
func (api *StreamingAPI) sendAgentMessage(ctx context.Context, userID string, caller triggerLinkCaller, target triggerTarget, message, inboxID, submissionID string) (map[string]interface{}, error) {
	message = strings.TrimSpace(message)
	if message == "" || len([]rune(message)) > codeChatMessageMaxRune {
		return nil, fmt.Errorf("message must contain 1 to %d characters", codeChatMessageMaxRune)
	}
	if len(submissionID) > 128 {
		return nil, fmt.Errorf("submission_id is longer than 128 characters")
	}
	agentMessageMu.Lock()
	s, err := readAgentMessageStore(ctx)
	if err != nil {
		agentMessageMu.Unlock()
		return nil, err
	}
	index, side := -1, 0
	if inboxID != "" {
		for i := range s.Conversations {
			if s.Conversations[i].ID == inboxID {
				index = i
				break
			}
		}
		if index < 0 {
			agentMessageMu.Unlock()
			return nil, fmt.Errorf("conversation unavailable or access denied")
		}
		c := &s.Conversations[index]
		if messageEndpointMatches(c.Endpoints[0], userID, caller) {
			side = 0
		} else if messageEndpointMatches(c.Endpoints[1], userID, caller) {
			side = 1
		} else {
			agentMessageMu.Unlock()
			return nil, fmt.Errorf("conversation unavailable or access denied")
		}
	} else {
		if caller.isTarget(target) && caller.Chat == nil {
			agentMessageMu.Unlock()
			return nil, fmt.Errorf("choose another agent or chat")
		}
		e := callerMessageEndpoint(userID, caller)
		e = api.agentMessageCallerRole(e)
		dest := targetMessageEndpoint(userID, target)
		if !e.External && e.Session == "" {
			agentMessageMu.Unlock()
			return nil, fmt.Errorf("sender must have a trusted conversation session")
		}
		// Recover an uncertain external first send only when its explicit
		// submission key, sender and target agree; new contacts otherwise stay separate.
		if e.External && submissionID != "" {
			for i := range s.Conversations {
				prior := &s.Conversations[i]
				peer := prior.Endpoints[1]
				if prior.Endpoints[0] != e || !sameAgentMessageContact(peer, dest) || dest.ChatKey != "" && peer.ChatKey != dest.ChatKey {
					continue
				}
				for _, m := range prior.Messages {
					if m.From == 0 && m.Submission == submissionID {
						if m.Text != message {
							agentMessageMu.Unlock()
							return nil, fmt.Errorf("submission_id already used for a different message")
						}
						if err := validateMessageDestination(ctx, peer); err != nil {
							agentMessageMu.Unlock()
							return nil, err
						}
						receipt := agentMessageReceipt(prior.ID, m)
						agentMessageMu.Unlock()
						return receipt, nil
					}
				}
			}
		}
		// Internal contacts reuse the exact two endpoints. External callers create
		// a fresh inbox unless they explicitly select their previously returned one.
		if !e.External {
			for i := range s.Conversations {
				c := &s.Conversations[i]
				if c.Endpoints[0] == e && sameAgentMessageContact(c.Endpoints[1], dest) {
					index = i
					break
				}
				if c.Endpoints[1] == e && sameAgentMessageContact(c.Endpoints[0], dest) {
					index = i
					side = 1
					break
				}
			}
		}
		if index < 0 {
			if len(s.Conversations) >= agentConversationKeep {
				agentMessageMu.Unlock()
				return nil, fmt.Errorf("conversation capacity reached")
			}
			s.Conversations = append(s.Conversations, agentConversation{ID: "inbox-" + uuid.NewString(), Endpoints: [2]agentMessageEndpoint{e, dest}})
			index = len(s.Conversations) - 1
		}
	}
	c := &s.Conversations[index]
	dest := c.Endpoints[1-side]
	if err = validateMessageDestination(ctx, dest); err != nil {
		agentMessageMu.Unlock()
		return nil, err
	}
	if submissionID != "" {
		for _, m := range c.Messages {
			if m.From == side && m.Submission == submissionID {
				if m.Text != message {
					agentMessageMu.Unlock()
					return nil, fmt.Errorf("submission_id already used for a different message")
				}
				out := agentMessageReceipt(c.ID, m)
				agentMessageMu.Unlock()
				api.kickAgentMessages(inboxID)
				return out, nil
			}
		}
	}
	// Admission budgets are delivery controls, not conversational loop detection.
	recent := 0
	for _, m := range c.Messages {
		if time.Since(m.SentAt) < time.Hour {
			recent++
		}
	}
	if recent >= 40 {
		agentMessageMu.Unlock()
		return nil, fmt.Errorf("conversation message limit reached; check back later")
	}
	if len(c.Messages) >= agentMessageKeep {
		if c.Messages[0].Delivery == "pending" || c.Messages[0].Delivery == "dispatching" {
			agentMessageMu.Unlock()
			return nil, fmt.Errorf("conversation inbox is full")
		}
		c.Messages = c.Messages[1:]
	}
	c.Next++
	m := agentMessage{ID: "msg-" + uuid.NewString(), Sequence: c.Next, From: side, Text: message, SentAt: time.Now().UTC(), Submission: submissionID, Delivery: "pending"}
	if dest.External {
		m.Delivery = "stored"
	}
	c.Messages = append(c.Messages, m)
	id := c.ID
	out := agentMessageReceipt(id, m)
	err = saveAgentMessageStore(ctx, s)
	agentMessageMu.Unlock()
	if err != nil {
		return nil, err
	}
	api.kickAgentMessages(id)
	return out, nil
}
func agentMessageReceipt(id string, m agentMessage) map[string]interface{} {
	return map[string]interface{}{"accepted": true, "status": "accepted", "message_id": m.ID, "inbox_id": id, "conversation_id": id, "sequence": m.Sequence, "next": "Replies are optional. Read explicitly sent messages with read_agent_messages or messages action=read using inbox_id."}
}

func (api *StreamingAPI) readAgentMessages(ctx context.Context, userID string, caller triggerLinkCaller, inboxID string, after, limit int, wait time.Duration) (map[string]interface{}, error) {
	if strings.TrimSpace(inboxID) == "" {
		return nil, fmt.Errorf("inbox_id is required for a cursor read")
	}
	if limit < 1 || limit > 100 {
		limit = 30
	}
	if after < 0 {
		return nil, fmt.Errorf("after must be non-negative")
	}
	if wait > 25*time.Second {
		wait = 25 * time.Second
	}
	deadline := time.Now().Add(wait)
	for {
		agentMessageMu.Lock()
		s, err := readAgentMessageStore(ctx)
		agentMessageMu.Unlock()
		if err != nil {
			return nil, err
		}
		items := []map[string]interface{}{}
		found := false
		next := after
		for _, c := range s.Conversations {
			if inboxID != "" && c.ID != inboxID {
				continue
			}
			side := -1
			if messageEndpointMatches(c.Endpoints[0], userID, caller) {
				side = 0
			} else if messageEndpointMatches(c.Endpoints[1], userID, caller) {
				side = 1
			}
			if side < 0 {
				continue
			}
			found = true
			for _, m := range c.Messages {
				if m.Sequence <= after || m.From == side {
					continue
				}
				items = append(items, map[string]interface{}{"inbox_id": c.ID, "conversation_id": c.ID, "message_id": m.ID, "sequence": m.Sequence, "sender": c.Endpoints[m.From].Label, "reply_address": c.ID, "message": m.Text, "sent_at": m.SentAt, "delivery": m.Delivery, "delivery_error": m.Error})
				next = m.Sequence
				if len(items) >= limit {
					break
				}
			}
		}
		if !found && inboxID != "" {
			return nil, fmt.Errorf("conversation unavailable or access denied")
		}
		if len(items) > 0 || wait <= 0 || time.Now().After(deadline) {
			api.resumeAgentMessages()
			return map[string]interface{}{"messages": items, "next_cursor": next, "inbox_id": inboxID}, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}
func agentMessageText(c agentConversation, m agentMessage) string {
	if m.Wakeup {
		return "[Your explicitly requested wakeup. Decide whether to read your inbox, follow up or continue work; no message was automatically resent.]\n\n" + m.Text
	}
	return fmt.Sprintf("[Agent message from %s. Conversation/reply address: %s; message: %s. Replies are optional. If you choose to send a message back, use send_message(inbox_id=%q, message=...). Your final chat answer is NOT forwarded. Decide yourself whether to reply, request a wakeup, follow up or continue work.]\n\n%s", c.Endpoints[m.From].Label, c.ID, m.ID, c.ID, m.Text)
}
func (api *StreamingAPI) recoverAgentMessages() {
	agentMessageMu.Lock()
	s, err := readAgentMessageStore(context.Background())
	if err == nil {
		changed := false
		for i := range s.Conversations {
			for j := range s.Conversations[i].Messages {
				m := &s.Conversations[i].Messages[j]
				if m.Delivery == "dispatching" {
					m.Delivery = "interrupted"
					m.Error = "Delivery was interrupted by a server restart; inspect the receiving transcript before deciding to send again."
					changed = true
				}
			}
		}
		if changed {
			_ = saveAgentMessageStore(context.Background(), s)
		}
	}
	agentMessageMu.Unlock()
	api.resumeAgentMessages()
}

func (api *StreamingAPI) resumeAgentMessages() {
	agentMessageMu.Lock()
	s, err := readAgentMessageStore(context.Background())
	if err == nil {
		if materializeAgentWakeupsWithPause(&s, time.Now().UTC()) {
			err = saveAgentMessageStore(context.Background(), s)
		}
	}
	agentMessageMu.Unlock()
	if err != nil {
		return
	}
	for _, c := range s.Conversations {
		api.kickAgentMessages(c.ID)
	}
}
func (api *StreamingAPI) kickAgentMessages(id string) {
	if api == nil {
		return
	}
	if _, loaded := agentMessageWorkers.LoadOrStore(id, true); loaded {
		return
	}
	go func() {
		followPending := true
		defer func() {
			agentMessageMu.Lock()
			agentMessageWorkers.Delete(id)
			s, err := readAgentMessageStore(context.Background())
			pending := false
			if err == nil {
				for _, c := range s.Conversations {
					if c.ID == id {
						for _, m := range c.Messages {
							if m.Delivery == "pending" {
								pending = true
								break
							}
						}
					}
				}
			}
			agentMessageMu.Unlock()
			if pending && followPending {
				api.kickAgentMessages(id)
			}
		}()
		for {
			ctx := context.Background()
			agentMessageMu.Lock()
			s, err := readAgentMessageStore(ctx)
			var conv agentConversation
			var msg agentMessage
			has := false
			if err == nil {
				for _, c := range s.Conversations {
					if c.ID == id {
						for _, m := range c.Messages {
							if m.Delivery == "pending" && !c.Endpoints[1-m.From].External {
								conv = c
								msg = m
								has = true
								break
							}
						}
						break
					}
				}
			}
			agentMessageMu.Unlock()
			if !has {
				return
			}
			dest := conv.Endpoints[1-msg.From]
			if msg.Wakeup {
				config, readErr := LoadSchedulerConfig(ctx)
				product := dest.Profile
				if dest.Kind == triggerCallerWorkflow {
					product = "agentworks"
				}
				if readErr != nil || config.ProductPaused(product) {
					followPending = false
					return
				}
			}

			laneKey := dest.UserID + ":" + dest.Session
			if dest.Session == "" {
				laneKey += ":" + conv.ID
			}
			laneValue, _ := agentMessageLanes.LoadOrStore(laneKey, &sync.Mutex{})
			lane := laneValue.(*sync.Mutex)
			lane.Lock()
			if dest.Session != "" && !api.agentMessageSessionClosed(dest.Session) && api.conversationTurnOccupied(dest.Session) {
				lane.Unlock()
				time.Sleep(time.Second)
				continue
			}
			// A started delivery is not replayed after a process crash. Its durable
			// message remains available and restart records uncertain delivery.
			agentMessageMu.Lock()
			claimed, claimErr := readAgentMessageStore(ctx)
			if claimErr == nil {
				for i := range claimed.Conversations {
					if claimed.Conversations[i].ID == id {
						for j := range claimed.Conversations[i].Messages {
							if claimed.Conversations[i].Messages[j].ID == msg.ID {
								claimed.Conversations[i].Messages[j].Delivery = "dispatching"
							}
						}
					}
				}
				claimErr = saveAgentMessageStore(ctx, claimed)
			}
			agentMessageMu.Unlock()
			if claimErr != nil {
				followPending = false
				lane.Unlock()
				return
			}
			deliveryCtx, cancel := context.WithTimeout(internalBotRequestContext(ctx, dest.UserID), goalLeadTurnHardCap)
			session, deliveryErr := api.deliverAgentMessage(deliveryCtx, conv, msg)
			cancel()
			lane.Unlock()
			agentMessageMu.Lock()
			s, err = readAgentMessageStore(ctx)
			if err == nil {
				for i := range s.Conversations {
					c := &s.Conversations[i]
					if c.ID != id {
						continue
					}
					if session != "" {
						c.Endpoints[1-msg.From].Session = session
					}
					for j := range c.Messages {
						m := &c.Messages[j]
						if m.ID == msg.ID {
							m.Delivery = "delivered"
							if deliveryErr != nil {
								m.Delivery = "undeliverable"
								m.Error = deliveryErr.Error()
							}
							break
						}
					}
					break
				}
				_ = saveAgentMessageStore(ctx, s)
			}
			agentMessageMu.Unlock()
		}
	}()
}

var agentMessageTurn func(*StreamingAPI, context.Context, map[string]interface{}, string, string) (internalSessionTurnResult, error)

// The context key cannot be supplied by an HTTP query or model argument.
// Its lease is acquired after turn admission and released before that lane.
type agentMessageAdmissionKey struct{}

func acquireAgentMessageAuthority(ctx context.Context, session string) (func(), error) {
	if admit, ok := ctx.Value(agentMessageAdmissionKey{}).(func(string) (func(), error)); ok {
		return admit(session)
	}
	return func() {}, nil
}

func (api *StreamingAPI) deliverAgentMessage(ctx context.Context, c agentConversation, m agentMessage) (string, error) {
	dest := c.Endpoints[1-m.From]
	source := c.Endpoints[m.From]
	text := agentMessageText(c, m)
	if err := validateMessageDestination(ctx, dest); err != nil {
		return dest.Session, err
	}
	if dest.FunctionCallID != "" {
		call := lookupCrewFunctionCall(dest.FunctionCallID)
		if call == nil {
			return dest.Session, fmt.Errorf("receiving function execution is unavailable")
		}
		call.mu.Lock()
		running := call.admissionHeld && call.Status == "running"
		call.mu.Unlock()
		if !running {
			return dest.Session, fmt.Errorf("receiving function execution has ended")
		}
	}
	if dest.Session != "" && api.agentMessageSessionClosed(dest.Session) {
		return dest.Session, fmt.Errorf("receiving conversation was stopped or replaced")
	}
	if dest.Kind == triggerCallerWorkflow && dest.ChatKey == pulseBuilderChatKey {
		_, session, err := api.runGoalLeadTurn(ctx, dest.Path, goalLeadTurn{Kind: goalLeadTurnAgentMessage, From: source.Label, Body: text, ExpectedSessionID: dest.Session})
		return session, err
	}
	var reqMap map[string]interface{}
	session := dest.Session
	if dest.Kind == triggerCallerWorkflow {
		manifest, exists, err := ReadWorkflowManifest(ctx, dest.Path)
		if err != nil || !exists || manifest == nil || manifest.ID != dest.ID {
			return session, fmt.Errorf("workflow unavailable")
		}
		access := workflowAccessForManifest(GetUserFromContext(ctx), manifest)
		builder := strings.HasPrefix(dest.ChatKey, "session:")
		if access != WorkflowAccessOwner && access != WorkflowAccessWrite && (builder || access != WorkflowAccessRead) {
			return session, fmt.Errorf("workflow unavailable or access denied")
		}
		if session == "" {
			session = "wfmsg-" + strings.TrimPrefix(c.ID, "inbox-")
		}
		query := QueryRequest{Query: text, AgentMode: "workflow_phase", PhaseID: "workflow-builder", SelectedFolder: dest.Path, PresetQueryID: manifest.ID, PinRunMode: !builder, TriggeredBy: "external", TriggeredByLabel: "From " + source.Label, SessionTitle: "Messages: " + source.Label, ExecutionOptions: &ExecutionOptions{WorkshopMode: "run"}}
		if builder {
			query.ExecutionOptions.WorkshopMode = "workshop"
		}
		if api.workflowAskSessionExists(session, dest.Path) {
			query.RestoredConversationSessionID = session
		}
		reqMap, err = queryRequestToMap(query)
		if err != nil {
			return session, err
		}
	} else if dest.Profile == codeproduct.ProfileID {
		var err error
		reqMap, session, err = api.codeChatTurnRequest(ctx, dest.UserID, endpointTarget(dest), text, source.Label)
		if err != nil {
			return session, err
		}
	} else {
		if api.agentProfiles == nil {
			return session, fmt.Errorf("Crew runtime unavailable")
		}
		profile, err := api.agentProfiles.Resolve("work", 0, dest.UserID)
		if err != nil {
			return session, err
		}
		var binding productConversationBinding
		if dest.ChatKey != "" && !strings.HasPrefix(dest.ChatKey, "messages:") {
			binding, _, err = resolveConversationBindingForUser(ctx, dest.UserID, profile, dest.ID)
			binding.ConversationKey = dest.ChatKey
			if strings.HasPrefix(dest.ChatKey, "session:") {
				document, loadErr := defaultProductConversationRegistryStore().loadDocument(ctx, productConversationRegistryPath(dest.UserID))
				if loadErr != nil {
					return session, loadErr
				}
				found := false
				for _, record := range document.Entries {
					if record.SessionID == session && record.ProfileID == dest.Profile && record.ResourceID == dest.ID {
						binding.ConversationKey = record.ConversationKey
						found = true
						break
					}
				}
				if !found {
					return session, fmt.Errorf("receiving conversation was replaced")
				}
			}
		} else {
			binding, err = resolveIsolatedProjectAutomationBinding(ctx, dest.UserID, profile, dest.ID, "messages", c.ID, "Messages: "+source.Label)
		}
		if err != nil {
			return session, err
		}
		conversation, err := defaultProductConversationRegistryStore().resolveOrCreate(ctx, dest.UserID, profile, binding, "")
		if err != nil {
			return session, err
		}
		if session != "" && session != conversation.SessionID {
			return session, fmt.Errorf("receiving conversation was replaced")
		}
		session = conversation.SessionID
		query, err := queryRequestForAgentProfileChat(profile, AgentProfileChatRequest{Message: text}, conversation)
		if err != nil {
			return session, err
		}
		reqMap, err = queryRequestToMap(query)
		if err != nil {
			return session, err
		}
		dest = api.agentMessageCallerRole(dest)
		guest := firstNonEmptyTrimmed(dest.Guest, source.UserID)
		runMode := dest.Mode != "builder"
		if runMode {
			applyCrewGuestCaller(reqMap, guest)
		}
		reqMap["pin_run_mode"] = runMode
		reqMap["triggered_by"] = "agent_message"
	}
	// Bind the receiving address before its turn: it can explicitly reply from
	// that trusted session while the delivery operation is still running.
	agentMessageMu.Lock()
	s, err := readAgentMessageStore(ctx)
	if err == nil {
		for i := range s.Conversations {
			if s.Conversations[i].ID == c.ID {
				s.Conversations[i].Endpoints[1-m.From].Session = session
				if dest.Kind == triggerCallerCrew && dest.ChatKey == "" {
					s.Conversations[i].Endpoints[1-m.From].ChatKey = "messages:" + c.ID
				}
				break
			}
		}
		err = saveAgentMessageStore(ctx, s)
	}
	agentMessageMu.Unlock()
	if err != nil {
		return session, err
	}
	reqMap["agent_message_inbox"] = c.ID
	// This store owns durable admission. Do not copy a delegated turn into
	// the browser queue: replay there would lose its current permission lease.
	reqMap["is_auto_notification"] = true
	reqMap["disable_live_input_delivery"] = true
	if source.Kind == triggerCallerWorkflow && source.ChatKey == pulseBuilderChatKey {
		// Install the authority only after the receiving turn owns its input
		// lane. A human turn that won admission must not inherit this hold.
		ctx = context.WithValue(ctx, agentMessageAdmissionKey{}, func(receivingSession string) (func(), error) {
			manifest, found, readErr := ReadWorkflowManifest(ctx, source.Path)
			if readErr != nil || !found || manifest == nil || manifest.ID != source.ID || !manifest.PulseEnabled() {
				return nil, fmt.Errorf("sending Pulse is disabled")
			}
			current, readErr := ensureGoalLeadConversation(ctx, source.Path, manifest.ID, time.Now().UTC(), false, nil)
			if readErr != nil || current.SessionID != source.Session {
				return nil, fmt.Errorf("sending Pulse conversation was replaced")
			}
			// The sending workflow owns this authority, including when it
			// delegates to a Crew with a different workspace.
			perms, _ := goalWorkAutonomy(ctx, source.Path)
			return beginGoalWorkTurn(receivingSession, perms), nil
		})
	}
	if agentMessageTurn != nil {
		_, err = agentMessageTurn(api, ctx, reqMap, session, dest.UserID)
	} else {
		_, err = api.startSessionInternalWithResult(ctx, reqMap, session, dest.UserID, nil)
	}
	return session, err // deliberately ignore the final answer
}

func (api *StreamingAPI) registerAgentMessagingTools(registrar definitionToolRegistrar, userID, sessionID string, parentReq QueryRequest, resolveCaller func(context.Context) (triggerLinkCaller, error), declare func(string)) error {
	callerFor := func(ctx context.Context) (triggerLinkCaller, error) {
		c, err := resolveCaller(internalBotRequestContext(ctx, userID))
		if err == nil && c.Chat == nil {
			key := "session:" + sessionID
			if c.Stamp.Type == triggerCallerWorkflow && isGoalLeadSessionID(sessionID) {
				key = pulseBuilderChatKey
			}
			c.Chat = &codeChat{Key: key, SessionID: sessionID, Name: c.Label}
		}
		return c, err
	}
	tools := []struct {
		name, description string
		parameters        map[string]interface{}
		execute           func(context.Context, map[string]interface{}) (string, error)
	}{
		{"send_message", "Send an explicit conversational message to an authorized agent, or to an existing inbox/reply address. Replies are optional; final chat text is not forwarded. No function call is created.", map[string]interface{}{"type": "object", "required": []string{"message"}, "properties": map[string]interface{}{"target": map[string]interface{}{"type": "string"}, "inbox_id": map[string]interface{}{"type": "string"}, "message": map[string]interface{}{"type": "string"}, "submission_id": map[string]interface{}{"type": "string"}}}, func(ctx context.Context, args map[string]interface{}) (string, error) {
			ctx = internalBotRequestContext(ctx, userID)
			caller, err := callerFor(ctx)
			if err != nil {
				return "", err
			}
			id, _ := args["inbox_id"].(string)
			var target triggerTarget
			if id == "" {
				raw, _ := args["target"].(string)
				target, err = resolveFunctionTarget(ctx, GetUserFromContext(ctx), caller, raw)
				if err != nil {
					return "", err
				}
			}
			message, _ := args["message"].(string)
			submission, _ := args["submission_id"].(string)
			out, err := api.sendAgentMessage(ctx, userID, caller, target, message, id, submission)
			if err != nil {
				return "", err
			}
			raw, err := json.Marshal(out)
			return string(raw), err
		}},
		{"read_agent_messages", "Read messages explicitly sent to your conversation. Reading does not ask an agent, require a reply or settle work. Use inbox_id to continue a specific conversation and its next_cursor for later reads.", map[string]interface{}{"type": "object", "required": []string{"inbox_id"}, "properties": map[string]interface{}{"inbox_id": map[string]interface{}{"type": "string"}, "after": map[string]interface{}{"type": "integer", "minimum": 0}, "limit": map[string]interface{}{"type": "integer", "minimum": 1, "maximum": 100}}}, func(ctx context.Context, args map[string]interface{}) (string, error) {
			ctx = internalBotRequestContext(ctx, userID)
			caller, err := callerFor(ctx)
			if err != nil {
				return "", err
			}
			id, _ := args["inbox_id"].(string)
			out, err := api.readAgentMessages(ctx, userID, caller, id, externalInt(args, "after", 0), externalInt(args, "limit", 30), 0)
			if err != nil {
				return "", err
			}
			raw, err := json.Marshal(out)
			return string(raw), err
		}},
	}
	tools = append(tools, struct {
		name, description string
		parameters        map[string]interface{}
		execute           func(context.Context, map[string]interface{}) (string, error)
	}{"schedule_message_wakeup", "Explicitly schedule, reschedule or cancel one durable wakeup in your exact conversation. It does not resend messages or infer whether work finished. Scheduler resolution is one minute.", map[string]interface{}{"type": "object", "properties": map[string]interface{}{"action": map[string]interface{}{"type": "string", "enum": []string{"schedule", "cancel"}}, "wakeup_id": map[string]interface{}{"type": "string"}, "message": map[string]interface{}{"type": "string"}, "after_seconds": map[string]interface{}{"type": "integer", "minimum": 1, "maximum": 604800}}}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		ctx = internalBotRequestContext(ctx, userID)
		caller, err := callerFor(ctx)
		if err != nil {
			return "", err
		}
		out, err := api.scheduleAgentMessageWakeup(ctx, userID, caller, stringToolArg(args, "action"), stringToolArg(args, "wakeup_id"), stringToolArg(args, "message"), externalInt(args, "after_seconds", 0))
		if err != nil {
			return "", err
		}
		raw, err := json.Marshal(out)
		return string(raw), err
	}})
	for _, t := range tools {
		if declare != nil {
			declare(t.name)
		}
		if err := registrar.RegisterCustomTool(t.name, t.description, t.parameters, t.execute, "agent_messaging"); err != nil {
			return err
		}
	}
	return nil
}

// Ordinary idle conversations can be restored. Explicit stops remain closed.
func (api *StreamingAPI) agentMessageSessionClosed(session string) bool {
	if api.isSessionMarkedStopped(session) {
		return true
	}
	active, ok := api.getActiveSession(session)
	return ok && active != nil && active.Status == "stopped"
}

func sameAgentMessageContact(existing, requested agentMessageEndpoint) bool {
	if existing.UserID != requested.UserID || existing.Kind != requested.Kind || existing.ID != requested.ID || existing.Profile != requested.Profile || existing.Path != requested.Path || existing.External != requested.External {
		return false
	}
	return requested.Session == "" || existing.Session == requested.Session
}

func (api *StreamingAPI) agentMessageCallerRole(e agentMessageEndpoint) agentMessageEndpoint {
	if e.Kind == triggerCallerCrew && !e.External && e.Session != "" {
		if id := common.GetSessionShellEnv(e.Session)["FUNCTION_CALL_ID"]; id != "" {
			e.FunctionCallID = id
		}
		api.lastQueryMu.RLock()
		prior, known := api.lastQueryRequests[e.Session]
		api.lastQueryMu.RUnlock()
		if known {
			e.Mode = "run"
			e.Guest = prior.CrewGuestCaller
			if !prior.PinRunMode && prior.CrewGuestCaller == "" {
				e.Mode = "builder"
			}
		}
	}
	return e
}
