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
	// Browsers lists every browser bound to this account and project (PLAT-856
	// 856-70); the flat fields above describe the resolved one, as before.
	Browsers []BrowserStatus `json:"browsers,omitempty"`
}

// BrowserStatus is one connected or remembered browser of an account. ID is
// server-issued and stable per extension instance; it is never a credential —
// binding still requires the paired account token on every connection.
type BrowserStatus struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Connected bool   `json:"connected"`
	Chosen    bool   `json:"chosen"`
	LastSeen  string `json:"last_seen,omitempty"`
}
type Binding struct {
	mu                                 sync.Mutex
	key, capability, endpoint, session string
	// browserID is the public stable identity of this browser slot (PLAT-856
	// 856-70); instanceID is what the extension reported (empty for old
	// extensions); name is its readable name, customName the person's rename.
	browserID, instanceID, name, customName string
	createdAt, lastSeen                     time.Time
	connectedAt                             time.Time
	owner                                   string
	clients                                 map[string]*Client
	chatClients                             bool
	profile                                 string
	tabTitles                               []string
	version                                 string
	label                                   string
	expires                                 time.Time
	extension, cdp                          *websocket.Conn
	tabs                                    int
	active                                  bool
	diagnostics                             map[string]*tabDiagnostics
	childTargets                            map[string]string
	gate                                    chan struct{}
	newTabRequest                           string
	newTabResult                            chan envelope
	recordingInterruptions                  uint64
}
type Manager struct {
	mu              sync.Mutex
	pairs           map[string]grant
	bindings        map[string]*Binding
	choices         map[string]string
	listener        net.Listener
	base            string
	selectionPath   string
	credentialsPath string
}

var Default = New()

func New() *Manager {
	return &Manager{pairs: map[string]grant{}, bindings: map[string]*Binding{}, choices: map[string]string{}}
}
func key(user, scope string) string { return user + "\x00" + scope }

// fullKey addresses one browser slot of a route; Binding.key stays the route
// key (user, scope) that conversations and change notifications use.
func fullKey(user, scope, browserID string) string { return key(user, scope) + "\x00" + browserID }

// publicBrowserID derives the stable, unguessable public id of one extension
// instance of an account. It identifies, never authorizes.
func publicBrowserID(user, instanceID string) string {
	sum := sha256.Sum256([]byte(user + "\x00" + instanceID))
	return "browser-" + hex.EncodeToString(sum[:8])
}

// bindingsFor returns the route's browser slots, earliest first. Caller holds m.mu.
func (m *Manager) bindingsFor(user, scope string) []*Binding {
	prefix := key(user, scope) + "\x00"
	var out []*Binding
	for full, b := range m.bindings {
		if strings.HasPrefix(full, prefix) && b.browserID != "" {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].createdAt.Before(out[j].createdAt) })
	return out
}

// placeholderFor returns the route's restored selection placeholder (no
// browser identity yet; the next connection adopts it). Caller holds m.mu.
func (m *Manager) placeholderFor(user, scope string) *Binding {
	return m.bindings[key(user, scope)+"\x00"]
}

