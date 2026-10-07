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
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const ConnectionLifetime = 8 * time.Hour
const maxMessage = 16 << 20

type grant struct {
	User, Scope, Label, ProfileID string
	Projects                      map[string]projectGrant `json:"projects,omitempty"`
}
type projectGrant struct {
	Scope     string `json:"scope"`
	Label     string `json:"workspace"`
	ProfileID string `json:"profile_id"`
	Name      string `json:"name,omitempty"`
}
type Status struct {
	Selected         bool     `json:"selected"`
	Connected        bool     `json:"connected"`
	AccountConnected bool     `json:"account_connected"`
	Workspace        string   `json:"workspace,omitempty"`
	Tabs             int      `json:"tabs"`
	ConnectionID     string   `json:"connection_id,omitempty"`
	TabTitles        []string `json:"tab_titles,omitempty"`
	// ExtensionVersion is what the connected Browser Bridge reports in its
	// diagnostics; LatestVersion is the one this server ships for download.
	ExtensionVersion string `json:"extension_version,omitempty"`
	LatestVersion    string `json:"latest_extension_version,omitempty"`
}
type Binding struct {
	mu                                 sync.Mutex
	key, capability, endpoint, session string
	owner                              string
	clients                            map[string]*Client
	chatClients                        bool
	profile                            string
	tabTitles                          []string
	version                            string
	label                              string
	expires                            time.Time
	extension, cdp                     *websocket.Conn
	tabs                               int
	active                             bool
	diagnostics                        map[string]*tabDiagnostics
	childTargets                       map[string]string
	gate                               chan struct{}
	newTabRequest                      string
	newTabResult                       chan envelope
	recordingInterruptions             uint64
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

// Pair returns the same private connection code for an account, registering
// the workspace separately. The token never grants arbitrary project access.
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
		for bindingKey, b := range m.bindings {
			if strings.HasPrefix(bindingKey, user+"\x00") {
				b.closeWithReason("Account connection code reset")
			}
		}
	}
	return token, err
}

// Caller holds m.mu. Upgrade the first legacy project code to the account code;
// keep other legacy copies usable for their original project until account Reset.
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
	next := make(map[string]grant, len(m.pairs)+1)
	projects := map[string]projectGrant{}
	var token string
	var legacy []string
	for existing, g := range m.pairs {
		if g.User == user {
			if g.Scope != "" {
				if _, registered := projects[g.Scope]; !registered {
					projects[g.Scope] = projectGrant{Scope: g.Scope, Label: g.Label, ProfileID: g.ProfileID}
				}
				legacy = append(legacy, existing)
			}
			if len(g.Projects) > 0 {
				token = existing
				for k, p := range g.Projects {
					projects[k] = p
				}
			}
			if reset {
				continue
			}
		}
		next[existing] = g
	}
	if !reset && token == "" && len(legacy) > 0 {
		sort.Strings(legacy)
		token = legacy[0]
	}
	if reset || token == "" {
		token = secret()
	}
	projects[scope] = projectGrant{Scope: scope, Label: label, ProfileID: profile}
	if len(projects) > 1000 {
		return "", errors.New("too many browser connections")
	}
	next[token] = grant{User: user, Projects: projects}
	if len(next) > 1000 {
		return "", errors.New("too many browser connections")
	}
	registered := map[string]bool{}
	for _, g := range next {
		if g.Scope != "" {
			registered[key(g.User, g.Scope)] = true
		}
		for scope := range g.Projects {
			registered[key(g.User, scope)] = true
		}
	}
	if len(registered) > 1000 {
		return "", errors.New("too many registered browser projects")
	}
	if err := m.persistCredentials(next); err != nil {
		return "", err
	}
	m.pairs = next
	return token, nil
}

// Scope is routing metadata, never authority. Only previously registered grants
// belonging to the token's account can be resolved. A legacy one-project code
// still works without scope; an ambiguous account code must name its project.
func resolveProject(g grant, scope string) (grant, bool) {
	if g.Scope != "" {
		return g, scope == "" || scope == g.Scope
	}
	if scope == "" && len(g.Projects) == 1 {
		for k := range g.Projects {
			scope = k
		}
	}
	p, ok := g.Projects[scope]
	return grant{User: g.User, Scope: p.Scope, Label: p.Label, ProfileID: p.ProfileID}, ok
}
func sameProject(a, b grant) bool {
	return a.User == b.User && a.Scope == b.Scope && a.Label == b.Label && a.ProfileID == b.ProfileID
}

