package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

// Remote endpoints are addresses on a linked server, never local agents or
// local people. These fields are server-authored, not caller identity hints.
type agentRemoteEndpoint struct {
	LinkID  string `json:"link_id"`
	Target  string `json:"target,omitempty"`
	InboxID string `json:"inbox_id,omitempty"`
}

type agentRelayMessage struct {
	LinkID         string    `json:"link_id"`
	AgentID        string    `json:"agent_id"`
	InboxID        string    `json:"inbox_id"`
	MessageID      string    `json:"message_id"`
	Incoming       bool      `json:"incoming,omitempty"`
	AcknowledgedAt time.Time `json:"acknowledged_at,omitempty"`
}

type agentServerRelayService struct {
	api     *StreamingAPI
	wake    chan struct{}
	mu      sync.Mutex
	workers map[string]*agentServerRelayWorker
}

type agentServerRelayWorker struct {
	service *agentServerRelayService
	link    agentServerLink
	agentID string
	ctx     context.Context
	cancel  context.CancelFunc
	wake    chan struct{}
	mu      sync.Mutex
	client  *agentworksclient.Client
	lease   agentworksclient.AgentAttachment
	status  string
}

func (api *StreamingAPI) agentServerRelay() *agentServerRelayService {
	api.serverRelayOnce.Do(func() {
		api.serverRelay = &agentServerRelayService{api: api, wake: make(chan struct{}, 1), workers: map[string]*agentServerRelayWorker{}}
		go api.serverRelay.run()
	})
	return api.serverRelay
}

func (s *agentServerRelayService) changed() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *agentServerRelayService) run() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		s.reconcile()
		changed := agentLiveChanged()
		select {
		case <-ticker.C:
		case <-s.wake:
		case <-changed:
		}
	}
}

func (s *agentServerRelayService) reconcile() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	links, err := readAgentServerLinks(ctx, "")
	if err != nil {
		return
	}
	mail, err := readAgentMessageSnapshot(ctx)
	if err != nil {
		return
	}
	wanted := map[string]bool{}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, l := range links {
		if userAccessForClaims(&UserClaims{UserID: l.UserID}).Disabled {
			continue
		}
		for _, a := range mail.Agents {
			if a.UserID != l.UserID || a.Relay != nil || !namedAgentDiscoverable(a, time.Now().UTC()) {
				continue
			}
			key := l.ID + ":" + a.ID
			wanted[key] = true
			if w := s.workers[key]; w != nil && w.ctx.Err() == nil && w.link.ClientID == l.ClientID {
				select {
				case w.wake <- struct{}{}:
				default:
				}
				continue
			}
			if old := s.workers[key]; old != nil {
				old.cancel()
			}
			workerCtx, stop := context.WithCancel(context.Background())
			w := &agentServerRelayWorker{service: s, link: l, agentID: a.ID, ctx: workerCtx, cancel: stop, wake: make(chan struct{}, 1), status: "Connecting"}
			s.workers[key] = w
			go w.run()
		}
	}
	for key, w := range s.workers {
		if !wanted[key] {
			w.cancel()
			delete(s.workers, key)
		}
	}
}

func (w *agentServerRelayWorker) state(message string) {
	w.mu.Lock()
	changed := w.status != message
	w.status = message
	w.mu.Unlock()
	if changed {
		log.Printf("[AGENT-RELAY] link=%s agent=%s state=%q", w.link.ID, w.agentID, message)
	}
}