// bindingFor resolves which browser a route's commands use: the recorded
// choice, else the only browser, else the most recently connected one (what
// people expect right after connecting a browser). With no browser slot at
// all it returns the selection placeholder, so a restored selection still
// reports itself and fails closed. Caller holds m.mu.
func (m *Manager) bindingFor(user, scope string) *Binding {
	all := m.bindingsFor(user, scope)
	if len(all) == 0 {
		return m.placeholderFor(user, scope)
	}
	if id := m.choices[key(user, scope)]; id != "" {
		for _, b := range all {
			if b.browserID == id {
				return b
			}
		}
	}
	if len(all) == 1 {
		return all[0]
	}
	var latest *Binding
	for _, b := range all {
		if b.Status().Connected && (latest == nil || b.connectedAt.After(latest.connectedAt)) {
			latest = b
		}
	}
	if latest != nil {
		return latest
	}
	return all[0]
}
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
	if len(m.bindings) >= 4096 {
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
	if b := m.bindingFor(user, scope); b != nil && b.Status().Connected {
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
	placeholderKey := key(user, scope) + "\x00"
	if m.bindings[placeholderKey] == nil && m.bindingFor(user, scope) == nil {
		m.bindings[placeholderKey] = &Binding{key: key(user, scope), label: g.Label, profile: g.ProfileID, gate: make(chan struct{}, 1)}
	}
	if err := m.persistSelection(); err != nil {
		return false
	}
	source.mu.Lock()
	defer source.mu.Unlock()
	if source.extension == nil {
		return false
	}
	source.extension.SetWriteDeadline(time.Now().Add(10 * time.Second))
	ok := source.extension.WriteJSON(envelope{Type: "connect-project", Scope: scope}) == nil
	notifyChange(key(user, scope))
	return ok
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
	return m.bindingFor(user, scope)
}

// ChooseBrowser records the route's chosen browser; "" clears the choice.
func (m *Manager) ChooseBrowser(user, scope, browserID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if browserID == "" {
		delete(m.choices, key(user, scope))
		return m.persistSelection()
	}
	for _, b := range m.bindingsFor(user, scope) {
		if b.browserID == browserID {
			m.choices[key(user, scope)] = browserID
			return m.persistSelection()
		}
	}
	return errors.New("browser is not connected to this project")
}

// RenameBrowser sets the cosmetic display name of one of the account's browsers.
func (m *Manager) RenameBrowser(user, browserID, name string) error {
	name = strings.TrimSpace(name)
	if len(name) > 120 {
		return errors.New("browser name is too long")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for full, b := range m.bindings {
		if strings.HasPrefix(full, user+"\x00") && b.browserID == browserID {
			b.customName = name
			return m.persistSelection()
		}
	}
	return errors.New("browser not found")
}

func (m *Manager) Status(user, scope string) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	status := Status{}
	if b := m.bindingFor(user, scope); b != nil {
		status = b.Status()
	}
	chosen := m.choices[key(user, scope)]
	for _, b := range m.bindingsFor(user, scope) {
		name := b.browserName()
		status.Browsers = append(status.Browsers, BrowserStatus{ID: b.browserID, Name: name, Connected: b.Status().Connected, Chosen: b.browserID == chosen, LastSeen: b.lastSeenText()})
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

// browserName is the person's rename when set, else the reported name.
func (b *Binding) browserName() string {
	if b.customName != "" {
		return b.customName
	}
	return b.name
}

func (b *Binding) lastSeenText() string {
	if b.lastSeen.IsZero() {
		return ""
	}
	return b.lastSeen.UTC().Format(time.RFC3339)
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
	return m.DisconnectBrowser(user, scope, "")
}

// DisconnectBrowser removes one browser slot of the route (the resolved one
// when browserID is empty), or the whole route with browserID "all".
func (m *Manager) DisconnectBrowser(user, scope, browserID string) error {
	m.mu.Lock()
	var targets []*Binding
	for _, b := range m.bindingsFor(user, scope) {
		if browserID == "all" || browserID == "" && m.bindingFor(user, scope) == b || b.browserID == browserID {
			targets = append(targets, b)
		}
	}
	if len(targets) == 0 {
		// No browser slot: still allow clearing a restored offline selection.
		if placeholder := m.placeholderFor(user, scope); placeholder != nil && (browserID == "" || browserID == "all") {
			delete(m.bindings, key(user, scope)+"\x00")
			delete(m.choices, key(user, scope))
			err := m.persistSelection()
			m.mu.Unlock()
			if err == nil {
				notifyChange(key(user, scope))
			}
			return err
		}
		m.mu.Unlock()
		return errors.New("browser is not connected to this project")
	}
	for _, b := range targets {
		delete(m.bindings, fullKey(user, scope, b.browserID))
		if m.choices[key(user, scope)] == b.browserID {
			delete(m.choices, key(user, scope))
		}
	}
	if err := m.persistSelection(); err != nil {
		m.mu.Unlock()
		return err
	}
	m.mu.Unlock()
	for _, b := range targets {
		b.closeWithReason("Disconnected in browser settings")
	}
	notifyChange(key(user, scope))
	return nil
}
func (b *Binding) close() {
	b.closeWithReason("")
}
func (b *Binding) closeWithReason(reason string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	defer notifyChange(b.key)
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
	Type      string   `json:"type"`
	ClientID  string   `json:"client_id,omitempty"`
	Features  []string `json:"features,omitempty"`
	Token     string   `json:"token,omitempty"`
	Scope     string   `json:"scope,omitempty"`
	Resume    bool     `json:"resume,omitempty"`
	ProfileID string   `json:"profile_id,omitempty"`
	// BrowserID is the extension instance's stable id as reported in hello
	// (empty from old extensions); BrowserName its readable name. The server
	// answers with its own canonical browser id in the paired reply.
	BrowserID   string          `json:"browser_id,omitempty"`
	BrowserName string          `json:"browser_name,omitempty"`
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
	// Diagnostic detail (PLAT-788). Every field is a number or a value from a fixed vocabulary, checked in
	// logExtensionDiagnostic; none is ever a page address, title or message.
	Attempt         int      `json:"attempt,omitempty"`
	DelayMS         int64    `json:"delay_ms,omitempty"`
	ElapsedMS       int64    `json:"elapsed_ms,omitempty"`
	SinceAttachMS   int64    `json:"since_attach_ms,omitempty"`
	SinceNavigateMS int64    `json:"since_navigate_ms,omitempty"`
	AgeMS           int64    `json:"age_ms,omitempty"`
	WSCode          int      `json:"ws_code,omitempty"`
	TargetType      string   `json:"target_type,omitempty"`
	Scheme          string   `json:"scheme,omitempty"`
	TabStatus       string   `json:"tab_status,omitempty"`
	TabFlags        string   `json:"tab_flags,omitempty"`
	Chrome          string   `json:"chrome,omitempty"`
	Online          *bool    `json:"online,omitempty"`
	ExtIDs          []string `json:"ext_ids,omitempty"`
}

var (
	diagnosticEvents = map[string]bool{"connection_paired": true, "connection_stopped": true, "debugger_attached": true, "debugger_detached": true, "session_detach_requested": true, "tab_unshared": true, "command_failed": true, "command_started": true, "command_succeeded": true, "child_attached": true, "child_detached": true, "tab_created": true, "tab_grouping_started": true, "tab_grouped": true, "tab_grouping_failed": true, "setup_waiting_for_page": true, "target_attach_failed": true, "target_setup_failed": true, "target_recovery_started": true, "target_recovered": true, "target_recovery_failed": true, "tab_shown_for_input": true, "window_restored_for_input": true,
		"recovery_attempt": true, "tab_snapshot": true, "frame_attached": true, "frame_navigated": true, "connect_failed": true, "connection_closed": true, "reconnect_scheduled": true}
	diagnosticReasons     = map[string]bool{"": true, "target_closed": true, "canceled_by_user": true, "replaced_with_devtools": true, "requested_unshare": true, "target_close": true, "debugger_detached": true, "tab_closed": true, "unsupported_url": true, "detached": true, "not_shared": true, "foreign_frame": true, "another_debugger": true, "cannot_attach": true, "no_tab": true, "not_allowed_url": true, "timeout": true, "ws_error": true, "ws_closed": true, "other": true}
	diagnosticTargetTypes = map[string]bool{"": true, "iframe": true, "page": true, "worker": true, "service_worker": true, "shared_worker": true, "background_page": true, "other": true}
	diagnosticSchemes     = map[string]bool{"": true, "https": true, "http": true, "chrome-extension": true, "about": true, "blob": true, "data": true, "chrome": true, "chrome-untrusted": true, "devtools": true, "file": true, "other": true}
	diagnosticTabStatus   = map[string]bool{"": true, "loading": true, "complete": true, "unloaded": true}
	diagnosticTabFlags    = map[string]bool{"active": true, "discarded": true, "incognito": true, "grouped": true, "audible": true, "pinned": true, "frozen": true}
)

const maxDiagnosticMS = 7 * 24 * 60 * 60 * 1000

// validExtensionID is Chrome's own form of an extension ID: 32 letters a-p.
func validExtensionID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, c := range id {
		if c < 'a' || c > 'p' {
			return false
		}
	}
	return true
}

// diagnosticDetail renders the optional fields, or "" plus false when one is invalid (the whole record is then dropped).
func diagnosticDetail(e envelope) (string, bool) {
	for _, ms := range []int64{e.DelayMS, e.ElapsedMS, e.SinceAttachMS, e.SinceNavigateMS, e.AgeMS} {
		if ms < 0 || ms > maxDiagnosticMS {
			return "", false
		}
	}
	if e.Attempt < 0 || e.Attempt > 64 || e.WSCode < 0 || e.WSCode > 4999 || len(e.ExtIDs) > 8 {
		return "", false
	}
	if !diagnosticTargetTypes[e.TargetType] || !diagnosticSchemes[e.Scheme] || !diagnosticTabStatus[e.TabStatus] {
		return "", false
	}
	if len(e.Chrome) > 4 {
		return "", false
	}
	for _, c := range e.Chrome {
		if c < '0' || c > '9' {
			return "", false
		}
	}
	if e.TabFlags != "" {
		for _, flag := range strings.Split(e.TabFlags, ",") {
			if !diagnosticTabFlags[flag] {
				return "", false
			}
		}
	}
	for _, id := range e.ExtIDs {
		if !validExtensionID(id) {
			return "", false
		}
	}
	var b strings.Builder
	add := func(name string, value interface{}) { fmt.Fprintf(&b, " %s=%v", name, value) }
	if e.Attempt != 0 {
		add("attempt", e.Attempt)
	}
	if e.DelayMS != 0 {
		add("delay_ms", e.DelayMS)
	}
	if e.ElapsedMS != 0 {
		add("elapsed_ms", e.ElapsedMS)
	}
	if e.SinceAttachMS != 0 {
		add("since_attach_ms", e.SinceAttachMS)
	}
	if e.SinceNavigateMS != 0 {
		add("since_navigate_ms", e.SinceNavigateMS)
	}
	if e.AgeMS != 0 {
		add("age_ms", e.AgeMS)
	}
	if e.WSCode != 0 {
		add("ws_code", e.WSCode)
	}
	if e.TargetType != "" {
		add("target_type", e.TargetType)
	}
	if e.Scheme != "" {
		add("scheme", e.Scheme)
	}
	if e.TabStatus != "" {
		add("tab_status", e.TabStatus)
	}
	if e.TabFlags != "" {
		add("tab_flags", e.TabFlags)
	}
	if e.Chrome != "" {
		add("chrome", e.Chrome)
	}
	if e.Online != nil {
		add("online", *e.Online)
	}
	if len(e.ExtIDs) > 0 {
		add("ext_ids", strings.Join(e.ExtIDs, ","))
	}
	return b.String(), true
}

// Extension diagnostics are deliberately limited to protocol metadata. Never
// log arbitrary message strings, CDP parameters, page URLs or credentials.
func logExtensionDiagnostic(scope, connection string, e envelope) {
	if !diagnosticEvents[e.Event] || !diagnosticReasons[e.Reason] {
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
	detail, ok := diagnosticDetail(e)
	if !ok {
		return
	}
	log.Printf("[CHROME_EXTENSION] scope=%q connection=%q event=%s tab_id=%d reason=%s last_method=%s request_id=%s version=%s duration_ms=%d%s", scope, connection, e.Event, e.TabID, e.Reason, e.Method, e.RequestID, e.Version, e.DurationMS, detail)
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
	// Resolve this connection's browser slot (PLAT-856 856-70): an extension
	// that reports a stable instance id reclaims its own slot; an old extension
	// revives the route's offline unnamed slot or takes a new one. A connection
	// never closes another browser.
	instanceID := strings.TrimSpace(hello.BrowserID)
	reportedName := strings.TrimSpace(hello.BrowserName)
	if len(reportedName) > 120 {
		reportedName = reportedName[:120]
	}
	var slot *Binding
	if instanceID != "" {
		slot = m.bindings[fullKey(g.User, g.Scope, publicBrowserID(g.User, instanceID))]
	}
	if slot == nil {
		if placeholder := m.bindings[key(g.User, g.Scope)+"\x00"]; placeholder != nil && !placeholder.Status().Connected {
			slot = placeholder
		}
	}
	if slot == nil && instanceID == "" {
		// An old extension cannot distinguish itself from a second old
		// browser; as before, its connection reclaims the route's unnamed
		// slot, connected or not.
		for _, candidate := range m.bindingsFor(g.User, g.Scope) {
			if candidate.instanceID == "" {
				slot = candidate
				break
			}
		}
	}
	// An offline browser may have missed the explicit Disconnect close frame.
	// Resume cannot recreate a selection removed by the authenticated app.
	if hello.Resume && slot == nil {
		m.mu.Unlock()
		conn.WriteJSON(envelope{Type: "error", Workspace: "Browser connection was disconnected; connect again to enable access"})
		return
	}
	if err := m.start(); err != nil {
		m.mu.Unlock()
		return
	}

	capability := secret()
	hash := sha256.Sum256([]byte(capability))
	now := time.Now()
	b := &Binding{key: key(g.User, g.Scope), label: g.Label, profile: g.ProfileID, capability: capability, endpoint: m.base + "/cdp/" + capability, session: "session-" + hex.EncodeToString(hash[:8]) + "--browser", expires: now.Add(ConnectionLifetime), extension: conn, gate: make(chan struct{}, 1), createdAt: now, lastSeen: now, connectedAt: now}
	switch {
	case slot != nil && slot.browserID != "":
		b.browserID, b.instanceID, b.createdAt = slot.browserID, slot.instanceID, slot.createdAt
		b.name, b.customName = slot.name, slot.customName
	case instanceID != "":
		b.browserID, b.instanceID = publicBrowserID(g.User, instanceID), instanceID
	default:
		sum := sha256.Sum256([]byte(secret()))
		b.browserID = "browser-" + hex.EncodeToString(sum[:8])
		if slot != nil {
			b.name, b.customName, b.createdAt = slot.name, slot.customName, slot.createdAt
		}
	}
	if reportedName != "" {
		b.name = reportedName
	}
	if b.name == "" {
		unnamed := 0
		for _, other := range m.bindings {
			if other.instanceID == "" && strings.HasPrefix(other.key, g.User+"\x00") {
				unnamed++
			}
		}
		b.name = fmt.Sprintf("Browser %d", unnamed+1)
	}
	for _, feature := range hello.Features {
		if feature == "chat-clients" {
			b.chatClients = true
		}
	}
	full := fullKey(g.User, g.Scope, b.browserID)
	old := m.bindings[full]
	b.mu.Lock()
	m.bindings[full] = b
	if slot != nil && slot.browserID == "" {
		delete(m.bindings, key(g.User, g.Scope)+"\x00")
	}
	if err := m.persistSelection(); err != nil {
		m.mu.Unlock()
		b.mu.Unlock()
		conn.WriteJSON(envelope{Type: "error", Workspace: "Cannot save Chrome selection; reconnect after checking server storage"})
		return
	}
	m.mu.Unlock()
	if old != nil && old != b {
		// The same browser's stale connection: close it as replaced so its
		// page stops auto-reconnecting; the new connection serves the slot.
		old.closeWithReason("Reconnected in a newer connection")
	}
	notifyChange(b.key)
	defer b.close()
	err = conn.WriteJSON(envelope{Type: "paired", Workspace: g.Label, Name: name, Scope: g.Scope, ProfileID: g.ProfileID, BrowserID: b.browserID, BrowserName: b.browserName(), Projects: available, Diagnostics: true})
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
			b.lastSeen = time.Now()
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
				if b.tabs != e.Tabs {
					notifyChange(b.key)
				}
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
			// every Manager.Status caller behind it (server A, 2026-10-07: all
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
