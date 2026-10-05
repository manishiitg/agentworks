// Package browserrelay connects a paired Chrome extension to an agent-browser
// CDP client. Authority is bound to a trusted account and browser workspace.
package browserrelay

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const ConnectionLifetime = 8 * time.Hour
const maxMessage = 16 << 20

type grant struct {
	User, Scope, Label, ProfileID string
}
type Status struct {
	Selected     bool     `json:"selected"`
	Connected    bool     `json:"connected"`
	Workspace    string   `json:"workspace,omitempty"`
	Tabs         int      `json:"tabs"`
	ConnectionID string   `json:"connection_id,omitempty"`
	TabTitles    []string `json:"tab_titles,omitempty"`
}
type Binding struct {
	mu                                 sync.Mutex
	key, capability, endpoint, session string
	owner                              string
	profile                            string
	tabTitles                          []string
	label                              string
	expires                            time.Time
	extension, cdp                     *websocket.Conn
	tabs                               int
	gate                               chan struct{}
}
type Manager struct {
	mu              sync.Mutex
	pairs           map[string]grant
	bindings        map[string]*Binding
	listener        net.Listener
	base            string
	selectionPath   string
	credentialsPath string
}

var Default = New()

func New() *Manager                 { return &Manager{pairs: map[string]grant{}, bindings: map[string]*Binding{}} }
func key(user, scope string) string { return user + "\x00" + scope }
func secret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// start runs a separate capability-protected CDP listener. Only the extension's
// outbound connection traverses the public gateway. Split services must provide
// an explicit private bind and advertised host.
func (m *Manager) start() error {
	if m.listener != nil {
		return nil
	}
	address := os.Getenv("AGENT_BROWSER_EXTENSION_RELAY_BIND")
	if address == "" {
		address = "127.0.0.1:0"
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	host := os.Getenv("AGENT_BROWSER_EXTENSION_RELAY_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	m.listener = listener
	m.base = "ws://" + net.JoinHostPort(host, fmt.Sprint(listener.Addr().(*net.TCPAddr).Port))
	server := &http.Server{Handler: http.HandlerFunc(m.serveCDP), ReadHeaderTimeout: 5 * time.Second}
	go server.Serve(listener)
	return nil
}

// Pair returns the same private connection code for an account/workspace.
// Copying or reconnecting never rotates it; Reset explicitly revokes the code.
func (m *Manager) Pair(user, scope, label string) (string, error) {
	return m.PairForProfile(user, scope, label, "")
}
func (m *Manager) PairForProfile(user, scope, label, profile string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.connectionCode(user, scope, label, profile, false)
}
func (m *Manager) Reset(user, scope, label, profile string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	token, err := m.connectionCode(user, scope, label, profile, true)
	if err == nil {
		if b := m.bindings[key(user, scope)]; b != nil {
			b.close()
		}
	}
	return token, err
}

// Caller holds m.mu. Persist before changing credentials or live authority.
func (m *Manager) connectionCode(user, scope, label, profile string, reset bool) (string, error) {
	if user == "" || scope == "" {
		return "", errors.New("account and browser scope required")
	}
	if m.bindings[key(user, scope)] == nil && len(m.bindings) >= 1000 {
		return "", errors.New("too many browser selections")
	}
	if err := m.start(); err != nil {
		return "", err
	}
	token := ""
	next := make(map[string]grant, len(m.pairs)+1)
	for existing, g := range m.pairs {
		if key(g.User, g.Scope) == key(user, scope) {
			if !reset {
				token = existing
			}
			continue
		}
		next[existing] = g
	}
	if len(next) >= 1000 {
		return "", errors.New("too many browser connections")
	}
	if token == "" {
		token = secret()
	}
	next[token] = grant{User: user, Scope: scope, Label: label, ProfileID: profile}
	if err := m.persistCredentials(next); err != nil {
		return "", err
	}
	m.pairs = next
	return token, nil
}
func (m *Manager) Lookup(user, scope string) *Binding {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.bindings[key(user, scope)]
}
func (m *Manager) Status(user, scope string) Status {
	b := m.Lookup(user, scope)
	if b == nil {
		return Status{}
	}
	return b.Status()
}
func (b *Binding) Status() Status {
	b.mu.Lock()
	defer b.mu.Unlock()
	return Status{Selected: true, Connected: b.extension != nil && time.Now().Before(b.expires), Workspace: b.label, Tabs: b.tabs, ConnectionID: b.session, TabTitles: append([]string(nil), b.tabTitles...)}
}
func (b *Binding) Session() string { return b.session }
func (b *Binding) Profile() string { return b.profile }
func (b *Binding) Acquire(ctx context.Context) (string, func(), error) {
	return b.AcquireFor(ctx, "")
}

// A connection has one controlling conversation. Delegates use the root ID.
// Switching conversations requires explicit re-pairing, so cached refs from
// another chat can never become actions in this browser session.
func (b *Binding) AcquireFor(ctx context.Context, owner string) (string, func(), error) {
	select {
	case b.gate <- struct{}{}:
	case <-ctx.Done():
		return "", nil, ctx.Err()
	}
	release := func() { <-b.gate }
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.extension == nil || time.Now().After(b.expires) || b.tabs == 0 {
		release()
		return "", nil, errors.New("CHROME_EXTENSION_DISCONNECTED: connect Chrome and share a tab, or disconnect the extension in browser settings to use the workspace browser")
	}
	if owner != "" {
		if b.owner != "" && b.owner != owner {
			release()
			return "", nil, errors.New("CHROME_EXTENSION_BUSY: another chat controls this Chrome connection; reconnect Chrome to switch chats")
		}
		b.owner = owner
	}
	return b.endpoint, release, nil
}
func (m *Manager) Disconnect(user, scope string) error {
	m.mu.Lock()
	b := m.bindings[key(user, scope)]
	if err := m.persistSelection(key(user, scope), nil); err != nil {
		m.mu.Unlock()
		return err
	}
	delete(m.bindings, key(user, scope))
	m.mu.Unlock()
	if b != nil {
		b.close()
	}
	return nil
}
func (b *Binding) close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.extension != nil {
		b.extension.Close()
		b.extension = nil
	}
	if b.cdp != nil {
		b.cdp.Close()
		b.cdp = nil
	}
	b.tabs = 0
	b.tabTitles = nil
}
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, b := range m.bindings {
		b.close()
	}
	if m.listener != nil {
		m.listener.Close()
		m.listener = nil
		m.base = ""
	}
	m.bindings = map[string]*Binding{}
	m.pairs = map[string]grant{}
}