func (w *agentServerRelayWorker) run() {
	for w.ctx.Err() == nil {
		err := w.connect()
		w.mu.Lock()
		w.client = nil
		w.lease = agentworksclient.AgentAttachment{}
		w.mu.Unlock()
		if w.ctx.Err() != nil {
			return
		}
		if err != nil {
			w.state(err.Error())
		}
		select {
		case <-w.ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func (w *agentServerRelayWorker) connect() error {
	a, err := namedAgentByID(w.ctx, w.link.UserID, w.agentID)
	if err != nil || a.Relay != nil {
		return errors.New("Home agent is unavailable.")
	}
	home, _, ok := mcpOAuthURLs()
	if !ok {
		return errors.New("Home server address is unavailable.")
	}
	namespace, err := agentServerNamespace(home)
	if err != nil {
		return err
	}
	name := a.Name + "@" + namespace
	parent, err := agentworksclient.New(w.link.Origin, "")
	if err != nil {
		return err
	}
	parent.WithHTTPClient(agentServerLinkHTTP).WithTokenProvider(func(ctx context.Context) (string, error) { return agentServerLinkAccess(ctx, w.link.UserID, w.link.ID) })
	registration := map[string]any{"action": "register", "agent_name": name}
	if a.Purpose != "" {
		registration["purpose"] = a.Purpose
	}
	if _, err = parent.Call(w.ctx, "messages", registration); err != nil {
		return errors.New("The linked-server connection needs to reconnect.")
	}
	cfg, err := parent.CreateAgentConnection(w.ctx, name)
	if err != nil {
		return errors.New("The linked server could not connect this agent.")
	}
	child, err := agentworksclient.New(w.link.Origin, cfg.Token)
	if err != nil {
		return err
	}
	var tokenMu sync.Mutex
	child.WithHTTPClient(agentServerLinkHTTP).WithTokenProvider(func(ctx context.Context) (string, error) {
		tokenMu.Lock()
		defer tokenMu.Unlock()
		if _, err := ownAgentServerLink(ctx, w.link.UserID, w.link.ID); err != nil {
			return "", err
		}
		if cfg.ExpiresAt.After(time.Now().Add(time.Minute)) {
			return cfg.Token, nil
		}
		pair, err := exchangeAgentServerLink(ctx, w.link.Origin, url.Values{"grant_type": {"refresh_token"}, "client_id": {w.link.ClientID}, "resource": {w.link.Origin + externalMCPPath}, "refresh_token": {cfg.RefreshToken}})
		if err != nil {
			return "", err
		}
		cfg.Token, cfg.RefreshToken, cfg.ExpiresAt = pair.Access, pair.Refresh, pair.ExpiresAt
		return cfg.Token, nil
	})
	lease, err := child.AttachAgent(w.ctx, agentworksclient.AgentAttachmentOptions{AgentName: name, Label: a.Name, Provider: "relay", SessionID: a.ID, Purpose: a.Purpose})
	if err != nil {
		return errors.New("The linked server refused this agent reader.")
	}
	w.mu.Lock()
	w.client, w.lease = child, lease
	w.mu.Unlock()
	streamCtx, stop := context.WithCancel(w.ctx)
	defer stop()
	streamDone := make(chan error, 1)
	go func() {
		streamDone <- child.StreamAgentMessages(streamCtx, lease, func(agentworksclient.AgentLiveMessage) error {
			select {
			case w.wake <- struct{}{}:
			default:
			}
			return nil
		}, func() {
			select {
			case w.wake <- struct{}{}:
			default:
			}
		})
	}()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	lastPresence := ""
	cursor := 0
	for {
		if err = w.reportPresence(&lastPresence); err != nil {
			return err
		}
		if err = w.forward(); err != nil {
			w.state(err.Error())
		} else if err = w.importPending(&cursor); err != nil {
			w.state(err.Error())
		} else {
			w.state("Connected")
		}
		select {
		case <-w.ctx.Done():
			return w.ctx.Err()
		case err = <-streamDone:
			return err
		case <-w.wake:
		case <-ticker.C:
		}
	}
}

func (w *agentServerRelayWorker) reportPresence(previous *string) error {
	a, err := namedAgentByID(w.ctx, w.link.UserID, w.agentID)
	if err != nil {
		return err
	}
	seen := a.LastOnlineAt
	if seen.IsZero() {
		seen = a.CreatedAt
	}
	provider := ""
	if a.Attachment != nil {
		provider = a.Attachment.Provider
	}
	w.mu.Lock()
	lease, child := w.lease, w.client
	w.mu.Unlock()
	body := map[string]any{"action": "label", "agent_name": lease.AgentName, "attachment_id": lease.ID, "label": a.Name, "delivery_route": "relay", "home_provider": provider, "home_online": namedAgentOnline(a), "home_delivery_mode": agentDeliveryMode(a), "home_last_seen_at": seen}
	raw, _ := json.Marshal(body)
	if string(raw) == *previous {
		return nil
	}
	_, err = child.AgentAttachmentCall(w.ctx, body)
	if err != nil {
		return errors.New("The linked server could not update this agent connection.")
	}
	*previous = string(raw)
	return nil
}

func (w *agentServerRelayWorker) forward() error {
	s, err := readAgentMessageSnapshot(w.ctx)
	if err != nil {
		return err
	}
	w.mu.Lock()
	child, lease := w.client, w.lease
	w.mu.Unlock()
	for _, c := range s.Conversations {
		for side, source := range c.Endpoints {
			peer := c.Endpoints[1-side]
			if source.NamedAgentID != w.agentID || source.UserID != w.link.UserID || peer.Remote == nil || peer.Remote.LinkID != w.link.ID {
				continue
			}
			for _, m := range c.Messages {
				if m.From != side {
					if m.Relay != nil && m.Relay.Incoming && !m.DeliveredAt.IsZero() && m.Relay.AcknowledgedAt.IsZero() {
						_, err = child.Call(w.ctx, "messages", map[string]any{"action": "ack", "agent_name": lease.AgentName, "attachment_id": lease.ID, "message_id": m.Relay.MessageID, "delivery": "delivered", "transport": "relay"})
						if err != nil {
							return errors.New("The linked server could not save a delivery receipt.")
						}
						if err = w.updateMessage(c.ID, m.ID, func(current *agentMessage) { current.Relay.AcknowledgedAt = time.Now().UTC() }); err != nil {
							return err
						}
					}
					continue
				}
				if m.Delivery != "stored" || agentMessagePendingExpired(m, time.Now().UTC()) {
					continue
				}
				if err = w.service.api.recheckAgentMessageAdmission(w.ctx, c, m); err != nil {
					return err
				}
				if m.Relay == nil {
					args := map[string]any{"action": "send", "agent_name": lease.AgentName, "message": m.Text, "submission_id": "relay:" + m.ID}
					if peer.Remote.InboxID != "" {
						args["inbox_id"] = peer.Remote.InboxID
					} else {
						args["target"] = peer.Remote.Target
					}
					if m.File != nil {
						return errors.New("Cross-server file delivery is not available yet. Send a message or a shared file link.")
					}
					raw, e := child.Call(w.ctx, "messages", args)
					if e != nil {
						return errors.New("The linked server could not accept a waiting message. Check the recipient and connection.")
					}
					var receipt struct {
						ID    string `json:"message_id"`
						Inbox string `json:"inbox_id"`
					}
					if json.Unmarshal(raw, &receipt) != nil || receipt.ID == "" || receipt.Inbox == "" {
						return errors.New("The linked server returned an invalid message receipt.")
					}
					m.Relay = &agentRelayMessage{LinkID: w.link.ID, AgentID: w.agentID, InboxID: receipt.Inbox, MessageID: receipt.ID}
					if err = w.updateMessage(c.ID, m.ID, func(current *agentMessage) { current.Relay = m.Relay }); err != nil {
						return err
					}
					if err = w.mapInbox(c.ID, side, receipt.Inbox); err != nil {
						return err
					}
					log.Printf("[AGENT-RELAY] link=%s agent=%s message=%s remote_message=%s event=forwarded", w.link.ID, w.agentID, m.ID, receipt.ID)
				}
				raw, e := child.Call(w.ctx, "messages", map[string]any{"action": "read", "agent_name": lease.AgentName, "inbox_id": m.Relay.InboxID, "message_id": m.Relay.MessageID, "after": 1000000000, "limit": 1})
				if e != nil {
					return errors.New("The linked server could not check a delivery receipt.")
				}
				var result struct {
					Sent []struct {
						State string    `json:"state"`
						At    time.Time `json:"delivered_at"`
					} `json:"sent"`
				}
				if json.Unmarshal(raw, &result) == nil && len(result.Sent) == 1 && result.Sent[0].State == "delivered" {
					at := result.Sent[0].At
					if at.IsZero() {
						return errors.New("The linked server returned an invalid delivery time.")
					}
					if err = w.updateMessage(c.ID, m.ID, func(current *agentMessage) {
						if current.DeliveredAt.IsZero() {
							current.DeliveredAt = at
							current.Delivery = "delivered"
							current.LiveTransport = "relay"
						}
					}); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func (w *agentServerRelayWorker) updateMessage(inbox, id string, update func(*agentMessage)) error {
	agentMessageMu.Lock()
	defer agentMessageMu.Unlock()
	s, err := readAgentMessageStore(w.ctx)
	if err != nil {
		return err
	}
	for ci := range s.Conversations {
		c := &s.Conversations[ci]
		if c.ID != inbox {
			continue
		}
		for mi := range c.Messages {
			m := &c.Messages[mi]
			if m.ID == id {
				update(m)
				return saveAgentMessageStore(w.ctx, s)
			}
		}
	}
	return errors.New("The message was removed from its inbox.")
}

func (w *agentServerRelayWorker) mapInbox(inbox string, side int, remote string) error {
	agentMessageMu.Lock()
	defer agentMessageMu.Unlock()
	s, err := readAgentMessageStore(w.ctx)
	if err != nil {
		return err
	}
	for i := range s.Conversations {
		c := &s.Conversations[i]
		if c.ID == inbox && c.Endpoints[1-side].Remote != nil {
			c.Endpoints[1-side].Remote.InboxID = remote
			return saveAgentMessageStore(w.ctx, s)
		}
	}
	return errors.New("The conversation is unavailable.")
}

func (w *agentServerRelayWorker) importPending(cursor *int) error {
	w.mu.Lock()
	child, lease := w.client, w.lease
	w.mu.Unlock()
	var err error
	for page := 0; page < 20; page++ {
		var result struct {
			Messages []agentworksclient.AgentLiveMessage `json:"messages"`
			Next     int                                 `json:"next_cursor"`
		}
		raw, e := child.AgentAttachmentCall(w.ctx, map[string]any{"action": "inbox", "agent_name": lease.AgentName, "attachment_id": lease.ID, "after": *cursor, "limit": 20})
		if e != nil {
			return errors.New("The linked server is unreachable; messages are waiting.")
		}
		if json.Unmarshal(raw, &result) != nil {
			return errors.New("The linked server returned an invalid inbox page.")
		}
		for _, message := range result.Messages {
			if err = w.importMessage(message); err != nil {
				return err
			}
		}
		if len(result.Messages) < 20 {
			*cursor = 0
			break
		}
		if result.Next <= *cursor {
			return errors.New("The linked server returned an invalid inbox position.")
		}
		*cursor = result.Next
	}
	return nil
}

func (w *agentServerRelayWorker) importMessage(remote agentworksclient.AgentLiveMessage) error {
	if remote.MessageID == "" || remote.InboxID == "" || remote.Message == "" || remote.Kind != "" {
		return nil
	}
	agentMessageMu.Lock()
	defer agentMessageMu.Unlock()
	s, err := readAgentMessageStore(w.ctx)
	if err != nil {
		return err
	}
	for _, c := range s.Conversations {
		for _, m := range c.Messages {
			if m.Relay != nil && m.Relay.LinkID == w.link.ID && m.Relay.MessageID == remote.MessageID && m.Relay.Incoming {
				return nil
			}
		}
	}
	var own namedMessageAgent
	for _, a := range s.Agents {
		if a.ID == w.agentID && a.UserID == w.link.UserID && a.Relay == nil {
			own = a
			break
		}
	}
	if own.ID == "" {
		return errors.New("Home agent is unavailable.")
	}
	index, side := -1, 0
	for i, c := range s.Conversations {
		for candidate, e := range c.Endpoints {
			if e.NamedAgentID == own.ID && e.UserID == own.UserID {
				peer := c.Endpoints[1-candidate]
				if peer.Remote != nil && peer.Remote.LinkID == w.link.ID && peer.Remote.InboxID == remote.InboxID {
					index, side = i, candidate
				}
			}
		}
	}
	if index < 0 {
		pruneAgentConversations(&s, time.Now().UTC(), own.UserID)
		if len(s.Conversations) >= agentConversationKeep {
			return errors.New("Conversation capacity reached.")
		}
		self := agentMessageEndpoint{UserID: own.UserID, Kind: triggerCallerUser, ID: own.UserID, NamedAgentID: own.ID, Label: own.Name, External: true}
		peer := agentMessageEndpoint{UserID: own.UserID, Kind: triggerCallerUser, ID: "remote:" + w.link.ID + ":" + remote.InboxID, Label: remote.Sender + "@" + w.link.Alias, External: true, Remote: &agentRemoteEndpoint{LinkID: w.link.ID, InboxID: remote.InboxID}}
		s.Conversations = append(s.Conversations, agentConversation{ID: "inbox-" + uuid.NewString(), Endpoints: [2]agentMessageEndpoint{self, peer}})
		index = len(s.Conversations) - 1
		side = 0
	}
	c := &s.Conversations[index]
	if len(c.Messages) >= agentMessageKeep {
		return errors.New("The receiving conversation is full.")
	}
	sent, err := time.Parse(time.RFC3339Nano, remote.SentAt)
	if err != nil {
		return errors.New("The linked server returned an invalid message time.")
	}
	c.Next++
	s.NextAgentSequence++
	m := agentMessage{ID: "msg-" + uuid.NewString(), Sequence: c.Next, AgentSequence: s.NextAgentSequence, From: 1 - side, Text: remote.Message, SentAt: sent, Delivery: "stored", InboxAtSend: !namedAgentOnline(own), Relay: &agentRelayMessage{LinkID: w.link.ID, AgentID: own.ID, InboxID: remote.InboxID, MessageID: remote.MessageID, Incoming: true}}
	c.Messages = append(c.Messages, m)
	if err = saveAgentMessageStore(w.ctx, s); err == nil {
		log.Printf("[AGENT-RELAY] link=%s agent=%s message=%s remote_message=%s event=imported receipt=waiting", w.link.ID, w.agentID, m.ID, remote.MessageID)
	}
	return err
}

func (s *agentServerRelayService) reader(link, agent string) (*agentServerRelayWorker, error) {
	s.mu.Lock()
	w := s.workers[link+":"+agent]
	s.mu.Unlock()
	if w == nil {
		return nil, errors.New("The linked server is reconnecting; messages are waiting.")
	}
	w.mu.Lock()
	ready := w.client != nil
	w.mu.Unlock()
	if !ready {
		return nil, fmt.Errorf("%s is unreachable; messages are waiting.", w.link.Alias)
	}
	return w, nil
}

func (api *StreamingAPI) validateRelayMessage(ctx context.Context, e agentMessageEndpoint, m agentMessage) error {
	if m.Relay == nil || !m.Relay.Incoming {
		return nil
	}
	if e.Remote == nil || e.Remote.LinkID != m.Relay.LinkID {
		return errors.New("Remote message identity is unavailable.")
	}
	l, err := ownAgentServerLink(ctx, e.UserID, m.Relay.LinkID)
	if err != nil {
		return err
	}
	w, err := api.agentServerRelay().reader(l.ID, m.Relay.AgentID)
	if err != nil {
		return err
	}
	w.mu.Lock()
	child, lease := w.client, w.lease
	w.mu.Unlock()
	var result struct {
		IDs []string `json:"message_ids"`
	}
	raw, err := child.AgentAttachmentCall(ctx, map[string]any{"action": "validate", "agent_name": lease.AgentName, "attachment_id": lease.ID, "message_ids": []string{m.Relay.MessageID}})
	if err != nil {
		return fmt.Errorf("%s is unreachable or its connection ended; messages are waiting.", l.Alias)
	}
	if json.Unmarshal(raw, &result) != nil {
		return errors.New("The linked server returned an invalid message check.")
	}
	if !slices.Contains(result.IDs, m.Relay.MessageID) {
		return errors.New("The sender no longer has access on the linked server.")
	}
	return nil
}

func resolveLinkedAgentTarget(ctx context.Context, user, address string) (triggerTarget, bool, error) {
	name, alias, qualified := strings.Cut(strings.ToLower(strings.TrimSpace(address)), "@")
	if !qualified {
		return triggerTarget{}, false, nil
	}
	l, err := ownAgentServerLink(ctx, user, alias)
	if err != nil {
		return triggerTarget{}, true, fmt.Errorf("Link your %s account in AgentBridge first.", alias)
	}
	if name == "" || strings.Contains(name, "@") {
		return triggerTarget{}, true, errors.New("Use name@server for a linked agent.")
	}
	for _, part := range strings.Split(name, "/") {
		if _, err := normalizeMessageAgentName(part); err != nil {
			return triggerTarget{}, true, err
		}
	}
	return triggerTarget{Kind: triggerCallerUser, Label: name + "@" + l.Alias, Remote: &agentRemoteEndpoint{LinkID: l.ID, Target: "agent:" + name}}, true, nil
}

func (s *agentServerRelayService) linkStatus(ctx context.Context, links []agentServerLink) {
	mail, err := readAgentMessageSnapshot(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range links {
		l := &links[i]
		l.State = "Waiting for an active agent"
		for _, w := range s.workers {
			if w.link.ID != l.ID || w.link.UserID != l.UserID {
				continue
			}
			w.mu.Lock()
			state := w.status
			w.mu.Unlock()
			if l.State == "Waiting for an active agent" || state != "Connected" {
				l.State = state
			}
		}
		if err != nil {
			continue
		}
		for _, c := range mail.Conversations {
			for _, m := range c.Messages {
				if !m.DeliveredAt.IsZero() || agentMessagePendingExpired(m, time.Now().UTC()) {
					continue
				}
				dest := c.Endpoints[1-m.From]
				if dest.UserID == l.UserID && dest.Remote != nil && dest.Remote.LinkID == l.ID || m.Relay != nil && m.Relay.LinkID == l.ID && m.Relay.Incoming {
					l.Waiting++
				}
			}
		}
	}
}