func (m *Manager) availableProjects(token string, authorize func(string, string, string, string) error) []projectGrant {
	m.mu.Lock()
	g, exists := m.pairs[token]
	projects := make([]projectGrant, 0, len(g.Projects)+1)
	for _, p := range g.Projects {
		projects = append(projects, p)
	}
	if g.Scope != "" {
		projects = append(projects, projectGrant{Scope: g.Scope, Label: g.Label, ProfileID: g.ProfileID})
	}
	m.mu.Unlock()
	if !exists {
		return nil
	}
	result := projects[:0]
	for _, p := range projects {
		if authorize == nil || authorize(g.User, p.Scope, p.Label, p.ProfileID) == nil {
			result = append(result, p)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Label < result[j].Label })
	return result
}

// A project registered by its authenticated owner can attach to the already
// paired account browser. This does not share any pre-existing tab.
func (m *Manager) RequestProjectConnection(user, scope string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if b := m.bindings[key(user, scope)]; b != nil && b.Status().Connected {
		return true
	}
	var g grant
	found := false
	for _, credential := range m.pairs {
		if credential.User == user {
			if resolved, ok := resolveProject(credential, scope); ok {
				g = resolved
				found = true
				break
			}
		}
	}
	if !found {
		return false
	}
	var source *Binding
	for bindingKey, b := range m.bindings {
		if strings.HasPrefix(bindingKey, user+"\x00") && b.Status().Connected {
			source = b
			break
		}
	}
	if source == nil {
		return false
	}
	if err := m.persistSelection("", &g); err != nil {
		return false
	}
	if m.bindings[key(user, scope)] == nil {
		m.bindings[key(user, scope)] = &Binding{key: key(user, scope), label: g.Label, profile: g.ProfileID, gate: make(chan struct{}, 1)}
	}
	source.mu.Lock()
	defer source.mu.Unlock()
	if source.extension == nil {
		return false
	}
	source.extension.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return source.extension.WriteJSON(envelope{Type: "connect-project", Scope: scope}) == nil
}

// Called under the binding's automation gate. Create the first project tab
// without a CLI that requires an existing target to bootstrap its CDP session.
func (b *Binding) CreateTarget(ctx context.Context, targetURL string) (string, error) {
	return b.targetCommand(ctx, targetURL, "")
}
func (b *Binding) CloseTarget(ctx context.Context, targetID string) error {
	_, err := b.targetCommand(ctx, "", targetID)
	return err
}
func (b *Binding) targetCommand(ctx context.Context, targetURL, targetID string) (string, error) {
	return b.targetCommandFor(ctx, targetURL, targetID, "")
}
func (b *Binding) targetCommandFor(ctx context.Context, targetURL, targetID, clientID string) (string, error) {
	request := secret()
	result := make(chan envelope, 1)
	b.mu.Lock()
	if b.extension == nil || time.Now().After(b.expires) {
		b.mu.Unlock()
		return "", errors.New("Chrome disconnected")
	}
	b.newTabRequest, b.newTabResult = request, result
	b.extension.SetWriteDeadline(time.Now().Add(10 * time.Second))
	err := b.extension.WriteJSON(envelope{Type: "target-command", ClientID: clientID, RequestID: request, URL: targetURL, TargetID: targetID, Active: b.active})
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		if b.newTabRequest == request {
			b.newTabRequest = ""
			b.newTabResult = nil
		}
		b.mu.Unlock()
	}()
	if err != nil {
		return "", errors.New("Chrome connection could not create a tab")
	}
	select {
	case response := <-result:
		if response.Error != "" {
			return "", errors.New(response.Error)
		}
		if response.TargetID == "" {
			return "", errors.New("Chrome returned no new target")
		}
		return response.TargetID, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (m *Manager) Lookup(user, scope string) *Binding {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.bindings[key(user, scope)]
}
func (m *Manager) Status(user, scope string) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	status := Status{}
	if b := m.bindings[key(user, scope)]; b != nil {
		status = b.Status()
	}
	status.LatestVersion = LatestExtensionVersion()
	for bindingKey, b := range m.bindings {
		if strings.HasPrefix(bindingKey, user+"\x00") && b.Status().Connected {
			status.AccountConnected = true
			break
		}
	}
	return status
}
func (b *Binding) Status() Status {
	b.mu.Lock()
	defer b.mu.Unlock()
	return Status{Selected: true, Connected: b.extension != nil && time.Now().Before(b.expires), Workspace: b.label, Tabs: b.tabs, ConnectionID: b.session, TabTitles: append([]string(nil), b.tabTitles...), ExtensionVersion: b.version}
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
	return b.AcquireForActive(ctx, owner, false)
}