type envelope struct {
	Type      string          `json:"type"`
	Token     string          `json:"token,omitempty"`
	Workspace string          `json:"workspace,omitempty"`
	Tabs      int             `json:"tabs,omitempty"`
	TabTitles []string        `json:"tab_titles,omitempty"`
	Message   json.RawMessage `json:"message,omitempty"`
}

// ServeExtension authenticates in the first frame. No app JWT is given to the
// extension. The stable credential is rechecked against current access on every
// connection and heartbeat; live authority is never restored after restart.
func (m *Manager) ServeExtension(w http.ResponseWriter, r *http.Request) {
	m.ServeExtensionAuthorized(w, r, nil)
}
func (m *Manager) ServeExtensionAuthorized(w http.ResponseWriter, r *http.Request, authorize func(user, scope, workspace, profile string) error) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", 405)
		return
	}
	upgrade := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return strings.HasPrefix(r.Header.Get("Origin"), "chrome-extension://") }}
	conn, err := upgrade.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	conn.SetReadLimit(maxMessage)
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	var hello envelope
	if conn.ReadJSON(&hello) != nil || hello.Type != "pair" {
		return
	}
	m.mu.Lock()
	g, ok := m.pairs[hello.Token]
	m.mu.Unlock()
	if !ok {
		conn.WriteJSON(envelope{Type: "error", Workspace: "Connection code is invalid or was reset"})
		return
	}
	if authorize != nil && authorize(g.User, g.Scope, g.Label, g.ProfileID) != nil {
		conn.WriteJSON(envelope{Type: "error", Workspace: "This account no longer has browser access to the workspace"})
		return
	}
	m.mu.Lock()
	// Reset may have revoked the code while the workspace access check ran.
	if current, exists := m.pairs[hello.Token]; !exists || current != g {
		m.mu.Unlock()
		conn.WriteJSON(envelope{Type: "error", Workspace: "Connection code was reset; copy it again"})
		return
	}
	if err := m.start(); err != nil {
		m.mu.Unlock()
		return
	}

	if err := m.persistSelection("", &g); err != nil {
		m.mu.Unlock()
		conn.WriteJSON(envelope{Type: "error", Workspace: "Cannot save Chrome selection; reconnect after checking server storage"})
		return
	}
	old := m.bindings[key(g.User, g.Scope)]
	capability := secret()
	hash := sha256.Sum256([]byte(capability))
	b := &Binding{key: key(g.User, g.Scope), label: g.Label, profile: g.ProfileID, capability: capability, endpoint: m.base + "/cdp/" + capability, session: "ext-" + hex.EncodeToString(hash[:12]), expires: time.Now().Add(ConnectionLifetime), extension: conn, gate: make(chan struct{}, 1)}
	b.mu.Lock()
	m.bindings[b.key] = b
	m.mu.Unlock()
	if old != nil {
		old.close()
	}
	defer b.close()
	err = conn.WriteJSON(envelope{Type: "paired", Workspace: g.Label})
	b.mu.Unlock()
	if err != nil {
		return
	}
	for {
		deadline := time.Now().Add(75 * time.Second)
		if deadline.After(b.expires) {
			deadline = b.expires
		}
		conn.SetReadDeadline(deadline)
		var e envelope
		if conn.ReadJSON(&e) != nil {
			return
		}
		if e.Type == "ping" && authorize != nil && authorize(g.User, g.Scope, g.Label, g.ProfileID) != nil {
			return
		}
		b.mu.Lock()
		switch e.Type {
		case "ping":
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			err = conn.WriteJSON(envelope{Type: "pong"})
		case "tabs":
			if e.Tabs >= 0 && e.Tabs <= 32 {
				b.tabs = e.Tabs
				b.tabTitles = nil
				for _, title := range e.TabTitles {
					if len(b.tabTitles) >= e.Tabs {
						break
					}
					if len(title) > 512 {
						title = title[:512]
					}
					b.tabTitles = append(b.tabTitles, title)
				}
			}
		case "cdp":
			if b.cdp != nil && len(e.Message) > 0 {
				b.cdp.SetWriteDeadline(time.Now().Add(10 * time.Second))
				err = b.cdp.WriteMessage(websocket.TextMessage, e.Message)
			}
		case "stop":
			b.mu.Unlock()
			return
		default:
			err = errors.New("invalid extension message")
		}
		b.mu.Unlock()
		if err != nil {
			return
		}
	}
}
func (m *Manager) serveCDP(w http.ResponseWriter, r *http.Request) {
	// This listener is for native CDP clients, never web pages.
	if r.Method != http.MethodGet || r.Header.Get("Origin") != "" {
		http.Error(w, "Forbidden", 403)
		return
	}
	cap := strings.TrimPrefix(r.URL.Path, "/cdp/")
	if cap == r.URL.Path || strings.Contains(cap, "/") {
		http.NotFound(w, r)
		return
	}
	m.mu.Lock()
	var b *Binding
	for _, candidate := range m.bindings {
		if candidate.capability == cap {
			b = candidate
			break
		}
	}
	m.mu.Unlock()
	if b == nil {
		http.Error(w, "Invalid browser capability", 401)
		return
	}
	b.mu.Lock()
	if b.extension == nil || time.Now().After(b.expires) || b.cdp != nil {
		b.mu.Unlock()
		http.Error(w, "Chrome disconnected or already controlled", 409)
		return
	}
	conn, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
	if err != nil {
		b.mu.Unlock()
		return
	}
	b.cdp = conn
	b.extension.SetWriteDeadline(time.Now().Add(10 * time.Second))
	err = b.extension.WriteJSON(envelope{Type: "client-connected"})
	b.mu.Unlock()
	defer func() {
		conn.Close()
		b.mu.Lock()
		if b.cdp == conn {
			b.cdp = nil
		}
		if b.extension != nil {
			b.extension.SetWriteDeadline(time.Now().Add(10 * time.Second))
			b.extension.WriteJSON(envelope{Type: "client-disconnected"})
		}
		b.mu.Unlock()
	}()
	if err != nil {
		return
	}
	conn.SetReadLimit(2 << 20)
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if !json.Valid(data) {
			return
		}
		b.mu.Lock()
		if b.extension == nil || time.Now().After(b.expires) {
			b.mu.Unlock()
			return
		}
		b.extension.SetWriteDeadline(time.Now().Add(10 * time.Second))
		err = b.extension.WriteJSON(envelope{Type: "cdp", Message: data})
		b.mu.Unlock()
		if err != nil {
			return
		}
	}
}
