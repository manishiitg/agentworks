package browserrelay

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// Client is a private native browser client for a trusted conversation. All
// state and websocket writes are protected by its project's binding mutex.
// Its capability and CDP request IDs never enter another conversation's client.
type Client struct {
	binding                *Binding
	owner, id, capability  string
	endpoint, session      string
	cdp                    *websocket.Conn
	tabs                   int
	recordingInterruptions uint64
	ready                  chan struct{}
}

func (c *Client) Session() string  { return c.session }
func (c *Client) Endpoint() string { return c.endpoint }
func (c *Client) Status() Status {
	s := c.binding.Status()
	if c.id != "" {
		c.binding.mu.Lock()
		s.Tabs = c.tabs
		c.binding.mu.Unlock()
	}
	return s
}
func (c *Client) CreateTarget(ctx context.Context, url string) (string, error) {
	return c.binding.targetCommandFor(ctx, url, "", c.id)
}
func (c *Client) CloseTarget(ctx context.Context, target string) error {
	_, err := c.binding.targetCommandFor(ctx, "", target, c.id)
	return err
}
func (c *Client) RecordingEpoch() uint64 {
	if c.id == "" {
		return c.binding.RecordingEpoch()
	}
	c.binding.mu.Lock()
	defer c.binding.mu.Unlock()
	return c.recordingInterruptions
}

// Code conversations have independent CLI/reference state, but the project
// gate still serializes complete tool actions. Workflow/delegate handoff and
// legacy Crew controller behavior retain their existing root identity.
func (b *Binding) AcquireClient(ctx context.Context, owner string, active bool) (*Client, func(), error) {
	if b.profile != "code" {
		endpoint, release, err := b.AcquireForActive(ctx, owner, active)
		if err != nil {
			return nil, nil, err
		}
		return &Client{binding: b, endpoint: endpoint, session: b.session}, release, nil
	}
	if owner == "" {
		return nil, nil, errors.New("CHROME_EXTENSION_ACCESS: a trusted chat identity is required")
	}
	select {
	case b.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	release := func() {
		b.mu.Lock()
		b.active = false
		b.mu.Unlock()
		<-b.gate
	}
	b.mu.Lock()
	fail := func(err error) (*Client, func(), error) { b.mu.Unlock(); release(); return nil, nil, err }
	if b.extension == nil || time.Now().After(b.expires) {
		return fail(errors.New("CHROME_EXTENSION_DISCONNECTED: reconnect the extension"))
	}
	if !b.chatClients {
		return fail(errors.New("CHROME_EXTENSION_UPDATE_REQUIRED: reload Browser Bridge 0.4.6 or newer for independent Code chat tabs"))
	}
	var client *Client
	for _, c := range b.clients {
		if c.owner == owner {
			client = c
			break
		}
	}
	if client == nil {
		if len(b.clients) >= 32 {
			return fail(errors.New("CHROME_EXTENSION_CLIENT_LIMIT: stop or close an unused Code chat before browsing"))
		}
		capability := secret()
		id := conversationID(b.key, owner)
		sessionHash := sha256.Sum256([]byte(capability))
		client = &Client{binding: b, owner: owner, id: id, capability: capability,
			ready:    make(chan struct{}),
			endpoint: b.endpoint[:len(b.endpoint)-len(b.capability)] + capability,
			session:  "session-" + hex.EncodeToString(sessionHash[:8]) + "--browser"}
		if b.clients == nil {
			b.clients = map[string]*Client{}
		}
		b.clients[id] = client
		b.extension.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := b.extension.WriteJSON(envelope{Type: "client-register", ClientID: id}); err != nil {
			delete(b.clients, id)
			return fail(errors.New("Chrome client registration failed"))
		}
	}
	b.active = active
	b.mu.Unlock()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case <-client.ready:
	case <-ctx.Done():
		b.discardClient(client)
		release()
		return nil, nil, ctx.Err()
	case <-timer.C:
		b.discardClient(client)
		release()
		return nil, nil, errors.New("Chrome chat client registration timed out")
	}
	b.mu.Lock()
	connected := b.extension != nil && b.clients[client.id] == client
	b.mu.Unlock()
	if !connected {
		release()
		return nil, nil, errors.New("CHROME_EXTENSION_DISCONNECTED: reconnect the extension")
	}
	return client, release, nil
}

func interruptedRecording(raw json.RawMessage) bool {
	var e struct {
		Method string
		Params struct{ Reason string }
	}
	return json.Unmarshal(raw, &e) == nil && e.Method == "Inspector.detached" &&
		(e.Params.Reason == "recording_target_detached" || e.Params.Reason == "recording_target_unshared")
}

// ReleaseConversation revokes only the named Code chat's private capability.
// It leaves the account token, project connection and other chats untouched.
func (m *Manager) ReleaseConversation(ctx context.Context, owner, scope string) []string {
	if owner == "" || scope == "" {
		return nil
	}
	m.mu.Lock()
	var bindings []*Binding
	for _, b := range m.bindings {
		if b.profile == "code" && strings.HasSuffix(b.key, "\x00"+scope) {
			bindings = append(bindings, b)
		}
	}
	m.mu.Unlock()
	var sessions []string
	for _, b := range bindings {
		select {
		case b.gate <- struct{}{}:
		case <-ctx.Done():
			return sessions
		}
		b.mu.Lock()
		id := conversationID(b.key, owner)
		if c := b.clients[id]; c != nil && c.owner == owner {
			delete(b.clients, id)
			sessions = append(sessions, c.session)
			if c.cdp != nil {
				c.cdp.Close()
				c.cdp = nil
			}
		}
		// A reconnect may have restored this chat's physical tabs before it
		// registers a new native client. Revoke those grants by stable route ID.
		if b.extension != nil {
			b.extension.SetWriteDeadline(time.Now().Add(10 * time.Second))
			_ = b.extension.WriteJSON(envelope{Type: "client-release", ClientID: id})
		}
		b.mu.Unlock()
		<-b.gate
	}
	return sessions
}

// ConversationTabs reports only the calling chat's view without registering or
// claiming a tab. Project status remains useful for the shared connection UI.
func (b *Binding) ConversationTabs(owner string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.profile != "code" {
		return b.tabs
	}
	for _, c := range b.clients {
		if c.owner == owner {
			return c.tabs
		}
	}
	return 0
}
func (b *Binding) discardClient(c *Client) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.clients[c.id] != c {
		return
	}
	delete(b.clients, c.id)
	if b.extension != nil {
		b.extension.SetWriteDeadline(time.Now().Add(time.Second))
		_ = b.extension.WriteJSON(envelope{Type: "client-release", ClientID: c.id})
	}
}

func conversationID(project, owner string) string {
	hash := sha256.Sum256([]byte(project + "\x00" + owner))
	return hex.EncodeToString(hash[:8])
}

// LiveSessions lists the agent-browser session names of every live extension
// connection and chat client. A relay-attached helper whose session is not in
// this list belongs to a connection or chat that has ended (PLAT-662).
func (m *Manager) LiveSessions() []string {
	m.mu.Lock()
	bindings := make([]*Binding, 0, len(m.bindings))
	for _, b := range m.bindings {
		bindings = append(bindings, b)
	}
	m.mu.Unlock()
	var sessions []string
	for _, b := range bindings {
		b.mu.Lock()
		sessions = append(sessions, b.session)
		for _, c := range b.clients {
			sessions = append(sessions, c.session)
		}
		b.mu.Unlock()
	}
	return sessions
}