// Visible activation is opt-in for one serialized tool call, never sticky.
func (b *Binding) AcquireForActive(ctx context.Context, owner string, active bool) (string, func(), error) {
	select {
	case b.gate <- struct{}{}:
	case <-ctx.Done():
		return "", nil, ctx.Err()
	}
	release := func() { <-b.gate }
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.extension == nil || time.Now().After(b.expires) {
		release()
		return "", nil, errors.New("CHROME_EXTENSION_DISCONNECTED: connect Chrome, or disconnect the extension in browser settings to use the workspace browser")
	}
	if owner != "" {
		if b.owner != "" && b.owner != owner {
			release()
			return "", nil, errors.New("CHROME_EXTENSION_BUSY: another chat controls this Chrome connection; reconnect Chrome to switch chats")
		}
		b.owner = owner
	}
	b.active = active
	return b.endpoint, func() {
		b.mu.Lock()
		b.active = false
		b.mu.Unlock()
		release()
	}, nil
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
		b.closeWithReason("Disconnected in browser settings")
	}
	return nil
}
func (b *Binding) close() {
	b.closeWithReason("")
}
func (b *Binding) closeWithReason(reason string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.newTabResult != nil {
		select {
		case b.newTabResult <- envelope{Error: "Chrome connection stopped"}:
		default:
		}
	}
	if b.extension != nil {
		if reason != "" {
			// Explicit revocation must not look like a retryable network outage.
			_ = b.extension.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(4001, reason), time.Now().Add(time.Second))
		}
		b.extension.Close()
		b.extension = nil
	}
	if b.cdp != nil {
		b.cdp.Close()
		b.cdp = nil
	}
	for _, client := range b.clients {
		select {
		case <-client.ready:
		default:
			close(client.ready)
		}
		if client.cdp != nil {
			client.cdp.Close()
			client.cdp = nil
		}
	}
	b.clients = nil
	b.tabs = 0
	b.tabTitles = nil
	b.diagnostics = nil
	b.childTargets = nil
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
	Type        string          `json:"type"`
	ClientID    string          `json:"client_id,omitempty"`
	Features    []string        `json:"features,omitempty"`
	Token       string          `json:"token,omitempty"`
	Scope       string          `json:"scope,omitempty"`
	Resume      bool            `json:"resume,omitempty"`
	ProfileID   string          `json:"profile_id,omitempty"`
	Projects    []projectGrant  `json:"projects,omitempty"`
	Workspace   string          `json:"workspace,omitempty"`
	Name        string          `json:"name,omitempty"`
	Tabs        int             `json:"tabs,omitempty"`
	TabTitles   []string        `json:"tab_titles,omitempty"`
	Message     json.RawMessage `json:"message,omitempty"`
	RequestID   string          `json:"request_id,omitempty"`
	URL         string          `json:"url,omitempty"`
	TargetID    string          `json:"target_id,omitempty"`
	Error       string          `json:"error,omitempty"`
	Active      bool            `json:"active,omitempty"`
	Event       string          `json:"event,omitempty"`
	Reason      string          `json:"reason,omitempty"`
	Method      string          `json:"method,omitempty"`
	TabID       int64           `json:"tab_id,omitempty"`
	Diagnostics bool            `json:"diagnostics,omitempty"`
	Version     string          `json:"version,omitempty"`
	DurationMS  int64           `json:"duration_ms,omitempty"`
}

// Extension diagnostics are deliberately limited to protocol metadata. Never
// log arbitrary message strings, CDP parameters, page URLs or credentials.
func logExtensionDiagnostic(scope, connection string, e envelope) {
	switch e.Event {
	case "connection_paired", "connection_stopped", "debugger_attached", "debugger_detached", "session_detach_requested", "tab_unshared", "command_failed", "command_started", "command_succeeded", "child_attached", "child_detached", "tab_created", "tab_grouping_started", "tab_grouped", "tab_grouping_failed", "setup_waiting_for_page", "target_attach_failed", "target_setup_failed", "target_recovery_started", "target_recovered", "target_recovery_failed", "tab_shown_for_input", "window_restored_for_input":
	default:
		return
	}
	switch e.Reason {
	case "", "target_closed", "canceled_by_user", "requested_unshare", "target_close", "debugger_detached", "tab_closed", "unsupported_url", "detached", "not_shared", "foreign_frame", "other":
	default:
		return
	}
	if e.TabID < 0 || e.TabID > 1<<53 || len(e.Method) > 80 {
		return
	}
	for _, c := range e.Method {
		if c != '.' && (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') {
			return
		}
	}
	if len(e.RequestID) > 16 {
		return
	}
	for _, c := range e.RequestID {
		if c < '0' || c > '9' {
			return
		}
	}
	if len(e.Version) > 20 || e.DurationMS < 0 || e.DurationMS > 24*60*60*1000 {
		return
	}
	for _, c := range e.Version {
		if c != '.' && (c < '0' || c > '9') {
			return
		}
	}
	log.Printf("[CHROME_EXTENSION] scope=%q connection=%q event=%s tab_id=%d reason=%s last_method=%s request_id=%s version=%s duration_ms=%d", scope, connection, e.Event, e.TabID, e.Reason, e.Method, e.RequestID, e.Version, e.DurationMS)
}

// ServeExtension authenticates in the first frame. No app JWT is given to the
// extension. The stable credential is rechecked against current access on every
// connection and heartbeat; live authority is never restored after restart.
func (m *Manager) ServeExtension(w http.ResponseWriter, r *http.Request) {
	m.ServeExtensionAuthorized(w, r, nil)
}
func (m *Manager) ServeExtensionAuthorized(w http.ResponseWriter, r *http.Request, authorize func(user, scope, workspace, profile string) error) {
	m.ServeExtensionAuthorizedWithNames(w, r, authorize, nil)
}

// Display names never replace physical workspace labels used for authorization.
func (m *Manager) ServeExtensionAuthorizedWithNames(w http.ResponseWriter, r *http.Request, authorize func(user, scope, workspace, profile string) error, displayName func(user, workspace, profile string) string) {
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
	credential, exists := m.pairs[hello.Token]
	g, ok := resolveProject(credential, hello.Scope)
	ok = ok && exists
	m.mu.Unlock()
	if !ok {
		conn.WriteJSON(envelope{Type: "error", Workspace: "Connection code is invalid or was reset"})
		return
	}
	if authorize != nil && authorize(g.User, g.Scope, g.Label, g.ProfileID) != nil {
		conn.WriteJSON(envelope{Type: "error", Workspace: "This account no longer has browser access to the workspace"})
		return
	}
	available := m.availableProjects(hello.Token, authorize)
	names := newProjectNames(g, displayName)
	defer names.close()
	available = names.cached(available)
	var name string
	for _, p := range available {
		if p.Scope == g.Scope {
			name = p.Name
		}
	}
	m.mu.Lock()
	// Reset may have revoked the code while the workspace access check ran.
	current, exists := m.pairs[hello.Token]
	checked, granted := resolveProject(current, g.Scope)
	if !exists || !granted || !sameProject(checked, g) {
		m.mu.Unlock()
		conn.WriteJSON(envelope{Type: "error", Workspace: "Connection code was reset; copy it again"})
		return
	}
	// An offline browser may have missed the explicit Disconnect close frame.
	// Resume cannot recreate a selection removed by the authenticated app.
	if hello.Resume && m.bindings[key(g.User, g.Scope)] == nil {
		m.mu.Unlock()
		conn.WriteJSON(envelope{Type: "error", Workspace: "Browser connection was disconnected; connect again to enable access"})
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
	b := &Binding{key: key(g.User, g.Scope), label: g.Label, profile: g.ProfileID, capability: capability, endpoint: m.base + "/cdp/" + capability, session: "session-" + hex.EncodeToString(hash[:8]) + "--browser", expires: time.Now().Add(ConnectionLifetime), extension: conn, gate: make(chan struct{}, 1)}
	for _, feature := range hello.Features {
		if feature == "chat-clients" {
			b.chatClients = true
		}
	}
	b.mu.Lock()
	m.bindings[b.key] = b
	m.mu.Unlock()
	if old != nil {
		old.closeWithReason("Connected from another browser")
	}
	defer b.close()
	err = conn.WriteJSON(envelope{Type: "paired", Workspace: g.Label, Name: name, Scope: g.Scope, ProfileID: g.ProfileID, Projects: available, Diagnostics: true})
	b.mu.Unlock()
	if err != nil {
		return
	}
	diagnosticWindow, diagnosticCount := time.Now(), 0
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
			b.closeWithReason("Browser access revoked")
			return
		}

		var projects []projectGrant
		if e.Type == "ping" {
			projects = names.cached(m.availableProjects(hello.Token, authorize))
		}
		b.mu.Lock()
		switch e.Type {
		case "ping":
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			err = conn.WriteJSON(envelope{Type: "pong", Projects: projects})
		case "tabs":
			if e.ClientID != "" {
				if client := b.clients[e.ClientID]; client != nil && e.Tabs >= 0 && e.Tabs <= 32 {
					client.tabs = e.Tabs
				}
				break
			}
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
		case "client-ready":
			if client := b.clients[e.ClientID]; client != nil && e.Tabs >= 0 && e.Tabs <= 32 {
				client.tabs = e.Tabs
				select {
				case <-client.ready:
				default:
					close(client.ready)
				}
			}
		case "target-result":
			if b.newTabResult != nil && e.RequestID == b.newTabRequest {
				select {
				case b.newTabResult <- e:
				default:
				}
			}
		case "diagnostic":
			// b.mu is already held for this whole switch. Locking it again here
			// deadlocked the reader on the first versioned diagnostic, and then
			// every Manager.Status caller behind it (RTS, 2026-10-07: all
			// browser and tool calls hung for 58 minutes).
			if validExtensionVersion(e.Version) {
				b.version = e.Version
			}
			if time.Since(diagnosticWindow) >= time.Minute {
				diagnosticWindow, diagnosticCount = time.Now(), 0
			}
			if diagnosticCount < 4096 {
				logExtensionDiagnostic(g.Scope, b.session, e)
				diagnosticCount++
			}
		case "cdp":
			b.collectDiagnostics(e.Message)
			cdp := b.cdp
			if e.ClientID != "" {
				cdp = nil
				if client := b.clients[e.ClientID]; client != nil {
					cdp = client.cdp
					if interruptedRecording(e.Message) {
						client.recordingInterruptions++
					}
				}
			}
			if cdp != nil && len(e.Message) > 0 {
				cdp.SetWriteDeadline(time.Now().Add(10 * time.Second))
				// A closed chat client must not tear down the project socket.
				_ = cdp.WriteMessage(websocket.TextMessage, e.Message)
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
	var client *Client
	for _, candidate := range m.bindings {
		candidate.mu.Lock()
		if candidate.capability == cap {
			b = candidate
			candidate.mu.Unlock()
			break
		}
		for _, c := range candidate.clients {
			if c.capability == cap {
				b, client = candidate, c
				break
			}
		}
		candidate.mu.Unlock()
		if b != nil {
			break
		}
	}
	m.mu.Unlock()
	if b == nil {
		http.Error(w, "Invalid browser capability", 401)
		return
	}
	b.mu.Lock()
	cdp, clientID := b.cdp, ""
	if client != nil {
		cdp, clientID = client.cdp, client.id
	}
	if b.extension == nil || time.Now().After(b.expires) || cdp != nil || client != nil && b.clients[clientID] != client {
		b.mu.Unlock()
		http.Error(w, "Chrome disconnected or already controlled", 409)
		return
	}
	conn, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
	if err != nil {
		b.mu.Unlock()
		return
	}
	if client == nil {
		b.cdp = conn
	} else {
		client.cdp = conn
	}
	b.extension.SetWriteDeadline(time.Now().Add(10 * time.Second))
	err = b.extension.WriteJSON(envelope{Type: "client-connected", ClientID: clientID})
	b.mu.Unlock()
	defer func() {
		conn.Close()
		b.mu.Lock()
		current := b.cdp == conn
		if client != nil {
			current = client.cdp == conn && b.clients[clientID] == client
		}
		if current && client == nil {
			b.cdp = nil
		} else if current {
			client.cdp = nil
		}
		if current && b.extension != nil {
			b.extension.SetWriteDeadline(time.Now().Add(10 * time.Second))
			b.extension.WriteJSON(envelope{Type: "client-disconnected", ClientID: clientID})
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
		if b.extension == nil || time.Now().After(b.expires) || client != nil && b.clients[clientID] != client {
			b.mu.Unlock()
			return
		}
		b.extension.SetWriteDeadline(time.Now().Add(10 * time.Second))
		err = b.extension.WriteJSON(envelope{Type: "cdp", ClientID: clientID, Message: data, Active: b.active})
		b.mu.Unlock()
		if err != nil {
			return
		}
	}
}
